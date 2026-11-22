package brew_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// adopting is Homebrew installing casks into apps, an Applications folder
// of their own, its catalogue the one in testdata, its update done.
func adopting(t *testing.T) (fake *runnertest.Fake, apps string, casks kind.Kind) {
	t.Helper()
	fake = runnertest.New(t)
	fake.On("brew", "update", "--quiet")
	catalogued(t, fake)
	apps = t.TempDir()
	return fake, apps, withApps(fake, apps, t.TempDir()).Casks()
}

// installs are the brew installs fake ran.
func installs(fake *runnertest.Fake) []string {
	return slices.DeleteFunc(fake.Calls(), func(c string) bool { return !strings.HasPrefix(c, "brew install") })
}

// Every cask Homebrew's catalogue knows installs with --adopt: with no app
// of its there, as ever, in one go.
func TestCasksInstallAdopting(t *testing.T) {
	fake, _, casks := adopting(t)
	fake.On("brew", "install", "--cask", "--adopt", "google-chrome", "zoom")
	if err := casks.Install(t.Context(), []string{"google-chrome", "zoom"}); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.Calls(), []string{"brew update --quiet", "brew --cache", "brew install --cask --adopt google-chrome zoom"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// An app already in the Applications folder where a cask puts its app is
// adopted when the cask states its bundle id, in any case, by its app or by
// the app its package installs. Otherwise the cask isn't installed, saying
// why, by the app: a fork under the cask's app's name, any app at all for a
// cask that states no id, or one with none, by its bundle id; the App
// Store's copy, whatever its id. The rest go in all the same.
func TestAnAppAlreadyThere(t *testing.T) {
	for _, tt := range []struct {
		name, cask, app, id string
		store               bool
		refused             string
	}{
		{"the cask's own", "google-chrome", "Google Chrome.app", "com.google.Chrome", false, ""},
		{"its id in another case", "google-chrome", "Google Chrome.app", "COM.GOOGLE.CHROME", false, ""},
		{"by a token the cask had", "chrome", "Google Chrome.app", "com.google.Chrome", false, ""},
		{"in Homebrew's own tap", "homebrew/cask/google-chrome", "Google Chrome.app", "com.google.Chrome", false, ""},
		{"its package's", "zoom", "zoom.us.app", "us.zoom.xos", false, ""},
		{"by its target", "browser", "Browser.app", "com.example.browser", false, ""},
		{"a fork under its name", "spaceman", "Spaceman.app", "dev.fork.Spaceman", false, "Spaceman.app is already in %s and isn't the cask's (dev.fork.Spaceman)"},
		{"another app by a token the cask had", "chrome", "Google Chrome.app", "com.example.fork", false, "Google Chrome.app is already in %s and isn't the cask's (com.example.fork)"},
		{"not its package's", "zoom", "zoom.us.app", "com.example.other", false, "zoom.us.app is already in %s and isn't the cask's (com.example.other)"},
		{"a cask stating no id", "quiet-tool", "Quiet Tool.app", "com.example.quiet", false, "Quiet Tool.app is already in %s and isn't the cask's (com.example.quiet)"},
		{"an app with no bundle id", "google-chrome", "Google Chrome.app", "", false, "Google Chrome.app is already in %s and isn't the cask's (no bundle id)"},
		{"the App Store's, its id the cask's", "google-chrome", "Google Chrome.app", "com.google.Chrome", true, "Google Chrome.app is already in %s and is the App Store's"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, apps, casks := adopting(t)
			app := installApp(t, apps, tt.app, tt.id)
			if tt.store {
				writeFile(t, filepath.Join(app, "Contents", "_MASReceipt", "receipt"), "")
			}
			fake.On("brew", "install", "--cask", "--adopt", tt.cask, "1password")
			fake.On("brew", "install", "--cask", "--adopt", "1password")
			err := casks.Install(t.Context(), []string{tt.cask, "1password"})
			want := []string{"brew install --cask --adopt " + tt.cask + " 1password"}
			if tt.refused != "" {
				want = []string{"brew install --cask --adopt 1password"}
			}
			if got := installs(fake); !slices.Equal(got, want) {
				t.Errorf("ran %q, want %q", got, want)
			}
			switch {
			case tt.refused == "" && err != nil:
				t.Errorf("Install() = %v, want the app adopted", err)
			case tt.refused != "" && (err == nil || err.Error() != strings.Replace(tt.refused, "%s", apps, 1)):
				t.Errorf("Install() = %v, want %q", err, strings.Replace(tt.refused, "%s", apps, 1))
			}
		})
	}
}

// A cask moving into place more than kit can vet, an app outside the
// Applications folder or anything but an app, installs as ever, without
// --adopt, whatever is there: --adopt would take over, and --force delete,
// whatever is at those targets.
func TestACaskKitCantVetInstallsAsEver(t *testing.T) {
	for _, tt := range []struct {
		name, cask, app, id string
	}{
		{"its app outside the Applications folder", "sidekick", "Sidekick.app", "com.example.other"},
		{"a suite beside its app", "office-tools", "Office.app", "com.example.fork"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, apps, casks := adopting(t)
			installApp(t, apps, tt.app, tt.id)
			fake.On("brew", "install", "--cask", tt.cask)
			fake.On("brew", "install", "--cask", "--adopt", "1password")
			if err := casks.Install(t.Context(), []string{tt.cask, "1password"}); err != nil {
				t.Fatal(err)
			}
			if got, want := installs(fake), []string{"brew install --cask " + tt.cask, "brew install --cask --adopt 1password"}; !slices.Equal(got, want) {
				t.Errorf("ran %q, want %q", got, want)
			}
		})
	}
}

