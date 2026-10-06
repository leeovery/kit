package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// secretsWorld is laptopWorld with a secret declared, short for a field of
// the item kit.toml names.
func secretsWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.write(t, ".config/kit/kit.toml", strings.Replace(twoMacs, "primary = \"laptop\"\n", "primary = \"laptop\"\nsecrets_item = \"op://vault/Item\"\n", 1))
	w.writeSection(t, "laptop", "secrets", "TOKEN   Section/token   # a token\n")
	return w
}

func TestSecretsSync(t *testing.T) {
	w := secretsWorld(t)
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "secret ok 1 declared, 0 synced\nsecret missing:new TOKEN (to install)\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	w.fake.On("op", "whoami")
	w.fake.On("op", "read", "op://vault/Item/Section/token").Prints("the value\n")
	out, _, code := w.run(t, "secrets", "sync")
	if code != 0 || !strings.Contains(out, "secret ok 1 synced, 1 changed\n") || strings.Contains(out, "the value") {
		t.Errorf("kit secrets sync printed\n%s exit %d", out, code)
	}
	if got := w.read(t, ".secrets.zsh"); !strings.Contains(got, "export TOKEN='the value'   # a token\n") {
		t.Errorf("~/.secrets.zsh = %q", got)
	}
	logs, _ := filepath.Glob(filepath.Join(w.home, "Library", "Logs", "kit", "*secrets-sync.jsonl"))
	for _, l := range logs {
		if data, _ := os.ReadFile(l); strings.Contains(string(data), "the value") {
			t.Errorf("%s holds the value", l)
		}
	}
}

func TestAPasswordSignInsSessionReachesOnePassword(t *testing.T) {
	w := laptopWorld(t)
	w.environ = []string{"OP_SESSION_abc=session-token", "UNRELATED=x"}
	w.run(t, "status")
	if !slices.Contains(w.childEnv, "OP_SESSION_abc=session-token") || slices.Contains(w.childEnv, "UNRELATED=x") {
		t.Errorf("programs get %q", w.childEnv)
	}
}
