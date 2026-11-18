package boot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	kitstate "github.com/leeovery/kit/internal/state"
	"github.com/leeovery/kit/internal/steps"
)

const appsToml = "format = 1\nprimary = \"laptop\"\nterminal = \"ghostty\"\n[macs.laptop]\n[macs.studio]\n"

// read leaves the config repository's files where signing in does, and
// names this Mac, as the boot order finds them.
func read(t *testing.T, b *Boot, files map[string]string, mac string) {
	t.Helper()
	writeFiles(t, b.archive(), files)
	if mac != "" {
		if err := config.WriteMachine(b.State, mac); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// bundle puts an app in the Applications folder, saying its version.
func bundle(t *testing.T, b *Boot, name, version string) {
	t.Helper()
	writeFiles(t, filepath.Join(b.Applications, name, "Contents"), map[string]string{"Info.plist": `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>` + version + `</string></dict></plist>`})
}

// The boot order is planned from kit-config: kit itself after kit-config's
// files are linked, unless the boot runs a build of its own; the apps
// kit.toml names, and,
// with a terminal, Full Disk Access for it and the hand-off to it, each
// step needing the one before.
func TestOrder(t *testing.T) {
	for _, tt := range []struct {
		name, toml, from string
		want             []string
	}{
		{"a terminal", appsToml, "", []string{"mac", "password", "homebrew", "apps", "kit-config", "kit", "full-disk-access", "terminal"}},
		{"neither", kitToml, "", []string{"mac", "password", "homebrew", "kit-config", "kit"}},
		{"a build of its own", appsToml, "/Users/someone/kit", []string{"mac", "password", "homebrew", "apps", "kit-config", "full-disk-access", "terminal"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := newBoot(t, runnertest.New(t), GitHub{}, &asker{})
			b.Getenv = env(map[string]string{"KIT_FROM": tt.from})
			read(t, b, map[string]string{config.File: tt.toml}, "")
			order := b.Order()
			var names []string
			for i, s := range order {
				names = append(names, s.Name)
				if s.Area != AreaBoot || i > 0 && !slices.Equal(s.Needs, []string{order[i-1].Name}) {
					t.Errorf("%s: area %q, needs %q", s.Name, s.Area, s.Needs)
				}
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("order = %q; want %q", names, tt.want)
			}
		})
	}
	b, _ := newBoot(t, runnertest.New(t), GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "")
	order := b.Order()
	if access, handOver := order[6], order[7]; access.Waiting != "for Ghostty" || handOver.Title != "Ghostty" {
		t.Errorf("Full Disk Access waits %q, the hand-off is %q; want them the terminal's", access.Waiting, handOver.Title)
	}
}

// The password, once, in its row, asked again when it's wrong, given to
// sudo on its input and kept for the run; then Touch ID turned on for sudo,
// as the Mac declares it.
// The Mac's features are read from declarations holding kinds' sections
// too, those the boot's given.
func TestDeclaresAmongTheKindsSections(t *testing.T) {
	b, _ := newBoot(t, runnertest.New(t), GitHub{}, &asker{})
	b.Kinds = kinds.Sections()
	read(t, b, map[string]string{config.File: kitToml, "shared/declarations": "[features]\ntouch-id-sudo\n\n[homebrew formulae]\njq\n"}, "laptop")
	if !b.declares("touch-id-sudo") {
		t.Error("touch-id-sudo isn't declared, want it read beside the formulae")
	}
}

func TestPasswordOnceThenTouchID(t *testing.T) {
	run := runnertest.New(t)
	run.On("brew", "--version").Fails(runner.ErrNotFound)
	sudo := run.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(2)
	ask := &asker{secrets: []string{"wrong", "right"}}
	b, s := newBoot(t, run, GitHub{}, ask)
	b.Getenv = func(name string) string { return map[string]string{"TERM_PROGRAM": "Apple_Terminal"}[name] }
	read(t, b, map[string]string{config.File: kitToml, "shared/declarations": "[features]\ntouch-id-sudo\n"}, "laptop")
	var kept string
	b.Keep = func(p string) { kept = p }
	run.On("sudo", "-n", "tee", b.SudoLocal).Does(func() {
		_ = os.WriteFile(b.SudoLocal, []byte("auth       sufficient     pam_tid.so\n"), 0o600)
	})
	step := b.passwordStep()
	if res := step.Check(context.Background()); res.State != check.Attention {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if !slices.Equal(sudo.Answered(), []string{"wrong", "right"}) || kept != "right" || !b.Held() {
		t.Errorf("sudo given %q, kept %q, held %v; want the password typed, the second kept", sudo.Answered(), kept, b.Held())
	}
	if want := []string{"password: this Mac's password, once", "password: that wasn't it: try again"}; !slices.Equal(ask.asked, want) {
		t.Errorf("asked %q; want %q", ask.asked, want)
	}
	for _, c := range run.Calls() {
		if strings.Contains(c, "right") {
			t.Errorf("the password was on a command line: %s", c)
		}
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "Touch ID on for sudo" {
		t.Errorf("check after = %+v", res)
	}
	var todo string
	for _, e := range s.events {
		if d, ok := e.(event.Doing); ok && d.Step == PasswordStep {
			todo = d.Todo
		}
	}
	if todo != "macOS asks whether Terminal may administer your computer: choose Allow" {
		t.Errorf("said %q before changing sudo's settings", todo)
	}
}

// macOS refusing the terminal leave to change sudo's settings leaves Touch
// ID off, for kit apply, and the boot goes on.
func TestTouchIDRefused(t *testing.T) {
	run := runnertest.New(t)
	run.On("brew", "--version").Fails(runner.ErrNotFound)
	run.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(1)
	b, _ := newBoot(t, run, GitHub{}, &asker{secrets: []string{"right"}})
	read(t, b, map[string]string{config.File: kitToml, "shared/declarations": "[features]\ntouch-id-sudo\n"}, "laptop")
	run.On("sudo", "-n", "tee", b.SudoLocal).Exits(1).PrintsToStderr("tee: /etc/pam.d/sudo_local: Operation not permitted")
	step := b.passwordStep()
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v; want the boot to go on", err)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "given · Touch ID off: kit apply asks again" {
		t.Errorf("check after = %+v", res)
	}
}

// A password sudo won't take stops the boot there, saying so; with
// Homebrew there and Touch ID not declared, nothing's asked.
func TestPasswordRefusedOrNotNeeded(t *testing.T) {
	run := runnertest.New(t)
	run.On("brew", "--version").Fails(runner.ErrNotFound)
	run.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(3).Exits(1)
	b, _ := newBoot(t, run, GitHub{}, &asker{secrets: []string{"a", "b", "c"}})
	read(t, b, map[string]string{config.File: kitToml}, "laptop")
	b.Keep = func(string) { t.Error("kept a password sudo refused") }
	if err := b.passwordStep().Apply(context.Background(), check.Result{}); err == nil || err.Error() != "sudo refused the password: paste the install line into Terminal again" || b.Held() {
		t.Errorf("apply = %v, held %v", err, b.Held())
	}

	run = runnertest.New(t)
	run.On("brew", "--version").Prints("Homebrew 7.0.9\n")
	b, _ = newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: kitToml}, "laptop")
	if res := b.passwordStep().Check(context.Background()); res.State != check.OK || res.Summary != "not needed" {
		t.Errorf("check = %+v; want nothing to ask", res)
	}
}

// Homebrew, installed by its own installer, unattended, once the password's
// held; then its version, and the Xcode tools'.
func TestHomebrew(t *testing.T) {
	run := runnertest.New(t)
	run.On("brew", "--version").Fails(runner.ErrNotFound).Then().Prints("Homebrew 7.0.9\n")
	run.On("pkgutil", "--pkg-info=com.apple.pkg.CLTools_Executables").Prints("package-id: com.apple.pkg.CLTools_Executables\nversion: 27.0.0.0.1.1757\nvolume: /\n")
	run.On("curl", "-fsSL", "--retry", "2", "-o", brew.InstallerFile(), "https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh")
	run.On("/bin/bash", brew.InstallerFile())
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	step := b.homebrewStep()
	if res := step.Check(context.Background()); res.State != check.Attention || res.Summary != "not installed" {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("apply without the password = %v", err)
	}
	b.held = true
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "7.0.9 · Xcode tools 27.0" {
		t.Errorf("check after = %+v", res)
	}
}

// env is a getenv reading vars alone.
func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// homebrewAt has brew say its prefix is a folder of the test's, and returns
// where Homebrew's kit is in it.
func homebrewAt(t *testing.T, run *runnertest.Fake) string {
	t.Helper()
	prefix := t.TempDir()
	run.On("brew", "--prefix").Prints(prefix + "\n")
	return filepath.Join(prefix, "bin", "kit")
}

// linkKit puts kit where Homebrew links it.
func linkKit(t *testing.T, kit string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(kit), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kit, nil, 0o755); err != nil {
		t.Fatal(err)
	}
}

