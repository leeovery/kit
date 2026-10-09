package boot

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	kitstate "github.com/leeovery/kit/internal/state"
)

const appsToml = "format = 1\nprimary = \"laptop\"\nterminal = \"ghostty\"\npassword_manager = \"1password\"\n[macs.laptop]\n[macs.studio]\n"

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

// The boot order is planned from kit-config: the apps kit.toml names, and,
// with a terminal, Full Disk Access for it and the hand-off to it, each
// step needing the one before.
func TestOrder(t *testing.T) {
	for _, tt := range []struct {
		name, toml string
		want       []string
	}{
		{"a terminal and a password manager", appsToml, []string{"mac", "password", "homebrew", "apps", "kit-config", "full-disk-access", "terminal"}},
		{"a password manager", "format = 1\nprimary = \"laptop\"\npassword_manager = \"1password\"\n[macs.laptop]\n", []string{"mac", "password", "homebrew", "apps", "kit-config"}},
		{"neither", kitToml, []string{"mac", "password", "homebrew", "kit-config"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := newBoot(t, runnertest.New(t), GitHub{}, &asker{})
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
	if access, handOver := order[5], order[6]; access.Waiting != "for Ghostty" || handOver.Title != "Ghostty" {
		t.Errorf("Full Disk Access waits %q, the hand-off is %q; want them the terminal's", access.Waiting, handOver.Title)
	}
}

// The password, once, in its row, asked again when it's wrong, given to
// sudo on its input and kept for the run; then Touch ID turned on for sudo,
// as the Mac declares it.
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
	if err := b.passwordStep().Apply(context.Background(), check.Result{}); err == nil || err.Error() != "sudo refused the password: run kit bootstrap again" || b.Held() {
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

// The apps kit.toml names, installed as casks where they're missing; then
// each with its version.
func TestApps(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	bundle(t, b, "1Password.app", "8.12.40")
	run.On("brew", "install", "--cask", "ghostty").Does(func() { bundle(t, b, "Ghostty.app", "1.3.1") })
	step := b.appsStep(b.apps(b.config()))
	found := step.Check(context.Background())
	if found.State != check.Attention || found.Summary != "to install: Ghostty" {
		t.Fatalf("check before = %+v", found)
	}
	if err := step.Apply(context.Background(), found); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "Ghostty 1.3.1 · 1Password 8.12.40" {
		t.Errorf("check after = %+v", res)
	}
}

// kit-config cloned through GitHub's CLI, installed and handed the
// sign-in, which kit's keychain item gives up; git signing in through it
// for that repository alone; then its files linked.
func TestKitConfig(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: kitToml}, "laptop")
	b.hold("gho_secret", "someone")
	run.On("brew", "install", "--formula", "gh").Does(func() {
		run.On("gh", "auth", "status", "--hostname", "github.com").Exits(1)
		run.On("gh", "auth", "login", "--hostname", "github.com", "--with-token")
		run.On("gh", "repo", "clone", "someone/kit-config", b.Config, "--", "--quiet").Does(func() {
			writeFiles(t, b.Config, map[string]string{".git/HEAD": "ref: refs/heads/main\n", config.File: kitToml, "shared/home/.zshrc": "# zsh\n"})
		})
	})
	run.On("security", "delete-generic-password", "-s", keychainItem)
	run.On("git", "-C", b.Config, "config", "credential.https://github.com.helper", "!gh auth git-credential")
	run.On("git", "-C", b.Config, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "shared/home").Prints("shared/home/.zshrc\x00")
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

// A new Mac is added to the clone's kit.toml, uncommitted.
func TestKitConfigAddsANewMac(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: kitToml}, "mini")
	if err := kitstate.Update(b.State, stateName, func(st *state) { st.NewMac = "mini" }); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, b.Config, map[string]string{".git/HEAD": "ref: refs/heads/main\n", config.File: kitToml})
	if err := b.configStep().Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	cfg, err := config.Load(b.Config)
	if err != nil || !cfg.Knows("mini") {
		t.Fatalf("kit.toml after: %v", err)
	}
	if res := b.configStep().Check(context.Background()); res.State != check.OK || !strings.HasSuffix(res.Summary, "mini added to kit.toml") {
		t.Errorf("check = %+v", res)
	}
}

// Full Disk Access, from Terminal: System Settings opened at the list and
// Finder at the app, then done when the person says so. In the terminal,
// it's the grant itself that's checked.
func TestFullDiskAccess(t *testing.T) {
	run := runnertest.New(t)
	run.On("open", accessSettings)
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

// The hand-off: the terminal opened running kit bootstrap, handed over;
// done once that kit arrives, saying what macOS asks meanwhile.
func TestHandOver(t *testing.T) {
	run := runnertest.New(t)
	b, s := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	launch := []string{"-na", filepath.Join(b.Applications, "Ghostty.app"), "--args", "-e", b.Self, "bootstrap", "--handed-over"}
	run.On("open", launch...).Does(func() {
		go func() {
			time.Sleep(50 * time.Millisecond)
			arrived := &Boot{State: b.State, Now: time.Now, HandedOver: true}
			if err := arrived.Arrive(); err != nil {
				t.Error(err)
			}
		}()
	})
	t2, _ := b.terminal(b.config())
	step := b.handOverStep(t2)
	if res := step.Check(context.Background()); res.State != check.Attention {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	for _, c := range run.Commands() {
		if c.Name == "open" && !slices.Contains(c.Env, "SUDO_ASKPASS=") {
			t.Errorf("opened Ghostty with %q; want this run's askpass kept from it", c.Env)
		}
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
