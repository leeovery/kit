package apps_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// Ghostty opens a window as a new instance, by its path, running the
// command with -e; maximized, with the option on its command line, before
// -e, for that instance alone, written twice to stay a pair; given the
// window's environment.
func TestGhosttyOpens(t *testing.T) {
	for _, tt := range []struct {
		name      string
		maximized bool
		want      []string
	}{
		{"its own size", false, []string{"-na", "/Applications/Ghostty.app", "--args", "-e", "/Users/someone/.local/bin/kit", "bootstrap"}},
		{"maximized", true, []string{"-na", "/Applications/Ghostty.app", "--args", "--maximize=true", "--maximize=true", "-e", "/Users/someone/.local/bin/kit", "bootstrap"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			run := runnertest.New(t)
			run.On("open", tt.want...)
			w := apps.Window{Command: []string{"/Users/someone/.local/bin/kit", "bootstrap"}, Env: []string{"SUDO_ASKPASS="}, Maximized: tt.maximized}
			if err := apps.Terminals["ghostty"].Open(context.Background(), run, "/Applications", w); err != nil {
				t.Fatalf("Open() = %v", err)
			}
			cmds := run.Commands()
			if len(cmds) != 1 || !slices.Equal(cmds[0].Env, w.Env) {
				t.Errorf("ran %+v; want one open, given %q", cmds, w.Env)
			}
		})
	}
}

// A window that fails to open says so.
func TestGhosttyFailsToOpen(t *testing.T) {
	run := runnertest.New(t)
	failed := errors.New("no such app")
	run.On("open", "-na", "/Applications/Ghostty.app", "--args", "-e", "kit").Fails(failed)
	if err := apps.Terminals["ghostty"].Open(context.Background(), run, "/Applications", apps.Window{Command: []string{"kit"}}); !errors.Is(err, failed) {
		t.Errorf("Open() = %v; want %v", err, failed)
	}
}

// Every app kit knows is known by the name it's under, with its cask and
// bundle, a terminal with how to tell kit's running in it and a driver.
func TestKnown(t *testing.T) {
	for name, term := range apps.Terminals {
		if term.Name != name || term.Title == "" || term.Cask == "" || term.Bundle == "" || term.Program == "" || term.Driver == nil {
			t.Errorf("terminal %s: %+v", name, term)
		}
	}
	if got := apps.Names(apps.Terminals); got != "ghostty" {
		t.Errorf("Names() = %q", got)
	}
}
