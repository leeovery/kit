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
	w.notACask("ripgrep")
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
	w.notACask("nosuch", "hello")
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
	w.notACask("rg")
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
	w.notACask("ripgrep")
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
	w.notACask("hello")
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
	if want := "brew ok installed; Example App's saved settings restored\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if !ran(w, "defaults import com.example.app "+filepath.Join(w.home, ".local", "share", "kit", "prefs", "laptop", "domains", "com.example.app.plist")) {
		t.Errorf("ran %q, want its settings imported", w.fake.Calls())
	}
}

// The settings kept for each app a line installs are restored even when
// brew fails on another name, each app named.
func TestAnInstallLineThatFailsStillRestoresKeptSettings(t *testing.T) {
	w := keptWorld(t)
	w.write(t, filepath.Join(".local", "state", "kit", "prefs.json"), `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.app", "com.example.other"],
		"why": {"com.example.app": "kept till its app is installed", "com.example.other": "kept till its app is installed"}}}`)
	clone := filepath.Join(w.home, ".local", "share", "kit", "prefs")
	w.write(t, filepath.Join(".local", "share", "kit", "prefs", "laptop", "domains", "com.example.other.plist"), exampleSettings)
	otherInfo := `{"token": "other-app", "full_token": "other-app", "old_tokens": [], "artifacts": [{"app": ["Other App.app"]}]}`
	w.fake.On("brew", "info", "--json=v2", "--cask", "other-app").Prints(`{"formulae": [], "casks": [` + otherInfo + `]}`)
	w.fake.On("brew", "info", "--json=v2", "--cask", "example-app", "other-app").Prints(strings.TrimSuffix(exampleInfo, `]}`) + `, ` + otherInfo + `]}`)
	w.notACask("nosuch")
	w.fake.On("brew", "install", "--cask", "example-app", "other-app", "nosuch").Does(func() {
		for name, id := range map[string]string{"Example App": "com.example.app", "Other App": "com.example.other"} {
			w.write(t, filepath.Join("Applications", name+".app", "Contents", "Info.plist"),
				`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>`+id+`</string></dict></plist>`)
		}
	}).Exits(1).PrintsToStderr(`Error: Cask 'nosuch' is unavailable: No Cask with this name exists.`)
	w.fake.On("osascript", "-e", `application id "com.example.other" is running`).Prints("false\n")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", filepath.Join(w.home, "Applications", "Other App.app"))
	w.fake.On("defaults", "export", "com.example.other", "-").Prints(noSettings).Then().Prints(exampleSettings)
	w.fake.On("defaults", "delete", "com.example.other")
	w.fake.On("defaults", "import", "com.example.other", filepath.Join(clone, "laptop", "domains", "com.example.other.plist"))
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(listedCasks).Then().Prints(listedCasks + "example-app\nother-app\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install example-app, other-app (laptop)")

	out, errOut, code := w.run(t, "brew", "install", "--cask", "example-app", "other-app", "nosuch")
	want := "brew failed couldn't install: Error: Cask 'nosuch' is unavailable: No Cask with this name exists.; " +
		"Example App's saved settings restored; Other App's saved settings restored\n"
	if !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit brew install printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	for _, id := range []string{"com.example.app", "com.example.other"} {
		if !ran(w, "defaults import "+id+" "+filepath.Join(clone, "laptop", "domains", id+".plist")) {
			t.Errorf("ran %q, want %s's settings imported", w.fake.Calls(), id)
		}
	}
}

// casksWorld is laptopWorld with example-editor a cask of version 3.2, its
// app, Example Editor.app, at version have there already, installed by
// hand, and brew install --adopt failing on it, as Homebrew does when the
// app there differs from its download.
func casksWorld(t *testing.T, have string) *world {
	t.Helper()
	w := laptopWorld(t)
	w.homebrewCache(t, `{"protected":"e30","signature":"c2ln"}
{"casks": {"example-editor": {"version": "3.2", "raw_artifacts": [[":uninstall", {":quit": "com.example.editor"}], [":app", ["Example Editor.app"]]]}}}`)
	w.installAppOf(t, "Example Editor", "com.example.editor", have)
	w.fake.On("brew", "info", "--json=v2", "--cask", "example-editor").Prints(`{"formulae": [], "casks": [{"token": "example-editor", "full_token": "example-editor", "old_tokens": [], "artifacts": [{"app": ["Example Editor.app"]}]}]}`)
	w.notAFormula("example-editor")
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--adopt", "example-editor").Exits(1).PrintsToStderr("Error: example-editor: It seems the existing App is different from the one being installed.\n")
	return w
}

// An app there already that's an older copy of the same major version is
// replaced, as kit's own cask installs replace it; one of another major
// version isn't, saying why.
func TestAnInstallLineReplacesAnOlderCopy(t *testing.T) {
	w := casksWorld(t, "3.1")
	w.fake.On("brew", "install", "--cask", "--force", "example-editor")
	w.listedAfter("", "example-editor\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install example-editor (laptop)")
	out, errOut, code := w.run(t, "brew", "install", "example-editor")
	if want := "brew ok installed\nexample-editor ok installed as a cask; declared in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew install printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}

	w = casksWorld(t, "2.0")
	out, _, code = w.run(t, "brew", "install", "example-editor")
	want := "brew failed couldn't install: Example Editor.app is 2.0 and the cask's is 3.2: kit replaces only an older copy of the same major version\n"
	if !strings.Contains(out, want) || code != 1 || ran(w, "brew install --cask --force example-editor") {
		t.Errorf("kit brew install printed\n%s exit %d, ran %q; want it to hold %q, nothing replaced", out, code, w.fake.Calls(), want)
	}
}

