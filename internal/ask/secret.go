package ask

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/leeovery/kit/internal/look"
)

// Secret asks, at the terminal in and out are, for a value typed or pasted
// without being shown: about is the row it's for, the field on the line
// under it, a dot a character, under lead, what's on screen above it, if
// anything. Enter takes it; escape cancels. It asks on a screen of its own,
// which goes when it's done.
func Secret(ctx context.Context, in io.Reader, out io.Writer, lead []string, about look.Row) (string, error) {
	program := tea.NewProgram(field{lead: lead, about: about, width: look.Width}, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return "", fmt.Errorf("ask for %s: %w", about.Name, err)
	}
	f := final.(field)
	if f.cancelled {
		return "", ErrCancelled
	}
	return string(f.typed), nil
}

// field is the question Secret asks.
type field struct {
	lead      []string
	about     look.Row
	typed     []rune
	width     int
	done      bool
	cancelled bool
}

func (f field) Init() tea.Cmd {
	return nil
}

func (f field) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		f.width = min(max(msg.Width, 40), look.Width)
	case tea.PasteMsg:
		f.typed = append(f.typed, []rune(strings.TrimRight(msg.Content, "\r\n"))...)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter":
			f.done = true
			return f, tea.Quit
		case "esc", "ctrl+c":
			f.cancelled = true
			return f, tea.Quit
		case "backspace":
			if len(f.typed) > 0 {
				f.typed = f.typed[:len(f.typed)-1]
			}
		default:
			if msg.Text != "" {
				f.typed = append(f.typed, []rune(msg.Text)...)
			}
		}
	}
	return f, nil
}

func (f field) View() tea.View {
	lines := append([]string{""}, f.lead...)
	if len(f.lead) > 0 {
		lines = append(lines, "")
	}
	if !f.done && !f.cancelled {
		row := f.about
		row.Under = []string{look.Field(len(f.typed))}
		lines = append(append(lines, look.Timeline("  ", f.width, row)...), "", look.Keys(look.Key{Key: "enter", Does: "done"}, look.Key{Key: "esc", Does: "cancel"}))
	}
	v := tea.NewView(strings.Join(lines, "\n") + "\n")
	v.AltScreen = true
	return v
}