// kit itself, installed through Homebrew by its formula's full name, which
// taps its tap: done when Homebrew's kit is in Homebrew's bin, and nothing
// kit itself, installed through Homebrew by its formula's full name, which
// taps its tap: done when Homebrew's kit is in Homebrew's bin, saying its
// version.
func TestKit(t *testing.T) {
	run := runnertest.New(t)
	kit := homebrewAt(t, run)
	run.On("brew", "install", "--formula", "leeovery/tools/kit").Does(func() { linkKit(t, kit) })
	run.On(kit, "--version").Prints("kit version 0.2.0\n")
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	b.Version = "0.2.0"
	step := b.kitStep()
	if res := step.Check(context.Background()); res.State != check.Attention || res.Summary != "not installed" {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "0.2.0 · through Homebrew" {
		t.Errorf("check after = %+v", res)
	}
	if !slices.Contains(run.Calls(), "brew install --formula leeovery/tools/kit") {
		t.Errorf("ran %q; want kit's formula installed", run.Calls())
	}
}

// Homebrew's kit there already is done, unless it's older than the boot's
// own, a release's: then it's upgraded. A boot that isn't a release's, as a
// build from source, compares none.
func TestKitThereAlready(t *testing.T) {
	for _, tt := range []struct {
		name, boot, homebrews string
		want                  check.State
	}{
		{"the same", "0.2.0", "0.2.0", check.OK},
		{"newer", "0.2.0", "0.2.1", check.OK},
		{"older", "0.2.0", "0.1.0", check.Attention},
		{"a boot from source", "dev", "0.1.0", check.OK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			run := runnertest.New(t)
			kit := homebrewAt(t, run)
			linkKit(t, kit)
			run.On(kit, "--version").Prints("kit version " + tt.homebrews + "\n").Then().Prints("kit version " + tt.boot + "\n")
			run.On("brew", "upgrade", "--formula", "leeovery/tools/kit")
			b, _ := newBoot(t, run, GitHub{}, &asker{})
			b.Version = tt.boot
			step := b.kitStep()
			res := step.Check(context.Background())
			if res.State != tt.want {
				t.Fatalf("check = %+v; want %s", res, tt.want)
			}
			if res.State == check.OK {
				return
			}
			if want := tt.homebrews + ", older than this kit, " + tt.boot; res.Summary != want {
				t.Errorf("check says %q; want %q", res.Summary, want)
			}
			if err := step.Apply(context.Background(), res); err != nil || !slices.Contains(run.Calls(), "brew upgrade --formula leeovery/tools/kit") {
				t.Errorf("apply = %v, ran %q; want kit's formula upgraded", err, run.Calls())
			}
			if res := step.Check(context.Background()); res.State != check.OK {
				t.Errorf("check after = %+v", res)
			}
		})
	}
}

