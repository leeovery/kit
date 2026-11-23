package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// lineWorld is laptopWorld with brew update scripted, as an install line
// runs after it.
func lineWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.fake.On("brew", "update", "--quiet")
	return w
}

// A line copied for brew install runs as given, then each name it
// installed is declared as what brew installed it as: a cask, given
// without --cask, as brew takes it.
func TestAnInstallLineDeclaresACaskGivenWithoutCask(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "firefox")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install firefox (laptop)")

	out, errOut, code := w.run(t, "brew", "install", "firefox")
	want := "kit brew install firefox · laptop\nbrew ok installed\nfirefox ok installed as a cask; declared in laptop\n" +
		"kit-config ok committed and pushed laptop/declarations\nNothing needs attention\n"
	if out != want || errOut != "" || code != 0 {
		t.Errorf("kit brew install printed\n%s%s exit %d\nwant\n%s", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "firefox\n" {
		t.Errorf("laptop's casks = %q, want firefox", got)
	}
	calls := w.fake.Calls()
	if installed, listed := slices.Index(calls, "brew install firefox"), slices.Index(calls, "brew list --cask --full-name -1"); listed < installed {
		t.Errorf("ran %q; want the casks listed after the install", calls)
	}
}

// A formula's declared as one, with its note.
func TestAnInstallLineDeclaresAFormula(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ffmpeg")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install ffmpeg (laptop): for video")

	out, _, code := w.run(t, "brew", "install", "ffmpeg", "--note", "for video")
	if want := "ffmpeg ok installed as a formula; declared in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install printed\n%s exit %d; want it to hold %q", out, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "ffmpeg   # for video\ngo\n" {
		t.Errorf("laptop's formulae = %q", got)
	}
}

