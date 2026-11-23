package cli_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/runner/runnertest"
)

// listsAround has list, a program's list of what it's installed, print
// before, and after once line, the program's line, has run, as an install
// changes what it lists. It returns line's script.
func (w *world) listsAround(line, list []string, before, after string) *runnertest.Script {
	w.fake.On(list[0], list[1:]...).Prints(before)
	return w.fake.On(line[0], line[1:]...).Does(func() { w.fake.On(list[0], list[1:]...).Prints(after) })
}

// npmLS is how kit asks npm what's installed globally.
var npmLS = []string{"npm", "ls", "--global", "--depth=0", "--json"}

// npmListed is npm's list of packages.
func npmListed(packages string) string { return `{"dependencies": {` + packages + `}}` }

// A line copied for npm install, by any of npm's names for it, runs as
// given, from the home folder, then each package is declared as kit npm
// add declares it: a version kept, a scope's package by its scope.
func TestAnNPMInstallLineDeclaresItsPackages(t *testing.T) {
	w := laptopWorld(t)
	w.listsAround([]string{"npm", "i", "-g", "typescript@5", "@scope/tool", "docx"}, npmLS, npmListed(`"docx": {}`), npmListed(`"docx": {}, "typescript": {}, "@scope/tool": {}`))
	w.expectSync(t, []string{"laptop/declarations"}, "kit npm install typescript@5, @scope/tool, docx (laptop)")

	out, errOut, code := w.run(t, "npm", "i", "-g", "typescript@5", "@scope/tool", "docx")
	want := "kit npm install -g typescript@5 @scope/tool docx · laptop\nnpm ok installed\n" +
		"typescript@5 ok installed; declared in laptop\n@scope/tool ok installed; declared in laptop\ndocx ok already installed; declared in laptop\n" +
		"kit-config ok committed and pushed laptop/declarations\nNothing needs attention\n"
	if out != want || code != 0 {
		t.Errorf("kit npm i -g printed\n%s%s exit %d\nwant\n%s", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "npm packages"); got != "docx\n@scope/tool\ntypescript@5\n" {
		t.Errorf("laptop's npm packages = %q", got)
	}
	for _, c := range w.fake.Commands() {
		if c.String() == "npm i -g typescript@5 @scope/tool docx" && c.Dir != w.home {
			t.Errorf("ran npm in %q, want the home folder, as kit's own npm installs run", c.Dir)
		}
	}
}

// A line that isn't global, or gives npm something other than a package
// from its registry, is refused before anything runs.
func TestAnNPMLineIsRefusedBeforeItRuns(t *testing.T) {
	for _, line := range [][]string{
		{"install", "typescript"},
		{"uninstall", "typescript"},
		{"install", "-g", "owner/repo"},
		{"install", "-g", "github:owner/repo"},
		{"install", "-g", "git+https://example.com/tool.git"},
		{"install", "-g", "./tool"},
		{"install", "-g", "tool-1.0.0.tgz"},
		{"install", "-g", "file:../tool"},
		{"install", "-g", "tool@link:../tool"},
		{"install", "-g"},
	} {
		w := laptopWorld(t)
		_, errOut, code := w.run(t, append([]string{"npm"}, line...)...)
		if code == 0 || !strings.HasPrefix(errOut, "kit: ") || len(w.fake.Calls()) != 0 {
			t.Errorf("kit npm %q: %q, exit %d, running %q; want it refused, nothing run", line, errOut, code, w.fake.Calls())
		}
	}
	w := laptopWorld(t)
	_, errOut, _ := w.run(t, "npm", "install", "typescript")
	if want := "kit: the line isn't global: kit declares npm's global packages alone, so give it -g\n"; errOut != want {
		t.Errorf("kit npm install typescript: %q, want\n%s", errOut, want)
	}
}

// --shared declares for every Mac; --location global, or
// --location=global, is as global as -g.
func TestAnNPMInstallLineShared(t *testing.T) {
	for _, location := range [][]string{{"--location", "global"}, {"--location=global"}} {
		w := laptopWorld(t)
		w.listsAround(slices.Concat([]string{"npm", "install"}, location, []string{"typescript"}), npmLS, npmListed(""), npmListed(`"typescript": {}`))
		w.expectSync(t, []string{"shared/declarations"}, "kit npm install typescript (laptop): for scripts")

		out, errOut, code := w.run(t, slices.Concat([]string{"npm", "install", "--shared"}, location, []string{"typescript", "--note", "for scripts"})...)
		if want := "typescript ok installed; declared in shared\n"; !strings.Contains(out, want) || code != 0 {
			t.Errorf("kit npm install --shared %q printed\n%s%s exit %d; want it to hold %q", location, out, errOut, code, want)
		}
		if got := w.readSection(t, "shared", "npm packages"); got != "typescript   # for scripts\n" {
			t.Errorf("%q: shared's npm packages = %q", location, got)
		}
	}
}

// Each command's help is its program's: its usage, what its verb has for
// a dry run, and no other program's words.
func TestALinesHelpIsItsPrograms(t *testing.T) {
	w := laptopWorld(t)
	help := func(args ...string) string {
		out, _, code := w.run(t, append(args, "--help")...)
		if code != 0 {
			t.Fatalf("kit %q --help: exit %d", args, code)
		}
		return strings.Join(strings.Fields(out), " ")
	}
	if out := help("npm", "install"); !strings.Contains(out, "kit npm install -g <name>...") {
		t.Errorf("kit npm install --help printed\n%s\nwant its usage to show -g", out)
	}
	if out := help("mas", "install"); strings.Contains(out, "--dry-run") {
		t.Errorf("kit mas install --help printed\n%s\nwant no --dry-run: mas install has none", out)
	}
	if out := help("mas", "uninstall"); !strings.Contains(out, "With --dry-run, nothing is undeclared.") {
		t.Errorf("kit mas uninstall --help printed\n%s\nwant its --dry-run", out)
	}
	for _, args := range [][]string{{"npm", "install"}, {"npm", "uninstall"}, {"composer", "global", "require"}, {"composer", "global", "remove"}, {"gh", "extension", "install"}, {"gh", "extension", "remove"}} {
		out := help(args...)
		for _, brews := range []string{"short name or its full one", "administrator's password"} {
			if strings.Contains(out, brews) {
				t.Errorf("kit %q --help printed\n%s\nwant none of Homebrew's words %q", args, out, brews)
			}
		}
	}
}

// A dry run declares nothing.
func TestAnNPMInstallLineDryRun(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("npm", "install", "-g", "--dry-run", "typescript")
	out, errOut, code := w.run(t, "npm", "install", "-g", "--dry-run", "typescript")
	if want := "npm ok a dry run: nothing declared or undeclared\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit npm install --dry-run printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
}

// An uninstall line, by npm's names for it, undeclares a package by its
// name, declared with a version or not; a scope's package isn't another's
// of its short name.
func TestAnNPMUninstallLineUndeclares(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "npm packages", "@scope/tool\ntool\ntypescript@5\n")
	w.listsAround([]string{"npm", "un", "-g", "typescript", "tool"}, npmLS, npmListed(`"typescript": {}, "@scope/tool": {}, "tool": {}`), npmListed(`"@scope/tool": {}`))
	w.expectSync(t, []string{"laptop/declarations"}, "kit npm uninstall typescript, tool (laptop)")

	out, errOut, code := w.run(t, "npm", "un", "-g", "typescript", "tool")
	for _, want := range []string{"typescript ok uninstalled; out of laptop\n", "tool ok uninstalled; out of laptop\n"} {
		if !strings.Contains(out, want) || code != 0 {
			t.Errorf("kit npm un -g printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
		}
	}
	if got := w.readSection(t, "laptop", "npm packages"); got != "@scope/tool\n" {
		t.Errorf("laptop's npm packages = %q, want @scope/tool alone left", got)
	}
}

// A package declared for every Mac needs --shared to come out.
func TestAnNPMUninstallLineOfASharedPackageNeedsShared(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "shared", "npm packages", "typescript@5\n")
	w.fake.On(npmLS[0], npmLS[1:]...).Prints(npmListed(`"typescript": {}`))
	_, errOut, code := w.run(t, "npm", "uninstall", "-g", "typescript")
	want := "kit: typescript@5 is declared for every Mac, in shared: --shared takes it out of every Mac's list; nothing was uninstalled\n"
	if errOut != want || code == 0 || ran(w, "npm uninstall -g typescript") {
		t.Errorf("kit npm uninstall: %q, exit %d, running %q; want\n%s", errOut, code, w.fake.Calls(), want)
	}
}

