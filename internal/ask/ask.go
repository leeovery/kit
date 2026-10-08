// Package ask asks a person things at a terminal, in kit's look. Only a
// command at a terminal asks: without one, a question is a flag's to
// answer.
package ask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
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

// Choose asks q at the terminal in and out are, and returns the answer
// taken, by its place: the arrow keys (or j and k) move, enter takes, and
// escape or q cancels. What was taken stays on screen, a row: the thing,
// and the answer.
func Choose(ctx context.Context, in io.Reader, out io.Writer, q Question) (int, error) {
	answers, err := Walk(ctx, in, out, nil, "", []Question{q})
	if errors.Is(err, ErrStopped) {
		err = ErrCancelled
	}
	row := q.About
	row.Under = nil
	switch {
	case errors.Is(err, ErrCancelled):
		row.State, row.Says = look.Skipped, look.Muted("cancelled")
	case err != nil:
		return 0, err
	default:
		row.State, row.Says = look.Done, look.Pink(strings.ToLower(q.Answers[answers[0]].Label))
	}
	if _, werr := io.WriteString(out, strings.Join(look.Rows("  ", look.Width, row), "\n")+"\n"); werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		return 0, err
	}
	return answers[0], nil
}

// Walk asks qs one after another, each in its row on a timeline: those
// answered above it, with their answer, those to come below it; header, if
// any, heads them, with how far through they are, and lead, any lines
// leading them, as a command's heading. It asks on a screen of its own,
// which goes when it's done, leaving the terminal as it was. It returns the
// answers taken, by their places: all of them, or, with ErrStopped, those
// taken before the person stopped (q); escape cancels.
func Walk(ctx context.Context, in io.Reader, out io.Writer, lead []string, header string, qs []Question) ([]int, error) {
	if len(qs) == 0 {
		return nil, nil
	}
	for _, q := range qs {
		if len(q.Answers) == 0 {
			return nil, errors.New("nothing to choose from")
		}
	}
	w := newWalk(header, qs)
	w.lead = lead
	program := tea.NewProgram(w, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("ask %s: %w", qs[0].Text(), err)
	}
	w = final.(walk)
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
	lead      []string
	header    string
	qs        []Question
	answers   []int
	cursor    int
	width     int
	cancelled bool
	stopped   bool
}

func newWalk(header string, qs []Question) walk {
	return walk{header: header, qs: qs, width: look.Width}
}

func (w walk) Init() tea.Cmd {
	return nil
}

// done is whether every question is answered.
func (w walk) done() bool { return len(w.answers) == len(w.qs) }

func (w walk) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w.width = min(max(msg.Width, 40), look.Width)
	case tea.KeyPressMsg:
		q := w.qs[len(w.answers)]
		switch msg.String() {
		case "up", "k":
			w.cursor = max(w.cursor-1, 0)
		case "down", "j":
			w.cursor = min(w.cursor+1, len(q.Answers)-1)
		case "enter":
			w.answers, w.cursor = append(w.answers, w.cursor), 0
			if w.done() {
				return w, tea.Quit
			}
		case "q":
			if len(w.qs) == 1 {
				w.cancelled = true
			} else {
				w.stopped = true
			}
			return w, tea.Quit
		case "esc", "ctrl+c":
			w.cancelled = true
			return w, tea.Quit
		}
	}
	return w, nil
}

func (w walk) View() tea.View {
	v := tea.NewView(strings.Join(w.lines(), "\n") + "\n")
	v.AltScreen = true
	return v
}

// lines are what the walk shows: the lines leading it, then the questions,
// a row each, the one asked with its answers under it, and the keys.
func (w walk) lines() []string {
	lines := append([]string{""}, w.lead...)
	if len(w.lead) > 0 {
		lines = append(lines, "")
	}
	if w.done() || w.cancelled || w.stopped {
		return lines
	}
	if w.header != "" {
		lines = append(lines, look.Header(w.header, fmt.Sprintf("%d of %d", len(w.answers)+1, len(w.qs))))
	}
	rows := make([]look.Row, 0, len(w.qs))
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
	}
	lines = append(lines, look.Timeline("  ", w.width, rows...)...)
	keys := []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "decide"}}
	if len(w.qs) > 1 {
		keys = append(keys, look.Key{Key: "q", Does: "stop"})
	}
	return append(lines, "", look.Keys(append(keys, look.Key{Key: "esc", Does: "cancel"})...))
}
