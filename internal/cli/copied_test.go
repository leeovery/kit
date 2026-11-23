package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// listedFormulae and listedCasks are what laptopWorld has installed, as
// brew lists them.
const (
	listedFormulae = "go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n"
	listedCasks    = "ghostty\nfirefox\n"
)

// itermInfo is what brew info says of a cask laptopWorld hasn't installed.
const itermInfo = `{"formulae": [], "casks": [{"token": "iterm2", "full_token": "iterm2", "old_tokens": [], "artifacts": [{"app": ["iTerm.app"]}]}]}`

// lineWorld is laptopWorld with brew update scripted, as an install line
// runs after it, and iterm2 a cask brew knows, and no formula.
func lineWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "info", "--json=v2", "--cask", "iterm2").Prints(itermInfo)
	w.notAFormula("iterm2")
	return w
}

// notAFormula has brew info know no formula of each of names.
func (w *world) notAFormula(names ...string) {
	for _, name := range names {
		w.fake.On("brew", "info", "--json=v2", "--formula", name).Exits(1).PrintsToStderr(`Error: No available formula with the name "` + name + `".`)
	}
}

// notACask has brew info know no cask of each of names.
func (w *world) notACask(names ...string) {
	for _, name := range names {
		w.fake.On("brew", "info", "--json=v2", "--cask", name).Exits(1).PrintsToStderr(`Error: No available cask with the name "` + name + `".`)
	}
}

// listedAfter has brew list, once a line's run, formulae and casks, a line
// each, besides those laptopWorld has; before it, those alone.
func (w *world) listedAfter(formulae, casks string) {
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints(listedFormulae).Then().Prints(listedFormulae + formulae)
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(listedCasks).Then().Prints(listedCasks + casks)
}

// installRan reports whether any brew install ran.
func installRan(w *world) bool {
	return slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "brew install") })
}