// Only the Applications folder casks put their apps in is looked in: an
// app in another folder apps are installed in, as ~/Applications, is in no
// cask's way, so is neither adopted nor refused.
func TestOnlyTheCasksFolderIsLookedIn(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "update", "--quiet")
	catalogued(t, fake)
	other := t.TempDir()
	installApp(t, other, "Spaceman.app", "dev.fork.Spaceman")
	fake.On("brew", "install", "--cask", "--adopt", "spaceman")
	if err := withApps(fake, t.TempDir(), other).Casks().Install(t.Context(), []string{"spaceman"}); err != nil {
		t.Errorf("Install() = %v, want spaceman installed, the app in another folder left alone", err)
	}
}

// An app that can't be looked at can't be told to be the cask's: the cask
// isn't installed, saying so.
func TestAnAppThatCantBeLookedAt(t *testing.T) {
	fake, apps, casks := adopting(t)
	if err := os.Chmod(apps, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(apps, 0o700) })
	err := casks.Install(t.Context(), []string{"google-chrome"})
	if want := "can't tell whose Google Chrome.app in " + apps + " is"; err == nil || !strings.HasPrefix(err.Error(), want) || len(installs(fake)) != 0 {
		t.Errorf("Install() = %v, running %q; want %q, nothing installed", err, installs(fake), want)
	}
}

// notAdopted is what brew install --adopt prints failing a cask whose app
// there isn't the same as its download, as Homebrew 7 says it.
const notAdopted = `==> Installing Cask example-editor
==> Adopting existing App at '/Applications/Example Editor.app'
Error: The bundle short version of /opt/homebrew/Caskroom/example-editor/3.2/Example Editor.app is 3.2 but is 3.1 for /Applications/Example Editor.app!
Error: example-editor: It seems the existing App is different from the one being installed.
`

