package tmux_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/tmux"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// write writes files, by path from the home, making their folders.
func write(t *testing.T, home string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		full := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// clone is a plugin's folder's git config, cloned from url.
func clone(url string) string {
	return "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = " + url + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
}

// newTmux is tmux's plugins in a home of the test's own, with no system
// config.
func newTmux(t *testing.T, fake *runnertest.Fake, home string) *tmux.Tmux {
	t.Helper()
	tm := tmux.New(fake, home, "")
	tm.System = filepath.Join(home, "no-system-config")
	return tm
}

// home is a home whose tmux config lives in XDG's folder, declaring
// plugins there and in a file it sources, with TPM's folder holding some.
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	write(t, h, map[string]string{
		".config/tmux/tmux.conf": `set -g mouse on
set -g @plugin 'tmux-plugins/tpm'
set -g @plugin "tmux-plugins/tmux-sensible"
  set-option -g @plugin 'jaclu/tmux-menus#main'   # a branch
source-file -q ~/.config/tmux/more.conf
run '~/.config/tmux/plugins/tpm/tpm'
`,
		".config/tmux/more.conf":                         "set -g @plugin 'joshmedeski/tmux-nerd-font-window-name'\n",
		".config/tmux/plugins/tpm/.git/config":           clone("https://git::@github.com/tmux-plugins/tpm"),
		".config/tmux/plugins/tmux-sensible/.git/config": clone("git@github.com:tmux-plugins/tmux-sensible.git"),
		".config/tmux/plugins/tmux-menus/.git/config":    clone("https://github.com/jaclu/tmux-menus"),
		".config/tmux/plugins/tmux-yank/.git/config":     clone("https://git::@github.com/tmux-plugins/tmux-yank"),
		".config/tmux/plugins/handmade/README":           "no clone",
		".tmux/plugins/tmux-resurrect/.git/config":       clone("https://github.com/tmux-plugins/tmux-resurrect"),
	})
	return h
}

func TestDeclared(t *testing.T) {
	h := home(t)
	got, err := newTmux(t, runnertest.New(t), h).Declared()
	want := []config.Entry{
		{Name: "tmux-plugins/tpm", File: "~/.config/tmux/tmux.conf", Line: 2},
		{Name: "tmux-plugins/tmux-sensible", File: "~/.config/tmux/tmux.conf", Line: 3},
		{Name: "jaclu/tmux-menus#main", File: "~/.config/tmux/tmux.conf", Line: 4},
		{Name: "joshmedeski/tmux-nerd-font-window-name", File: "~/.config/tmux/more.conf", Line: 1},
	}
	if err != nil || got.Kind != "tmux" || !slices.Equal(got.Entries, want) {
		t.Errorf("Declared() = %+v, %v\nwant %+v", got.Entries, err, want)
	}
}

