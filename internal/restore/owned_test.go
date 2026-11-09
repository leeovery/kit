package restore_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/skill"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const kitToml = `format = 1
primary = "laptop"

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"
`

// What's kit's on a Mac is read from what it declares for it, the shared
// file's and its own: the files linked; [prefs files], while prefs is on;
// the folders the kinds installing into the home folder name; [backup
// exclusions]; and kit's own folders. Paths given in full in the home
// folder are from ~; those outside it go.
func TestReadOwned(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, ".config", "kit")
	files := map[string]string{
		config.File:                     kitToml,
		"shared/declarations":           "[backup exclusions]\n/Applications\n~/.cache\n~/Library/Application Support/*/GPUCache\n\n[prefs files]\n~/.claude/settings.json\n",
		"laptop/declarations":           "[claude skills]\nacme/tools\nacme/tools/review\n\n[backup exclusions]\n" + home + "/Library/Caches\n\n[prefs files]\n~/.claude/projects/*/memory\n/Library/Preferences/x.plist\n",
		"shared/home/.zshrc":            "",
		"laptop/home/.claude/CLAUDE.md": "",
	}
	for name, content := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(repo, kinds.ConfigSections())
	if err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	fake.On("git", "-C", repo, "--literal-pathspecs", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "shared/home", "laptop/home").
		Prints(strings.Join([]string{"shared/home/.zshrc", "laptop/home/.claude/CLAUDE.md"}, "\x00") + "\x00")
	dirs, err := config.Locate(home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	mac := restore.Mac{
		Config: cfg, Name: "laptop", Home: home, Dirs: dirs,
		Linked: &linked.Files{Repo: gitrepo.Repo{Dir: repo, Run: fake}, Home: home, StateDir: dirs.State, Scopes: []string{config.Shared, "laptop"}},
		Prefs:  true,
		Kinds:  []kind.Kind{skill.New(fake, home)},
	}
	want := restore.Owned{
		Linked:    []string{"~/.claude/CLAUDE.md", "~/.zshrc"},
		Prefs:     []string{"~/.claude/settings.json", "~/.claude/projects/*/memory"},
		Installed: []string{"~/.claude/skills/review"},
		Excluded:  []string{"~/.cache", "~/Library/Application Support/*/GPUCache", "~/Library/Caches"},
		Own:       []string{"~/.config/kit", "~/.local/state/kit", "~/.local/share/kit", "~/Library/Logs/kit"},
	}
	got, err := restore.ReadOwned(t.Context(), mac)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ReadOwned() = %+v, %v\nwant %+v", got, err, want)
	}

	mac.Prefs = false
	want.Prefs = nil
	if got, err := restore.ReadOwned(t.Context(), mac); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("with prefs off, ReadOwned() = %+v, %v\nwant %+v", got, err, want)
	}
}
