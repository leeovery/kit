package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// itemSection is the header of the secrets section for the item the tests'
// secrets are in.
const itemSection = "secrets op://vault/Item"

// secretsWorld is laptopWorld with a secret declared, short for a field of
// the item its section names.
func secretsWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", itemSection, "TOKEN   Section/token   # a token\n")
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
	out, _, code := w.run(t, "secret", "sync")
	if code != 0 || !strings.Contains(out, "secret ok 1 synced, 1 changed\n") || strings.Contains(out, "the value") {
		t.Errorf("kit secret sync printed\n%s exit %d", out, code)
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
	w.expectSync([]string{"laptop/declarations"}, "kit secret add NEW_ONE (laptop): a new one")
	out, _, code := w.run(t, "secret", "add", "NEW_ONE", "--field", "Section/new", "--stdin", "--note", "a new one")
	if code != 0 || !strings.Contains(out, "kept in 1Password (op://vault/Item/Section/new) and read back; declared in laptop, synced") {
		t.Errorf("kit secret add printed\n%s exit %d", out, code)
	}
	for _, c := range f.Commands() {
		if strings.Contains(strings.Join(c.Args, " "), "new value") {
			t.Errorf("%s: the value is on a command line", c)
		}
		if c.Name == "op" && len(c.Args) > 1 && c.Args[1] == "edit" && (!strings.Contains(c.Input, `"value":"new value"`) || !strings.Contains(c.Input, `"label":"new"`) || !c.Secret) {
			t.Errorf("op item edit got %q (secret: %v)", c.Input, c.Secret)
		}
	}
	if got := w.readSection(t, "laptop", itemSection); !strings.Contains(got, "NEW_ONE Section/new   # a new one\n") {
		t.Errorf("[%s] = %q", itemSection, got)
	}
	if got := w.read(t, ".secrets.zsh"); !strings.Contains(got, "export NEW_ONE='new value'   # a new one\n") {
		t.Errorf("~/.secrets.zsh = %q", got)
	}
}

// A reference goes in the section of its item, short, when there's one;
// else in the plain [secrets], in full. Nothing is stored.
func TestAddASecretByItsReference(t *testing.T) {
	w := secretsWorld(t)
	f := w.fake
	f.On("op", "whoami")
	f.On("op", "read", "op://vault/Item/Section/token").Prints("the value\n")
	f.On("op", "read", "op://vault/Item/Section/other").Prints("other\n")
	f.On("op", "read", "op://vault/Elsewhere/key").Prints("key\n")
	w.expectSync([]string{"laptop/declarations"}, "kit secret add OTHER (laptop)")
	if out, _, code := w.run(t, "secret", "add", "OTHER", "--ref", "op://vault/Item/Section/other"); code != 0 {
		t.Errorf("kit secret add printed\n%s exit %d", out, code)
	}
	w.expectSync([]string{"laptop/declarations"}, "kit secret add KEY (laptop)")
	if out, _, code := w.run(t, "secret", "add", "KEY", "--ref", "op://vault/Elsewhere/key"); code != 0 {
		t.Errorf("kit secret add printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", itemSection); !strings.Contains(got, "OTHER Section/other\n") {
		t.Errorf("[%s] = %q", itemSection, got)
	}
	if got := w.readSection(t, "laptop", "secrets"); got != "# To be sorted\nKEY op://vault/Elsewhere/key\n" {
		t.Errorf("[secrets] = %q", got)
	}
	for _, c := range f.Commands() {
		if c.Name == "op" && c.Args[0] == "item" {
			t.Errorf("ran %s: a reference stores nothing", c)
		}
	}
}

// A new value goes in the item of the file's one secrets section for an
// item; with none, or several, --item says which.
func TestAddASecretAsksWhichItem(t *testing.T) {
	w := laptopWorld(t)
	w.stdin = "x"
	if _, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--field", "S/f"); code != 2 || !strings.Contains(errOut, "laptop/declarations has no secrets section naming an item to keep it in: say which with --item op://vault/item") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	w.writeSection(t, "laptop", "secrets op://vault/A", "A_TOKEN S/a\n")
	w.writeSection(t, "laptop", "secrets op://vault/B", "B_TOKEN S/b\n")
	if _, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--field", "S/f"); code != 2 || !strings.Contains(errOut, "has secrets sections for op://vault/A, op://vault/B: say which item it goes in with --item") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	if _, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--ref", "op://vault/A/S/f", "--item", "op://vault/A"); code != 2 || !strings.Contains(errOut, "--item is where a new value goes") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
}

func TestAddASecretSaysWhereItsValueComesFrom(t *testing.T) {
	w := secretsWorld(t)
	_, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--field", "Section/new")
	if code != 2 || !strings.Contains(errOut, "say where its value comes from: --stdin") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	w.stdin = "x"
	if _, errOut, _ := w.run(t, "secret", "add", "NEW_ONE", "--stdin"); !strings.Contains(errOut, "--field <section>/<field>") {
		t.Errorf("printed %q", errOut)
	}
}

func TestRemoveASecretAndItsValue(t *testing.T) {
	w := secretsWorld(t)
	w.write(t, ".secrets.zsh", "export TOKEN='the value'\n")
	_, errOut, code := w.run(t, "secret", "remove", "TOKEN")
	if code != 2 || !strings.Contains(errOut, "--keep-value or --delete-value") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	w.fake.On("op", "item", "edit", "Item", "--vault", "vault", "Section.token[delete]")
	w.expectSync([]string{"laptop/declarations"}, "kit secret remove TOKEN (laptop)")
	out, _, code := w.run(t, "secret", "remove", "TOKEN", "--delete-value")
	if code != 0 || !strings.Contains(out, "its value deleted from 1Password") {
		t.Errorf("kit secret remove printed\n%s exit %d", out, code)
	}
	if got := w.read(t, ".secrets.zsh"); strings.Contains(got, "TOKEN") {
		t.Errorf("~/.secrets.zsh = %q", got)
	}
}

// A value given in full, in the plain [secrets], isn't in an item kit keeps
// secrets in: removing the secret leaves it in 1Password.
func TestRemoveASecretLeavesAValueElsewhere(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "secrets", "KEY op://vault/Elsewhere/key\n")
	w.expectSync([]string{"laptop/declarations"}, "kit secret remove KEY (laptop)")
	out, _, code := w.run(t, "secret", "remove", "KEY", "--delete-value")
	if code != 0 || !strings.Contains(out, "its value is outside every item a secrets section names (op://vault/Elsewhere/key), so kit leaves it") {
		t.Errorf("kit secret remove printed\n%s exit %d", out, code)
	}
	for _, c := range w.fake.Commands() {
		if c.Name == "op" {
			t.Errorf("ran %s", c)
		}
	}
}