// A tap's cask, with --force, is declared with its tap, the flag the
// install's alone; --note= is kit's too.
func TestAnInstallLineDeclaresATapsCask(t *testing.T) {
	w := privateWorld(t, "200")
	w.fake.On("brew", "install", "--cask", "--force", privateCask)
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n" + privateCask + "\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install owner/tools/app (laptop): the menu bar")

	out, errOut, code := w.run(t, "brew", "install", "--cask", "--force", privateCask, "--note=the menu bar")
	want := "kit brew install --cask --force owner/tools/app · laptop\nbrew ok installed\nowner/tools/app ok installed; declared in laptop\n"
	if !strings.HasPrefix(out, want) || code != 0 {
		t.Errorf("kit brew install printed\n%s%s exit %d\nwant it to start\n%s", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != privateCask+"   # the menu bar\n" {
		t.Errorf("laptop's casks = %q", got)
	}
}

// Of several names, one that didn't install isn't declared, and the run
// fails as brew did, saying what was declared and what wasn't.
func TestAnInstallLineThatPartlyFails(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ffmpeg", "nosuch").Exits(1).PrintsToStderr(`Error: No available formula with the name "nosuch".`)
	w.fake.On("brew", "info", "--json=v2", "--cask", "nosuch").Exits(1).PrintsToStderr(`Error: No available cask with the name "nosuch".`)
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install ffmpeg (laptop)")

	out, _, code := w.run(t, "brew", "install", "ffmpeg", "nosuch")
	for _, want := range []string{
		"brew failed couldn't install: Error: No available formula with the name \"nosuch\".\n",
		"ffmpeg ok installed as a formula; declared in laptop\n",
		"nosuch failed couldn't declare: it isn't installed\n",
		"kit-config ok committed and pushed laptop/declarations\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit brew install printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "ffmpeg\ngo\n" {
		t.Errorf("laptop's formulae = %q, want ffmpeg alone added", got)
	}
}

// --shared declares for every Mac, taking it out of each Mac's own; a name
// declared already isn't declared again; every change is one commit.
func TestAnInstallLineSharedAndDeclaredAlready(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ffmpeg", "jq", "firefox")
	w.expectSync(t, []string{"shared/declarations", "studio/declarations"}, "kit brew install ffmpeg, firefox (laptop)")

	out, _, code := w.run(t, "brew", "install", "ffmpeg", "--shared", "jq", "firefox")
	for _, want := range []string{
		"kit brew install ffmpeg jq firefox · laptop\n",
		"ffmpeg ok installed as a formula; declared in shared, out of studio\n",
		"jq ok installed as a formula; declared already, in shared\n",
		"firefox ok installed as a cask; declared in shared\n",
		"kit-config ok committed and pushed shared/declarations, studio/declarations\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit brew install --shared printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if got := w.readSection(t, "shared", "homebrew casks"); got != "firefox\nghostty\n" {
		t.Errorf("shared's casks = %q", got)
	}
	if got := w.readSection(t, "studio", "homebrew formulae"); got != "" {
		t.Errorf("studio's formulae = %q, want ffmpeg out of it", got)
	}
}

// A file's path or an address in place of a name, or no name at all, is
// refused before anything runs.
func TestAnInstallLineRefusesWhatCantBeDeclared(t *testing.T) {
	for _, args := range [][]string{{"./tool.rb"}, {"jq", "https://example.com/tool.rb"}, {"--cask", "--force"}} {
		w := laptopWorld(t)
		_, errOut, code := w.run(t, slices.Concat([]string{"brew", "install"}, args)...)
		if code == 0 || !strings.HasPrefix(errOut, "kit: ") || len(w.fake.Calls()) != 0 {
			t.Errorf("kit brew install %q: %q, exit %d, running %q; want it refused, nothing run", args, errOut, code, w.fake.Calls())
		}
	}
}

// kit's flags go before brew; in the line, a flag of the same name is
// brew's.
func TestAnInstallLinesVerboseIsBrews(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "--verbose", "jq")
	if _, errOut, code := w.run(t, "brew", "install", "--verbose", "jq"); code != 0 {
		t.Fatalf("kit brew install --verbose jq: exit %d, %s", code, errOut)
	}
	w = lineWorld(t)
	w.fake.On("brew", "install", "jq").PrintsToStderr(strings.Repeat("x", 100_000))
	out, errOut, code := w.run(t, "--plain", "--verbose", "brew", "install", "jq")
	if code != 0 || !strings.HasPrefix(out, "kit brew install jq · laptop\n") {
		t.Fatalf("kit --plain --verbose brew install jq printed\n%s%s exit %d", out, errOut, code)
	}
	logs, err := filepath.Glob(filepath.Join(w.home, "Library", "Logs", "kit", "*brew-install*"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs = %q, %v; want the run's", logs, err)
	}
	if got := w.read(t, strings.TrimPrefix(logs[0], w.home+"/")); !strings.Contains(got, strings.Repeat("x", 100_000)) {
		t.Errorf("the run's log keeps %d bytes; want brew's output whole, as kit's --verbose keeps it", len(got))
	}
}

// kit's help, not brew's.
func TestAnInstallLinesHelpIsKits(t *testing.T) {
	w := laptopWorld(t)
	out, _, code := w.run(t, "brew", "install", "--help")
	if code != 0 || !strings.Contains(out, "kit brew install <name>... [flags]") || len(w.fake.Calls()) != 0 {
		t.Errorf("kit brew install --help printed\n%s exit %d, running %q", out, code, w.fake.Calls())
	}
}

// An uninstall line runs as given, then each name no longer installed is
// undeclared.
func TestAnUninstallLineUndeclares(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "--force", "go")
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints("jq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n")
	w.fake.On("brew", "info", "--json=v2", "--cask", "go").Exits(1).PrintsToStderr(`Error: No available cask with the name "go".`)
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew uninstall go (laptop)")

	out, errOut, code := w.run(t, "brew", "uninstall", "--force", "go")
	want := "kit brew uninstall --force go · laptop\nbrew ok uninstalled\ngo ok uninstalled; out of laptop\n" +
		"kit-config ok committed and pushed laptop/declarations\nNothing needs attention\n"
	if out != want || code != 0 {
		t.Errorf("kit brew uninstall printed\n%s%s exit %d\nwant\n%s", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "" {
		t.Errorf("laptop's formulae = %q, want go out of it", got)
	}
}

// One declared for every Mac needs --shared: without it, nothing runs.
func TestAnUninstallLineOfASharedThingNeedsShared(t *testing.T) {
	w := laptopWorld(t)
	_, errOut, code := w.run(t, "brew", "uninstall", "jq")
	want := "kit: jq is declared for every Mac, in shared: --shared takes it out of every Mac's list; nothing was uninstalled\n"
	if errOut != want || code == 0 || len(w.fake.Calls()) != 0 {
		t.Errorf("kit brew uninstall jq: %q, exit %d, running %q; want\n%s", errOut, code, w.fake.Calls(), want)
	}
}

// With --shared, one declared for every Mac comes out of the shared file;
// one still installed, as brew failed, stays declared.
func TestAnUninstallLineShared(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "jq", "go").Exits(1).PrintsToStderr("Error: go is required by something")
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints("go\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n")
	w.fake.On("brew", "info", "--json=v2", "--cask", "jq").Exits(1).PrintsToStderr(`Error: No available cask with the name "jq".`)
	w.expectSync(t, []string{"shared/declarations"}, "kit brew uninstall jq (laptop)")

	out, _, code := w.run(t, "brew", "uninstall", "--shared", "jq", "go")
	for _, want := range []string{
		"kit brew uninstall jq go · laptop\n",
		"brew failed couldn't uninstall: Error: go is required by something\n",
		"jq ok uninstalled; out of shared\n",
		"go failed couldn't undeclare: it's still installed\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit brew uninstall --shared printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "go\n" {
		t.Errorf("laptop's formulae = %q, want go still declared", got)
	}
}

// A name brew installs under another, as an alias names a formula, is
// found by its full name, and declared as given.
func TestAnInstallLineFindsAnAliasByItsFullName(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "rg")
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\nripgrep\n")
	w.fake.On("brew", "info", "--json=v2", "--formula", "rg").Prints(`{"formulae": [{"name": "ripgrep", "full_name": "ripgrep", "aliases": ["rg"], "oldnames": []}], "casks": []}`)
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install rg (laptop)")

	out, _, code := w.run(t, "brew", "install", "rg")
	if want := "rg ok installed as a formula; declared in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install rg printed\n%s exit %d; want it to hold %q", out, code, want)
	}
}