// A line copied for brew install runs as given, then each name it
// installed is declared as what brew installed it as: a cask, given
// without --cask, as brew takes it.
func TestAnInstallLineDeclaresACaskGivenWithoutCask(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "iterm2")
	w.listedAfter("", "iterm2\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install iterm2 (laptop)")

	out, errOut, code := w.run(t, "brew", "install", "iterm2")
	want := "kit brew install iterm2 · laptop\nbrew ok installed\niterm2 ok installed as a cask; declared in laptop\n" +
		"kit-config ok committed and pushed laptop/declarations\nNothing needs attention\n"
	if out != want || errOut != "" || code != 0 {
		t.Errorf("kit brew install printed\n%s%s exit %d\nwant\n%s", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "iterm2\n" {
		t.Errorf("laptop's casks = %q, want iterm2", got)
	}
}

// A formula's declared as one, with its note.
func TestAnInstallLineDeclaresAFormula(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ripgrep")
	w.listedAfter("ripgrep\n", "")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install ripgrep (laptop): for search")

	out, _, code := w.run(t, "brew", "install", "ripgrep", "--note", "for search")
	if want := "ripgrep ok installed as a formula; declared in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install printed\n%s exit %d; want it to hold %q", out, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "go\nripgrep   # for search\n" {
		t.Errorf("laptop's formulae = %q", got)
	}
}

// A tap's cask, with --force, is declared with its tap, the flag the
// install's alone; --note= is kit's too.
func TestAnInstallLineDeclaresATapsCask(t *testing.T) {
	w := privateWorld(t, "200")
	w.fake.On("brew", "install", "--cask", "--force", privateCask)
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(listedCasks).Then().Prints(listedCasks + privateCask + "\n")
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

// A tap's cask whose download is private is held for Homebrew's GitHub
// token, judged before the line runs, and the line runs with the token
// set from kit's own secrets, as kit cask add installs it.
func TestAnInstallLineGetsAPrivateDownloadsToken(t *testing.T) {
	w := privateWorld(t, "404")
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(listedCasks).Then().Prints(listedCasks + privateCask + "\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install owner/tools/app (laptop)")

	out, errOut, code := w.run(t, "brew", "install", "--cask", privateCask)
	if want := "owner/tools/app ok installed; declared in laptop; held for HOMEBREW_GITHUB_API_TOKEN: its download is private\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := tokensOf(t, w, "brew install --cask "+privateCask); !slices.Equal(got, []string{"plain-token-value"}) {
		t.Errorf("brew install ran with the token %q, want kit's own", got)
	}
	if strings.Contains(out, "plain-token-value") {
		t.Errorf("kit printed the token's value:\n%s", out)
	}
}

// Of several names, one installed already is declared, saying so; one
// that didn't install isn't declared; and the run fails as brew did.
func TestAnInstallLineThatPartlyFails(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ffmpeg", "hello", "nosuch").Exits(1).PrintsToStderr(`Error: No available formula with the name "nosuch".`)
	w.notACask("nosuch")
	w.listedAfter("hello\n", "")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install ffmpeg, hello (laptop)")

	out, _, code := w.run(t, "brew", "install", "ffmpeg", "hello", "nosuch")
	for _, want := range []string{
		"brew failed couldn't install: Error: No available formula with the name \"nosuch\".\n",
		"ffmpeg ok already installed as a formula; declared in laptop\n",
		"hello ok installed as a formula; declared in laptop\n",
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
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "ffmpeg\ngo\nhello\n" {
		t.Errorf("laptop's formulae = %q, want ffmpeg and hello added", got)
	}
}

// --shared declares for every Mac, taking it out of each Mac's own; a name
// declared already isn't declared again; every change is one commit.
func TestAnInstallLineSharedAndDeclaredAlready(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ffmpeg", "jq", "iterm2")
	w.listedAfter("", "iterm2\n")
	w.expectSync(t, []string{"shared/declarations", "studio/declarations"}, "kit brew install ffmpeg, iterm2 (laptop)")

	out, _, code := w.run(t, "brew", "install", "ffmpeg", "--shared", "jq", "iterm2")
	for _, want := range []string{
		"kit brew install ffmpeg jq iterm2 · laptop\n",
		"ffmpeg ok already installed as a formula; declared in shared, out of studio\n",
		"jq ok already installed as a formula; declared already, in shared\n",
		"iterm2 ok installed as a cask; declared in shared\n",
		"kit-config ok committed and pushed shared/declarations, studio/declarations\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit brew install --shared printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if got := w.readSection(t, "shared", "homebrew casks"); got != "ghostty\niterm2\n" {
		t.Errorf("shared's casks = %q", got)
	}
	if got := w.readSection(t, "studio", "homebrew formulae"); got != "" {
		t.Errorf("studio's formulae = %q, want ffmpeg out of it", got)
	}
	commits := slices.DeleteFunc(w.fake.Calls(), func(call string) bool { return !strings.Contains(call, " commit --quiet ") })
	if len(commits) != 1 {
		t.Errorf("committed %q, want one commit", commits)
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

// What kit <kind> add would refuse refuses the line whole, before it runs:
// a declare that can't be, and, without a terminal to ask at, a cask that
// needs an administrator's password.
func TestAnInstallLineIsRefusedBeforeItRuns(t *testing.T) {
	w := privateWorld(t, "404")
	_, errOut, code := w.run(t, "brew", "install", "--shared", "--cask", privateCask)
	if !strings.HasPrefix(errOut, "kit: owner/tools/app can't be declared: ") || !strings.HasSuffix(errOut, "; nothing was installed\n") || code == 0 || installRan(w) {
		t.Errorf("kit brew install --shared: %q, exit %d, running %q; want it refused, nothing installed", errOut, code, w.fake.Calls())
	}

	w = lineWorld(t)
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	_, errOut, code = w.run(t, "brew", "install", "--cask", "zoom")
	want := "kit: zoom needs an administrator's password: run kit brew install at a terminal; nothing was installed\n"
	if errOut != want || code == 0 || installRan(w) {
		t.Errorf("kit brew install --cask zoom: %q, exit %d, running %q; want\n%s", errOut, code, w.fake.Calls(), want)
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
	for _, want := range []string{"kit brew install <name>... [flags]", "kit adds --adopt"} {
		if code != 0 || !strings.Contains(out, want) || len(w.fake.Calls()) != 0 {
			t.Errorf("kit brew install --help printed\n%s exit %d, running %q; want it to hold %q", out, code, w.fake.Calls(), want)
		}
	}
	out, _, _ = w.run(t, "brew", "uninstall", "--help")
	if want := "without it, the whole line is refused before anything runs"; !strings.Contains(strings.Join(strings.Fields(out), " "), want) {
		t.Errorf("kit brew uninstall --help printed\n%s\nwant it to hold %q", out, want)
	}
}

// A dry run declares nothing, and says so.
func TestAnInstallLineDryRun(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "-n", "ripgrep")
	out, errOut, code := w.run(t, "brew", "install", "-n", "ripgrep")
	want := "kit brew install -n ripgrep · laptop\nbrew ok a dry run: nothing declared or undeclared\nkit-config ok nothing changed\nNothing needs attention\n"
	if out != want || code != 0 {
		t.Errorf("kit brew install -n printed\n%s%s exit %d\nwant\n%s", out, errOut, code, want)
	}
}

// A name brew installs under another, as an alias names a formula, is
// found by its full name, and declared as given.
func TestAnInstallLineFindsAnAliasByItsFullName(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "rg")
	w.listedAfter("ripgrep\n", "")
	w.fake.On("brew", "info", "--json=v2", "--formula", "rg").Prints(`{"formulae": [{"name": "ripgrep", "full_name": "ripgrep", "aliases": ["rg"], "oldnames": []}], "casks": []}`)
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install rg (laptop)")

	out, _, code := w.run(t, "brew", "install", "rg")
	if want := "rg ok installed as a formula; declared in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install rg printed\n%s exit %d; want it to hold %q", out, code, want)
	}
}

// kit's --json, before brew, prints one document.
func TestAnInstallLineAsJSON(t *testing.T) {
	w := lineWorld(t)
	w.fake.On("brew", "install", "ripgrep")
	w.listedAfter("ripgrep\n", "")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install ripgrep (laptop)")
	out, errOut, code := w.run(t, "--json", "brew", "install", "ripgrep")
	if code != 0 || !strings.HasPrefix(out, "{") || !strings.Contains(out, `"ripgrep"`) {
		t.Errorf("kit --json brew install printed\n%s%s exit %d; want a document", out, errOut, code)
	}
}

// At a terminal, a cask that installs through a package has kit ask for
// the password first, as kit cask add does, looked up of itself in a line
// with a formula, and Homebrew keeps it.
func TestAnInstallLineAtATerminalAsksForThePasswordFirst(t *testing.T) {
	w := lineWorld(t)
	w.terminal = true
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	w.notAFormula("zoom")
	w.listedAfter("hello\n", "zoom\n")
	w.expectPassword()
	w.fake.On("brew", "install", "hello", "zoom")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install hello, zoom (laptop)")
	if _, errOut, code := w.run(t, "brew", "install", "hello", "zoom"); code != 0 {
		t.Fatalf("kit brew install hello zoom: exit %d, %s", code, errOut)
	}
	calls := w.fake.Calls()
	if asked, install := slices.Index(calls, askedFor), slices.Index(calls, "brew install hello zoom"); asked < 0 || install < asked {
		t.Errorf("ran %q; want the password asked for before the install", calls)
	}
	for _, cmd := range w.fake.Commands() {
		if cmd.String() == "brew install hello zoom" && !slices.Contains(cmd.Env, "HOMEBREW_SUDO_CHECKED=1") {
			t.Errorf("brew install ran with %q; want Homebrew to keep the password given", cmd.Env)
		}
	}
}

// An app kit prefs kept settings for has them restored straight after
// the line installs it, before it's first opened, as kit cask add does.
func TestAnInstallLineRestoresTheSettingsKeptForAnApp(t *testing.T) {
	w := keptWorld(t)
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(listedCasks).Then().Prints(listedCasks + "example-app\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install example-app (laptop)")
	out, errOut, code := w.run(t, "brew", "install", "--cask", "example-app")
	if want := "brew ok installed; its saved settings restored\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if !ran(w, "defaults import com.example.app "+filepath.Join(w.home, ".local", "share", "kit", "prefs", "laptop", "domains", "com.example.app.plist")) {
		t.Errorf("ran %q, want its settings imported", w.fake.Calls())
	}
}