// An app there that isn't the cask's own refuses the line before anything
// runs, with the other refusals, as kit cask add refuses it; a line with a
// flag that changes where or how a cask installs runs as given instead.
func TestAnInstallLineRefusesAnAppThatIsntTheCasks(t *testing.T) {
	w := casksWorld(t, "3.1")
	w.installAppOf(t, "Example Editor", "dev.fork.editor", "3.1")
	_, errOut, code := w.run(t, "brew", "install", "example-editor")
	want := "kit: Example Editor.app is already in ~/Applications and isn't the cask's (dev.fork.editor); nothing was installed\n"
	if errOut != want || code == 0 || installRan(w) || ran(w, "brew update --quiet") {
		t.Errorf("kit brew install printed %q, exit %d, ran %q; want\n%s", errOut, code, w.fake.Calls(), want)
	}

	w = casksWorld(t, "3.1")
	w.installAppOf(t, "Example Editor", "dev.fork.editor", "3.1")
	w.fake.On("brew", "install", "--no-quarantine", "example-editor")
	w.listedAfter("", "example-editor\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew install example-editor (laptop)")
	if _, errOut, code := w.run(t, "brew", "install", "--no-quarantine", "example-editor"); code != 0 {
		t.Errorf("kit brew install --no-quarantine: exit %d, %s; want the line run as given", code, errOut)
	}
}

// A dry run readies nothing: no brew update, no tap added; and runs with
// the secrets the line declared for its name is held for, as an install
// of it would.
func TestAnInstallLineDryRunsWithItsToken(t *testing.T) {
	w := privateWorld(t, "404")
	w.writeSection(t, "laptop", "homebrew casks --secret "+tokenName, privateCask+"\n")
	w.fake.On("brew", "install", "-n", "--cask", privateCask)
	if _, errOut, code := w.run(t, "brew", "install", "-n", "--cask", privateCask); code != 0 {
		t.Fatalf("kit brew install -n: exit %d, %s", code, errOut)
	}
	if got := tokensOf(t, w, "brew install -n --cask "+privateCask); !slices.Equal(got, []string{"plain-token-value"}) {
		t.Errorf("the dry run ran with the token %q, want kit's own", got)
	}
	if ran(w, "brew update --quiet") || ran(w, "brew tap") {
		t.Errorf("ran %q; want no update, and no tap looked for", w.fake.Calls())
	}
}

// A name no kind knew before the line ran, installed, is judged as kit
// <kind> add judges it before it's declared: refused, it isn't declared.
func TestAnInstallLineJudgesANameItFoundAfter(t *testing.T) {
	w := privateWorld(t, "404")
	unknown := `Error: No available cask with the name "` + privateCask + `".`
	w.fake.On("brew", "info", "--json=v2", "--cask", privateCask).Exits(1).PrintsToStderr(unknown).Then().Exits(1).PrintsToStderr(unknown).Then().Prints(privateInfo)
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(listedCasks).Then().Prints(listedCasks + privateCask + "\n")
	out, errOut, code := w.run(t, "brew", "install", "--shared", "--cask", privateCask)
	want := "owner/tools/app failed installed, but couldn't declare: "
	if !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit brew install --shared printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "shared", "homebrew casks"); got != "ghostty\n" {
		t.Errorf("shared's casks = %q, want nothing declared", got)
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

// A formula uninstalled is undeclared, and a cask of the same name, still
// installed, stays declared, as it's undeclared only once its own kind
// has it no more.
func TestAnUninstallLineKeepsAnotherKindsThingDeclared(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "foo\ngo\n")
	w.writeSection(t, "laptop", "homebrew casks", "owner/tap/foo\n")
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints(listedFormulae + "foo\n").Then().Prints(listedFormulae)
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(listedCasks + "owner/tap/foo\n")
	w.fake.On("brew", "uninstall", "foo")
	w.fake.On("brew", "info", "--json=v2", "--formula", "foo").Prints(`{"formulae": [{"name": "foo", "full_name": "foo", "aliases": [], "oldnames": []}], "casks": []}`)
	w.expectSync(t, []string{"laptop/declarations"}, "kit brew uninstall foo (laptop)")

	out, errOut, code := w.run(t, "brew", "uninstall", "foo")
	if want := "foo ok uninstalled; out of laptop; owner/tap/foo stays declared, still installed\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit brew uninstall foo printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "go\n" {
		t.Errorf("laptop's formulae = %q, want foo out of it", got)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "owner/tap/foo\n" {
		t.Errorf("laptop's casks = %q, want owner/tap/foo still declared", got)
	}
}

// A name whose kind can't be listed after the line ran stays declared,
// saying why.
func TestAnUninstallLineThatCantTellUndeclaresNothing(t *testing.T) {
	w := leavingWorld(t, false)
	w.notAFormula("example-app")
	w.fake.On("brew", "uninstall", "--zap", "example-app").Exits(1).PrintsToStderr("Error: example-app: uninstall failed")
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nexample-app\n").Then().Exits(1).PrintsToStderr("Error: brew list failed")

	out, errOut, code := w.run(t, "brew", "uninstall", "--zap", "example-app")
	if want := "example-app failed couldn't tell whether it's installed: brew list --cask --full-name -1 exited 1: Error: brew list failed\n"; code != 1 || !strings.Contains(out, want) {
		t.Errorf("kit brew uninstall --zap printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "example-app\n" {
		t.Errorf("laptop's casks = %q, want example-app still declared", got)
	}
}
