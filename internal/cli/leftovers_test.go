package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// exampleInfo is what brew info says of a cask whose zap list names files
// in the home folder, its preferences among them, by a glob that matches
// another app's too, as a nightly's can its stable's, and one outside it.
const exampleInfo = `{"formulae": [], "casks": [{"token": "example-app", "full_token": "example-app", "name": ["Example App"], "old_tokens": [],
	"artifacts": [{"app": ["Example App.app"]}, {"zap": [{"trash": ["~/Library/Application Support/Example App", "~/Library/Caches/com.example.app*", "~/Library/Preferences/*example.app.plist"], "delete": "/Library/Example App/none"}]}]}]}`

// exampleLeaves are the files example-app leaves, as the question lists
// them: the stable app's settings among them, which the zap trashes too.
var exampleLeaves = []string{
	"~/Library/Application Support/Example App",
	"~/Library/Caches/com.example.app.ShipIt",
	"~/Library/Preferences/com.example.app.plist",
	"~/Library/Preferences/com.stable.example.app.plist",
}

// leavingWorld is laptopWorld saving apps' settings, with the cask
// example-app installed and declared, which leaves files on the Mac when
// files says so, and another app installed whose settings its zap list
// names too.
func leavingWorld(t *testing.T, files bool) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "prefs\n")
	w.writeSection(t, "laptop", "homebrew casks", "example-app\n")
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nexample-app\n")
	w.fake.On("brew", "info", "--json=v2", "--cask", "example-app").Prints(exampleInfo)
	if files {
		w.write(t, filepath.Join("Library", "Application Support", "Example App", "data"), "data")
		w.write(t, filepath.Join("Library", "Caches", "com.example.app.ShipIt", "cache"), "cache")
		w.write(t, filepath.Join("Library", "Preferences", "com.example.app.plist"), "settings")
		w.write(t, filepath.Join("Library", "Preferences", "com.stable.example.app.plist"), "another app's settings")
	}
	w.write(t, filepath.Join("Applications", "Stable.app", "Contents", "Info.plist"),
		`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.stable.example.app</string></dict></plist>`)
	return w
}

// ran reports whether the fake ran call.
func ran(w *world, call string) bool {
	return slices.Contains(w.fake.Calls(), call)
}

// Removing a cask whose zap list names files on the Mac asks once, at a
// terminal, before anything's uninstalled, by the app's name, each file
// listed under it: removing them uninstalls with Homebrew's zap, and
// deletes its preferences' domain, so the next capture drops it from kit
// prefs' store, never another app's that's installed; keeping them
// uninstalls as before.
func TestRemovingACaskAsksAboutWhatItLeaves(t *testing.T) {
	for _, tt := range []struct {
		name     string
		answer   int
		runs     []string
		runsNone []string
	}{
		{"remove them", 1, []string{"brew uninstall --cask --zap example-app", "defaults delete com.example.app"}, []string{"brew uninstall --cask example-app", "defaults delete com.stable.example.app"}},
		{"keep them", 0, []string{"brew uninstall --cask example-app"}, []string{"brew uninstall --cask --zap example-app", "defaults delete com.example.app"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := leavingWorld(t, true)
			w.terminal = true
			var labels []string
			w.choose = func(_ string, options []string) (int, error) {
				labels = options
				if ran(w, "brew uninstall --cask example-app") || ran(w, "brew uninstall --cask --zap example-app") {
					t.Error("asked once it had uninstalled")
				}
				return tt.answer, nil
			}
			w.fake.On("brew", "uninstall", "--cask", "example-app")
			w.fake.On("brew", "uninstall", "--cask", "--zap", "example-app")
			w.fake.On("defaults", "delete", "com.example.app")
			w.expectSync(t, []string{"laptop/declarations"}, "kit cask remove example-app (laptop)")
			if _, errOut, code := w.run(t, "cask", "remove", "example-app"); code != 0 {
				t.Fatalf("kit cask remove exit %d: %s", code, errOut)
			}
			if want := []string{"Example App's settings and files\n\n" + strings.Join(exampleLeaves, "\n")}; !slices.Equal(w.asked, want) {
				t.Errorf("asked %q, want %q", w.asked, want)
			}
			if want := []string{"Keep them", "Remove them"}; !slices.Equal(labels, want) {
				t.Errorf("offered %q, want %q", labels, want)
			}
			for _, call := range tt.runs {
				if !ran(w, call) {
					t.Errorf("ran %q, want %s", w.fake.Calls(), call)
				}
			}
			for _, call := range tt.runsNone {
				if ran(w, call) {
					t.Errorf("ran %s, want it not run", call)
				}
			}
		})
	}
}

