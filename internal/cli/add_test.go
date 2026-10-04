package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/ask"
)

var brewLaptop = filepath.Join(".config", "kit", "brew.laptop")

func TestAddInstallsAndDeclares(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "hello")
	w.expectSync([]string{"brew.laptop"}, "kit add brew hello (laptop): a test")

	out, errOut, code := w.run(t, "add", "brew", "hello", "--note", "a test")
	want := "kit add · laptop\nhello ok installed; declared in brew.laptop (To be sorted)\nkit-config ok committed and pushed brew.laptop\nNothing needs attention\n"
	if out != want || errOut != "" || code != 0 {
		t.Errorf("kit add printed\n%s%s exit %d\nwant\n%s", out, errOut, code, want)
	}
	if got := w.read(t, brewLaptop); got != "go\n\n# To be sorted\nhello   # a test\n" {
		t.Errorf("brew.laptop = %q", got)
	}
}

func TestAddAsksWhichGroupFirst(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.write(t, brewLaptop, "# Shell\nbat\n\n# Go\ngo\n")
	callsWhenAsked := -1
	w.choose = func(question string, options []string) (int, error) {
		callsWhenAsked = len(w.fake.Calls())
		if want := []string{"To be sorted", "Shell", "Go"}; !slices.Equal(options, want) {
			t.Errorf("offered %q, want %q", options, want)
		}
		return 2, nil
	}
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "golangci-lint")
	w.expectSync([]string{"brew.laptop"}, "kit add brew golangci-lint (laptop)")

	if _, errOut, code := w.run(t, "add", "brew", "golangci-lint"); code != 0 {
		t.Fatalf("kit add exit %d: %s", code, errOut)
	}
	if want := []string{"Which group of brew.laptop for golangci-lint?"}; !slices.Equal(w.asked, want) {
		t.Errorf("asked %q, want %q", w.asked, want)
	}
	if callsWhenAsked != 0 {
		t.Errorf("%d commands ran before kit asked, want none: questions come first", callsWhenAsked)
	}
	if got := w.read(t, brewLaptop); got != "# Shell\nbat\n\n# Go\ngo\ngolangci-lint\n" {
		t.Errorf("brew.laptop = %q", got)
	}
}

func TestAddCancelledDoesNothing(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.choose = func(string, []string) (int, error) { return 0, ask.ErrCancelled }
	_, errOut, code := w.run(t, "add", "brew", "hello")
	if errOut != "kit: cancelled: nothing was installed or declared\n" || code != 2 || len(w.fake.Calls()) != 0 {
		t.Errorf("kit add, cancelled: %q, exit %d, running %q; want nothing done", errOut, code, w.fake.Calls())
	}
}

func TestAddInAGroupWithoutAsking(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "golangci-lint")
	w.expectSync([]string{"brew.laptop"}, "kit add brew golangci-lint (laptop)")
	if _, errOut, code := w.run(t, "add", "brew", "golangci-lint", "--group", "Go"); code != 0 {
		t.Fatalf("kit add exit %d: %s", code, errOut)
	}
	if got := w.read(t, brewLaptop); got != "go\n\n# Go\ngolangci-lint\n" {
		t.Errorf("brew.laptop = %q", got)
	}
}

func TestAddSharedMovesItOutOfTheMacsFiles(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, filepath.Join(".config", "kit", "brew.studio"), "ffmpeg\ngo\n")
	w.expectSync([]string{"brew", "brew.laptop", "brew.studio"}, "kit add brew go (laptop)")

	out, errOut, code := w.run(t, "add", "brew", "go", "--shared")
	if !strings.Contains(out, "go ok already installed; declared in brew (To be sorted), out of brew.laptop and brew.studio\n") || code != 0 {
		t.Errorf("kit add --shared printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "brew")); got != "# Shell\njq\nowner/tap/tool\n\n# To be sorted\ngo\n" {
		t.Errorf("brew = %q", got)
	}
	if got := w.read(t, brewLaptop); got != "" {
		t.Errorf("brew.laptop = %q, want go out of it", got)
	}
}

func TestAddTemporarily(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "hello")
	w.fake.On("brew", "info", "--json=v2", "--formula", "hello").Prints(`{"formulae": [{"name": "hello", "full_name": "hello", "aliases": [], "oldnames": []}], "casks": []}`)

	out, _, code := w.run(t, "add", "brew", "hello", "--temp")
	if !strings.Contains(out, "hello ok installed, for now: not declared, quiet for 7 days, then kit reconcile asks\n") ||
		!strings.Contains(out, "kit-config ok nothing changed\n") || code != 0 {
		t.Errorf("kit add --temp printed\n%s exit %d", out, code)
	}
	if got := w.read(t, filepath.Join(".local", "state", "kit", "drift.json")); !strings.Contains(got, `"brew:hello": "2026-01-02T03:04:05Z"`) {
		t.Errorf("drift.json = %s, want hello noted as temporary", got)
	}
	if got := w.read(t, brewLaptop); got != "go\n" {
		t.Errorf("brew.laptop = %q, want it unchanged", got)
	}
}

