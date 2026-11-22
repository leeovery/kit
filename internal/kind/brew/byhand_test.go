package brew_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// installApp puts an app named name in dir, its bundle id id, as macOS
// keeps an app: a folder holding its Info.plist.
func installApp(t *testing.T, dir, name, id string) string {
	t.Helper()
	return installVersion(t, dir, name, id, "")
}

// installVersion is installApp, the app's version given as version, as
// people are shown it: none, given "".
func installVersion(t *testing.T, dir, name, id, version string) string {
	t.Helper()
	app := filepath.Join(dir, name)
	short := ""
	if version != "" {
		short = `<key>CFBundleShortVersionString</key><string>` + version + `</string>`
	}
	writeFile(t, filepath.Join(app, "Contents", "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>`+id+`</string>`+short+`</dict></plist>`)
	return app
}

// writeFile writes content at path, making its folders.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// catalogued has Homebrew's cache, as brew --cache names it to fake, hold
// the catalogue of casks in testdata, as Homebrew keeps it.
func catalogued(t *testing.T, fake *runnertest.Fake) {
	t.Helper()
	cache := t.TempDir()
	writeFile(t, filepath.Join(cache, "api", "internal", "packages.arm64_tahoe.jws.json.payload"), fixture(t, "catalogue.jws.json.payload"))
	fake.On("brew", "--cache").Prints(cache + "\n")
}

// withApps is Homebrew, as kit's kinds share it, its apps in dirs.
func withApps(fake *runnertest.Fake, dirs ...string) *brew.Homebrew {
	return brew.Of(&kind.Deps{Run: fake, Home: "/Users/someone", AppDirs: dirs})
}

// noCasks has Homebrew's Caskroom hold no cask.
func noCasks(t *testing.T, fake *runnertest.Fake) {
	t.Helper()
	caskroom(t, fake, nil)
}

// names are the names of apps.
func names(apps []brew.ByHand) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.Name)
	}
	return out
}

// The apps installed by hand are those that are neither Apple's, nor the
// App Store's, nor made by Chrome for a website, wherever it put them, nor
// put in place by a cask, as its app, in its suite's folder, or through
// its package, in any of the folders apps are installed in. Chrome itself,
// installed by hand, is.
func TestTheAppsInstalledByHand(t *testing.T) {
	fake := runnertest.New(t)
	apps, mine := t.TempDir(), t.TempDir()
	for name, id := range map[string]string{
		"Editor.app": "com.example.editor", "Driver.app": "com.example.driver", "Agent.app": "com.example.agent",
		"Safari.app": "com.apple.Safari", "Utilities/Terminal.app": "com.apple.Terminal",
		"Tool.app": "com.example.tool", "Example Suite/Part.app": "com.example.part", "Other Tool.app": "com.example.other",
		"Google Chrome.app": "com.google.Chrome",
		// As Chrome names the apps it makes for websites, measured.
		"Chrome Apps.localized/Google Meet.app": "com.google.Chrome.app.kjgfgldnnfoeklkmfkjfagphfepbbdan",
		"Mail Site.app":                         "com.google.Chrome.app.pommaclcbfghclhalboakcipcmmndhcj",
	} {
		installApp(t, apps, name, id)
	}
	writeFile(t, filepath.Join(installApp(t, apps, "Store.app", "com.example.store"), "Contents", "_MASReceipt", "receipt"), "")
	installApp(t, mine, "Mine.app", "com.example.mine")
	caskroom(t, fake, map[string]string{
		"suite":  `[{"suite": ["Example Suite"]}]`,
		"editor": `[{"app": ["Editor.app"]}]`,
		"driver": `[{"uninstall": [{"pkgutil": "com.example.driver.*"}]}]`,
		"agent":  `[{"uninstall": [{"pkgutil": "com.example.agent"}]}]`,
	})
	fake.On("pkgutil", "--pkgs").Prints("com.apple.pkg.Core\ncom.example.driver.app\ncom.example.agent\ncom.example.other\n")
	receipt := func(location string) string {
		return `<plist version="1.0"><dict><key>install-location</key><string>` + location + `</string><key>volume</key><string>/</string></dict></plist>`
	}
	fake.On("pkgutil", "--pkg-info-plist", "com.example.driver.app").Prints(receipt(strings.TrimPrefix(apps, "/")))
	fake.On("pkgutil", "--files", "com.example.driver.app").Prints("Driver.app\nDriver.app/Contents\nDriver.app/Contents/Info.plist\n")
	fake.On("pkgutil", "--pkg-info-plist", "com.example.agent").Prints(receipt(filepath.Join(apps, "Agent.app")))
	fake.On("pkgutil", "--files", "com.example.agent").Prints("Contents\nContents/Info.plist\n")
	catalogued(t, fake)
	found, err := withApps(fake, apps, mine).AppsByHand(t.Context())
	if want := []string{"Google Chrome", "Other Tool", "Tool", "Mine"}; err != nil || found.Unmatched != nil || !slices.Equal(names(found.Apps), want) {
		t.Errorf("AppsByHand() = %q, %v, %v; want %q", names(found.Apps), found.Unmatched, err, want)
	}
	if slices.Contains(fake.Calls(), "pkgutil --files com.example.other") {
		t.Error("asked pkgutil of a package no cask installed")
	}
}