// Without a terminal, and without --zap or --keep, what a cask leaves is
// kept, the output saying so, and that --zap removes it; --zap removes it
// without asking, --keep keeps it without asking, and not both.
func TestRemovingACaskWithoutAsking(t *testing.T) {
	w := leavingWorld(t, true)
	w.fake.On("brew", "uninstall", "--cask", "example-app")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask remove example-app (laptop)")
	out, errOut, code := w.run(t, "cask", "remove", "example-app")
	if want := "example-app ok uninstalled, its files kept: --zap removes them; out of laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit cask remove printed\n%s%s exit %d; want %q", out, errOut, code, want)
	}

	w = leavingWorld(t, true)
	w.fake.On("brew", "uninstall", "--cask", "--zap", "example-app")
	w.fake.On("defaults", "delete", "com.example.app")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask remove example-app (laptop)")
	out, errOut, code = w.run(t, "cask", "remove", "example-app", "--zap")
	if want := "example-app ok uninstalled with its files and settings; out of laptop\n"; !strings.Contains(out, want) || code != 0 || !ran(w, "defaults delete com.example.app") {
		t.Errorf("kit cask remove --zap printed\n%s%s exit %d, ran %q; want %q", out, errOut, code, w.fake.Calls(), want)
	}

	w = leavingWorld(t, true)
	w.terminal = true
	w.fake.On("brew", "uninstall", "--cask", "example-app")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask remove example-app (laptop)")
	if _, errOut, code := w.run(t, "cask", "remove", "example-app", "--keep"); code != 0 || len(w.asked) != 0 || !ran(w, "brew uninstall --cask example-app") {
		t.Errorf("kit cask remove --keep: exit %d (%s), asked %q, ran %q; want it uninstalled as before, unasked", code, errOut, w.asked, w.fake.Calls())
	}

	w = leavingWorld(t, true)
	if _, errOut, code := w.run(t, "cask", "remove", "example-app", "--zap", "--keep"); code != 2 || !strings.Contains(errOut, "--zap or --keep, not both") {
		t.Errorf("kit cask remove --zap --keep printed %q, exit %d", errOut, code)
	}
}

// A cask whose zap list names nothing on the Mac leaves nothing to ask
// about: it's uninstalled as before.
func TestRemovingACaskThatLeftNothing(t *testing.T) {
	w := leavingWorld(t, false)
	w.terminal = true
	w.fake.On("brew", "uninstall", "--cask", "example-app")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask remove example-app (laptop)")
	out, errOut, code := w.run(t, "cask", "remove", "example-app")
	if code != 0 || len(w.asked) != 0 || !strings.Contains(ansi.Strip(out), "example-app  uninstalled · out of laptop") {
		t.Errorf("kit cask remove printed\n%s%s exit %d, asked %q; want it uninstalled, unasked", ansi.Strip(out), errOut, code, w.asked)
	}
}

// notesListed is mas's list of an App Store app, its bundle id and all.
const notesListed = `{"adamID":123456789,"bundleID":"com.example.notes","name":"Notes Example"}` + "\n"

