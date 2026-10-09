package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// Handed over to, kit bootstrap says at once that it's arrived, for the
// boot in Terminal to finish; then it runs as kit apply does, led by the
// boot's steps, checked again.
func TestBootstrapHandedOverIsApply(t *testing.T) {
	w := missingWorld(t)
	w.write(t, filepath.Join(".config", "kit", "kit.toml"), strings.Replace(twoMacs, "primary = \"laptop\"\n", "primary = \"laptop\"\nterminal = \"ghostty\"\n", 1))
	w.env["TERM_PROGRAM"] = "ghostty"
	f := w.fake
	f.On("security", "find-generic-password", "-s", "kit-github", "-w").Exits(44)
	f.On("gh", "auth", "token").Exits(1)
	f.On("brew", "update", "--quiet")
	f.On("brew", "install", "--formula", "ripgrep")

	out, errOut, _ := w.run(t, "bootstrap", "--handed-over")
	for _, want := range []string{"kit bootstrap · laptop\n", "boot-github attention not signed in\n", "brew installed ripgrep\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit bootstrap --handed-over printed\n%s%s\nwant it to hold %q", out, errOut, want)
		}
	}
	if state := w.read(t, filepath.Join(".local", "state", "kit", "bootstrap.json")); !strings.Contains(state, `"arrived"`) {
		t.Errorf("the boot's state is %s: want it to say kit arrived", state)
	}
}

// A step by hand saying what it comes after and before is a step of its
// own: due once the cask it's about is installed, and not done till its
// command says so; one called as another step is, or placed before what
// isn't a step, says what's wrong.
func TestAStepByHandOfItsOwn(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "manual", strings.Join([]string{
		`sign-in "Sign in to Ghostty" --after cask:ghostty --before secret --test test -f /signed-in`,
		`lost "Comes before nothing" --before nothing`,
		`plain "Done by hand, with the rest" --test test -f /done`,
	}, "\n")+"\n")
	w.fake.On("test", "-f", "/signed-in").Exits(1)
	w.fake.On("test", "-f", "/done")
	out, _, _ := w.run(t, "status")
	for _, want := range []string{
		"sign-in attention not done",
		"lost failed it comes before nothing, which isn't a step",
		"manual ok all 1 done",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit status printed\n%s\nwant it to hold %q", out, want)
		}
	}
	w.writeSection(t, "laptop", "manual", `brew "Shadows a step" --before secret`+"\n")
	if _, errOut, code := w.run(t, "status"); code != 2 || !strings.Contains(errOut, "laptop/declarations:") || !strings.Contains(errOut, "a step by hand can't be called brew, as another step is: rename it") {
		t.Errorf("a step by hand named as another step printed %q, exit %d", errOut, code)
	}
}
