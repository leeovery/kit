package boot

import (
	"context"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
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