// notesWorld is laptopWorld saving apps' settings, with an App Store app
// installed and declared, whose settings kit prefs saved when saved says
// so, beside another app's, and a domain it shares with that app.
func notesWorld(t *testing.T, saved bool) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "prefs\n")
	w.writeSection(t, "laptop", "app store apps", "notes-example@123456789\n")
	w.fake.On("mas", "list", "--json").Prints(notesListed)
	w.write(t, filepath.Join("Applications", "Notes Example.app", "Contents", "Info.plist"),
		`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.notes</string></dict></plist>`)
	w.write(t, filepath.Join("Applications", "Other Example.app", "Contents", "Info.plist"),
		`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.other</string></dict></plist>`)
	domains := filepath.Join(".local", "share", "kit", "prefs", "laptop", "domains")
	w.write(t, filepath.Join(domains, "com.example.other.plist"), "settings")
	w.write(t, filepath.Join(domains, "group.com.example.plist"), "both apps' settings")
	if saved {
		w.write(t, filepath.Join(domains, "com.example.notes.plist"), "settings")
	}
	return w
}

// Removing an App Store app, which has no list of the files it leaves,
// asks about its settings kit prefs saved, once there are some, listing
// their domains: removing them deletes those domains while the app's still
// there, then uninstalls it, and the next capture drops them; one it
// shares with another app is never deleted.
func TestRemovingAnAppStoreAppAsksAboutItsSavedSettings(t *testing.T) {
	w := notesWorld(t, true)
	w.terminal = true
	var labels []string
	w.choose = func(_ string, options []string) (int, error) {
		labels = options
		return 1, nil
	}
	w.expectPassword()
	w.fake.On("sudo", "-n", "mas", "uninstall", "123456789")
	w.fake.On("defaults", "delete", "com.example.notes")
	w.expectSync(t, []string{"laptop/declarations"}, "kit mas remove notes-example@123456789 (laptop)")
	if _, errOut, code := w.run(t, "mas", "remove", "notes-example@123456789"); code != 0 {
		t.Fatalf("kit mas remove exit %d: %s", code, errOut)
	}
	if want := []string{"Notes Example's settings\n\ncom.example.notes"}; !slices.Equal(w.asked, want) || !slices.Equal(labels, []string{"Keep them", "Remove them"}) {
		t.Errorf("asked %q, offered %q; want %q, keep or remove", w.asked, labels, want)
	}
	calls := w.fake.Calls()
	if deleted, uninstalled := slices.Index(calls, "defaults delete com.example.notes"), slices.Index(calls, "sudo -n mas uninstall 123456789"); deleted < 0 || uninstalled < deleted {
		t.Errorf("ran %q; want its settings deleted, then the app uninstalled", calls)
	}
	for _, c := range calls {
		if c == "defaults delete com.example.other" || c == "defaults delete group.com.example" {
			t.Errorf("ran %s; want the other app's settings, and those it shares, left", c)
		}
	}

	w = notesWorld(t, false)
	w.terminal = true
	w.expectPassword()
	w.fake.On("sudo", "-n", "mas", "uninstall", "123456789")
	w.expectSync(t, []string{"laptop/declarations"}, "kit mas remove notes-example@123456789 (laptop)")
	if _, errOut, code := w.run(t, "mas", "remove", "notes-example@123456789"); code != 0 || len(w.asked) != 0 {
		t.Errorf("with no settings saved: exit %d (%s), asked %q; want it uninstalled, unasked", code, errOut, w.asked)
	}
}