// A cask whose app is there already, installed by hand, is taken over,
// kit adding --adopt to the line, as its own cask installs do.
func TestAnInstallLineAdoptsAnAppInstalledByHand(t *testing.T) {
	w := handWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.notAFormula("editor")
	w.fake.On("brew", "install", "--adopt", "editor")
	w.listedAfter("", "editor\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install editor (laptop)")
	out, errOut, code := w.run(t, "brew", "install", "editor")
	if want := "editor ok installed as a cask; declared in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install editor printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
}

// An uninstall line runs as given, then each name no longer installed is
// undeclared.
func TestAnUninstallLineUndeclares(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "--force", "go")
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints(listedFormulae).Then().Prints("jq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n")
	w.notACask("go")
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

// A tap's thing given by its short name is undeclared by its full one, as
// it's declared.
func TestAnUninstallLineUndeclaresByTheNameDeclared(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "info", "--json=v2", "--formula", "tool").Prints(`{"formulae": [{"name": "tool", "full_name": "owner/tap/tool", "aliases": [], "oldnames": []}], "casks": []}`)
	w.fake.On("brew", "uninstall", "tool")
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints(listedFormulae).Then().Prints("go\njq\noniguruma\nnode@20\nffmpeg\n")
	w.expectSync(t, []string{"shared/declarations"}, "kit brew uninstall tool (laptop)")

	out, errOut, code := w.run(t, "brew", "uninstall", "--shared", "tool")
	if want := "tool ok uninstalled; out of shared\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew uninstall --shared tool printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "shared", "homebrew formulae"); got != "# Shell\njq\n" {
		t.Errorf("shared's formulae = %q, want owner/tap/tool out of it", got)
	}
}

