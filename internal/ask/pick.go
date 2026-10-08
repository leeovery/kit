package ask

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/leeovery/kit/internal/look"
)

// Line is a line of a list to pick from: Text, as it shows. One that can
// be picked has Chosen, as it shows with the cursor on it, and Value, what
// picking it gives; one that folds others has them, Folds, which picking it
// shows in its place. The cursor starts on the line that says Start, else
// on the first that can be picked.
type Line struct {
	Text, Chosen, Value string
	Folds               []Line
	Start               bool
}

// picks is whether the line can be picked.
func (l Line) picks() bool { return l.Chosen != "" }

// Pick shows lines at t, under head, lines that stay put above them, and
// returns the Value of the line picked: the arrow keys (or j and k) move
// among those that can be picked, enter picks (a fold, it opens in place),
// and escape or q cancels. keys say so, under the lines, which scroll to keep
// the cursor in view when there are more than fit. Nothing stays on screen.
func Pick(ctx context.Context, t Terminal, head []string, lines []Line, keys []look.Key) (string, error) {
	l := &list{head: head, lines: lines, keys: keys}
	l.cursor = slices.IndexFunc(lines, func(l Line) bool { return l.Start && l.picks() })
	if l.cursor < 0 {
		l.cursor = l.next(-1, 1)
	}
	if l.cursor < 0 {
		return "", errors.New("nothing to pick from")
	}
	if err := show(ctx, t, l); err != nil {
		return "", fmt.Errorf("pick: %w", err)
	}
	if l.cancelled {
		return "", ErrCancelled
	}
	return l.lines[l.cursor].Value, nil
}

// list is the list Pick shows.
type list struct {
	head      []string
	lines     []Line
	keys      []look.Key
	cursor    int
	cancelled bool
}

// next is the next line that can be picked after at, going by step: -1 when
// there's none, going down from -1, and at otherwise.
func (l *list) next(at, step int) int {
	for i := at + step; i >= 0 && i < len(l.lines); i += step {
		if l.lines[i].picks() {
			return i
		}
	}
	return at
}

func (l *list) update(k key) bool {
	switch {
	case k.is("up", "k"):
		l.cursor = l.next(l.cursor, -1)
	case k.is("down", "j"):
		l.cursor = l.next(l.cursor, 1)
	case k.is("enter"):
		folds := l.lines[l.cursor].Folds
		if len(folds) == 0 {
			return true
		}
		l.lines = slices.Concat(l.lines[:l.cursor], folds, l.lines[l.cursor+1:])
		l.cursor = l.next(l.cursor-1, 1)
	case k.is("esc", "q", "ctrl+c"):
		l.cancelled = true
		return true
	}
	return false
}

// view is the head, then the lines, those around the cursor when there are
// more than fit, the cursor's chosen, then the keys.
func (l *list) view(width, height int) []string {
	lines := make([]string, 0, height)
	for _, h := range l.head {
		lines = append(lines, look.Cut(h, width))
	}
	from, to := 0, len(l.lines)
	if room := max(height-1-len(l.head)-2, 3); to > room {
		from = min(max(l.cursor-room/2, 0), to-room)
		to = from + room
	}
	for i := from; i < to; i++ {
		text := l.lines[i].Text
		if i == l.cursor {
			text = l.lines[i].Chosen
		}
		lines = append(lines, look.Cut(text, width))
	}
	return append(lines, "", look.Keys(l.keys...))
}

func (l *list) leaves(int) []string { return nil }