// An uninstall that fails once its saved settings were deleted, the app
// still there, puts them back from kit prefs' store; with the app running,
// they're held till it's quit, so the next capture keeps them, and it says
// so; failing otherwise, it says how to put them back.
func TestRemovingAnAppThatFailsToUninstallPutsItsSettingsBack(t *testing.T) {
	for _, tt := range []struct {
		name             string
		running, imports int
		says             string
	}{
		{"put back", 0, 0, ""},
		{"running", 1, 0, "couldn't uninstall: sudo exited 1; its settings, deleted first, come back once it's quit"},
		{"not put back", 0, 1, "couldn't uninstall: sudo exited 1; its settings were deleted first: kit prefs restore com.example.notes puts them back"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := notesWorld(t, true)
			store := filepath.Join(".local", "share", "kit", "prefs")
			clone := filepath.Join(w.home, store)
			saved := filepath.Join(clone, "laptop", "domains", "com.example.notes.plist")
			w.write(t, filepath.Join(store, ".git", "HEAD"), "")
			w.write(t, filepath.Join(store, "laptop", "domains", "com.example.notes.plist"), exampleSettings)
			for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
				w.fake.On("git", "-C", clone, "config", kv[0], kv[1])
			}
			w.fake.On("git", "-C", clone, "remote")
			w.fake.On("git", "-C", clone, "rev-parse", "HEAD").Prints("abc123\n")
			w.expectPassword()
			w.fake.On("sudo", "-n", "mas", "uninstall", "123456789").Exits(1)
			w.fake.On("defaults", "delete", "com.example.notes")
			w.fake.On("osascript", "-e", `application id "com.example.notes" is running`).Prints([]string{"false\n", "true\n"}[tt.running])
			w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", filepath.Join(w.home, "Applications", "Notes Example.app"))
			w.fake.On("defaults", "export", "com.example.notes", "-").Prints(noSettings).Then().Prints(exampleSettings)
			w.fake.On("defaults", "import", "com.example.notes", saved).Exits(tt.imports)
			out, errOut, code := w.run(t, "mas", "remove", "notes-example@123456789", "--zap")
			printed := ansi.Strip(out + errOut)
			calls := w.fake.Calls()
			failed, imported := slices.Index(calls, "sudo -n mas uninstall 123456789"), slices.Index(calls, "defaults import com.example.notes "+saved)
			if code == 0 || failed < 0 || (imported < failed) != (tt.running == 1) {
				t.Errorf("kit mas remove --zap printed\n%s exit %d, ran %q; want it failed, then its settings imported back unless it's running", printed, code, calls)
			}
			if tt.says == "" && strings.Contains(printed, "its settings") || tt.says != "" && !strings.Contains(printed, tt.says) {
				t.Errorf("kit mas remove --zap printed\n%s\nwant %q", printed, tt.says)
			}
			if held := strings.Contains(w.read(t, filepath.Join(".local", "state", "kit", "prefs.json")), "com.example.notes"); held != (tt.running == 1) {
				t.Errorf("its settings held to be restored: %v; want them held only while it's running", held)
			}
		})
	}
}

// keptWorld is laptopWorld saving apps' settings, switched on, with
// example-app's settings kept till it's installed, and kit prefs' store
// cloned; installing example-app puts the app in place.
func keptWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.write(t, filepath.Join(".config", "kit", "kit.toml"), strings.Replace(twoMacs, "primary = \"laptop\"\n", "primary = \"laptop\"\nprefs_repo = \"git@github.com:someone/prefs.git\"\n", 1))
	w.writeSection(t, "laptop", "features", "prefs\n")
	w.write(t, filepath.Join(".local", "state", "kit", "prefs.json"), `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.app"],
		"why": {"com.example.app": "kept till its app is installed"}}}`)
	store := filepath.Join(".local", "share", "kit", "prefs")
	clone := filepath.Join(w.home, store)
	w.write(t, filepath.Join(store, ".git", "HEAD"), "")
	w.write(t, filepath.Join(store, "laptop", "domains", "com.example.app.plist"), exampleSettings)
	w.fake.On("brew", "info", "--json=v2", "--cask", "example-app").Prints(exampleInfo)
	w.fake.On("brew", "update", "--quiet")
	app := filepath.Join(w.home, "Applications", "Example App.app")
	w.fake.On("brew", "install", "--cask", "example-app").Does(func() {
		w.write(t, filepath.Join("Applications", "Example App.app", "Contents", "Info.plist"),
			`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.app</string></dict></plist>`)
	})
	for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
		w.fake.On("git", "-C", clone, "config", kv[0], kv[1])
	}
	w.fake.On("git", "-C", clone, "remote")
	w.fake.On("git", "-C", clone, "rev-parse", "HEAD").Prints("abc123\n")
	w.fake.On("osascript", "-e", `application id "com.example.app" is running`).Prints("false\n")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app)
	w.fake.On("defaults", "export", "com.example.app", "-").Prints(noSettings).Then().Prints(exampleSettings)
	w.fake.On("defaults", "delete", "com.example.app")
	w.fake.On("defaults", "import", "com.example.app", filepath.Join(clone, "laptop", "domains", "com.example.app.plist"))
	return w
}

