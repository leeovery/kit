package brew_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// installApp puts an app named name in dir, its bundle id id, as macOS
// keeps an app: a folder holding its Info.plist.
func installApp(t *testing.T, dir, name, id string) string {
	t.Helper()
	app := filepath.Join(dir, name)
	writeFile(t, filepath.Join(app, "Contents", "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>`+id+`</string></dict></plist>`)
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

// noCasks has Homebrew say no cask is installed.
func noCasks(fake *runnertest.Fake) {
	fake.On("brew", "info", "--json=v2", "--cask", "--installed").Prints(`{"formulae": [], "casks": []}`)
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
	fake.On("brew", "info", "--json=v2", "--cask", "--installed").Prints(`{"formulae": [], "casks": [
		{"token": "suite", "artifacts": [{"suite": ["Example Suite"]}]},
		{"token": "editor", "artifacts": [{"app": ["Editor.app"]}]},
		{"token": "driver", "artifacts": [{"uninstall": [{"pkgutil": "com.example.driver.*"}]}, {"pkg": ["Driver.pkg"]}]},
		{"token": "agent", "artifacts": [{"uninstall": [{"pkgutil": "com.example.agent"}]}, {"pkg": ["Agent.pkg"]}]}]}`)
	fake.On("pkgutil", "--pkgs").Prints("com.apple.pkg.Core\ncom.example.driver.app\ncom.example.agent\ncom.example.other\n")
	receipt := func(location string) string {
		return `<plist version="1.0"><dict><key>install-location</key><string>` + location + `</string><key>volume</key><string>/</string></dict></plist>`
	}
	fake.On("pkgutil", "--pkg-info-plist", "com.example.driver.app").Prints(receipt(strings.TrimPrefix(apps, "/")))
	fake.On("pkgutil", "--files", "com.example.driver.app").Prints("Driver.app\nDriver.app/Contents\nDriver.app/Contents/Info.plist\n")
	fake.On("pkgutil", "--pkg-info-plist", "com.example.agent").Prints(receipt(filepath.Join(apps, "Agent.app")))
	fake.On("pkgutil", "--files", "com.example.agent").Prints("Contents\nContents/Info.plist\n")
	catalogued(t, fake)
	found, unmatched, err := withApps(fake, apps, mine).AppsByHand(t.Context())
	if want := []string{"Google Chrome", "Other Tool", "Tool", "Mine"}; err != nil || unmatched != nil || !slices.Equal(names(found), want) {
		t.Errorf("AppsByHand() = %q, %v, %v; want %q", names(found), unmatched, err, want)
	}
	if slices.Contains(fake.Calls(), "pkgutil --files com.example.other") {
		t.Error("asked pkgutil of a package no cask installed")
	}
}

// An app installed by hand has the cask that installs it, as Homebrew's
// catalogue has it: of the casks installing an app of its name, or, if
// none does, those whose uninstall quits its bundle id, the one that states
// its id, in any case; one without @ over those with. None states a fork's
// id, under the cask's app's name; a cask that states no id, or is
// disabled, is never the one; nor is any when more than one would do.
func TestTheCaskThatInstallsAnAppByHand(t *testing.T) {
	for _, tt := range []struct {
		name, app, id, want string
	}{
		{"by its name", "Google Chrome.app", "com.google.Chrome", "google-chrome"},
		{"its id in another case", "Google Chrome.app", "COM.GOOGLE.CHROME", "google-chrome"},
		{"a fork, under its name", "Spaceman.app", "dev.fork.Spaceman", ""},
		{"the cask's own, under its name", "Spaceman.app", "dev.original.Spaceman", "spaceman"},
		{"its package's, as its uninstall deletes it, by the id it signals", "zoom.us.app", "us.zoom.xos", "zoom"},
		{"the plain token over one with @", "1Password.app", "com.1password.1password", "1password"},
		{"a cask stating no id", "Quiet Tool.app", "com.example.quiet", ""},
		{"by its target, the id its saved state is named for", "Browser.app", "com.example.browser", "browser"},
		{"under the name its target renames", "Chromium.app", "com.example.browser", ""},
		{"by the id a cask quits, under another name", "Editor.app", "com.example.editor", "example-editor"},
		{"from a folder of the download, by an id it signals of several", "Signals.app", "com.example.signals.agent", "signals"},
		{"a disabled cask", "Old Tool.app", "com.example.oldtool", ""},
		{"no bundle id", "Google Chrome.app", "", ""},
		{"no cask", "Kubius.app", "com.example.kubius", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			apps := t.TempDir()
			path := installApp(t, apps, tt.app, tt.id)
			noCasks(fake)
			catalogued(t, fake)
			found, unmatched, err := withApps(fake, apps).AppsByHand(t.Context())
			want := []brew.ByHand{{Path: path, Name: strings.TrimSuffix(tt.app, ".app"), ID: tt.id, Cask: tt.want}}
			if err != nil || unmatched != nil || !slices.Equal(found, want) {
				t.Errorf("AppsByHand() = %+v, %v, %v; want %+v", found, unmatched, err, want)
			}
		})
	}
}

// When Homebrew's catalogue can't be read, which casks install the apps
// installed by hand can't be told, and says why: the apps are found all
// the same, with none. With no app by hand, it isn't looked for.
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
			noCasks(fake)
			cache := t.TempDir()
			if tt.payload != "" {
				writeFile(t, filepath.Join(cache, "api", "internal", "packages.arm64_tahoe.jws.json.payload"), tt.payload)
			}
			fake.On("brew", "--cache").Prints(cache + "\n")
			found, unmatched, err := withApps(fake, apps).AppsByHand(t.Context())
			want := []brew.ByHand{{Path: path, Name: "Google Chrome", ID: "com.google.Chrome"}}
			if err != nil || unmatched == nil || !strings.Contains(unmatched.Error(), tt.why) || !slices.Equal(found, want) {
				t.Errorf("AppsByHand() = %+v, %v, %v; want %+v, unmatched: %s", found, unmatched, err, want, tt.why)
			}
		})
	}
	fake := runnertest.New(t)
	noCasks(fake)
	if found, unmatched, err := withApps(fake, t.TempDir()).AppsByHand(t.Context()); found != nil || unmatched != nil || err != nil || slices.Contains(fake.Calls(), "brew --cache") {
		t.Errorf("AppsByHand() of none = %+v, %v, %v, running %q; want none, the catalogue not looked for", found, unmatched, err, fake.Calls())
	}
}