// An app installed by hand has the cask that installs it, as Homebrew's
// catalogue has it: of the casks putting an app where it is, in the folder
// casks put their apps in, the one that states its bundle id, in any case;
// one without @ over those with, the app's version aside (that's
// TestTheCaskOfTheAppsVersion). A cask puts its app where its target says,
// or, installing through a package with no app of its own, where its
// uninstall deletes one. An app anywhere else is no cask's: installing
// one would put a second copy in place, whatever id it quits. But a
// package that says nowhere is the one an app at the folder's top is, when
// it alone quits the app's id. None states a fork's id under the cask's
// app's name; a cask that states no id, is disabled, or moves into place
// what kit can't vet is never the one; nor is any when more than one would
// do.
func TestTheCaskThatInstallsAnAppByHand(t *testing.T) {
	for _, tt := range []struct {
		name, in, app, id, version, want string
	}{
		{"by its name", "", "Google Chrome.app", "com.google.Chrome", "", "google-chrome"},
		{"its id in another case", "", "Google Chrome.app", "COM.GOOGLE.CHROME", "", "google-chrome"},
		{"in another folder apps are in", "other", "Google Chrome.app", "com.google.Chrome", "", ""},
		{"in a folder in the casks' folder", "Utilities", "Google Chrome.app", "com.google.Chrome", "", ""},
		{"a fork, under its name", "", "Spaceman.app", "dev.fork.Spaceman", "", ""},
		{"the cask's own, under its name", "", "Spaceman.app", "dev.original.Spaceman", "", "spaceman"},
		{"its package's, as its uninstall deletes it, by the id it signals", "", "zoom.us.app", "us.zoom.xos", "7.2.5", "zoom"},
		{"a package's that says nowhere, by the id it quits", "", "Monitor.app", "com.example.monitor", "2.7.0", "monitor"},
		{"a package's that says nowhere, in a folder in the casks' folder", "Utilities", "Monitor.app", "com.example.monitor", "2.7.0", ""},
		{"a package's that says nowhere, in another folder apps are in", "other", "Monitor.app", "com.example.monitor", "2.7.0", ""},
		{"by an id two packages that say nowhere quit", "", "Relay.app", "com.example.relay", "4.1", ""},
		{"the plain token over one with @", "", "1Password.app", "com.1password.1password", "", "1password"},
		{"a cask stating no id", "", "Quiet Tool.app", "com.example.quiet", "2.1", ""},
		{"by its target, the id its saved state is named for", "", "Browser.app", "com.example.browser", "140.0", "browser"},
		{"under the name its target renames, by the id it quits", "", "Chromium.app", "com.example.browser", "140.0", ""},
		{"under another name, by the id a cask quits", "", "Editor.app", "com.example.editor", "3.2", ""},
		{"from a folder of the download, by an id it signals of several", "", "Signals.app", "com.example.signals.agent", "1.4", "signals"},
		{"a cask moving a suite too", "", "Office.app", "com.example.office", "", ""},
		{"a cask putting its app elsewhere, by the id it quits", "", "Sidekick.app", "com.example.sidekick", "5.0", ""},
		{"a disabled cask", "", "Old Tool.app", "com.example.oldtool", "0.9", ""},
		{"no bundle id", "", "Google Chrome.app", "", "", ""},
		{"no cask", "", "Kubius.app", "com.example.kubius", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			apps, other := t.TempDir(), t.TempDir()
			dir := filepath.Join(apps, tt.in)
			if tt.in == "other" {
				dir = other
			}
			path := installVersion(t, dir, tt.app, tt.id, tt.version)
			noCasks(t, fake)
			catalogued(t, fake)
			found, err := withApps(fake, apps, other).AppsByHand(t.Context())
			want := []brew.ByHand{{Path: path, Name: strings.TrimSuffix(tt.app, ".app"), ID: tt.id, Cask: tt.want}}
			if err != nil || found.Unmatched != nil || !slices.Equal(found.Apps, want) {
				t.Errorf("AppsByHand() = %+v, %v; want %+v", found, err, want)
			}
		})
	}
}

