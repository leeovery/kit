package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// Handed over to, kit bootstrap says at once that it's arrived, for the
// boot in Terminal to finish; then it runs as kit apply does, led by the
// boot's steps, checked again, and by the password manager kit.toml names.
func TestBootstrapHandedOverIsApply(t *testing.T) {
	w := missingWorld(t)
	w.write(t, filepath.Join(".config", "kit", "kit.toml"), strings.Replace(twoMacs, "primary = \"laptop\"\n", "primary = \"laptop\"\nterminal = \"ghostty\"\npassword_manager = \"1password\"\n", 1))
	w.env["TERM_PROGRAM"] = "ghostty"
	f := w.fake
	f.On("security", "find-generic-password", "-s", "kit-github", "-w").Exits(44)
	f.On("gh", "auth", "token").Exits(1)
	f.On("op", "whoami")
	f.On("brew", "update", "--quiet")
	f.On("brew", "install", "--formula", "ripgrep")

	out, errOut, _ := w.run(t, "bootstrap", "--handed-over")
	for _, want := range []string{"kit bootstrap · laptop\n", "boot-github attention not signed in\n", "1password ok signed in\n", "brew installed ripgrep\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit bootstrap --handed-over printed\n%s%s\nwant it to hold %q", out, errOut, want)
		}
	}
	if state := w.read(t, filepath.Join(".local", "state", "kit", "bootstrap.json")); !strings.Contains(state, `"arrived"`) {
		t.Errorf("the boot's state is %s: want it to say kit arrived", state)
	}
}
