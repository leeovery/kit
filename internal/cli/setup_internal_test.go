package cli

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/linked"
)

// Setup links only what isn't linked already, as by its run part-way: a
// file declared linked is left out, and a folder any file in it is.
func TestUnlinked(t *testing.T) {
	declared := []linked.Link{{Name: "~/.zshrc"}, {Name: "~/.config/git/config"}}
	for _, tt := range []struct {
		shown, want []string
	}{
		{[]string{"~/.zshrc", "~/.zprofile"}, []string{"~/.zprofile"}},
		{[]string{"~/.config/git", "~/.config/gitx"}, []string{"~/.config/gitx"}},
		{[]string{"~/.zshrc"}, nil},
		{nil, nil},
	} {
		full := make([]string, len(tt.shown))
		for i, s := range tt.shown {
			full[i] = "/Users/someone/" + s[2:]
		}
		gotFull, gotShown := unlinked(declared, full, tt.shown)
		if !slices.Equal(gotShown, tt.want) || len(gotFull) != len(gotShown) {
			t.Errorf("unlinked(%q) = %q, %q; want %q", tt.shown, gotFull, gotShown, tt.want)
		}
		for i, s := range gotShown {
			if gotFull[i] != "/Users/someone/"+s[2:] {
				t.Errorf("unlinked(%q) paired %s with %s", tt.shown, gotFull[i], s)
			}
		}
	}
}
