// Package ask asks a person things at a terminal, in kit's look, in place:
// a question in the row it's about, a list to pick from, a field to type in,
// drawn under what's on screen and drawn over as keys come, never taller
// than the terminal, folding back when it's answered, so what's left is
// the record. Only a command at a terminal asks: without one, a question is
// a flag's to answer.
package ask

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/look"
)

// ErrCancelled is returned when the person cancels a question: nothing is
// done.
var ErrCancelled = errors.New("cancelled")

// ErrStopped is returned when the person stops going through questions:
// what they've answered so far is done.
var ErrStopped = errors.New("stopped")

// Question is something asked: the thing it's about, as a row, what more
// there is to see of it, and its answers.
type Question struct {
	About   look.Row
	More    []string
	Answers []look.Choice
}

// Text is the question in plain words: the thing's name, then what its row
// says.
func (q Question) Text() string {
	return strings.TrimSpace(ansi.Strip(q.About.Name + "  " + q.About.Says))
}

// Labels are the answers' labels, in order.
func (q Question) Labels() []string {
	labels := make([]string, len(q.Answers))
	for i, a := range q.Answers {
		labels[i] = a.Label
	}
	return labels
}

// Choose asks q at t and returns the answer taken, by its place: the arrow
// keys (or j and k) move, enter takes, and escape or q cancels. What was
// taken stays on screen, a row after a blank line: the thing, and the
// answer.
func Choose(ctx context.Context, t Terminal, q Question) (int, error) {
	answers, err := ask(ctx, t, "", []Question{q})
	if errors.Is(err, ErrStopped) {
		err = ErrCancelled
	}
	if err != nil {
		return 0, err
	}
	return answers[0], nil
}

// Walk asks qs one after another, each in its row on a timeline: those
// answered above it, with their answer, those to come below it; header heads
// them, with how far through they are. When there are more than fit, what
// shows keeps the question asked in view. It returns the answers taken, by
// their places: all of them, or, with ErrStopped, those taken before the
// person stopped (q); escape cancels. Nothing stays on screen.
func Walk(ctx context.Context, t Terminal, header string, qs []Question) ([]int, error) {
	return ask(ctx, t, header, qs)
}

// ask asks qs, under header, if any.
func ask(ctx context.Context, t Terminal, header string, qs []Question) ([]int, error) {
	if len(qs) == 0 {
		return nil, nil
	}
	for _, q := range qs {
		if len(q.Answers) == 0 {
			return nil, errors.New("nothing to choose from")
		}
	}
	w := newWalk(header, qs)
	if err := show(ctx, t, w); err != nil {
		return nil, fmt.Errorf("ask %s: %w", qs[0].Text(), err)
	}
	switch {
	case w.cancelled:
		return nil, ErrCancelled
	case w.stopped:
		return w.answers, ErrStopped
	}
	return w.answers, nil
}

// walk is the questions Walk asks.
type walk struct {
	header    string
	qs        []Question
	answers   []int
	cursor    int
	cancelled bool
	stopped   bool
}

func newWalk(header string, qs []Question) *walk {
	return &walk{header: header, qs: qs}
}

// done is whether every question is answered.
func (w *walk) done() bool { return len(w.answers) == len(w.qs) }

func (w *walk) update(k key) bool {
	q := w.qs[len(w.answers)]
	switch {
	case k.is("up", "k"):
		w.cursor = (w.cursor - 1 + len(q.Answers)) % len(q.Answers)
	case k.is("down", "j"):
		w.cursor = (w.cursor + 1) % len(q.Answers)
	case k.is("enter"):
		w.answers, w.cursor = append(w.answers, w.cursor), 0
		return w.done()
	case k.is("q"):
		if len(w.qs) == 1 {
			w.cancelled = true
		} else {
			w.stopped = true
		}
		return true
	case k.is("esc", "ctrl+c"):
		w.cancelled = true
		return true
	}
	return false
}

// view is what the walk shows: a blank line, when it has no header, then
// the header, the questions, a row each, the one asked with its answers
// under it, and the keys; when that's more than the terminal holds, those
// around the question asked, as low as they go, the keys under them.
func (w *walk) view(width, height int) []string {
	var lines []string
	if w.header == "" {
		lines = append(lines, "")
	} else {
		lines = append(lines, look.Header(w.header, fmt.Sprintf("%d of %d", len(w.answers)+1, len(w.qs))))
	}
	rows, asked := w.rows(width)
	lines = append(lines, rows...)
	asked += len(lines) - len(rows)
	keys := []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "decide"}}
	if len(w.qs) > 1 {
		keys = append(keys, look.Key{Key: "q", Does: "stop"})
	}
	foot := []string{"", look.Keys(append(keys, look.Key{Key: "esc", Does: "cancel"})...)}
	if room := height - 1 - len(foot); len(lines) > room {
		from := min(max(asked-room, 0), len(lines)-room)
		lines = lines[from : from+room]
	}
	return append(lines, foot...)
}

// rows are the questions' rows on their timeline, and where the question
// asked ends among them.
func (w *walk) rows(width int) ([]string, int) {
	rows := make([]look.Row, 0, len(w.qs))
	asked := 0
	for i, q := range w.qs {
		row := q.About
		row.Under = nil
		switch {
		case i < len(w.answers):
			row.State, row.Says = look.Done, look.Pink(strings.ToLower(q.Answers[w.answers[i]].Label))
		case i == len(w.answers):
			row.Under = append(append(row.Under, q.More...), look.Answers(q.Answers, w.cursor)...)
		default:
			row.State, row.Says = look.Queued, look.Muted(ansi.Strip(q.About.Says))
		}
		rows = append(rows, row)
		if i == len(w.answers) {
			asked = len(look.Timeline("  ", width, rows...))
		}
	}
	return look.Timeline("  ", width, rows...), asked
}

// leaves is what a question asked alone leaves: a blank line, then its row
// with the answer taken, or cancelled. A walk leaves nothing: what's done
// about its questions follows.
func (w *walk) leaves(width int) []string {
	if w.header != "" {
		return nil
	}
	q := w.qs[0]
	row := q.About
	row.Under = nil
	switch {
	case w.cancelled:
		row.State, row.Says = look.Skipped, look.Muted("cancelled")
	default:
		row.State, row.Says = look.Done, look.Pink(strings.ToLower(q.Answers[w.answers[0]].Label))
	}
	return append([]string{""}, look.Rows("  ", width, row)...)
}