// Of the casks putting an app where it is, the one without @ is the one
// when it updates itself and the app's major version is its or later, as
// an app that updates itself runs ahead of the catalogue: Homebrew adopts
// it as it is. An app of an earlier major version is the line's at that
// major version, never a channel's, and none's with no such line, as the
// one without @ would declare a major version the person may not own.
// Else the one whose version shares the most of the app's numbers it
// starts with is: the app's own version most of all, its build after a
// comma aside, as an older line's, an LTS or a beta; a tie going to the
// one without @. And only if installing it would take the app over: the
// app is its version, or an older copy of the same major version, which
// kit replaces, as a package's installer runs over it whether it updates
// itself or not. Never one whose install would refuse it.
func TestTheCaskOfTheAppsVersion(t *testing.T) {
	for _, tt := range []struct {
		name, app, id, version, want string
	}{
		{"an older major version, its line's", "Charles.app", "com.xk72.Charles", "4.6.8", "charles@4"},
		{"an older copy of an older line", "Charles.app", "com.xk72.Charles", "4.5.0", "charles@4"},
		{"an older major version, of no line", "Charles.app", "com.xk72.Charles", "3.11.6", ""},
		{"an older copy of the same major version", "Charles.app", "com.xk72.Charles", "5.1.0", "charles"},
		{"an older line's, as 4.2 is 4.2.0", "Godot.app", "org.godotengine.godot", "3.6.3.0", "godot@3"},
		{"the current version", "Godot.app", "org.godotengine.godot", "4.7.2", "godot"},
		{"an LTS", "Blender.app", "org.blenderfoundation.blender", "5.2.4", "blender@lts"},
		{"an older copy of an LTS, its major the current one's", "Blender.app", "org.blenderfoundation.blender", "5.2.3", "blender@lts"},
		{"an older LTS", "Blender.app", "org.blenderfoundation.blender", "4.5.6", ""},
		{"a beta", "KeePassXC.app", "org.keepassxc.keepassxc", "2.8.0-beta1", "keepassxc@beta"},
		{"a version, its build aside", "KeePassXC.app", "org.keepassxc.keepassxc", "2.8.0", "keepassxc"},
		{"as near the beta as the cask without @", "KeePassXC.app", "org.keepassxc.keepassxc", "2.7.10", "keepassxc"},
		{"newer than the cask's", "Example Editor.app", "com.example.editor", "3.3", ""},
		{"a prerelease", "Example Editor.app", "com.example.editor", "3.2-rc1", ""},
		{"no version", "Example Editor.app", "com.example.editor", "", ""},
		{"a cask of the latest", "Nightly.app", "com.example.nightly", "1.0", ""},
		{"a cask that updates itself, a newer copy", "Spaceman.app", "dev.original.Spaceman", "1.1", "spaceman"},
		{"a cask that updates itself, a later major version", "Google Chrome.app", "com.google.Chrome", "159.0.8200.12", "google-chrome"},
		{"a cask that updates itself, at the beta's version", "Dropbox.app", "com.getdropbox.dropbox", "281.3.5397", "dropbox"},
		{"a cask that updates itself, a beta build", "1Password.app", "com.1password.1password", "8.12.48-24.BETA", "1password"},
		{"a cask that updates itself, a version that doesn't start with numbers", "Spaceman.app", "dev.original.Spaceman", "Build 7", "spaceman"},
		{"a cask that updates itself, an earlier major version, its line's", "BBEdit.app", "com.barebones.bbedit", "14.6.2", "bbedit@14"},
		{"a cask that updates itself, an earlier major version of no line", "BBEdit.app", "com.barebones.bbedit", "15.0.3", ""},
		{"a cask that updates itself, an ESR", "Firefox.app", "org.mozilla.firefox", "153.7.0", "firefox@esr"},
		{"a cask that updates itself, its own major version beside an ESR and a beta", "Firefox.app", "org.mozilla.firefox", "160.0", "firefox"},
		{"a cask that updates itself, ahead of the beta", "Firefox.app", "org.mozilla.firefox", "162.0", "firefox"},
		{"a cask that updates itself, an earlier major version, never a channel's", "Little Snitch.app", "at.obdev.littlesnitch", "5.9.1", "little-snitch@5"},
		{"a cask that updates itself, an earlier major version, with only a channel's", "1Password.app", "com.1password.1password", "7.9", ""},
		{"a cask that updates itself, an earlier major version of no line at all", "Google Chrome.app", "com.google.Chrome", "120.0", ""},
		{"a package that updates itself, an older copy", "Monitor.app", "com.example.monitor", "2.7.0", "monitor"},
		{"a package that updates itself, its version, as a prefix of it", "Monitor.app", "com.example.monitor", "2.8.1", "monitor"},
		{"a package that updates itself, its version", "Monitor.app", "com.example.monitor", "2.8.1.316", "monitor"},
		{"a package that updates itself, a newer copy", "Monitor.app", "com.example.monitor", "2.9", ""},
		{"a package that updates itself, an earlier major version", "Monitor.app", "com.example.monitor", "1.9", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			apps := t.TempDir()
			path := installVersion(t, apps, tt.app, tt.id, tt.version)
			noCasks(t, fake)
			catalogued(t, fake)
			found, err := withApps(fake, apps).AppsByHand(t.Context())
			want := []brew.ByHand{{Path: path, Name: strings.TrimSuffix(tt.app, ".app"), ID: tt.id, Cask: tt.want}}
			if err != nil || found.Unmatched != nil || !slices.Equal(found.Apps, want) {
				t.Errorf("AppsByHand() = %+v, %v; want %+v", found, err, want)
			}
		})
	}
}