// A cask Homebrew didn't adopt, as its app there differs from the cask's,
// is installed again, alone, with --force, which replaces the app, once its
// app is still there and still its own: never with --force first, and
// never when the adopt failed otherwise, or the app's gone, or isn't its
// own now. Replaced, and the rest in, nothing failed.
func TestAnAppNotAdoptedIsReplaced(t *testing.T) {
	for _, tt := range []struct {
		name     string
		stderr   string
		meantime func(t *testing.T, app string)
		replaced bool
	}{
		{"its own, still there", notAdopted, nil, true},
		{"gone meanwhile", notAdopted, func(t *testing.T, app string) {
			if err := os.RemoveAll(app); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"another's now", notAdopted, func(t *testing.T, app string) {
			installApp(t, filepath.Dir(app), filepath.Base(app), "com.example.fork")
		}, false},
		{"failing otherwise", "Error: example-editor: Download failed on Cask 'example-editor' with message: timed out\n", nil, false},
		{"another not adopted", "Error: google-chrome: It seems the existing App is different from the one being installed.\n", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, apps, casks := adopting(t)
			app := installVersion(t, apps, "Example Editor.app", "com.example.editor", "3.1")
			fake.On("brew", "install", "--cask", "--adopt", "example-editor", "google-chrome").Does(func() {
				if tt.meantime != nil {
					tt.meantime(t, app)
				}
			}).Exits(1).PrintsToStderr(tt.stderr)
			fake.On("brew", "install", "--cask", "--force", "example-editor")
			fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("example-editor\ngoogle-chrome\n")
			err := casks.Install(t.Context(), []string{"example-editor", "google-chrome"})
			want := []string{"brew install --cask --adopt example-editor google-chrome"}
			if tt.replaced {
				want = append(want, "brew install --cask --force example-editor")
			}
			if got := installs(fake); !slices.Equal(got, want) {
				t.Errorf("ran %q, want %q", got, want)
			}
			if (err == nil) != tt.replaced {
				t.Errorf("Install() = %v; want failed %v", err, !tt.replaced)
			}
		})
	}
}

// Replaced, a cask the adopt failed for otherwise is still missing, and the
// adopt's failure stands; a replacing that fails says so too. Refused, a
// cask says why in place of Homebrew's line, beside what else failed.
func TestReplacingLeavesWhatElseFailed(t *testing.T) {
	for _, tt := range []struct {
		name, version, listed, force, want string
	}{
		{"another failed", "3.1", "example-editor\n", "", "Error: google-chrome: Download failed"},
		{"replacing failed", "3.1", "", "Error: example-editor: It seems there is already an App at '/Applications/Example Editor.app'; overwriting failed.", "Error: example-editor: It seems the existing App is different from the one being installed.; Error: example-editor: It seems there is already an App at '/Applications/Example Editor.app'; overwriting failed."},
		{"refused, another failed", "2.9", "", "", "Error: google-chrome: Download failed; Example Editor.app is 2.9 and the cask's is 3.2: kit replaces only an older copy of the same major version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, apps, casks := adopting(t)
			installVersion(t, apps, "Example Editor.app", "com.example.editor", tt.version)
			fake.On("brew", "install", "--cask", "--adopt", "example-editor", "google-chrome").Exits(1).PrintsToStderr("Error: google-chrome: Download failed\n" + notAdopted)
			force := fake.On("brew", "install", "--cask", "--force", "example-editor")
			if tt.force != "" {
				force.Exits(1).PrintsToStderr(tt.force)
			}
			fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(tt.listed)
			err := casks.Install(t.Context(), []string{"example-editor", "google-chrome"})
			if err == nil || engine.Reason(err) != tt.want {
				t.Errorf("Install() = %v, want %q", err, tt.want)
			}
		})
	}
}