// exampleSettings are example-app's settings, as kit prefs saved them.
const exampleSettings = `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Theme</key><string>dark</string></dict></plist>`

// noSettings is what defaults exports of a domain with none, as one not on
// the Mac.
const noSettings = `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict/></plist>`

// Installing an app kit prefs kept settings for restores them straight
// after, before it's first opened, saying so; with none kept for it, it
// says nothing of settings, nor touches another app's kept; a sandboxed
// app with no settings folder yet is to be opened once first; with kit
// prefs off, nothing's looked at.
func TestAddingAnAppRestoresTheSettingsKeptForIt(t *testing.T) {
	w := keptWorld(t)
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add example-app (laptop)")
	out, errOut, code := w.run(t, "cask", "add", "example-app")
	if want := "example-app ok installed; declared in laptop; its saved settings restored\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit cask add printed\n%s%s exit %d; want %q", out, errOut, code, want)
	}
	if !ran(w, "defaults import com.example.app "+filepath.Join(w.home, ".local", "share", "kit", "prefs", "laptop", "domains", "com.example.app.plist")) {
		t.Errorf("ran %q, want its settings imported", w.fake.Calls())
	}
	if got := w.read(t, filepath.Join(".local", "state", "kit", "prefs.json")); strings.Contains(got, "com.example.app") {
		t.Errorf("prefs.json says\n%s\nwant nothing held for it", got)
	}

	w = keptWorld(t)
	w.write(t, filepath.Join(".local", "state", "kit", "prefs.json"), `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.other"],
		"why": {"com.example.other": "kept till its app is installed"}}}`)
	w.write(t, filepath.Join("Applications", "Other.app", "Contents", "Info.plist"),
		`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.other</string></dict></plist>`)
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add example-app (laptop)")
	out, errOut, code = w.run(t, "cask", "add", "example-app")
	if want := "example-app ok installed; declared in laptop\n"; !strings.Contains(out, want) || code != 0 || slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.Contains(c, "com.example.other") }) {
		t.Errorf("with nothing kept for it, and another app's kept, its app there, kit cask add printed\n%s%s exit %d, ran %q; want %q, and nothing of the other's run", out, errOut, code, w.fake.Calls(), want)
	}
	if got := w.read(t, filepath.Join(".local", "state", "kit", "prefs.json")); !strings.Contains(got, "com.example.other") {
		t.Errorf("prefs.json says\n%s\nwant the other app's still kept", got)
	}

	w = keptWorld(t)
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", filepath.Join(w.home, "Applications", "Example App.app")).Prints(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>com.apple.security.app-sandbox</key><true/></dict></plist>`)
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add example-app (laptop)")
	out, errOut, code = w.run(t, "cask", "add", "example-app")
	if want := "example-app ok installed; declared in laptop; its saved settings come back once it's been opened and quit\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("a sandboxed app with no settings folder yet, kit cask add printed\n%s%s exit %d; want %q", out, errOut, code, want)
	}

	w = keptWorld(t)
	w.writeSection(t, "laptop", "features", "")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add example-app (laptop)")
	out, errOut, code = w.run(t, "cask", "add", "example-app")
	if want := "example-app ok installed; declared in laptop\n"; !strings.Contains(out, want) || code != 0 || ran(w, "defaults delete com.example.app") {
		t.Errorf("with kit prefs off, kit cask add printed\n%s%s exit %d, ran %q; want %q, and nothing restored", out, errOut, code, w.fake.Calls(), want)
	}
}