// When Homebrew's catalogue can't be read, which casks install the apps
// installed by hand can't be told, and says why: the apps are found all
// the same, with none. With no app by hand, Homebrew is asked nothing.
func TestAppsByHandWithoutACatalogue(t *testing.T) {
	for _, tt := range []struct {
		name    string
		payload string
		why     string
	}{
		{"none in the cache", "", "no catalogue of casks in Homebrew's cache"},
		{"one that doesn't read", `{"protected":"e30"}` + "\n" + `{"casks": [`, "read Homebrew's catalogue of casks"},
		{"one with no casks", `{"protected":"e30"}` + "\n" + `{"formulae": {}}`, "it lists no casks"},
		{"no line of its signature", `{"casks": {}}`, "its signature"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			apps := t.TempDir()
			path := installApp(t, apps, "Google Chrome.app", "com.google.Chrome")
			noCasks(t, fake)
			cache := t.TempDir()
			if tt.payload != "" {
				writeFile(t, filepath.Join(cache, "api", "internal", "packages.arm64_tahoe.jws.json.payload"), tt.payload)
			}
			fake.On("brew", "--cache").Prints(cache + "\n")
			found, err := withApps(fake, apps).AppsByHand(t.Context())
			want := []brew.ByHand{{Path: path, Name: "Google Chrome", ID: "com.google.Chrome"}}
			if err != nil || found.Unmatched == nil || !strings.Contains(found.Unmatched.Error(), tt.why) || !slices.Equal(found.Apps, want) {
				t.Errorf("AppsByHand() = %+v, %v; want %+v, unmatched: %s", found, err, want, tt.why)
			}
		})
	}
	fake := runnertest.New(t)
	noCasks(t, fake)
	if found, err := withApps(fake, t.TempDir()).AppsByHand(t.Context()); found.Apps != nil || found.Unmatched != nil || err != nil || len(fake.Calls()) > 0 {
		t.Errorf("AppsByHand() of none = %+v, %v, running %q; want none, Homebrew asked nothing", found, err, fake.Calls())
	}
}