// The commit and push are one, of every name's change.
func TestAnInstallLineCommitsOnce(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ffmpeg", "firefox")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install ffmpeg, firefox (laptop)")
	if _, errOut, code := w.run(t, "brew", "install", "ffmpeg", "firefox"); code != 0 {
		t.Fatalf("kit brew install: exit %d, %s", code, errOut)
	}
	commits := slices.DeleteFunc(w.fake.Calls(), func(call string) bool { return !strings.Contains(call, " commit --quiet ") })
	if len(commits) != 1 {
		t.Errorf("committed %q, want one commit", commits)
	}
}

// kit's --json, before brew, prints one document.
func TestAnInstallLineAsJSON(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ffmpeg")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install ffmpeg (laptop)")
	out, errOut, code := w.run(t, "--json", "brew", "install", "ffmpeg")
	if code != 0 || !strings.HasPrefix(out, "{") || !strings.Contains(out, `"ffmpeg"`) {
		t.Errorf("kit --json brew install printed\n%s%s exit %d; want a document", out, errOut, code)
	}
}

// At a terminal, a cask that installs through a package has kit ask for
// the password first, as kit cask add does, and Homebrew keeps it.
func TestAnInstallLineAtATerminalAsksForThePasswordFirst(t *testing.T) {
	w := lineWorld(t)
	w.terminal = true
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\nzoom\n")
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	w.expectPassword()
	w.fake.On("brew", "install", "--cask", "zoom")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install zoom (laptop)")
	if _, errOut, code := w.run(t, "brew", "install", "--cask", "zoom"); code != 0 {
		t.Fatalf("kit brew install --cask zoom: exit %d, %s", code, errOut)
	}
	calls := w.fake.Calls()
	if asked, install := slices.Index(calls, askedFor), slices.Index(calls, "brew install --cask zoom"); asked < 0 || install < asked {
		t.Errorf("ran %q; want the password asked for before the install", calls)
	}
	for _, cmd := range w.fake.Commands() {
		if cmd.String() == "brew install --cask zoom" && !slices.Contains(cmd.Env, "HOMEBREW_SUDO_CHECKED=1") {
			t.Errorf("brew install ran with %q; want Homebrew to keep the password given", cmd.Env)
		}
	}
}
