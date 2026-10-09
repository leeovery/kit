package apps_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/apps"
)

// A terminal is opened running a command as a new instance, by its path:
// Ghostty's way, proven in a virtual machine.
func TestLaunch(t *testing.T) {
	got := apps.Terminals["ghostty"].Launch("/Applications", "/Users/someone/.local/bin/kit", "bootstrap")
	want := []string{"-na", "/Applications/Ghostty.app", "--args", "-e", "/Users/someone/.local/bin/kit", "bootstrap"}
	if !slices.Equal(got, want) {
		t.Errorf("Launch() = %q, want %q", got, want)
	}
}

// Every app kit knows is known by the name it's under, with its cask and
// bundle, a terminal with how to tell kit's running in it.
func TestKnown(t *testing.T) {
	for name, term := range apps.Terminals {
		if term.Name != name || term.Title == "" || term.Cask == "" || term.Bundle == "" || term.Program == "" {
			t.Errorf("terminal %s: %+v", name, term)
		}
	}
	if got := apps.Names(apps.Terminals); got != "ghostty" {
		t.Errorf("Names() = %q", got)
	}
}