func TestAddWhatsDeclaredAlready(t *testing.T) {
	w := laptopWorld(t)
	out, _, code := w.run(t, "add", "brew", "go", "jq")
	for _, want := range []string{"go ok already installed; declared already, in brew.laptop\n", "jq ok already installed; declared already, in brew\n", "kit-config ok nothing changed\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit add printed\n%s\nwant it to hold %q", out, want)
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
	out, _, code := w.run(t, "add", "brew", "nosuch")
	if !strings.Contains(out, `nosuch failed couldn't install: brew install --formula nosuch exited 1: Error: No available formula with the name "nosuch".`) || code != 1 {
		t.Errorf("kit add printed\n%s exit %d; want the install's failure, exit 1", out, code)
	}
	if got := w.read(t, brewLaptop); got != "go\n" {
		t.Errorf("brew.laptop = %q, want it unchanged", got)
	}
}

func TestAddAnUnknownKind(t *testing.T) {
	_, errOut, code := laptopWorld(t).run(t, "add", "mas", "Xcode")
	if errOut != "kit: no kind named mas: one of brew, cask\n" || code != 2 {
		t.Errorf("kit add mas printed %q, exit %d", errOut, code)
	}
}

func TestRemoveUninstallsAndUndeclares(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "--formula", "go")
	w.expectSync([]string{"brew.laptop"}, "kit remove brew go (laptop)")
	out, errOut, code := w.run(t, "remove", "brew", "go")
	if !strings.Contains(out, "go ok uninstalled; out of brew.laptop\n") || code != 0 {
		t.Errorf("kit remove printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, brewLaptop); got != "" {
		t.Errorf("brew.laptop = %q, want go out of it", got)
	}
}

func TestRemoveFromEveryMacNeedsShared(t *testing.T) {
	w := laptopWorld(t)
	out, _, code := w.run(t, "remove", "brew", "jq")
	if !strings.Contains(out, "jq failed declared for every Mac, in brew: --shared takes it out of every Mac's list\n") || code != 1 {
		t.Errorf("kit remove printed\n%s exit %d", out, code)
	}
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "uninstall") {
			t.Errorf("ran %q, want nothing uninstalled", c)
		}
	}

	w.fake.On("brew", "uninstall", "--formula", "jq")
	w.expectSync([]string{"brew"}, "kit remove brew jq (laptop)")
	out, _, code = w.run(t, "remove", "brew", "jq", "--shared")
	if !strings.Contains(out, "jq ok uninstalled; out of brew\n") || code != 0 {
		t.Errorf("kit remove --shared printed\n%s exit %d", out, code)
	}
}

func TestRemoveRefusedByHomebrewChangesNothing(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "--formula", "go").Exits(1).PrintsToStderr("Error: Refusing to uninstall go because it is required by gopls")
	out, _, code := w.run(t, "remove", "brew", "go")
	if !strings.Contains(out, "go failed couldn't uninstall: brew uninstall --formula go exited 1: Error: Refusing to uninstall go because it is required by gopls\n") || code != 1 {
		t.Errorf("kit remove printed\n%s exit %d", out, code)
	}
	if got := w.read(t, brewLaptop); got != "go\n" {
		t.Errorf("brew.laptop = %q, want it unchanged", got)
	}
}

// Requirement 4 for kit add: an install that needs an administrator's
// password waits without a terminal, saying why, and declares nothing.
func TestAddWithoutATerminalWaitsForThePassword(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	w.fake.On("sudo", "-n", "true").Exits(1).PrintsToStderr("sudo: a password is required")

	out, errOut, code := w.run(t, "add", "cask", "zoom")
	if want := "zoom failed needs an administrator's password: run kit add at a terminal\n"; !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit add printed\n%s exit %d (%s); want zoom waiting, exit 1", out, code, errOut)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "cask.laptop")); got != "" {
		t.Errorf("cask.laptop = %q, want nothing declared", got)
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
	w.expectSync([]string{"cask.laptop"}, "kit add cask zoom (laptop)")

	if _, errOut, code := w.run(t, "add", "cask", "zoom"); code != 0 {
		t.Fatalf("kit add exit %d: %s", code, errOut)
	}
	calls := w.fake.Calls()
	if sudo, install := slices.Index(calls, "sudo -v"), slices.Index(calls, "brew install --cask zoom"); sudo < 0 || install < sudo {
		t.Errorf("ran %q, want sudo -v asked before the install", calls)
	}
}