// Installing kit's formula failing fails the step, saying so.
func TestKitFails(t *testing.T) {
	run := runnertest.New(t)
	homebrewAt(t, run)
	run.On("brew", "install", "--formula", "leeovery/tools/kit").Exits(1).PrintsToStderr("Error: kit: failed to download")
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	if err := b.kitStep().Apply(context.Background(), check.Result{}); err == nil || !strings.HasPrefix(err.Error(), "install kit through Homebrew: ") {
		t.Errorf("apply = %v; want it failed, saying so", err)
	}
}

// The apps kit.toml names, installed as casks where they're missing; then
// each with its version.
func TestApps(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	run.On("brew", "install", "--cask", "ghostty").Does(func() { bundle(t, b, "Ghostty.app", "1.3.1") })
	step := b.appsStep(b.apps(b.config()))
	found := step.Check(context.Background())
	if found.State != check.Attention || found.Summary != "to install: Ghostty" {
		t.Fatalf("check before = %+v", found)
	}
	if err := step.Apply(context.Background(), found); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "Ghostty 1.3.1" {
		t.Errorf("check after = %+v", res)
	}
	for _, cmd := range run.Commands() {
		if cmd.Name == "brew" && !cmd.SudoAsks {
			t.Errorf("%s ran without its sudo asking the boot for the password", cmd)
		}
	}
}