// Plugins are named by where they were cloned from, and found in TPM's
// folder: XDG's, as tmux's config is there.
func TestCompare(t *testing.T) {
	h := home(t)
	tm := newTmux(t, runnertest.New(t), h)
	list, err := tm.Declared()
	if err != nil {
		t.Fatal(err)
	}
	got := kind.Compare(t.Context(), tm, list)
	want := []check.Item{
		{ID: "tmux:joshmedeski/tmux-nerd-font-window-name", Name: "joshmedeski/tmux-nerd-font-window-name", State: kind.Missing, Action: kind.Install},
		{ID: "tmux:handmade", Name: "handmade", State: kind.Extra},
		{ID: "tmux:tmux-plugins/tmux-yank", Name: "tmux-plugins/tmux-yank", State: kind.Extra},
	}
	if got.Summary != "4 declared, 3 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

// Without a config in XDG's folder, TPM's folder is ~/.tmux/plugins; the
// config can name its own.
func TestTPMsFolder(t *testing.T) {
	h := t.TempDir()
	write(t, h, map[string]string{
		".tmux.conf": "set -g @plugin 'tmux-plugins/tmux-resurrect'\n",
		".tmux/plugins/tmux-resurrect/.git/config": clone("https://github.com/tmux-plugins/tmux-resurrect"),
	})
	tm := newTmux(t, runnertest.New(t), h)
	if got, err := tm.Installed(t.Context()); err != nil || len(got) != 1 || got[0].Name != "tmux-plugins/tmux-resurrect" {
		t.Errorf("Installed() = %+v, %v; want resurrect, from ~/.tmux/plugins", got, err)
	}
	write(t, h, map[string]string{".tmux.conf": "set-environment -g TMUX_PLUGIN_MANAGER_PATH '~/elsewhere/'\n"})
	if got, err := tm.Installed(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("Installed() = %+v, %v; want nothing, from the config's own folder", got, err)
	}
}

func TestInstallClonesAsTPMDoes(t *testing.T) {
	h := home(t)
	plugins := filepath.Join(h, ".config", "tmux", "plugins")
	fake := runnertest.New(t)
	fake.On("git", "clone", "--single-branch", "--recursive", "https://github.com/joshmedeski/tmux-nerd-font-window-name", filepath.Join(plugins, "tmux-nerd-font-window-name"))
	fake.On("git", "clone", "--single-branch", "--recursive", "--branch", "main", "https://github.com/jaclu/tmux-menus", filepath.Join(plugins, "tmux-menus"))
	fake.On("git", "clone", "--single-branch", "--recursive", "git@example.com:someone/plugin.git", filepath.Join(plugins, "plugin"))
	if err := newTmux(t, fake, h).Install(t.Context(), []string{"joshmedeski/tmux-nerd-font-window-name", "jaclu/tmux-menus#main", "git@example.com:someone/plugin.git"}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveDeletesThePluginsFolder(t *testing.T) {
	h := home(t)
	tm := newTmux(t, runnertest.New(t), h)
	if err := tm.Remove(t.Context(), []string{"tmux-plugins/tmux-yank"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h, ".config", "tmux", "plugins", "tmux-yank")); !os.IsNotExist(err) {
		t.Errorf("tmux-yank's folder: %v, want it gone", err)
	}
	if err := tm.Remove(t.Context(), []string{"someone/nosuch"}); err == nil || !strings.Contains(err.Error(), "no plugin nosuch in ~/.config/tmux/plugins") {
		t.Errorf("Remove(nosuch) = %v", err)
	}
}

func TestDeclareAddsALineInItsPlace(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo", "tmux.conf")
	write(t, home, map[string]string{
		"repo/tmux.conf": "set -g mouse on\nset -g @plugin 'tmux-plugins/tpm'\nset -g @plugin 'tmux-plugins/tmux-yank'\n\nrun '~/.config/tmux/plugins/tpm/tpm'\n",
	})
	if err := os.MkdirAll(filepath.Join(home, ".config", "tmux"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, filepath.Join(home, ".config", "tmux", "tmux.conf")); err != nil {
		t.Fatal(err)
	}
	tm := newTmux(t, runnertest.New(t), home)

	file, err := tm.Declare("owner/plugin")
	if err != nil || file != repo {
		t.Fatalf("Declare() = %q, %v; want the file the link leads to", file, err)
	}
	data, _ := os.ReadFile(repo)
	if want := "set -g mouse on\nset -g @plugin 'tmux-plugins/tpm'\nset -g @plugin 'tmux-plugins/tmux-yank'\nset -g @plugin 'owner/plugin'\n\nrun '~/.config/tmux/plugins/tpm/tpm'\n"; string(data) != want {
		t.Errorf("tmux's config =\n%s\nwant\n%s", data, want)
	}
	if target, _ := os.Readlink(filepath.Join(home, ".config", "tmux", "tmux.conf")); target != repo {
		t.Errorf("the link leads to %q, want it kept", target)
	}
	if _, err := tm.Declare("someone/plugin"); err == nil || !strings.Contains(err.Error(), "declared already") {
		t.Errorf("Declare() of a plugin by the same folder = %v", err)
	}

	file, err = tm.Undeclare("tmux-plugins/tmux-yank")
	data, _ = os.ReadFile(repo)
	if err != nil || file != repo || strings.Contains(string(data), "tmux-yank") {
		t.Errorf("Undeclare() = %q, %v; tmux's config =\n%s", file, err, data)
	}
	if _, err := tm.Undeclare("nobody/nothing"); err == nil {
		t.Error("Undeclare() of a plugin not declared: no error")
	}
}

func TestDeclareTheFirstPluginGoesBeforeTPMStarts(t *testing.T) {
	home := t.TempDir()
	write(t, home, map[string]string{".config/tmux/tmux.conf": "set -g mouse on\nrun '~/.tmux/plugins/tpm/tpm'\n"})
	if _, err := newTmux(t, runnertest.New(t), home).Declare("tmux-plugins/tpm"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".config", "tmux", "tmux.conf"))
	if want := "set -g mouse on\nset -g @plugin 'tmux-plugins/tpm'\nrun '~/.tmux/plugins/tpm/tpm'\n"; string(data) != want {
		t.Errorf("tmux's config =\n%s\nwant\n%s", data, want)
	}
}
