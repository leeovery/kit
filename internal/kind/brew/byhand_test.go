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
	return installBuild(t, dir, name, id, version, "")
}

// installBuild is installVersion, the app's build given as build: none,
// given "".
func installBuild(t *testing.T, dir, name, id, version, build string) string {
	t.Helper()
	app := filepath.Join(dir, name)
	said := ""
	for key, value := range map[string]string{"CFBundleShortVersionString": version, "CFBundleVersion": build} {
		if value != "" {
			said += `<key>` + key + `</key><string>` + value + `</string>`
		}
	}
	writeFile(t, filepath.Join(app, "Contents", "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>`+id+`</string>`+said+`</dict></plist>`)
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
			path := installVersion(t, apps, "Google Chrome.app", "com.google.Chrome", "158.0.8127.37")
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
	installVersion(t, apps, "Google Chrome.app", "com.google.Chrome", "158.0.8127.37")
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
	installVersion(t, apps, "Google Chrome.app", "com.google.Chrome", "158.0.8127.37")
	installVersion(t, apps, "Spaceman.app", "dev.original.Spaceman", "1.0")
	installVersion(t, apps, "1Password.app", "com.1password.1password", "8.12.46")
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
	if want := []string{"com.1password.1password", "com.example.kubius", "com.google.Chrome", "dev.original.Spaceman"}; got.HandLook == nil || !slices.Equal(slices.Sorted(slices.Values(got.HandLook.Apps)), want) {
		t.Errorf("its look saw %+v, want every app installed by hand: %q", got.HandLook, want)
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
			installVersion(t, apps, "Google Chrome.app", "com.google.Chrome", "158.0.8127.37")
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
	installVersion(t, apps, "Google Chrome.app", "com.google.Chrome", "158.0.8127.37")
	found, err := withApps(runnertest.New(t), apps).Casks().(kind.Adoptable).InstalledByHand(t.Context())
	if !errors.Is(err, runner.ErrNotFound) || found.Things != nil || found.Apps != nil {
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
	if err != nil || len(found.Things) != 0 || !slices.Equal(found.Apps, []string{"com.example.kubius"}) {
		t.Errorf("InstalledByHand() = %v, %v; want none to take over, the Kubius installed by hand", found, err)
	}
	if want := []string{"brew --caskroom", "brew --cache"}; !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q, want %q", fake.Calls(), want)
	}
	hand, err := withApps(fake, apps).AppsByHand(t.Context())
	if err != nil || !slices.Equal(names(hand.Apps), []string{"Kubius"}) || !slices.Contains(fake.Calls(), "pkgutil --pkgs") {
		t.Errorf("AppsByHand() = %+v, %v, running %q; want the Kubius, pkgutil asked", hand, err, fake.Calls())
	}
}