// composerShow is how kit asks Composer what's required globally.
var composerShow = []string{"composer", "global", "show", "--direct", "--format=json"}

// composerListed is Composer's list of packages.
func composerListed(packages string) string { return `{"installed": [` + packages + `]}` }

// A line copied for composer global require, by Composer's names for its
// words too, runs as given, then each package is declared as kit composer
// add declares it: a constraint kept, however the line gives it.
func TestAComposerRequireLineDeclaresItsPackages(t *testing.T) {
	w := laptopWorld(t)
	line := []string{"laravel/valet", "^4.0", "vendor/one=^1.2", "vendor/two 2.*", "vendor/three"}
	w.listsAround(append([]string{"composer", "g", "r"}, line...), composerShow, composerListed(""),
		composerListed(`{"name": "laravel/valet"}, {"name": "vendor/one"}, {"name": "vendor/two"}, {"name": "vendor/three"}`))
	w.expectSync(t, []string{"laptop/declarations"}, "kit composer global require laravel/valet:^4.0, vendor/one:^1.2, vendor/two:2.*, vendor/three (laptop)")

	out, errOut, code := w.run(t, append([]string{"composer", "g", "r"}, line...)...)
	if want := "laravel/valet:^4.0 ok installed; declared in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit composer g r printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "composer packages"); got != "vendor/one:^1.2\nvendor/three\nvendor/two:2.*\nlaravel/valet:^4.0\n" {
		t.Errorf("laptop's composer packages = %q", got)
	}
}