// The catalogue read is Homebrew's newest, as brew --cache names its cache,
// asked once; and read again once it's changed, as brew update changes it.
func TestTheCatalogueIsTheNewestAsItChanges(t *testing.T) {
	fake := runnertest.New(t)
	apps := t.TempDir()
	installApp(t, apps, "Google Chrome.app", "com.google.Chrome")
	noCasks(t, fake)
	cache := t.TempDir()
	fake.On("brew", "--cache").Prints(cache + "\n")
	internal := filepath.Join(cache, "api", "internal")
	writeFile(t, filepath.Join(internal, "packages.x86_64_sequoia.jws.json.payload"), `{"protected":"e30"}`+"\n"+`{"casks": {}}`)
	writeFile(t, filepath.Join(internal, "packages.arm64_tahoe.jws.json.payload"), fixture(t, "catalogue.jws.json.payload"))
	old := filepath.Join(internal, "packages.x86_64_sequoia.jws.json.payload")
	past, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(old, past.ModTime().AddDate(0, 0, -1), past.ModTime().AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	h := withApps(fake, apps)
	cask := func() string {
		t.Helper()
		found, err := h.AppsByHand(t.Context())
		if err != nil || found.Unmatched != nil || len(found.Apps) != 1 {
			t.Fatalf("AppsByHand() = %+v, %v", found, err)
		}
		return found.Apps[0].Cask
	}
	if got := cask(); got != "google-chrome" {
		t.Errorf("by the newest catalogue, Chrome's cask is %q, want google-chrome", got)
	}
	writeFile(t, filepath.Join(internal, "packages.arm64_tahoe.jws.json.payload"), `{"protected":"e30"}`+"\n"+`{"casks": {"chrome-renamed": {"auto_updates": true, "raw_artifacts": [[":uninstall", {":quit": "com.google.Chrome"}], [":app", ["Google Chrome.app"]]]}}}`)
	if got := cask(); got != "chrome-renamed" {
		t.Errorf("once the catalogue's changed, Chrome's cask is %q, want chrome-renamed", got)
	}
	if n := len(slices.DeleteFunc(fake.Calls(), func(c string) bool { return c != "brew --cache" })); n != 1 {
		t.Errorf("ran brew --cache %d times, want once", n)
	}
}

// When Homebrew's cache can't be found, which casks install the apps can't
// be told, saying why.
func TestAppsByHandWithoutHomebrewsCache(t *testing.T) {
	fake := runnertest.New(t)
	apps := t.TempDir()
	installApp(t, apps, "Tool.app", "com.example.tool")
	noCasks(t, fake)
	fake.On("brew", "--cache").Exits(1).PrintsToStderr("Error: broken")
	found, err := withApps(fake, apps).AppsByHand(t.Context())
	if exit, ok := errors.AsType[*runner.ExitError](found.Unmatched); err != nil || !ok || !strings.HasPrefix(found.Unmatched.Error(), "find Homebrew's cache") || exit.Code != 1 {
		t.Errorf("AppsByHand() = unmatched %v, %v; want Homebrew's cache not found", found.Unmatched, err)
	}
}

// The apps installed by hand that a cask installs are the casks' drift,
// each by its cask, saying what the app is, its bundle id the item's App,
// the look said to have gone well; not one whose cask is declared,
// which stays missing, its install adopting the app; nor one no cask
// installs.
func TestTheCasksAppsInstalledByHandAreDrift(t *testing.T) {
	fake := runnertest.New(t)
	apps := t.TempDir()
	installApp(t, apps, "Google Chrome.app", "com.google.Chrome")
	installApp(t, apps, "Spaceman.app", "dev.original.Spaceman")
	installApp(t, apps, "1Password.app", "com.1password.1password")
	installApp(t, apps, "Kubius.app", "com.example.kubius")
	noCasks(t, fake)
	catalogued(t, fake)
	fake.On("brew", "list", "--cask", "--full-name", "-1")
	fake.On("brew", "info", "--json=v2", "--cask", "1password").Prints(`{"formulae": [], "casks": [{"token": "1password", "full_token": "1password"}]}`)
	got := kind.Drift(t.Context(), withApps(fake, apps).Casks(), config.List{Kind: "cask", Entries: []config.Entry{{Name: "1password"}}})
	want := []check.Item{
		{ID: "cask:1password", Name: "1password", State: kind.Missing, Action: kind.Install},
		{ID: "cask:google-chrome", Name: "google-chrome", State: kind.ByHand, Detail: "installed by hand as Google Chrome.app", App: "com.google.Chrome"},
		{ID: "cask:spaceman", Name: "spaceman", State: kind.ByHand, Detail: "installed by hand as Spaceman.app", App: "dev.original.Spaceman"},
	}
	if got.State != check.Attention || !slices.Equal(got.Items, want) || got.HandLook == nil || got.HandLook.Unseen != "" {
		t.Errorf("Drift() = %+v\nwant items %+v", got, want)
	}
}

// When which casks install the apps installed by hand can't be told,
// however that fails, none is drift, and the casks' check stands as ever,
// its HandLook saying why, so what it couldn't see is kept, as does the
// run's log.
func TestTheCasksAppsByHandThatCantBeTold(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(t *testing.T, fake *runnertest.Fake)
		why   string
	}{
		{"no catalogue", func(t *testing.T, fake *runnertest.Fake) {
			noCasks(t, fake)
			fake.On("brew", "--cache").Prints(t.TempDir() + "\n")
		}, "no catalogue of casks in Homebrew's cache"},
		{"no Caskroom told", func(t *testing.T, fake *runnertest.Fake) {
			fake.On("brew", "--caskroom").Exits(1).PrintsToStderr("Error: broken\n")
		}, "find the casks' apps: "},
		{"a cask brew info can't describe, gone from its tap", func(t *testing.T, fake *runnertest.Fake) {
			caskroom(t, fake, map[string]string{"gone": ""})
			fake.On("brew", "info", "--json=v2", "--cask", "gone").Exits(1).PrintsToStderr("Error: Cask 'gone' is unavailable\n")
		}, "find the casks' apps: "},
		{"pkgutil failing", func(t *testing.T, fake *runnertest.Fake) {
			caskroom(t, fake, map[string]string{"driver": `[{"uninstall": [{"pkgutil": "com.example.driver"}]}]`})
			catalogued(t, fake)
			fake.On("pkgutil", "--pkgs").Exits(1)
		}, "find the casks' packages' apps: "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			apps := t.TempDir()
			installApp(t, apps, "Google Chrome.app", "com.google.Chrome")
			fake.On("brew", "list", "--cask", "--full-name", "-1")
			tt.setup(t, fake)
			var logged []string
			ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l.Line) })
			got := kind.Drift(ctx, withApps(fake, apps).Casks(), config.List{Kind: "cask"})
			why := "kit can't tell which apps are installed by hand: " + tt.why
			if got.State != check.OK || len(got.Items) != 0 || got.HandLook == nil || !strings.HasPrefix(got.HandLook.Unseen, why) {
				t.Errorf("Drift() = %+v, want ok, nothing by hand, the look unseen: %s", got, tt.why)
			}
			if len(logged) != 1 || !strings.HasPrefix(logged[0], why) {
				t.Errorf("logged %q, want why: %s", logged, tt.why)
			}
		})
	}
}