// A name declared by an alias is undeclared by it, as brew resolves both,
// before the line runs.
func TestAnUninstallLineUndeclaresAnAlias(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "go\nrg\n")
	formula := func(name, full string, aliases string) string {
		return `{"name": "` + name + `", "full_name": "` + full + `", "aliases": [` + aliases + `], "oldnames": []}`
	}
	w.fake.On("brew", "tap").Prints("owner/tap\n")
	w.fake.On("brew", "info", "--json=v2", "--formula", "ripgrep", "jq", "owner/tap/tool", "go", "rg").Prints(`{"formulae": [` +
		formula("ripgrep", "ripgrep", `"rg"`) + ", " + formula("jq", "jq", "") + ", " + formula("tool", "owner/tap/tool", "") + ", " + formula("go", "go", "") + `], "casks": []}`)
	w.fake.On("brew", "info", "--json=v2", "--cask", "ripgrep", "ghostty").Exits(1).PrintsToStderr(`Error: No available cask with the name "ripgrep".`)
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints(listedFormulae + "ripgrep\n").Then().Prints(listedFormulae)
	w.fake.On("brew", "uninstall", "ripgrep")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew uninstall ripgrep (laptop)")

	out, errOut, code := w.run(t, "brew", "uninstall", "ripgrep")
	if want := "ripgrep ok uninstalled; out of laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew uninstall ripgrep printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "go\n" {
		t.Errorf("laptop's formulae = %q, want rg out of it", got)
	}
}

