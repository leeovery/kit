package brew_test

import (
	"reflect"
	"testing"

	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// What the casks put in place is each app their app artifacts name, and
// each folder of apps their suite artifacts do, by the target renaming it
// when there's one, and, for a cask installing through a package alone, its
// uninstall's receipts, one or a list.
func TestCasked(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--cask", "--installed").Prints(`{"formulae": [], "casks": [
		{"token": "editor", "artifacts": [{"app": ["Editor.app"]}, {"zap": [{"trash": "~/.editor"}]}]},
		{"token": "renamed", "artifacts": [{"app": ["Old Name.app", {"target": "New Name.app"}]}]},
		{"token": "office", "artifacts": [{"suite": ["Example Office"]}]},
		{"token": "driver", "artifacts": [{"uninstall": [{"pkgutil": ["com.example.driver", "com.example.helper.*"], "quit": "com.example.driver"}]}, {"pkg": ["Driver.pkg"]}]},
		{"token": "agent", "artifacts": [{"uninstall": [{"pkgutil": "com.example.agent"}]}, {"pkg": ["Agent.pkg"]}]},
		{"token": "tidy", "artifacts": [{"app": ["Tidy.app"]}, {"uninstall": [{"pkgutil": "com.example.tidy-leftover"}]}]}]}`)
	got, err := brew.New(fake).Casked(t.Context())
	want := brew.Casked{
		Apps:     []string{"Editor.app", "New Name.app", "Tidy.app"},
		Suites:   []string{"Example Office"},
		Receipts: []string{"com.example.driver", "com.example.helper.*", "com.example.agent"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Casked() = %+v, %v\nwant %+v: receipts of the casks installing through a package alone", got, err, want)
	}
}
