// Package ask asks a person things at a terminal. Only a command at a
// terminal asks: without one, a question is a flag's to answer.
package ask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ErrCancelled is returned when the person cancels a question.
var ErrCancelled = errors.New("cancelled")

var (
	bold  = lipgloss.NewStyle().Bold(true)
	faint = lipgloss.NewStyle().Faint(true)
	cyan  = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
)

// Choose asks, at the terminal in and out are, which of options to take,
// starting on the first, and returns its index: the arrow keys (or j and k)
// move, enter takes, and escape or q cancels. What was taken stays on the
// screen, a line.
func Choose(ctx context.Context, in io.Reader, out io.Writer, question string, options []string) (int, error) {
	if len(options) == 0 {
		return 0, errors.New("nothing to choose from")
	}
	program := tea.NewProgram(newChooser(question, options), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return 0, fmt.Errorf("ask %s: %w", question, err)
	}
	c := final.(chooser)
	if c.cancelled {
		return 0, ErrCancelled
	}
	return c.cursor, nil
}

// chooser is the question Choose asks.
type chooser struct {
	question  string
	options   []string
	cursor    int
	chosen    bool
	cancelled bool
}

func newChooser(question string, options []string) chooser {
	return chooser{question: question, options: options}
}

func (c chooser) Init() tea.Cmd {
	return nil
}

func (c chooser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch key.String() {
	case "up", "k":
		c.cursor = max(c.cursor-1, 0)
	case "down", "j":
		c.cursor = min(c.cursor+1, len(c.options)-1)
	case "enter":
		c.chosen = true
		return c, tea.Quit
	case "esc", "q", "ctrl+c":
		c.cancelled = true
		return c, tea.Quit
	}
	return c, nil
}

func (c chooser) View() tea.View {
	switch {
	case c.chosen:
		return tea.NewView(bold.Render(c.question) + " " + c.options[c.cursor] + "\n")
	case c.cancelled:
		return tea.NewView(bold.Render(c.question) + faint.Render(" cancelled") + "\n")
	}
	lines := []string{bold.Render(c.question)}
	for i, option := range c.options {
		if i == c.cursor {
			lines = append(lines, cyan.Render("› "+option))
		} else {
			lines = append(lines, "  "+option)
		}
	}
	lines = append(lines, faint.Render("↑↓ to move · enter to take · esc to cancel"))
	return tea.NewView(strings.Join(lines, "\n") + "\n")
}
