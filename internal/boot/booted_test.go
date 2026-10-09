package boot

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// In the terminal handed over to, the boot's steps are a light of their
// own: every one of them, checked again but never applied, named apart
// from kit apply's, each needing the one before.
func TestBooted(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	booted := b.Booted()
	var names []string
	for i, s := range booted {
		names = append(names, s.Name)
		if s.Area != AreaBooted || s.Apply != nil || s.Check == nil {
			t.Errorf("%s: area %q, applied %v", s.Name, s.Area, s.Apply != nil)
		}
		if i > 0 && !slices.Equal(s.Needs, []string{booted[i-1].Name}) {
			t.Errorf("%s needs %q, want the step before", s.Name, s.Needs)
		}
	}
	want := []string{"boot-github", "boot-mac", "boot-password", "boot-homebrew", "boot-apps", "boot-kit-config", "boot-full-disk-access", "boot-terminal"}
	if !slices.Equal(names, want) {
		t.Errorf("Booted() = %q, want %q", names, want)
	}
}

// Handed over to, Touch ID was tried before the hand-off: the password
// asks for nothing more, Touch ID off left to kit apply.
func TestPasswordHandedOver(t *testing.T) {
	run := runnertest.New(t)
	run.On("brew", "--version")
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	b.HandedOver = true
	read(t, b, map[string]string{config.File: appsToml, "laptop/declarations": "[features]\ntouch-id-sudo\n"}, "laptop")
	if res := b.passwordStep().Check(context.Background()); res.State != check.OK {
		t.Errorf("check = %+v, want ok: the boot before the hand-off tried Touch ID", res)
	}
}

// 1Password signed in on the new Mac: the app opened, what to do said, till
// its CLI lists an account; then the app asks whether the terminal may use
// it, and once it's allowed, it's signed in.
func TestPasswordManagerSignsIn(t *testing.T) {
	defer func(poll time.Duration) { signInPoll = poll }(signInPoll)
	signInPoll = time.Millisecond
	run := runnertest.New(t)
	b, s := newBoot(t, run, GitHub{}, &asker{})
	b.Getenv = func(name string) string { return map[string]string{"TERM_PROGRAM": "ghostty"}[name] }
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	run.On("op", "whoami").Exits(1)
	run.On("op", "account", "list", "--format", "json").Prints("[]").Then().Prints(`[{"url":"my.1password.com"}]`)
	run.On("open", filepath.Join(b.Applications, "1Password.app"))
	run.On("op", "vault", "list", "--format", "json").Does(func() { run.On("op", "whoami") })
	step, ok := b.PasswordManagerStep()
	if !ok || step.Name != "1password" || step.Title != "1Password" {
		t.Fatalf("PasswordManagerStep() = %+v, %v", step, ok)
	}
	if res := step.Check(context.Background()); res.State != check.Attention || res.Summary != "not signed in" {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "signed in" {
		t.Errorf("check after = %+v", res)
	}
	var said []string
	for _, e := range s.events {
		if d, ok := e.(event.Doing); ok && d.Step == "1password" {
			said = append(said, d.Says+": "+d.Todo)
		}
	}
	want := []string{
		"not signed in: sign in with your phone's 1Password, then turn on\nSettings › Developer › Integrate with 1Password CLI",
		"asking you: 1Password asks whether Ghostty may use it: allow it",
	}
	if !slices.Equal(said, want) {
		t.Errorf("it said %q, want %q", said, want)
	}
}

// Signed in already, with its CLI on, 1Password only asks to let the
// terminal in: the app isn't opened, and there's no signing in to do.
func TestPasswordManagerSignedInAlready(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	run.On("op", "account", "list", "--format", "json").Prints(`[{"url":"my.1password.com"}]`)
	run.On("op", "vault", "list", "--format", "json")
	step, _ := b.PasswordManagerStep()
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	for _, c := range run.Commands() {
		if c.Name == "open" {
			t.Errorf("opened %q: want nothing opened", c.Args)
		}
	}
}

// Without its CLI, 1Password can't be signed in to for kit: the step says
// so, and applying names the cask to declare. Stopped, it stops waiting.
func TestPasswordManagerWithoutItsCLIOrStopped(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: appsToml}, "laptop")
	step, _ := b.PasswordManagerStep()
	if res := step.Check(context.Background()); res.State != check.Attention || res.Summary != "its CLI isn't installed" {
		t.Errorf("check = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err == nil || err.Error() != "op isn't installed: declare its cask, 1password-cli, then run kit apply" {
		t.Errorf("apply = %v", err)
	}
	run.On("op", "account", "list", "--format", "json").Prints("[]")
	run.On("open", filepath.Join(b.Applications, "1Password.app"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := step.Apply(ctx, check.Result{}); err == nil {
		t.Error("stopped, it went on waiting, or said it was signed in")
	}
}

// A kit.toml naming no password manager has no step for one.
func TestNoPasswordManager(t *testing.T) {
	run := runnertest.New(t)
	b, _ := newBoot(t, run, GitHub{}, &asker{})
	read(t, b, map[string]string{config.File: "format = 1\nprimary = \"laptop\"\n[macs.laptop]\n"}, "laptop")
	if _, ok := b.PasswordManagerStep(); ok {
		t.Error("a step for a password manager kit.toml doesn't name")
	}
}