// Applied after a check that failed, finding nothing, the apps step installs
// what's missing all the same; with nothing missing, it runs nothing, never
// brew install --cask with no casks.
func TestAppsAfterAFailedCheck(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	run.On("brew", "install", "--cask", "ghostty").Does(func() { bundle(t, b, "Ghostty.app", "1.3.1") })
	step := b.appsStep(b.apps(b.config()))
	failed := check.Result{State: check.Failed, Reason: "didn't answer"}
	if err := step.Apply(context.Background(), failed); err != nil {
		t.Fatalf("apply, Ghostty missing = %v", err)
	}
	if err := step.Apply(context.Background(), failed); err != nil {
		t.Fatalf("apply, Ghostty there = %v", err)
	}
	if got, want := run.Calls(), []string{"brew install --cask ghostty"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q: Ghostty installed once, then nothing", got, want)
	}
}

// kit-config cloned through GitHub's CLI, installed and handed the
// sign-in, which kit's keychain item gives up, its own branch alone, never
// the prefs branch beside it; git signing in through it for that repository
// alone; then its files linked.
func TestKitConfig(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: kitToml}, "laptop")
	b.hold("gho_secret", "someone")
	run.On("brew", "install", "--formula", "gh").Does(func() {
		run.On("gh", "auth", "status", "--hostname", "github.com").Exits(1)
		run.On("gh", "auth", "login", "--hostname", "github.com", "--with-token")
		run.On("gh", "repo", "clone", "someone/kit-config", b.Config, "--", "--quiet", "--single-branch").Does(func() {
			writeFiles(t, b.Config, map[string]string{".git/HEAD": "ref: refs/heads/main\n", config.File: kitToml, "shared/home/.zshrc": "# zsh\n"})
		})
	})
	run.On("security", "delete-generic-password", "-s", keychainItem)
	run.On("git", "-C", b.Config, "config", "credential.https://github.com.helper", "!gh auth git-credential")
	run.On("git", "-C", b.Config, "--literal-pathspecs", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "shared/home").Prints("shared/home/.zshrc\x00")
	step := b.configStep()
	if res := step.Check(context.Background()); res.State != check.Attention || res.Summary != "not cloned yet" {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	for _, c := range run.Commands() {
		if c.Name == "gh" && slices.Contains(c.Args, "login") && c.Input != "gho_secret\n" {
			t.Errorf("signed GitHub's CLI in with input %q", c.Input)
		}
		if strings.Contains(c.String(), "gho_secret") {
			t.Errorf("the sign-in was on a command line: %s", c)
		}
	}
	if target, err := os.Readlink(filepath.Join(b.Home, ".zshrc")); err != nil || target != filepath.Join(b.Config, "shared/home/.zshrc") {
		t.Errorf("~/.zshrc links to %q (%v)", target, err)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "cloned · 1 file linked" {
		t.Errorf("check after = %+v", res)
	}
}

// A new Mac is added to the clone's kit.toml, uncommitted: started fresh,
// its table alone; started from another Mac, a copy of that one's folder,
// its table saying where it started from.
func TestKitConfigAddsANewMac(t *testing.T) {
	for _, tt := range []struct{ name, from, decl string }{{"fresh", "", ""}, {"from studio", "studio", "[homebrew formulae]\nffmpeg\n"}} {
		t.Run(tt.name, func(t *testing.T) {
			run := runnertest.New(t)
			b, _ := newBoot(t, run, GitHub{}, &asker{})
			read(t, b, map[string]string{config.File: kitToml}, "mini")
			if err := kitstate.Update(b.State, stateName, func(st *state) { st.NewMac, st.From = "mini", tt.from }); err != nil {
				t.Fatal(err)
			}
			writeFiles(t, b.Config, map[string]string{".git/HEAD": "ref: refs/heads/main\n", config.File: kitToml, "studio/declarations": "[homebrew formulae]\nffmpeg\n"})
			if err := b.configStep().Apply(context.Background(), check.Result{}); err != nil {
				t.Fatalf("apply = %v", err)
			}
			cfg, err := config.Load(b.Config, kinds.Sections())
			if err != nil || !cfg.Knows("mini") || cfg.Macs["mini"].From != tt.from {
				t.Fatalf("kit.toml after: %+v, %v", cfg, err)
			}
			if got, _ := os.ReadFile(filepath.Join(b.Config, "mini", "declarations")); string(got) != tt.decl {
				t.Errorf("mini/declarations = %q; want %q", got, tt.decl)
			}
			if cmds := run.Commands(); len(cmds) > 0 {
				t.Errorf("ran %s; want nothing committed", cmds)
			}
			if res := b.configStep().Check(context.Background()); res.State != check.OK || !strings.HasSuffix(res.Summary, "mini added to kit.toml") {
				t.Errorf("check = %+v", res)
			}
		})
	}
}

// Full Disk Access, from Terminal: System Settings opened at the list and
// Finder at the app, then done when the person says so. In the terminal,
// it's the grant itself that's checked.
func TestFullDiskAccess(t *testing.T) {
	run := runnertest.New(t)
	run.On("open", steps.AccessSettings)
	ask := &asker{picks: []string{"enter"}}
	b, _ := newBoot(t, run, GitHub{}, ask)
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	run.On("open", "-R", filepath.Join(b.Applications, "Ghostty.app"))
	t2, _ := b.terminal(b.config())
	step := b.accessStep(t2)
	if res := step.Check(context.Background()); res.State != check.Attention {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if want := []string{"full-disk-access: for Ghostty, before it first opens: System Settings is open at Full Disk Access, and Finder at Applications: drag Ghostty into the list, then press enter"}; !slices.Equal(ask.asked, want) {
		t.Errorf("asked %q", ask.asked)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "granted to Ghostty" {
		t.Errorf("check after = %+v", res)
	}

	b.Getenv = func(name string) string { return map[string]string{"TERM_PROGRAM": "ghostty"}[name] }
	safari := filepath.Join(b.Home, "Library", "Safari")
	if err := os.MkdirAll(safari, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(safari, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(safari, 0o700) })
	if res := step.Check(context.Background()); res.State != check.Attention || !strings.Contains(res.Reason, "quit it, add it") {
		t.Errorf("in Ghostty, without the grant: %+v", res)
	}
}

// opener is a terminal's driver for a test: it notes the windows it's
// asked to open, failing as told, else doing what it's told.
type opener struct {
	app     string
	windows []apps.Window
	fails   error
	does    func()
}

func (o *opener) Open(_ context.Context, _ runner.Runner, app string, w apps.Window) error {
	o.app = app
	o.windows = append(o.windows, w)
	if o.fails != nil {
		return o.fails
	}
	o.does()
	return nil
}

func (*opener) TabProgress() bool { return false }

// The hand-off, through the terminal's driver, whichever it is: a window
// opened running Homebrew's kit bootstrap, handed over, maximized, without
// this run's askpass; done once that kit arrives, saying what macOS asks
// meanwhile.
func TestHandOver(t *testing.T) {
	run := runnertest.New(t)
	kit := homebrewAt(t, run)
	b, s := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	driver := &opener{does: func() {
		go func() {
			time.Sleep(50 * time.Millisecond)
			arrived := &Boot{State: b.State, Now: time.Now, HandedOver: true}
			if err := arrived.Arrive(); err != nil {
				t.Error(err)
			}
		}()
	}}
	t2, _ := b.terminal(b.config())
	t2.Driver = driver
	step := b.handOverStep(t2)
	if res := step.Check(context.Background()); res.State != check.Attention {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	want := apps.Window{Command: []string{kit, "bootstrap", "--handed-over"}, Env: []string{"SUDO_ASKPASS="}, Maximized: true}
	if len(driver.windows) != 1 || !reflect.DeepEqual(driver.windows[0], want) || driver.app != filepath.Join(b.Applications, "Ghostty.app") {
		t.Errorf("opened %q with %+v; want %+v", driver.app, driver.windows, want)
	}
	var todo string
	for _, e := range s.events {
		if d, ok := e.(event.Doing); ok && d.Step == TerminalStep {
			todo = d.Todo
		}
	}
	if todo != "macOS asks before Ghostty first opens: choose Open" {
		t.Errorf("said %q meanwhile", todo)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "opened · kit carries on there" {
		t.Errorf("check after = %+v", res)
	}
	b.Getenv = func(name string) string { return map[string]string{"TERM_PROGRAM": "ghostty"}[name] }
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "opened" {
		t.Errorf("check in Ghostty = %+v", res)
	}
}

// A boot running a build of its own hands over to it, the build named on to
// the boot handed over to.
func TestHandOverToABuildOfItsOwn(t *testing.T) {
	b, _ := newBoot(t, runnertest.New(t), GitHub{}, &asker{})
	b.Getenv = env(map[string]string{"KIT_FROM": "/Users/someone/kit"})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	t2, _ := b.terminal(b.config())
	driver := &opener{fails: errors.New("stop here")}
	t2.Driver = driver
	_ = b.handOverStep(t2).Apply(context.Background(), check.Result{})
	want := apps.Window{Command: []string{b.Self, "bootstrap", "--handed-over"}, Env: []string{"SUDO_ASKPASS=", "KIT_FROM=/Users/someone/kit"}, Maximized: true}
	if len(driver.windows) != 1 || !reflect.DeepEqual(driver.windows[0], want) {
		t.Errorf("opened %+v; want %+v", driver.windows, want)
	}
}

// In the terminal kit.toml names, the boot carries on as Homebrew's kit; or,
// running a build of its own, as that build.
func TestCarriesOnAs(t *testing.T) {
	run := runnertest.New(t)
	kit := homebrewAt(t, run)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	if got, err := b.CarriesOnAs(context.Background()); err != nil || got != kit {
		t.Errorf("CarriesOnAs() = %q, %v; want Homebrew's kit, %q", got, err, kit)
	}
	b.Getenv = env(map[string]string{"KIT_FROM": "/Users/someone/kit"})
	if got, err := b.CarriesOnAs(context.Background()); err != nil || got != b.Self {
		t.Errorf("CarriesOnAs(), a build of its own = %q, %v; want itself, %q", got, err, b.Self)
	}
}

// In the terminal kit.toml names already, the boot becomes Homebrew's kit,
// unless it's that kit, a link to it included, or a build of its own; brew
// failing to say where it is fails.
func TestBecomes(t *testing.T) {
	for _, tt := range []struct {
		name, from string
		self       func(kit string) string
		brewFails  bool
		becomes    bool
		fails      bool
	}{
		{name: "the download", self: func(string) string { return "/Users/someone/tmp/kit" }, becomes: true},
		{name: "Homebrew's kit, through its link", self: func(kit string) string { return linkTo(t, kit) }},
		{name: "a build of its own", from: "/Users/someone/kit", self: func(string) string { return "/Users/someone/.local/bin/kit" }},
		{name: "brew failing", self: func(string) string { return "/Users/someone/tmp/kit" }, brewFails: true, fails: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			run := runnertest.New(t)
			kit := homebrewAt(t, run)
			linkKit(t, kit)
			if tt.brewFails {
				run.On("brew", "--prefix").Exits(1)
			}
			b, _ := newBoot(t, run, GitHub{}, &asker{})
			b.Getenv = env(map[string]string{"KIT_FROM": tt.from})
			got, err := b.Becomes(context.Background(), tt.self(kit))
			want := ""
			if tt.becomes {
				want = kit
			}
			if got != want || (err != nil) != tt.fails {
				t.Errorf("Becomes() = %q, %v; want %q, failing %v", got, err, want, tt.fails)
			}
		})
	}
}

// linkTo makes a link to path, as Homebrew's bin holds, and returns it.
func linkTo(t *testing.T, path string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "kit")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	return link
}

// A terminal that fails to open fails the hand-off, naming it.
func TestHandOverFails(t *testing.T) {
	run := runnertest.New(t)
	homebrewAt(t, run)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	failed := errors.New("no such app")
	t2, _ := b.terminal(b.config())
	t2.Driver = &opener{fails: failed}
	if err := b.handOverStep(t2).Apply(context.Background(), check.Result{}); !errors.Is(err, failed) || !strings.HasPrefix(err.Error(), "open Ghostty: ") {
		t.Errorf("apply = %v", err)
	}
}
