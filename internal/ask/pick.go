package ask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/leeovery/kit/internal/look"
)

// Line is a line of a list to pick from: Text, as it shows. One that can
// be picked has Chosen, as it shows with the cursor on it, and Value, what
// picking it gives; one that folds others has them, Folds, which picking it
// shows in its place.
type Line struct {
	Text, Chosen, Value string
	Folds               []Line
}

// picks is whether the line can be picked.
func (l Line) picks() bool { return l.Chosen != "" }

// Pick shows lines, after lead, on a screen of their own, which goes when
// it's done, and returns the Value of the line picked: the arrow keys (or j
// and k) move among those that can be picked, enter picks (a fold, it opens
// in place), and escape or q cancels. keys say so, under the list, which
// scrolls to keep the cursor in view.
func Pick(ctx context.Context, in io.Reader, out io.Writer, lead []string, lines []Line, keys []look.Key) (string, error) {
	l := list{lead: lead, lines: lines, keys: keys, width: look.Width, height: 40}
	l.cursor = l.next(-1, 1)
	if l.cursor < 0 {
		return "", errors.New("nothing to pick from")
	}
	program := tea.NewProgram(l, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return "", fmt.Errorf("pick: %w", err)
	}
	l = final.(list)
	if l.cancelled {
		return "", ErrCancelled
	}
	return l.lines[l.cursor].Value, nil
}

// list is the list Pick shows.
type list struct {
	lead          []string
	lines         []Line
	keys          []look.Key
	width, height int
	cursor        int
	picked        bool
	cancelled     bool
}

// next is the next line that can be picked after at, going by step: -1 when
// there's none, going down from -1, and at otherwise.
func (l list) next(at, step int) int {
	for i := at + step; i >= 0 && i < len(l.lines); i += step {
		if l.lines[i].picks() {
			return i
		}
	}
	return at
}

func (l list) Init() tea.Cmd {
	return nil
}

func (l list) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.width, l.height = min(max(msg.Width, 40), look.Width), msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			l.cursor = l.next(l.cursor, -1)
		case "down", "j":
			l.cursor = l.next(l.cursor, 1)
		case "enter":
			if folds := l.lines[l.cursor].Folds; len(folds) > 0 {
				l.lines = slices.Concat(l.lines[:l.cursor], folds, l.lines[l.cursor+1:])
				l.cursor = l.next(l.cursor-1, 1)
				return l, nil
			}
			l.picked = true
			return l, tea.Quit
		case "esc", "q", "ctrl+c":
			l.cancelled = true
			return l, tea.Quit
		}
	}
	return l, nil
}

func (l list) View() tea.View {
	lines := append([]string{""}, l.lead...)
	if len(l.lead) > 0 {
		lines = append(lines, "")
	}
	if !l.picked && !l.cancelled {
		room := max(l.height-len(lines)-3, 3)
		from, to := 0, len(l.lines)
		if to > room {
			from = min(max(l.cursor-room/2, 0), to-room)
			to = from + room
		}
		for i := from; i < to; i++ {
			text := l.lines[i].Text
			if i == l.cursor {
				text = l.lines[i].Chosen
			}
			lines = append(lines, look.Cut(text, l.width))
		}
		lines = append(lines, "", look.Keys(l.keys...))
	}
	// No newline after the keys: the last line never scrolls the screen.
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}