// Something other than a package's name is refused before anything runs.
func TestAComposerLineIsRefusedBeforeItRuns(t *testing.T) {
	for _, line := range [][]string{{"require", "valet"}, {"require", "https://example.com/tool.zip"}, {"require", "./tool"}, {"remove", "--dev"}} {
		w := laptopWorld(t)
		_, errOut, code := w.run(t, append([]string{"composer", "global"}, line...)...)
		if code == 0 || !strings.HasPrefix(errOut, "kit: ") || len(w.fake.Calls()) != 0 {
			t.Errorf("kit composer global %q: %q, exit %d, running %q; want it refused, nothing run", line, errOut, code, w.fake.Calls())
		}
	}
}

// An uninstall line undeclares a package declared with its constraint; a
// dry run declares nothing.
func TestAComposerRemoveLineUndeclares(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "composer packages", "laravel/valet:^4.0\n")
	w.listsAround([]string{"composer", "global", "rm", "laravel/valet"}, composerShow, composerListed(`{"name": "laravel/valet"}`), composerListed(""))
	w.expectSync(t, []string{"laptop/declarations"}, "kit composer global remove laravel/valet (laptop)")
	out, errOut, code := w.run(t, "composer", "global", "rm", "laravel/valet")
	if want := "laravel/valet ok uninstalled; out of laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit composer global rm printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}

	w = laptopWorld(t)
	w.fake.On("composer", "global", "remove", "--dry-run", "laravel/valet")
	out, errOut, code = w.run(t, "composer", "global", "remove", "--dry-run", "laravel/valet")
	if want := "composer ok a dry run: nothing declared or undeclared\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit composer global remove --dry-run printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
}

// ghList is how kit asks gh for its extensions; ghListed is what laptopWorld
// has installed.
var ghList = []string{"gh", "extension", "list"}

const ghListed = "gh dash\tdlvhdr/gh-dash\tv4.0.0\n"

// A line copied for gh extension install, by gh's names for its words,
// runs as given, then the extension's declared by its repository, the pin
// the install's alone.
func TestAGHExtensionInstallLineDeclaresIt(t *testing.T) {
	w := laptopWorld(t)
	w.listsAround([]string{"gh", "ext", "install", "owner/gh-tool", "--pin", "v1.0.0"}, ghList, ghListed, ghListed+"gh tool\towner/gh-tool\tv1.0.0\n")
	w.expectSync(t, []string{"shared/declarations"}, "kit gh extension install owner/gh-tool (laptop)")

	out, errOut, code := w.run(t, "gh", "ext", "install", "owner/gh-tool", "--pin", "v1.0.0", "--shared")
	want := "kit gh extension install owner/gh-tool --pin v1.0.0 · laptop\ngh ok installed\nowner/gh-tool ok installed; declared in shared\n"
	if !strings.HasPrefix(out, want) || code != 0 {
		t.Errorf("kit gh ext install printed\n%s%s exit %d\nwant it to start\n%s", out, errOut, code, want)
	}
	if got := w.readSection(t, "shared", "github extensions"); got != "owner/gh-tool\n" {
		t.Errorf("shared's extensions = %q", got)
	}
}

