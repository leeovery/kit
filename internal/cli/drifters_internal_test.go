package cli

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/kind"
)

// Adopting an app installed by hand says what taking it over can do: the
// app taken over in place, or an older copy updated.
func TestAdoptingAnAppByHandSaysWhatItDoes(t *testing.T) {
	d := kindDrifter{tool: "Homebrew"}
	var said []string
	for _, c := range d.choices(check.Item{State: kind.ByHand}) {
		if c.action == adopt {
			said = append(said, c.words.Does)
		}
	}
	want := []string{
		"declare it; Homebrew takes it over, replacing an older copy",
		"declare it; Homebrew takes it over, replacing an older copy",
	}
	if !slices.Equal(said, want) {
		t.Errorf("adopting says %q\nwant %q", said, want)
	}
}
