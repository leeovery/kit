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

// Setup's snapshot looks for a program off kit's PATH in Homebrew's and the
// system's folders first, as kit's runs would find it there, then along
// the PATH setup was run with; and never declares a temporary folder, the
// environment's among them.
func TestTheFoldersOffKitsPath(t *testing.T) {
	env := map[string]string{"PATH": "/Users/someone/.local/share/mise/shims:/opt/homebrew/bin:bin", "TMPDIR": "/var/tmp/someone/"}
	getenv := func(name string) string { return env[name] }
	want := slices.Concat(bootPath, []string{"/Users/someone/.local/share/mise/shims", "/opt/homebrew/bin", "bin"})
	if got := offPath(getenv); !slices.Equal(got, want) {
		t.Errorf("offPath() = %q, want %q", got, want)
	}
	if got := tempDirs(getenv); !slices.Contains(got, "/var/tmp/someone/") || !slices.Contains(got, "/private/var/folders") {
		t.Errorf("tempDirs() = %q, want the system's and TMPDIR", got)
	}
}
