package brew_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// What the casks put in place is each app their app artifacts name, by the
// target renaming it when there's one, and, for a cask installing through
// a package alone, its uninstall's receipts, one or a list.
func TestCasked(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--cask", "--installed").Prints(`{"formulae": [], "casks": [
		{"token": "editor", "artifacts": [{"app": ["Editor.app"]}, {"zap": [{"trash": "~/.editor"}]}]},
		{"token": "renamed", "artifacts": [{"app": ["Old Name.app", {"target": "New Name.app"}]}]},
		{"token": "driver", "artifacts": [{"uninstall": [{"pkgutil": ["com.example.driver", "com.example.helper.*"], "quit": "com.example.driver"}]}, {"pkg": ["Driver.pkg"]}]},
		{"token": "agent", "artifacts": [{"uninstall": [{"pkgutil": "com.example.agent"}]}, {"pkg": ["Agent.pkg"]}]},
		{"token": "tidy", "artifacts": [{"app": ["Tidy.app"]}, {"uninstall": [{"pkgutil": "com.example.tidy-leftover"}]}]}]}`)
	apps, receipts, err := brew.New(fake).Casked(t.Context())
	if want := []string{"Editor.app", "New Name.app", "Tidy.app"}; err != nil || !slices.Equal(apps, want) {
		t.Errorf("Casked() apps = %q, %v; want %q", apps, err, want)
	}
	if want := []string{"com.example.driver", "com.example.helper.*", "com.example.agent"}; !slices.Equal(receipts, want) {
		t.Errorf("Casked() receipts = %q; want %q, of the casks installing through a package alone", receipts, want)
	}
}