// One declared for every Mac needs --shared: without it, the line's
// refused before it runs, as one declared by another of its names is.
func TestAnUninstallLineOfASharedThingNeedsShared(t *testing.T) {
	for _, name := range []string{"jq", "tool"} {
		w := laptopWorld(t)
		w.fake.On("brew", "info", "--json=v2", "--formula", "tool").Prints(`{"formulae": [{"name": "tool", "full_name": "owner/tap/tool", "aliases": [], "oldnames": []}], "casks": []}`)
		_, errOut, code := w.run(t, "brew", "uninstall", name)
		declared := map[string]string{"jq": "jq", "tool": "owner/tap/tool"}[name]
		want := "kit: " + declared + " is declared for every Mac, in shared: --shared takes it out of every Mac's list; nothing was uninstalled\n"
		if errOut != want || code == 0 || slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "brew uninstall") }) {
			t.Errorf("kit brew uninstall %s: %q, exit %d, running %q; want\n%s", name, errOut, code, w.fake.Calls(), want)
		}
	}
}

// With --shared, one declared for every Mac comes out of the shared file;
// one still installed, as brew failed, stays declared.
func TestAnUninstallLineShared(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "jq", "go").Exits(1).PrintsToStderr("Error: go is required by something")
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints(listedFormulae).Then().Prints("go\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n")
	w.notACask("jq")
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

// A name neither installed nor declared has nothing to undeclare.
func TestAnUninstallLineOfNothing(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "--cask", "iterm2").Exits(1).PrintsToStderr("Error: Cask 'iterm2' is not installed.")
	w.fake.On("brew", "info", "--json=v2", "--cask", "iterm2", "ghostty").Prints(itermInfo)
	w.fake.On("brew", "info", "--json=v2", "--cask", "iterm2").Prints(itermInfo)
	out, _, _ := w.run(t, "brew", "uninstall", "--cask", "iterm2")
	if want := "iterm2 ok wasn't installed or declared: nothing to undeclare\n"; !strings.Contains(out, want) {
		t.Errorf("kit brew uninstall printed\n%s\nwant it to hold %q", out, want)
	}
}

// An uninstall line takes --note for no one: kit refuses it.
func TestAnUninstallLineRefusesANote(t *testing.T) {
	w := laptopWorld(t)
	_, errOut, code := w.run(t, "brew", "uninstall", "go", "--note", "done with it")
	if !strings.HasPrefix(errOut, "kit: --note records why a name's declared") || code == 0 || len(w.fake.Calls()) != 0 {
		t.Errorf("kit brew uninstall --note: %q, exit %d, running %q; want it refused", errOut, code, w.fake.Calls())
	}
}

// Uninstalling a cask does kit's own clean-up, as kit cask remove does:
// with --zap, the saved settings and the domains of what it leaves go;
// without, they're kept, saying so.
func TestAnUninstallLineOfACaskCleansUp(t *testing.T) {
	w := leavingWorld(t, true)
	w.notAFormula("example-app")
	w.fake.On("brew", "uninstall", "--zap", "example-app")
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nexample-app\n").Then().Prints("ghostty\n")
	w.fake.On("defaults", "delete", "com.example.app")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew uninstall example-app (laptop)")
	out, errOut, code := w.run(t, "brew", "uninstall", "--zap", "example-app")
	if want := "example-app ok uninstalled with its files and settings; out of laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew uninstall --zap printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if !ran(w, "defaults delete com.example.app") || ran(w, "defaults delete com.stable.example.app") {
		t.Errorf("ran %q; want its own domain deleted, never another app's", w.fake.Calls())
	}

	w = leavingWorld(t, true)
	w.notAFormula("example-app")
	w.fake.On("brew", "uninstall", "example-app")
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nexample-app\n").Then().Prints("ghostty\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew uninstall example-app (laptop)")
	out, _, _ = w.run(t, "brew", "uninstall", "example-app")
	if want := "example-app ok uninstalled, its files kept: --zap removes them; out of laptop\n"; !strings.Contains(out, want) || ran(w, "defaults delete com.example.app") {
		t.Errorf("kit brew uninstall printed\n%s\nwant it to hold %q, nothing deleted", out, want)
	}
}
