package cli_test

import (
	"slices"
	"strings"
	"testing"
)

const zoomInfo = `{"formulae": [], "casks": [{"token": "zoom", "full_token": "zoom", "old_tokens": [], "artifacts": [{"pkg": ["zoomusInstallerFull.pkg"]}]}]}`

// missingWorld is laptopWorld declaring ripgrep, which isn't installed until
// kit installs it.
func missingWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "go\nripgrep\n")
	f := w.fake
	f.On("brew", "list", "--formula", "--full-name", "-1").Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n").
		Then().Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\nripgrep\n")
	f.On("brew", "leaves").Prints("go\njq\nowner/tap/tool\nnode@20\nffmpeg\n").
		Then().Prints("go\njq\nowner/tap/tool\nnode@20\nffmpeg\nripgrep\n")
	f.On("brew", "leaves", "--installed-on-request").Prints("go\njq\nowner/tap/tool\nffmpeg\n").
		Then().Prints("go\njq\nowner/tap/tool\nffmpeg\nripgrep\n")
	f.On("brew", "info", "--json=v2", "--formula", "ripgrep").Prints(`{"formulae": [{"name": "ripgrep", "full_name": "ripgrep", "aliases": ["rg"], "oldnames": []}], "casks": []}`)
	return w
}

func TestApplyInstallsWhatsMissing(t *testing.T) {
	w := missingWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "ripgrep")

	out, errOut, code := w.run(t, "apply")
	for _, want := range []string{"kit apply · laptop\n", "brew installed ripgrep\n", "brew attention 4 declared, all installed\n", "brew extra ffmpeg\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit apply printed\n%s%s\nwant it to hold %q", out, errOut, want)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1: ffmpeg still needs reconciling", code)
	}
	calls := w.fake.Calls()
	for _, unwanted := range []string{"uninstall"} {
		for _, c := range calls {
			if strings.Contains(c, unwanted) {
				t.Errorf("ran %q: applying never removes", c)
			}
		}
	}
	if i, j := slices.Index(calls, "brew update --quiet"), slices.Index(calls, "brew install --formula ripgrep"); i < 0 || j < i {
		t.Errorf("ran %q, want brew update, then the install", calls)
	}
}

func TestApplyPlanChangesNothing(t *testing.T) {
	w := missingWorld(t)
	out, errOut, code := w.run(t, "apply", "--plan")
	if !strings.Contains(out, "kit apply --plan · laptop\n") || !strings.Contains(out, "brew missing:new ripgrep (to install)\n") || code != 1 {
		t.Errorf("kit apply --plan printed %q exit %d (%q); want ripgrep to install, exit 1", out, code, errOut)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "brew install ") || strings.HasPrefix(c, "brew update") || strings.HasPrefix(c, "sudo") {
			t.Errorf("--plan ran %q, want nothing changed or asked", c)
		}
	}
}

// Requirement 4: questions up front, then unattended. Without a terminal,
// apply never prompts: a cask needing an administrator's password waits,
// saying why.
func TestApplyWithoutATerminalNeverPrompts(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew casks", "firefox\nzoom\n")
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	w.fake.On("sudo", "-n", "true").Exits(1).PrintsToStderr("sudo: a password is required")

	out, errOut, code := w.run(t, "apply")
	if want := "cask missing:new zoom (needs an administrator's password: run kit apply at a terminal)\n"; !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit apply printed\n%s exit %d (%s); want zoom waiting, exit 1", out, code, errOut)
	}
	for _, cmd := range w.fake.Commands() {
		if cmd.Interactive || slices.Equal(cmd.Args, []string{"-v"}) || slices.Contains(cmd.Args, "--cask") && cmd.Args[0] == "install" {
			t.Errorf("ran %s, want nothing asked and nothing installed for zoom", cmd)
		}
	}
}

func TestApplyAtATerminalAsksForThePasswordUpFront(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.writeSection(t, "laptop", "homebrew casks", "firefox\nzoom\n")
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n").
		Then().Prints("ghostty\nfirefox\n").
		Then().Prints("ghostty\nfirefox\nzoom\n")
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	sudo := w.expectPassword()
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--cask", "zoom")

	w.choose = func(string, []string) (int, error) { return 0, nil }

	if _, errOut, code := w.run(t, "apply"); code != 1 {
		t.Errorf("kit apply exit %d (%s), want 1: ffmpeg still needs reconciling", code, errOut)
	}
	// What applying leaves differing from the config, it offers to settle.
	if !slices.Equal(w.asked, []string{"Reconcile now?"}) || len(w.became) != 1 || !slices.Equal(w.became[0], []string{"reconcile"}) {
		t.Errorf("kit asked %q and became %q; want Reconcile now?, answered yes, kit reconcile", w.asked, w.became)
	}
	calls := w.fake.Calls()
	asked, install := slices.Index(calls, askedFor), slices.Index(calls, "brew install --cask zoom")
	if asked < 0 || install < asked {
		t.Fatalf("ran %q, want the password asked for before anything's installed", calls)
	}
	// Asked in kit's own field, a row naming what needs it, and given to
	// sudo on its input, never on a command line.
	if !slices.Equal(w.typedFor, []string{"zoom  needs an administrator's password"}) || !slices.Equal(sudo.Answered(), []string{"hunter2"}) {
		t.Errorf("asked for %q, sudo given %q", w.typedFor, sudo.Answered())
	}
	if strings.Contains(strings.Join(calls, "\n"), "hunter2") {
		t.Errorf("the password was on a command line: %q", calls)
	}
	for _, c := range calls[:asked] {
		if strings.HasPrefix(c, "brew install") || strings.HasPrefix(c, "brew update") {
			t.Errorf("ran %q before asking for the password, want nothing applied first", c)
		}
	}
}