// An app Homebrew didn't adopt is replaced only when it's an older copy of
// the same major version than the cask's, as both say in dotted numbers,
// its build after a comma aside, as --force deletes it. Any other, another
// major version, a newer copy, a prerelease, the same version of another
// build, an app of no version or one kit can't read, or a cask of the
// latest, is left, and the cask fails saying why.
func TestAnAppIsReplacedOnlyAsAnOlderCopyOfItsMajorVersion(t *testing.T) {
	const why = ": kit replaces only an older copy of the same major version"
	for _, tt := range []struct {
		name, cask, app, id, version, refused string
	}{
		{"an older copy", "example-editor", "Example Editor.app", "com.example.editor", "3.1", ""},
		{"an older copy, as 3.1 is 3.1.0", "example-editor", "Example Editor.app", "com.example.editor", "3.1.0.9", ""},
		{"an older copy than a version and build", "keepassxc", "KeePassXC.app", "org.keepassxc.keepassxc", "2.7.10", ""},
		{"an older major version", "charles", "Charles.app", "com.xk72.Charles", "4.6.8", "Charles.app is 4.6.8 and the cask's is 5.2.3" + why},
		{"a newer copy", "example-editor", "Example Editor.app", "com.example.editor", "3.3", "Example Editor.app is 3.3 and the cask's is 3.2" + why + ", and this one is newer"},
		{"a newer major version", "godot@3", "Godot.app", "org.godotengine.godot", "4.7.2", "Godot.app is 4.7.2 and the cask's is 3.6.3" + why + ", and this one is newer"},
		{"the same version, another build", "example-editor", "Example Editor.app", "com.example.editor", "3.2", "Example Editor.app is 3.2 and the cask's is 3.2" + why + ", and can't tell whether this one is"},
		{"a prerelease", "example-editor", "Example Editor.app", "com.example.editor", "3.2-rc1", "Example Editor.app is 3.2-rc1 and the cask's is 3.2" + why + ", and this one is a prerelease"},
		{"the cask's a prerelease", "keepassxc@beta", "KeePassXC.app", "org.keepassxc.keepassxc", "2.7.10", "KeePassXC.app is 2.7.10 and the cask's is 2.8.0-beta1" + why + ", and the cask's is a prerelease"},
		{"a cask of the latest", "nightly", "Nightly.app", "com.example.nightly", "1.0", "Nightly.app is 1.0 and the cask's is latest" + why + ", and latest says no version"},
		{"no version", "example-editor", "Example Editor.app", "com.example.editor", "", "Example Editor.app says no version and the cask's is 3.2" + why + ", and can't tell whether this one is"},
		{"a version kit can't read", "example-editor", "Example Editor.app", "com.example.editor", "3.1 (7)", "Example Editor.app is 3.1 (7) and the cask's is 3.2" + why + ", and can't tell whether this one is"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, apps, casks := adopting(t)
			installVersion(t, apps, tt.app, tt.id, tt.version)
			fake.On("brew", "install", "--cask", "--adopt", tt.cask).Exits(1).PrintsToStderr("Error: " + tt.cask + ": It seems the existing App is different from the one being installed.\n")
			fake.On("brew", "install", "--cask", "--force", tt.cask)
			fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(tt.cask + "\n")
			err := casks.Install(t.Context(), []string{tt.cask})
			want := []string{"brew install --cask --adopt " + tt.cask}
			if tt.refused == "" {
				want = append(want, "brew install --cask --force "+tt.cask)
			}
			if got := installs(fake); !slices.Equal(got, want) {
				t.Errorf("ran %q, want %q", got, want)
			}
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tt.refused {
				t.Errorf("Install() = %q, want %q", got, tt.refused)
			}
		})
	}
}

// Without an Applications folder to look in, a tap's casks, which
// Homebrew's catalogue doesn't hold, and every cask when the catalogue
// can't be read, saying so in the run's log, install as ever, without
// --adopt: what's there can't be told to be theirs, and Homebrew fails one
// whose app is there.
func TestCasksInstallAsEverWhenWhoseCantBeTold(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(t *testing.T, fake *runnertest.Fake) kind.Kind
		names []string
		want  []string
		logs  bool
	}{
		{"no Applications folder", func(t *testing.T, fake *runnertest.Fake) kind.Kind {
			return withApps(fake).Casks()
		}, []string{"google-chrome"}, []string{"brew install --cask google-chrome"}, false},
		{"a tap's", func(t *testing.T, fake *runnertest.Fake) kind.Kind {
			return withApps(fake, t.TempDir()).Casks()
		}, []string{"owner/tap/tool"}, []string{"brew install --cask owner/tap/tool"}, false},
		{"unknown to the catalogue", func(t *testing.T, fake *runnertest.Fake) kind.Kind {
			catalogued(t, fake)
			return withApps(fake, t.TempDir()).Casks()
		}, []string{"google-chrome", "brand-new"}, []string{"brew install --cask brand-new", "brew install --cask --adopt google-chrome"}, false},
		{"no catalogue", func(t *testing.T, fake *runnertest.Fake) kind.Kind {
			fake.On("brew", "--cache").Prints(t.TempDir() + "\n")
			return withApps(fake, t.TempDir()).Casks()
		}, []string{"google-chrome"}, []string{"brew install --cask google-chrome"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On("brew", "update", "--quiet")
			fake.On("brew", "tap").Prints("owner/tap\n")
			for _, cmd := range tt.want {
				fake.On("brew", strings.Fields(cmd)[1:]...)
			}
			var logged []event.Logged
			ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l) })
			if err := tt.setup(t, fake).Install(ctx, tt.names); err != nil {
				t.Fatal(err)
			}
			if got := installs(fake); !slices.Equal(got, tt.want) {
				t.Errorf("ran %q, want %q", got, tt.want)
			}
			if said := len(logged) == 1 && strings.Contains(logged[0].Line, "casks install without adopting") && !logged[0].Alert; said != tt.logs {
				t.Errorf("logged %+v; want it said %v", logged, tt.logs)
			}
		})
	}
}

