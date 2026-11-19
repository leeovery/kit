package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/boot/boottest"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// Without a terminal, the boot's questions fail, naming the flag that
// answers each: which repository is kit-config, with the question, and
// that a new one is made at a terminal or by kit setup; this Mac's name.
func TestBootWithoutATerminal(t *testing.T) {
	ask := &plainAsker{}
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"which repository", func() error {
			_, err := ask.Choose(context.Background(), boot.GitHubStep, "someone has no repository named kit-config", nil)
			return err
		}(), "no terminal to ask at: someone has no repository named kit-config: name your config repository with --config owner/name; a new one is made at a terminal, or with kit setup (the github step)"},
		{"its name, typed", func() error {
			_, err := ask.Text(context.Background(), boot.GitHubStep, "its name, as owner/name", "")
			return err
		}(), "no terminal to ask at: its name, as owner/name: name your config repository with --config owner/name; a new one is made at a terminal, or with kit setup (the github step)"},
		{"this Mac", func() error {
			_, err := ask.Choose(context.Background(), boot.MacStep, "which of your Macs is this?", nil)
			return err
		}(), "no terminal to ask at: give this Mac's name with --mac (the mac step)"},
	} {
		if !errors.Is(tt.err, errNoTerminal) || tt.err.Error() != tt.want {
			t.Errorf("%s: %v; want %q", tt.name, tt.err, tt.want)
		}
	}
}

// Without a terminal, kit-config starting on this Mac, in the empty
// repository --config names, asks nothing of its terminal, as no flag
// answers it: there's none, so the boot hands over to none.
func TestBootWithoutATerminalStartsWithNone(t *testing.T) {
	run := runnertest.New(t)
	run.On("security", "find-generic-password", "-s", "kit-github", "-w").Prints(boottest.Token)
	dir := t.TempDir()
	b := &boot.Boot{
		Run: run, Sink: event.NewFanout(), Ask: &plainAsker{}, GitHub: boottest.Serve(t, map[string]bool{boottest.Login + "/empty": true}), Now: time.Now,
		State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data"), Config: filepath.Join(dir, "config"),
		Repo: boottest.Login + "/empty", Getenv: func(string) string { return "" },
	}
	step := b.SignIn()[0]
	_ = step.Check(context.Background())
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("signing in = %v; want nothing asked", err)
	}
	if res := step.Check(context.Background()); res.State != check.OK {
		t.Errorf("signed in = %+v", res)
	}
	for _, s := range b.Order() {
		if s.Name == boot.TerminalStep || s.Name == boot.AccessStep {
			t.Errorf("planned %s; want no terminal to hand over to", s.Name)
		}
	}
}