// The catalogue read is Homebrew's newest, as brew --cache names its cache,
// asked once; and read again once it's changed, as brew update changes it.
func TestTheCatalogueIsTheNewestAsItChanges(t *testing.T) {
	fake := runnertest.New(t)
	apps := t.TempDir()
	installApp(t, apps, "Google Chrome.app", "com.google.Chrome")
	noCasks(fake)
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
		found, unmatched, err := h.AppsByHand(t.Context())
		if err != nil || unmatched != nil || len(found) != 1 {
			t.Fatalf("AppsByHand() = %+v, %v, %v", found, unmatched, err)
		}
		return found[0].Cask
	}
	if got := cask(); got != "google-chrome" {
		t.Errorf("by the newest catalogue, Chrome's cask is %q, want google-chrome", got)
	}
	writeFile(t, filepath.Join(internal, "packages.arm64_tahoe.jws.json.payload"), `{"protected":"e30"}`+"\n"+`{"casks": {"chrome-renamed": {"raw_artifacts": [[":uninstall", {":quit": "com.google.Chrome"}], [":app", ["Google Chrome.app"]]]}}}`)
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
	noCasks(fake)
	fake.On("brew", "--cache").Exits(1).PrintsToStderr("Error: broken")
	_, unmatched, err := withApps(fake, apps).AppsByHand(t.Context())
	if exit, ok := errors.AsType[*runner.ExitError](unmatched); err != nil || !ok || !strings.HasPrefix(unmatched.Error(), "find Homebrew's cache") || exit.Code != 1 {
		t.Errorf("AppsByHand() = unmatched %v, %v; want Homebrew's cache not found", unmatched, err)
	}
}