// adoptingChrome is what brew install --cask --adopt google-chrome prints,
// as Homebrew 7 does, Chrome already there.
const adoptingChrome = `==> Fetching downloads for: google-chrome
✔︎ Cask google-chrome (158.0.8127.37)
==> Installing Cask google-chrome
==> Adopting existing App at '/Applications/Google Chrome.app'
🍺  google-chrome was successfully installed!
`

// An app adopted is its cask installed, as brew says it: the run's log is
// told of it downloaded, then installed.
func TestAnAppAdoptedIsInstalled(t *testing.T) {
	fake, apps, casks := adopting(t)
	installApp(t, apps, "Google Chrome.app", "com.google.Chrome")
	fake.On("brew", "list", "--cask", "--full-name", "-1")
	noCasks(t, fake)
	fake.On("brew", "info", "--json=v2", "--cask", "google-chrome").Prints(`{"formulae": [], "casks": [{"token": "google-chrome", "full_token": "google-chrome"}]}`)
	fake.On("brew", "install", "--cask", "--adopt", "google-chrome").Prints(adoptingChrome)
	var told []string
	ctx := event.WithLogging(t.Context(), func(l event.Logged) { told = append(told, l.Line) })
	s := kind.Step(casks, config.List{Kind: "cask", Entries: []config.Entry{{Name: "google-chrome"}}})
	if err := s.Apply(ctx, s.Check(ctx)); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Downloaded google-chrome", "Installed google-chrome"}; !slices.Equal(told, want) {
		t.Errorf("adopting told the log %q, want %q", told, want)
	}
}

// A cask whose app there isn't its own fails at apply's row, saying so:
// the app, where it is and its bundle id; the rest installed.
func TestAForkFailsItsCaskSayingWhy(t *testing.T) {
	fake, apps, casks := adopting(t)
	installApp(t, apps, "Spaceman.app", "dev.fork.Spaceman")
	fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("").Then().Prints("google-chrome\n")
	noCasks(t, fake)
	fake.On("brew", "info", "--json=v2", "--cask", "google-chrome", "spaceman").Prints(`{"formulae": [], "casks": [{"token": "google-chrome", "full_token": "google-chrome"}, {"token": "spaceman", "full_token": "spaceman"}]}`)
	fake.On("brew", "info", "--json=v2", "--cask", "spaceman").Prints(`{"formulae": [], "casks": [{"token": "spaceman", "full_token": "spaceman"}]}`)
	fake.On("brew", "install", "--cask", "--adopt", "google-chrome")
	s := kind.Step(casks, config.List{Kind: "cask", Entries: []config.Entry{{Name: "google-chrome"}, {Name: "spaceman"}}})
	err := s.Apply(t.Context(), s.Check(t.Context()))
	if want := "couldn't install spaceman: Spaceman.app is already in " + apps + " and isn't the cask's (dev.fork.Spaceman)"; err == nil || engine.Reason(err) != want {
		t.Errorf("Apply() = %v, want %q", err, want)
	}
}