// Without brew on kit's PATH, which casks install the apps installed by
// hand can't be told: that's no look that found none.
func TestTheCasksAppsByHandWithoutBrew(t *testing.T) {
	apps := t.TempDir()
	installApp(t, apps, "Google Chrome.app", "com.google.Chrome")
	found, err := withApps(runnertest.New(t), apps).Casks().(kind.Adoptable).InstalledByHand(t.Context())
	if !errors.Is(err, runner.ErrNotFound) || found != nil {
		t.Errorf("InstalledByHand() = %v, %v; want brew not found", found, err)
	}
}

// Finding what the casks could take over asks brew where the Caskroom is
// and for its catalogue alone, as the installed casks' receipts say what
// they put in place; and pkgutil nothing while no app left a cask installs,
// though a cask's package might have put it there. Finding every app by
// hand, as a snapshot does, asks pkgutil of each.
func TestFindingAppsByHandRunsLittle(t *testing.T) {
	fake := runnertest.New(t)
	apps := t.TempDir()
	installApp(t, apps, "Kubius.app", "com.example.kubius")
	installApp(t, apps, "Editor.app", "com.example.editor")
	caskroom(t, fake, map[string]string{
		"editor": `[{"app": ["Editor.app"]}]`,
		"driver": `[{"uninstall": [{"pkgutil": "com.example.driver"}]}]`,
	})
	catalogued(t, fake)
	fake.On("pkgutil", "--pkgs").Prints("com.apple.pkg.Core\n")
	found, err := withApps(fake, apps).Casks().(kind.Adoptable).InstalledByHand(t.Context())
	if err != nil || len(found) != 0 {
		t.Errorf("InstalledByHand() = %v, %v; want none", found, err)
	}
	if want := []string{"brew --caskroom", "brew --cache"}; !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q, want %q", fake.Calls(), want)
	}
	hand, err := withApps(fake, apps).AppsByHand(t.Context())
	if err != nil || !slices.Equal(names(hand.Apps), []string{"Kubius"}) || !slices.Contains(fake.Calls(), "pkgutil --pkgs") {
		t.Errorf("AppsByHand() = %+v, %v, running %q; want the Kubius, pkgutil asked", hand, err, fake.Calls())
	}
}
