package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/ask"
)

func TestAddInstallsAndDeclares(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "hello")
	w.expectSync([]string{"laptop/declarations"}, "kit brew add hello (laptop): a test")

	out, errOut, code := w.run(t, "brew", "add", "hello", "--note", "a test")
	want := "kit brew add · laptop\nhello ok installed; declared in laptop\nkit-config ok committed and pushed laptop/declarations\nNothing needs attention\n"
	if out != want || errOut != "" || code != 0 {
		t.Errorf("kit printed add\n%s%s exit %d\nwant\n%s", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "go\nhello   # a test\n" {
		t.Errorf("laptop = %q", got)
	}
}

// At a terminal, adding asks nothing: the name goes in its sorted place,
// and the section stays sorted, an old file's headings read as notes.
func TestAddAsksNothing(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.writeSection(t, "laptop", "homebrew formulae", "# Shell\nbat\n\n# Go\ngo\n")
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "golangci-lint")
	w.expectSync([]string{"laptop/declarations"}, "kit brew add golangci-lint (laptop)")

	if _, errOut, code := w.run(t, "brew", "add", "golangci-lint"); code != 0 {
		t.Fatalf("kit exit add %d: %s", code, errOut)
	}
	if len(w.asked) != 0 {
		t.Errorf("asked %q, want nothing", w.asked)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "# Shell\nbat\n# Go\ngo\ngolangci-lint\n" {
		t.Errorf("laptop = %q", got)
	}
}

// A question cancelled leaves everything as it was.
func TestAddCancelledDoesNothing(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.fake.On("mas", "search", "--json", "sleep").Prints(sleepSearch)
	w.choose = func(string, []string) (int, error) { return 0, ask.ErrCancelled }
	_, errOut, code := w.run(t, "mas", "add", "sleep")
	if errOut != "kit: cancelled: nothing was installed or declared\n" || code != 2 || !slices.Equal(w.fake.Calls(), []string{"mas search --json sleep"}) {
		t.Errorf("kit mas add, cancelled: %q, exit %d, running %q; want nothing done", errOut, code, w.fake.Calls())
	}
}

func TestAddHasNoGroups(t *testing.T) {
	w := laptopWorld(t)
	if _, errOut, code := w.run(t, "brew", "add", "golangci-lint", "--group", "Go"); errOut != "kit: unknown flag: --group\n" || code != 2 {
		t.Errorf("kit brew add --group: %q, exit %d; want the flag unknown", errOut, code)
	}
}

func TestAddSharedMovesItOutOfTheMacsFiles(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "studio", "homebrew formulae", "ffmpeg\ngo\n")
	w.expectSync([]string{"laptop/declarations", "shared/declarations", "studio/declarations"}, "kit brew add go (laptop)")

	out, errOut, code := w.run(t, "brew", "add", "go", "--shared")
	if !strings.Contains(out, "go ok already installed; declared in shared, out of laptop and studio\n") || code != 0 {
		t.Errorf("kit add --shared printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "shared", "homebrew formulae"); got != "go\n# Shell\njq\nowner/tap/tool\n" {
		t.Errorf("shared = %q", got)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "" {
		t.Errorf("laptop = %q, want go out of it", got)
	}
}

func TestAddTemporarily(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "hello")
	w.fake.On("brew", "info", "--json=v2", "--formula", "hello").Prints(`{"formulae": [{"name": "hello", "full_name": "hello", "aliases": [], "oldnames": []}], "casks": []}`)

	out, _, code := w.run(t, "brew", "add", "hello", "--temp")
	if !strings.Contains(out, "hello ok installed, for now: not declared, quiet for 7 days, then kit reconcile asks\n") ||
		!strings.Contains(out, "kit-config ok nothing changed\n") || code != 0 {
		t.Errorf("kit add --temp printed\n%s exit %d", out, code)
	}
	if got := w.read(t, filepath.Join(".local", "state", "kit", "drift.json")); !strings.Contains(got, `"brew:hello": "2026-01-02T03:04:05Z"`) {
		t.Errorf("drift.json = %s, want hello noted as temporary", got)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "go\n" {
		t.Errorf("laptop = %q, want it unchanged", got)
	}
}

func TestAddWhatsDeclaredAlready(t *testing.T) {
	w := laptopWorld(t)
	out, _, code := w.run(t, "brew", "add", "go", "jq")
	for _, want := range []string{"go ok already installed; declared already, in laptop\n", "jq ok already installed; declared already, in shared\n", "kit-config ok nothing changed\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit printed add\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestAddThatCantInstallDeclaresNothing(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "nosuch").Exits(1).PrintsToStderr(`Error: No available formula with the name "nosuch".`)
	out, _, code := w.run(t, "brew", "add", "nosuch")
	if !strings.Contains(out, `nosuch failed couldn't install: brew install --formula nosuch exited 1: Error: No available formula with the name "nosuch".`) || code != 1 {
		t.Errorf("kit printed add\n%s exit %d; want the install's failure, exit 1", out, code)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "go\n" {
		t.Errorf("laptop = %q, want it unchanged", got)
	}
}

func TestAddAnUnknownKind(t *testing.T) {
	_, errOut, code := laptopWorld(t).run(t, "app", "add", "Xcode")
	if !strings.HasPrefix(errOut, `kit: unknown command "app" for "kit"`) || code != 2 {
		t.Errorf("kit app add printed %q, exit %d", errOut, code)
	}
}

func TestRemoveUninstallsAndUndeclares(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "--formula", "go")
	w.expectSync([]string{"laptop/declarations"}, "kit brew remove go (laptop)")
	out, errOut, code := w.run(t, "brew", "remove", "go")
	if !strings.Contains(out, "go ok uninstalled; out of laptop\n") || code != 0 {
		t.Errorf("kit printed remove\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "" {
		t.Errorf("laptop = %q, want go out of it", got)
	}
}

func TestRemoveFromEveryMacNeedsShared(t *testing.T) {
	w := laptopWorld(t)
	out, _, code := w.run(t, "brew", "remove", "jq")
	if !strings.Contains(out, "jq failed declared for every Mac, in shared: --shared takes it out of every Mac's list\n") || code != 1 {
		t.Errorf("kit printed remove\n%s exit %d", out, code)
	}
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "uninstall") {
			t.Errorf("ran %q, want nothing uninstalled", c)
		}
	}

	w.fake.On("brew", "uninstall", "--formula", "jq")
	w.expectSync([]string{"shared/declarations"}, "kit brew remove jq (laptop)")
	out, _, code = w.run(t, "brew", "remove", "jq", "--shared")
	if !strings.Contains(out, "jq ok uninstalled; out of shared\n") || code != 0 {
		t.Errorf("kit remove --shared printed\n%s exit %d", out, code)
	}
}

func TestRemoveRefusedByHomebrewChangesNothing(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "--formula", "go").Exits(1).PrintsToStderr("Error: Refusing to uninstall go because it is required by gopls")
	out, _, code := w.run(t, "brew", "remove", "go")
	if !strings.Contains(out, "go failed couldn't uninstall: brew uninstall --formula go exited 1: Error: Refusing to uninstall go because it is required by gopls\n") || code != 1 {
		t.Errorf("kit printed remove\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "go\n" {
		t.Errorf("laptop = %q, want it unchanged", got)
	}
}

// Requirement 4 for kit add: an install that needs an administrator's
// password waits without a terminal, saying why, and declares nothing.
func TestAddWithoutATerminalWaitsForThePassword(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	w.fake.On("sudo", "-n", "true").Exits(1).PrintsToStderr("sudo: a password is required")

	out, errOut, code := w.run(t, "cask", "add", "zoom")
	if want := "zoom failed needs an administrator's password: run kit cask add at a terminal\n"; !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit printed add\n%s exit %d (%s); want zoom waiting, exit 1", out, code, errOut)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "" {
		t.Errorf("laptop = %q, want nothing declared", got)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "brew install") {
			t.Errorf("ran %q, want nothing installed", c)
		}
	}
}

func TestAddAtATerminalAsksForThePasswordFirst(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.choose = func(string, []string) (int, error) { return 0, nil }
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	w.fake.On("sudo", "-v")
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--cask", "zoom")
	w.expectSync([]string{"laptop/declarations"}, "kit cask add zoom (laptop)")

	if _, errOut, code := w.run(t, "cask", "add", "zoom"); code != 0 {
		t.Fatalf("kit exit add %d: %s", code, errOut)
	}
	calls := w.fake.Calls()
	if sudo, install := slices.Index(calls, "sudo -v"), slices.Index(calls, "brew install --cask zoom"); sudo < 0 || install < sudo {
		t.Errorf("ran %q, want sudo -v asked before the install", calls)
	}
}
