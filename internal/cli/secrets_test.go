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

func TestAddASecretKeepsItInOnePassword(t *testing.T) {
	w := secretsWorld(t)
	w.stdin = "new value\n"
	f := w.fake
	f.On("op", "whoami")
	f.On("op", "item", "get", "Item", "--vault", "vault", "--format", "json").Prints(`{"id":"abc","title":"Item","sections":[{"id":"s1","label":"Section"}],"fields":[{"id":"f1","label":"token","type":"CONCEALED","value":"the value","section":{"id":"s1"}}]}`)
	f.On("op", "item", "edit", "Item", "--vault", "vault")
	f.On("op", "read", "op://vault/Item/Section/new").Prints("new value\n")
	f.On("op", "read", "op://vault/Item/Section/token").Prints("the value\n")
	w.expectSync([]string{"laptop/declarations"}, "kit add secret NEW_ONE (laptop): a new one")
	out, _, code := w.run(t, "add", "secret", "NEW_ONE", "--field", "Section/new", "--stdin", "--note", "a new one")
	if code != 0 || !strings.Contains(out, "kept in 1Password (Section/new) and read back; declared in laptop, synced") {
		t.Errorf("kit add secret printed\n%s exit %d", out, code)
	}
	for _, c := range f.Commands() {
		if strings.Contains(strings.Join(c.Args, " "), "new value") {
			t.Errorf("%s: the value is on a command line", c)
		}
		if c.Name == "op" && len(c.Args) > 1 && c.Args[1] == "edit" && (!strings.Contains(c.Input, `"value":"new value"`) || !strings.Contains(c.Input, `"label":"new"`) || !c.Secret) {
			t.Errorf("op item edit got %q (secret: %v)", c.Input, c.Secret)
		}
	}
	if got := w.readSection(t, "laptop", "secrets"); !strings.Contains(got, "NEW_ONE Section/new   # a new one\n") {
		t.Errorf("[secrets] = %q", got)
	}
	if got := w.read(t, ".secrets.zsh"); !strings.Contains(got, "export NEW_ONE='new value'   # a new one\n") {
		t.Errorf("~/.secrets.zsh = %q", got)
	}
}

func TestAddASecretSaysWhereItsValueComesFrom(t *testing.T) {
	w := secretsWorld(t)
	_, errOut, code := w.run(t, "add", "secret", "NEW_ONE", "--field", "Section/new")
	if code != 2 || !strings.Contains(errOut, "say where its value comes from: --stdin") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	w.stdin = "x"
	if _, errOut, _ := w.run(t, "add", "secret", "NEW_ONE", "--stdin"); !strings.Contains(errOut, "--field <section>/<field>") {
		t.Errorf("printed %q", errOut)
	}
}

func TestRemoveASecretAndItsValue(t *testing.T) {
	w := secretsWorld(t)
	w.write(t, ".secrets.zsh", "export TOKEN='the value'\n")
	_, errOut, code := w.run(t, "remove", "secret", "TOKEN")
	if code != 2 || !strings.Contains(errOut, "--keep-value or --delete-value") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	w.fake.On("op", "item", "edit", "Item", "--vault", "vault", "Section.token[delete]")
	w.expectSync([]string{"laptop/declarations"}, "kit remove secret TOKEN (laptop)")
	out, _, code := w.run(t, "remove", "secret", "TOKEN", "--delete-value")
	if code != 0 || !strings.Contains(out, "its value deleted from 1Password") {
		t.Errorf("kit remove secret printed\n%s exit %d", out, code)
	}
	if got := w.read(t, ".secrets.zsh"); strings.Contains(got, "TOKEN") {
		t.Errorf("~/.secrets.zsh = %q", got)
	}
}