// An address, a folder, or an extension by its command alone, which gh
// installs by its repository, is refused before anything runs.
func TestAGHExtensionInstallLineIsRefusedBeforeItRuns(t *testing.T) {
	for _, name := range []string{".", "https://github.com/owner/gh-tool", "example.com/owner/gh-tool"} {
		w := laptopWorld(t)
		_, errOut, code := w.run(t, "gh", "extension", "install", name)
		if code == 0 || !strings.HasPrefix(errOut, "kit: ") || slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "gh ext") }) {
			t.Errorf("kit gh extension install %s: %q, exit %d, running %q; want it refused, nothing run", name, errOut, code, w.fake.Calls())
		}
	}
	w := laptopWorld(t)
	_, errOut, code := w.run(t, "gh", "extension", "install", "dash")
	want := "kit: dash isn't a repository: give gh one, as owner/gh-dash; nothing was installed\n"
	if errOut != want || code == 0 || ran(w, "gh extension install dash") {
		t.Errorf("kit gh extension install dash: %q, exit %d, running %q; want\n%s", errOut, code, w.fake.Calls(), want)
	}
}

// gh removes an extension by its command's name, which kit undeclares by
// the repository it's declared by.
func TestAGHExtensionRemoveLineUndeclaresByTheRepository(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "github extensions", "dlvhdr/gh-dash\n")
	w.listsAround([]string{"gh", "extension", "uninstall", "dash"}, ghList, ghListed, "")
	w.expectSync(t, []string{"laptop/declarations"}, "kit gh extension remove dash (laptop)")

	out, errOut, code := w.run(t, "gh", "extension", "uninstall", "dash")
	want := "kit gh extension remove dash · laptop\ngh ok uninstalled\ndash ok uninstalled; out of laptop\n"
	if !strings.HasPrefix(out, want) || code != 0 {
		t.Errorf("kit gh extension uninstall printed\n%s%s exit %d\nwant it to start\n%s", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "github extensions"); got != "" {
		t.Errorf("laptop's extensions = %q, want dlvhdr/gh-dash out of it", got)
	}
}

// masList is how kit asks mas for its apps.
var masList = []string{"mas", "list", "--json"}

// masWorld is laptopWorld with mas listing nothing, then, once line has
// run, Xcode, which it looks up by its id.
func masWorld(t *testing.T, line ...string) *world {
	t.Helper()
	w := laptopWorld(t)
	w.fake.On("mas", "lookup", "--json", "497799835").Prints(xcodeListed)
	w.listsAround(line, masList, "", xcodeListed)
	return w
}

// A line copied for mas install, or its get, looks each id up, then runs
// through sudo, the password asked for first, at a terminal, and declares
// the app by its name and id, as kit mas add does.
func TestAMasInstallLineDeclaresTheApp(t *testing.T) {
	for _, verb := range []string{"install", "get"} {
		w := masWorld(t, "sudo", "-n", "mas", verb, "497799835")
		w.terminal = true
		w.expectPassword()
		w.expectSync(t, []string{"laptop/declarations"}, "kit mas install xcode@497799835 (laptop)")
		if _, errOut, code := w.run(t, "mas", verb, "497799835"); code != 0 {
			t.Fatalf("kit mas %s: exit %d, %s", verb, code, errOut)
		}
		calls := w.fake.Calls()
		if asked, install := slices.Index(calls, askedFor), slices.Index(calls, "sudo -n mas "+verb+" 497799835"); asked < 0 || install < asked {
			t.Errorf("kit mas %s ran %q; want the password asked for, then mas %s through sudo", verb, calls, verb)
		}
		if got := w.readSection(t, "laptop", "app store apps"); got != "xcode@497799835\n" {
			t.Errorf("kit mas %s: laptop's apps = %q, want xcode@497799835", verb, got)
		}
	}
}

// Without a terminal to ask at, and no password held, the line's refused
// before it runs, in the words it was typed in, a --dry-run, which mas
// install hasn't, not making it a dry run; an id the App Store hasn't, or
// what isn't an id, is refused too.
func TestAMasInstallLineIsRefusedBeforeItRuns(t *testing.T) {
	for _, line := range [][]string{{"install", "497799835"}, {"get", "497799835"}, {"install", "--dry-run", "497799835"}} {
		w := masWorld(t, append([]string{"sudo", "-n", "mas"}, line...)...)
		w.fake.On("sudo", "-n", "true").Exits(1).PrintsToStderr("sudo: a password is required")
		_, errOut, code := w.run(t, append([]string{"mas"}, line...)...)
		want := "kit: xcode@497799835 needs an administrator's password: run kit mas " + line[0] + " at a terminal; nothing was installed\n"
		if errOut != want || code == 0 || slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.Contains(c, "mas "+line[0]) }) {
			t.Errorf("kit mas %q: %q, exit %d, running %q; want\n%s", line, errOut, code, w.fake.Calls(), want)
		}
	}

	w := laptopWorld(t)
	w.fake.On("mas", "lookup", "--json", "1").Exits(1).PrintsToStderr("Error: No apps found in the App Store for ADAM ID 1")
	_, errOut, code := w.run(t, "mas", "install", "1")
	if want := "kit: no App Store app has the id 1; nothing was installed\n"; errOut != want || code == 0 {
		t.Errorf("kit mas install 1: %q, exit %d; want\n%s", errOut, code, want)
	}

	for _, line := range [][]string{{"install", "xcode"}, {"install", "--bundle", "com.apple.dt.Xcode"}, {"uninstall", "--all"}} {
		w = laptopWorld(t)
		_, errOut, code = w.run(t, append([]string{"mas"}, line...)...)
		if code == 0 || !strings.HasPrefix(errOut, "kit: ") || len(w.fake.Calls()) != 0 {
			t.Errorf("kit mas %q: %q, exit %d, running %q; want it refused, nothing run", line, errOut, code, w.fake.Calls())
		}
	}
}

