package rendertest_test

import (
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/render/rendertest"
)

// Two screens are the same, styled, only when they're the same cell for
// cell: each style said once, where it starts, whatever sequences set it;
// a cell drawn over takes its new style; a colour or bold apart, they
// differ. Its text is Screen's.
func TestStyled(t *testing.T) {
	for _, tt := range []struct {
		name, raw, want string
	}{
		{"one style, two ways", "\x1b[1m\x1b[38;2;1;2;3mab\x1b[m", "\x1b[0;1;38;2;1;2;3mab\x1b[0m"},
		{"said once", "\x1b[1;38;2;1;2;3ma\x1b[0m\x1b[38;2;1;2;3;1mb\x1b[m", "\x1b[0;1;38;2;1;2;3mab\x1b[0m"},
		{"drawn over", "\x1b[1mab\x1b[m\rx\x1b[K", "x"},
		{"the alternate screen, left", "a\x1b[?1049h\x1b[31mb\x1b[?1049l", "a"},
		{"colours apart", "\x1b[31ma\x1b[32mb\x1b[m", "\x1b[0;31ma\x1b[0;32mb\x1b[0m"},
		{"a background, reversed", "\x1b[7;48;5;9ma\x1b[27mb\x1b[49mc", "\x1b[0;7;48;5;9ma\x1b[0;48;5;9mb\x1b[0mc"},
	} {
		if got := rendertest.Styled(tt.raw); got != tt.want {
			t.Errorf("%s: Styled(%q) = %q, want %q", tt.name, tt.raw, got, tt.want)
		}
		if got, styled := rendertest.Screen(tt.raw), ansi.Strip(rendertest.Styled(tt.raw)); got != styled {
			t.Errorf("%s: Screen(%q) = %q; want Styled's text, %q", tt.name, tt.raw, got, styled)
		}
	}
}