// kit apply restores the settings kept for the apps it installs once
// they're in, before they're first opened: the settings step's applied
// after the kinds of apps the run has.
func TestApplyRestoresTheSettingsKeptForTheAppsItInstalls(t *testing.T) {
	w := keptWorld(t)
	w.writeSection(t, "laptop", "homebrew casks", "example-app\n")
	listed := w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\n")
	w.fake.On("brew", "install", "--cask", "example-app").Does(func() {
		w.write(t, filepath.Join("Applications", "Example App.app", "Contents", "Info.plist"),
			`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.app</string></dict></plist>`)
		listed.Then().Prints("ghostty\nexample-app\n")
	})
	out, errOut, _ := w.run(t, "apply")
	calls := w.fake.Calls()
	install, restore := slices.Index(calls, "brew install --cask example-app"), slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "defaults import com.example.app ") })
	if install < 0 || restore < install {
		t.Errorf("kit apply printed\n%s%s\nran %q; want the app installed, then its settings restored", out, errOut, calls)
	}
	if waits := settingsWaits(t, w); !slices.Equal(waits, []string{"cask"}) {
		t.Errorf("the settings step waits for %q, want the casks alone, as the run has no App Store apps", waits)
	}

	w = keptWorld(t)
	w.writeSection(t, "laptop", "app store apps", "notes-example@123456789\n")
	w.fake.On("mas", "list", "--json").Prints(notesListed)
	w.run(t, "status")
	if waits := settingsWaits(t, w); !slices.Equal(waits, []string{"cask", "mas"}) {
		t.Errorf("the settings step waits for %q, want the casks and the App Store apps", waits)
	}
}

// settingsWaits are the steps the settings step of w's last run waits for,
// failing the test when the run had none.
func settingsWaits(t *testing.T, w *world) []string {
	t.Helper()
	for _, s := range runStarted(t, w).Steps {
		if s.Name == "prefs" {
			return s.Waits
		}
	}
	t.Fatal("the run has no settings step")
	return nil
}

// kit prefs restore --pending says what's kept for apps not installed as
// kept, never as waiting, nor as a restore of nothing.
func TestPrefsRestoreSaysWhatsKept(t *testing.T) {
	w := keptWorld(t)
	out, errOut, code := w.run(t, "prefs", "restore", "--pending")
	want := "prefs ok nothing to restore from laptop; 1 kept for apps not installed\n" +
		"prefs kept:restored once it is com.example.app (its app isn't installed)\n" +
		"Nothing needs attention\n"
	if !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit prefs restore --pending printed\n%s%s exit %d\nwant\n%s", out, errOut, code, want)
	}
}

// kit prefs restore of a domain whose app is running holds it till the app's
// quit, as a full restore does, so capture leaves its stored copy be.
func TestPrefsRestoreOfADomainHoldsItWhileItsAppRuns(t *testing.T) {
	w := keptWorld(t)
	w.write(t, filepath.Join(".local", "state", "kit", "prefs.json"), `{"on": "restored"}`)
	w.write(t, filepath.Join("Applications", "Example App.app", "Contents", "Info.plist"),
		`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.app</string></dict></plist>`)
	w.fake.On("osascript", "-e", `application id "com.example.app" is running`).Prints("true\n")
	out, errOut, code := w.run(t, "prefs", "restore", "com.example.app")
	if !strings.Contains(out, "com.example.app") || code != 0 || ran(w, "defaults delete com.example.app") {
		t.Errorf("kit prefs restore printed\n%s%s exit %d, ran %q; want it waiting, nothing deleted", out, errOut, code, w.fake.Calls())
	}
	if got := w.read(t, filepath.Join(".local", "state", "kit", "prefs.json")); !strings.Contains(got, `"com.example.app": "Example App is running"`) {
		t.Errorf("prefs.json says\n%s\nwant it held till its app's quit", got)
	}
}

