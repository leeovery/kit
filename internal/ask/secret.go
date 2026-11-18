package ask

import (
	"context"
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/look"
)

// Secret asks at t for a value typed or pasted without being shown: about
// is the row it's for, under what's on screen (a run's heading, its blank
// line), the field on the line under it, a dot a character. Enter takes it;
// escape cancels. Nothing stays on screen.
func Secret(ctx context.Context, t Terminal, about look.Row) (string, error) {
	f := &field{about: about}
	if err := show(ctx, t, f); err != nil {
		return "", fmt.Errorf("ask for %s: %w", about.Name, err)
	}
	if f.cancelled {
		return "", ErrCancelled
	}
	return string(f.typed), nil
}

// Text asks at t for a value typed, shown as it's typed, starting as typed
// says, as Secret asks: enter takes it; escape cancels. Nothing stays on
// screen.
func Text(ctx context.Context, t Terminal, about look.Row, typed string) (string, error) {
	f := &field{about: about, typed: []rune(typed), shown: true}
	if err := show(ctx, t, f); err != nil {
		return "", fmt.Errorf("ask for %s: %w", about.Name, err)
	}
	if f.cancelled {
		return "", ErrCancelled
	}
	return string(f.typed), nil
}

// field is the question Secret and Text ask: shown is whether what's typed
// shows.
type field struct {
	about     look.Row
	typed     []rune
	shown     bool
	cancelled bool
}

func (f *field) update(k key) bool {
	switch {
	case k.is("enter"):
		return true
	case k.is("esc", "ctrl+c"):
		f.cancelled = true
		return true
	case k.is("backspace"):
		if len(f.typed) > 0 {
			f.typed = f.typed[:len(f.typed)-1]
		}
	case k.name == "":
		f.typed = append(f.typed, []rune(strings.TrimRight(k.text, "\r\n"))...)
	}
	return false
}

func (f *field) view(width, _ int) []string {
	row := f.about
	row.Whole = true
	row.Under = []string{look.Field(len(f.typed))}
	if f.shown {
		row.Under = []string{look.Typed(string(f.typed))}
	}
	return append(look.Timeline("  ", width, row), "", look.Keys(look.Key{Key: "enter", Does: "done"}, look.Key{Key: "esc", Does: "cancel"}))
}

func (f *field) leaves(int) []string { return nil }
