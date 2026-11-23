package brew_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// caskroom has brew --caskroom name a Caskroom holding a folder for each
// of casks, by token, with its install receipt, the artifacts it installed
// as Homebrew writes them: none for "", as a receipt Homebrew didn't write.
func caskroom(t *testing.T, fake *runnertest.Fake, casks map[string]string) {
	t.Helper()
	room := t.TempDir()
	for token, artifacts := range casks {
		receipt := "{}"
		if artifacts != "" {
			receipt = `{"installed_on_request": true, "uninstall_artifacts": ` + artifacts + `}`
		}
		writeFile(t, filepath.Join(room, token, ".metadata", "INSTALL_RECEIPT.json"), receipt)
	}
	fake.On("brew", "--caskroom").Prints(room + "\n")
}

// What the casks put in place is each app their app artifacts name, and
// each folder of apps their suite artifacts do, by the target renaming it
// when there's one, and their uninstall's receipts, one or a list: each as
// its install receipt says, without asking brew, or as brew info does for
// those whose receipt doesn't say.
func TestCasked(t *testing.T) {
	fake := runnertest.New(t)
	caskroom(t, fake, map[string]string{
		"editor":  `[{"app": ["Editor.app"]}, {"zap": [{"trash": "~/.editor"}]}]`,
		"renamed": `[{"app": ["Old Name.app", {"target": "New Name.app"}]}]`,
		"office":  `[{"suite": ["Example Office"]}]`,
		"driver":  `[{"uninstall": [{"pkgutil": ["com.example.driver", "com.example.helper.*"], "quit": "com.example.driver"}]}]`,
		"old":     "",
		"tool":    "",
	})
	fake.On("brew", "info", "--json=v2", "--cask", "old", "tool").Prints(`{"formulae": [], "casks": [
		{"token": "old", "artifacts": [{"app": ["Old.app"]}, {"uninstall": [{"pkgutil": "com.example.old"}]}, {"pkg": ["Old.pkg"]}]},
		{"token": "tool", "artifacts": [{"binary": ["tool"]}]}]}`)
	got, err := brew.New(fake).Casked(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range [][]string{got.Apps, got.Suites, got.Receipts} {
		slices.Sort(list)
	}
	want := brew.Casked{
		Apps:     []string{"Editor.app", "New Name.app", "Old.app"},
		Suites:   []string{"Example Office"},
		Receipts: []string{"com.example.driver", "com.example.helper.*", "com.example.old"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Casked() = %+v\nwant %+v", got, want)
	}
}

// With every receipt saying, brew is asked only where the Caskroom is; with
// no Caskroom, no cask's installed.
func TestCaskedAsksBrewNothingMore(t *testing.T) {
	fake := runnertest.New(t)
	caskroom(t, fake, map[string]string{"editor": `[{"app": ["Editor.app"]}]`})
	if got, err := brew.New(fake).Casked(t.Context()); err != nil || !slices.Equal(got.Apps, []string{"Editor.app"}) || !slices.Equal(fake.Calls(), []string{"brew --caskroom"}) {
		t.Errorf("Casked() = %+v, %v, running %q; want the editor's app, brew asked only where the Caskroom is", got, err, fake.Calls())
	}
	fake = runnertest.New(t)
	fake.On("brew", "--caskroom").Prints(filepath.Join(t.TempDir(), "Caskroom") + "\n")
	if got, err := brew.New(fake).Casked(t.Context()); err != nil || !reflect.DeepEqual(got, brew.Casked{}) {
		t.Errorf("Casked() with no Caskroom = %+v, %v; want nothing", got, err)
	}
}