// A cask removing would refuse, declared for every Mac without --shared,
// is never asked about: the question could do nothing.
func TestRemovingARefusedCaskAsksNothing(t *testing.T) {
	w := leavingWorld(t, true)
	w.terminal = true
	w.writeSection(t, "laptop", "homebrew casks", "")
	w.writeSection(t, "shared", "homebrew casks", "example-app\nghostty\n")
	out, errOut, code := w.run(t, "cask", "remove", "example-app")
	if code != 1 || len(w.asked) != 0 || !strings.Contains(ansi.Strip(out), "declared for every Mac") {
		t.Errorf("kit cask remove printed\n%s%s exit %d, asked %q; want it refused, unasked", ansi.Strip(out), errOut, code, w.asked)
	}
}

// Its settings that couldn't be taken off what's held to be restored, once
// it's uninstalled with its files, it's undeclared all the same, the
// summary saying so: else the next kit apply would install it again.
func TestRemovingACaskWhoseSettingsStayHeld(t *testing.T) {
	w := leavingWorld(t, true)
	w.write(t, filepath.Join(".local", "state", "kit", "prefs.json", "in-the-way"), "")
	w.fake.On("brew", "uninstall", "--cask", "--zap", "example-app")
	w.fake.On("defaults", "delete", "com.example.app")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask remove example-app (laptop)")
	out, errOut, code := w.run(t, "cask", "remove", "example-app", "--zap")
	if want := "example-app ok uninstalled with its files and settings, but its settings are still held to be restored: "; !strings.Contains(out, want) || !strings.Contains(out, "; out of laptop\n") || code != 0 {
		t.Errorf("kit cask remove --zap printed\n%s%s exit %d; want %q, and it undeclared", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "" {
		t.Errorf("laptop = %q, want it undeclared", got)
	}
}

// --zap with --keep is refused whatever's named; --zap on an app that isn't
// installed says it had nothing to remove.
func TestRemovingWithZapWhatIsntThere(t *testing.T) {
	w := leavingWorld(t, false)
	if _, errOut, code := w.run(t, "cask", "remove", "absent-app", "--zap", "--keep"); code != 2 || !strings.Contains(errOut, "--zap or --keep, not both") {
		t.Errorf("kit cask remove absent-app --zap --keep printed %q, exit %d", errOut, code)
	}
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask remove example-app (laptop)")
	out, errOut, code := w.run(t, "cask", "remove", "example-app", "--zap")
	if want := "example-app ok wasn't installed; out of laptop; nothing for --zap to remove\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit cask remove --zap printed\n%s%s exit %d; want %q", out, errOut, code, want)
	}
}

// Removing's help says what an app of its kind leaves, and what removing
// that takes: a cask's files and settings; an App Store app's settings.
func TestRemovingAppsHelp(t *testing.T) {
	w := laptopWorld(t)
	cask, _, _ := w.run(t, "cask", "remove", "--help")
	mas, _, _ := w.run(t, "mas", "remove", "--help")
	brew, _, _ := w.run(t, "brew", "remove", "--help")
	if !strings.Contains(cask, "An app leaves its files and settings behind") || !strings.Contains(cask, "--zap") {
		t.Errorf("kit cask remove --help printed\n%s", cask)
	}
	if !strings.Contains(mas, "An app leaves its settings behind") || strings.Contains(mas, "files") || !strings.Contains(mas, "--zap") {
		t.Errorf("kit mas remove --help printed\n%s", mas)
	}
	if strings.Contains(brew, "--zap") || strings.Contains(brew, "An app leaves") {
		t.Errorf("kit brew remove --help printed\n%s", brew)
	}
}
