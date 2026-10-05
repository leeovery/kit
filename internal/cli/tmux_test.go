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
	if i < 0 || !slices.Equal(doc.Items[i].Choices, []string{"remove", "snooze"}) {
		t.Errorf("kit reconcile --json printed %s\nwant tmux-yank to remove or snooze", out)
	}

	_, errOut, code := w.run(t, "add", "tmux", "owner/plugin")
	if !strings.Contains(errOut, "tmux plugins are declared in tmux's config (~/.config/tmux/tmux.conf): add or remove the line set -g @plugin 'owner/plugin' there") || code != 2 {
		t.Errorf("kit add tmux printed %q, exit %d; want how to declare it", errOut, code)
	}

	out, _, code = w.run(t, "remove", "tmux", "tmux-plugins/tpm")
	if !strings.Contains(out, "declared in ~/.config/tmux/tmux.conf:1") || code != 1 {
		t.Errorf("kit remove tmux tpm printed\n%s exit %d; want it refused, as declared", out, code)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "tmux", "plugins", "tpm")); err != nil {
		t.Errorf("tpm's folder: %v, want it kept", err)
	}

	out, errOut, code = w.run(t, "reconcile", "tmux:tmux-plugins/tmux-yank", "--remove")
	if code != 0 || !strings.Contains(out, "uninstalled; it wasn't declared") {
		t.Errorf("kit reconcile --remove printed\n%s%s exit %d", out, errOut, code)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "tmux", "plugins", "tmux-yank")); !os.IsNotExist(err) {
		t.Errorf("tmux-yank's folder: %v, want it gone", err)
	}
}

func TestWhyOfATmuxPlugin(t *testing.T) {
	out, _, _ := tmuxWorld(t).run(t, "why", "tmux-plugins/tpm")
	want := "tmux-plugins/tpm (tmux)\n  declared in ~/.config/tmux/tmux.conf, line 1\n  installed here, by kit apply's tmux plugins step\n"
	if out != want {
		t.Errorf("kit why printed\n%s\nwant\n%s", out, want)
	}
}