// An uninstall line runs mas through sudo, then undeclares the app, the
// settings kit prefs saved for it kept, saying they come back with it and
// what, in place of the line, would have removed them; a dry run runs mas
// without sudo, and declares nothing.
func TestAMasUninstallLineUndeclares(t *testing.T) {
	w := notesWorld(t, true)
	w.fake.On("mas", "lookup", "--json", "123456789").Prints(notesListed)
	w.listsAround([]string{"sudo", "-n", "mas", "uninstall", "123456789"}, masList, notesListed, "")
	w.fake.On("sudo", "-n", "true")
	w.expectSync(t, []string{"laptop/declarations"}, "kit mas uninstall notes-example@123456789 (laptop)")
	out, errOut, code := w.run(t, "mas", "uninstall", "123456789")
	want := "notes-example@123456789 ok uninstalled, its saved settings kept, to come back if it's installed again: " +
		"kit mas remove --zap, in place of the line, removes them; out of laptop\n"
	if !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit mas uninstall printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "app store apps"); got != "" {
		t.Errorf("laptop's apps = %q, want it undeclared", got)
	}

	// Declared by the name the app had, it's found by its id.
	w = notesWorld(t, false)
	w.writeSection(t, "laptop", "app store apps", "old-notes@123456789\n")
	w.fake.On("mas", "lookup", "--json", "123456789").Prints(notesListed)
	w.listsAround([]string{"sudo", "-n", "mas", "uninstall", "123456789"}, masList, notesListed, "")
	w.fake.On("sudo", "-n", "true")
	w.expectSync(t, []string{"laptop/declarations"}, "kit mas uninstall notes-example@123456789 (laptop)")
	out, errOut, code = w.run(t, "mas", "uninstall", "123456789")
	if want := "notes-example@123456789 ok uninstalled; out of laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit mas uninstall of an app declared by its old name printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "app store apps"); got != "" {
		t.Errorf("laptop's apps = %q, want old-notes@123456789 undeclared", got)
	}

	w = notesWorld(t, false)
	w.terminal = true
	w.fake.On("mas", "lookup", "--json", "123456789").Prints(notesListed)
	w.listsAround([]string{"sudo", "-n", "mas", "uninstall", "123456789"}, masList, notesListed, "")
	w.expectPassword()
	w.expectSync(t, []string{"laptop/declarations"}, "kit mas uninstall notes-example@123456789 (laptop)")
	if _, errOut, code := w.run(t, "mas", "uninstall", "123456789"); code != 0 {
		t.Fatalf("kit mas uninstall at a terminal: exit %d, %s", code, errOut)
	}
	calls := w.fake.Calls()
	if asked, uninstall := slices.Index(calls, askedFor), slices.Index(calls, "sudo -n mas uninstall 123456789"); asked < 0 || uninstall < asked {
		t.Errorf("kit mas uninstall at a terminal ran %q; want the password asked for first", calls)
	}

	w = laptopWorld(t)
	w.fake.On("mas", "lookup", "--json", "123456789").Prints(notesListed)
	w.fake.On("mas", "uninstall", "--dry-run", "123456789")
	out, errOut, code = w.run(t, "mas", "uninstall", "--dry-run", "123456789")
	if want := "mas ok a dry run: nothing declared or undeclared\n"; !strings.Contains(out, want) || code != 0 || slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "sudo") }) {
		t.Errorf("kit mas uninstall --dry-run printed\n%s%s exit %d, ran %q; want it to hold %q, no sudo", out, errOut, code, w.fake.Calls(), want)
	}
}
