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
// the app its package installs; one the cask puts elsewhere isn't looked
// for there. Otherwise the cask isn't installed, saying the app there isn't
// its own, by the app and its bundle id: a fork under the cask's app's
// name, any app at all for a cask that states no id, or one with none. The
// rest go in all the same.
func TestAnAppAlreadyThere(t *testing.T) {
	for _, tt := range []struct {
		name, cask, app, id string
		refused             string
	}{
		{"the cask's own", "google-chrome", "Google Chrome.app", "com.google.Chrome", ""},
		{"its id in another case", "google-chrome", "Google Chrome.app", "COM.GOOGLE.CHROME", ""},
		{"by a token the cask had", "chrome", "Google Chrome.app", "com.google.Chrome", ""},
		{"in Homebrew's own tap", "homebrew/cask/google-chrome", "Google Chrome.app", "com.google.Chrome", ""},
		{"its package's", "zoom", "zoom.us.app", "us.zoom.xos", ""},
		{"by its target", "browser", "Browser.app", "com.example.browser", ""},
		{"one the cask puts elsewhere", "sidekick", "Sidekick.app", "com.example.other", ""},
		{"a fork under its name", "spaceman", "Spaceman.app", "dev.fork.Spaceman", "Spaceman.app is already in %s and isn't the cask's (dev.fork.Spaceman)"},
		{"another app by a token the cask had", "chrome", "Google Chrome.app", "com.example.fork", "Google Chrome.app is already in %s and isn't the cask's (com.example.fork)"},
		{"not its package's", "zoom", "zoom.us.app", "com.example.other", "zoom.us.app is already in %s and isn't the cask's (com.example.other)"},
		{"a cask stating no id", "quiet-tool", "Quiet Tool.app", "com.example.quiet", "Quiet Tool.app is already in %s and isn't the cask's (com.example.quiet)"},
		{"an app with no bundle id", "google-chrome", "Google Chrome.app", "", "Google Chrome.app is already in %s and isn't the cask's (no bundle id)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, apps, casks := adopting(t)
			installApp(t, apps, tt.app, tt.id)
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
Error: The bundle short version of /opt/homebrew/Caskroom/example-editor/3.0/Example Editor.app is 3.0 but is 2.9 for /Applications/Example Editor.app!
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
			app := installApp(t, apps, "Example Editor.app", "com.example.editor")
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
// adopt's failure stands; a replacing that fails says so too.
func TestReplacingLeavesWhatElseFailed(t *testing.T) {
	for _, tt := range []struct {
		name, listed, force, want string
	}{
		{"another failed", "example-editor\n", "", "Error: example-editor: It seems the existing App is different from the one being installed."},
		{"replacing failed", "", "Error: example-editor: It seems there is already an App at '/Applications/Example Editor.app'; overwriting failed.", "Error: example-editor: It seems the existing App is different from the one being installed.; Error: example-editor: It seems there is already an App at '/Applications/Example Editor.app'; overwriting failed."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, apps, casks := adopting(t)
			installApp(t, apps, "Example Editor.app", "com.example.editor")
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
	fake.On("brew", "info", "--json=v2", "--cask", "google-chrome", "spaceman").Prints(`{"formulae": [], "casks": [{"token": "google-chrome", "full_token": "google-chrome"}, {"token": "spaceman", "full_token": "spaceman"}]}`)
	fake.On("brew", "info", "--json=v2", "--cask", "spaceman").Prints(`{"formulae": [], "casks": [{"token": "spaceman", "full_token": "spaceman"}]}`)
	fake.On("brew", "install", "--cask", "--adopt", "google-chrome")
	s := kind.Step(casks, config.List{Kind: "cask", Entries: []config.Entry{{Name: "google-chrome"}, {Name: "spaceman"}}})
	err := s.Apply(t.Context(), s.Check(t.Context()))
	if want := "couldn't install spaceman: Spaceman.app is already in " + apps + " and isn't the cask's (dev.fork.Spaceman)"; err == nil || engine.Reason(err) != want {
		t.Errorf("Apply() = %v, want %q", err, want)
	}
}
