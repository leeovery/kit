package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tmuxWorld is laptopWorld whose tmux config declares TPM, with TPM's
// folder holding it and tmux-yank, declared nowhere.
func tmuxWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.write(t, filepath.Join(".config", "tmux", "tmux.conf"), "set -g @plugin 'tmux-plugins/tpm'\n")
	for _, name := range []string{"tpm", "tmux-yank"} {
		w.write(t, filepath.Join(".config", "tmux", "plugins", name, ".git", "config"), "[remote \"origin\"]\n\turl = https://git::@github.com/tmux-plugins/"+name+"\n")
	}
	return w
}

// tmux's plugins are declared in tmux's config, which kit reads and never
// writes: there's no adopting or undeclaring them, and kit says how.
func TestTmuxPluginsAreDeclaredInTmuxsConfig(t *testing.T) {
	w := tmuxWorld(t)
	out, _, _ := w.run(t, "reconcile", "--json")
	var doc struct {
		Items []struct {
			ID      string   `json:"id"`
			Choices []string `json:"choices"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(doc.Items, func(it struct {
		ID      string   `json:"id"`
		Choices []string `json:"choices"`
	}) bool {
		return it.ID == "tmux:tmux-plugins/tmux-yank"
	})
	if i < 0 || !slices.Equal(doc.Items[i].Choices, []string{"adopt", "remove", "snooze"}) {
		t.Errorf("kit reconcile --json printed %s\nwant tmux-yank to adopt, remove or snooze", out)
	}

	out, _, code := w.run(t, "reconcile", "tmux:tmux-plugins/tmux-yank", "--adopt")
	if code != 0 || !strings.Contains(out, "already installed; declared in ~/.config/tmux/tmux.conf") {
		t.Errorf("kit reconcile --adopt printed\n%s exit %d", out, code)
	}
	if got := w.read(t, ".config/tmux/tmux.conf"); got != "set -g @plugin 'tmux-plugins/tpm'\nset -g @plugin 'tmux-plugins/tmux-yank'\n" {
		t.Errorf("tmux's config = %q", got)
	}

	out, _, code = w.run(t, "remove", "tmux", "tmux-plugins/tpm")
	if code != 0 || !strings.Contains(out, "uninstalled; out of ~/.config/tmux/tmux.conf") {
		t.Errorf("kit remove tmux printed\n%s exit %d", out, code)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "tmux", "plugins", "tpm")); !os.IsNotExist(err) {
		t.Errorf("tpm's folder: %v, want it gone", err)
	}
}

func TestTmuxsConfigInKitConfigIsCommitted(t *testing.T) {
	w := tmuxWorld(t)
	repo := filepath.Join(w.home, ".config", "kit", "shared", "home", ".config", "tmux", "tmux.conf")
	w.write(t, ".config/kit/shared/home/.config/tmux/tmux.conf", "set -g @plugin 'tmux-plugins/tpm'\n")
	if err := os.Remove(filepath.Join(w.home, ".config", "tmux", "tmux.conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, filepath.Join(w.home, ".config", "tmux", "tmux.conf")); err != nil {
		t.Fatal(err)
	}
	w.keeps("shared/home/.config/tmux/tmux.conf")
	w.expectSync([]string{"shared/home/.config/tmux/tmux.conf"}, "kit reconcile (laptop): adopt tmux:tmux-plugins/tmux-yank")
	out, _, code := w.run(t, "reconcile", "tmux:tmux-plugins/tmux-yank", "--adopt")
	if code != 0 || !strings.Contains(out, "declared in ~/.config/kit/shared/home/.config/tmux/tmux.conf") || !strings.Contains(out, "committed and pushed shared/home/.config/tmux/tmux.conf") {
		t.Errorf("kit reconcile --adopt printed\n%s exit %d", out, code)
	}
}

func TestWhyOfATmuxPlugin(t *testing.T) {
	out, _, _ := tmuxWorld(t).run(t, "why", "tmux-plugins/tpm")
	want := "tmux-plugins/tpm (tmux)\n  declared in ~/.config/tmux/tmux.conf, line 1\n  installed here, by kit apply's tmux plugins step\n"
	if out != want {
		t.Errorf("kit why printed\n%s\nwant\n%s", out, want)
	}
}
