package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/kind/secret/onepassword/optest"
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
	optest.Holds(w.fake, map[string]string{"op://vault/Item/Section/token": "the value"})
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
	optest.Holds(f, map[string]string{"op://vault/Item/Section/new": "new value", "op://vault/Item/Section/token": "the value"})
	w.expectSync([]string{"laptop/declarations"}, "kit secret add NEW_ONE (laptop): a new one")
	out, _, code := w.run(t, "secret", "add", "NEW_ONE", "--item", "op://vault/Item", "--field", "Section/new", "--stdin", "--note", "a new one")
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
	optest.Holds(f, map[string]string{"op://vault/Item/Section/token": "the value", "op://vault/Item/Section/other": "other", "op://vault/Elsewhere/key": "key"})
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
	if got := w.readSection(t, "laptop", "secrets"); got != "KEY op://vault/Elsewhere/key\n" {
		t.Errorf("[secrets] = %q", got)
	}
	for _, c := range f.Commands() {
		if c.Name == "op" && c.Args[0] == "item" {
			t.Errorf("ran %s: a reference stores nothing", c)
		}
	}
}

// A new value goes where it's told, never by default: without --in or
// --item, even with one block the file could take it in, it's refused,
// listing the blocks, by name and item; a reference says where it is
// already, so it's told neither.
func TestAddASecretSaysWhereItGoes(t *testing.T) {
	w := laptopWorld(t)
	w.stdin = "x"
	if _, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--field", "S/f"); code != 2 || !strings.Contains(errOut, "say where it goes: --in <a block's name>, or --item <an item> (1Password's, as in op://vault/item), its block made when the file has none; there are no blocks for items yet") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	w.writeSection(t, "shared", "secrets op://vault/A --as tokens", "A_TOKEN S/a\n")
	if _, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--field", "S/f"); code != 2 || !strings.Contains(errOut, "the blocks are tokens (op://vault/A, shared/declarations)") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	w.writeSection(t, "laptop", "secrets op://vault/B", "B_TOKEN S/b\n")
	if _, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--field", "S/f", "--in", "keys"); code != 2 || !strings.Contains(errOut, "no secrets block goes by keys: the blocks are tokens (op://vault/A, shared/declarations), op://vault/B (laptop/declarations)") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	for _, args := range [][]string{{"--ref", "op://vault/A/S/f", "--item", "op://vault/A"}, {"--ref", "op://vault/A/S/f", "--in", "tokens"}} {
		if _, errOut, code := w.run(t, append([]string{"secret", "add", "NEW_ONE"}, args...)...); code != 2 || !strings.Contains(errOut, "--in and --item say where a new value goes; --ref says where one is already") {
			t.Errorf("%q: printed %q, exit %d", args, errOut, code)
		}
	}
	if _, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--field", "S/f", "--in", "tokens", "--item", "op://vault/A"); code != 2 || !strings.Contains(errOut, "--in or --item, not both") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	if _, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--field", "S/f", "--item", "op://vault/A/S"); code != 2 || !strings.Contains(errOut, `--item: "op://vault/A/S" isn't an item's reference kit knows: 1Password's, as in op://vault/item`) {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
}

// --in names a block: the secret joins this file's block for its item,
// whatever it's called, or, the file having none, kit makes one going by
// that name, for the item the name stands for.
func TestAddASecretInANamedBlock(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "shared", "secrets op://vault/Item --as tokens", "TOKEN Section/token\n")
	w.stdin = "new value"
	f := w.fake
	f.On("op", "whoami")
	f.On("op", "item", "get", "Item", "--vault", "vault", "--format", "json").Prints(`{"id":"abc","title":"Item","sections":[{"id":"s1","label":"Section"}],"fields":[]}`)
	f.On("op", "item", "edit", "Item", "--vault", "vault")
	optest.Holds(f, map[string]string{"op://vault/Item/Section/new": "new value", "op://vault/Item/Section/token": "the value", "op://vault/Item/Section/more": "more"})
	w.expectSync([]string{"laptop/declarations"}, "kit secret add NEW_ONE (laptop)")
	if out, _, code := w.run(t, "secret", "add", "NEW_ONE", "--in", "tokens", "--field", "Section/new", "--stdin"); code != 0 {
		t.Fatalf("kit secret add printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "secrets op://vault/Item --as tokens"); got != "NEW_ONE Section/new\n" {
		t.Errorf("laptop's [secrets op://vault/Item --as tokens] = %q, want it made, by the name", got)
	}
	w.stdin = "more"
	w.expectSync([]string{"shared/declarations"}, "kit secret add MORE (laptop)")
	if out, _, code := w.run(t, "secret", "add", "MORE", "--shared", "--in", "tokens", "--field", "Section/more", "--stdin"); code != 0 {
		t.Fatalf("kit secret add --shared printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "secrets op://vault/Item --as tokens"); got != "MORE Section/more\nTOKEN Section/token\n" {
		t.Errorf("shared's [secrets op://vault/Item --as tokens] = %q, want it joined", got)
	}
}

// Everything's checked before a value's asked for: told nowhere, or
// somewhere that isn't, or not the field, kit asks for nothing, and asks
// once it knows.
func TestAddASecretAsksForNoValueTillItKnowsWhereItGoes(t *testing.T) {
	w := laptopWorld(t)
	w.terminal, w.size = true, [2]int{160, 64}
	w.writeSection(t, "shared", "secrets op://vault/A --as tokens", "A_TOKEN S/a\n")
	w.writeSection(t, "studio", "secrets op://vault/B --as keys", "B_TOKEN S/b\n")
	for _, args := range [][]string{
		{"--field", "S/f"},
		{"--in", "typo", "--field", "S/f"},
		{"--item", "op://vault/A/S", "--field", "S/f"},
		{"--in", "tokens"},
		{"--ref", "elsewhere:x"},
		{"--in", "keys", "--item", "op://vault/A", "--field", "S/f"},
	} {
		if _, errOut, code := w.run(t, append([]string{"secret", "add", "NEW_ONE"}, args...)...); code == 0 || len(w.typedFor) > 0 {
			t.Errorf("%q: printed %q, exit %d, asked for %q: want it refused, nothing asked", args, errOut, code, w.typedFor)
		}
	}
	w.notTyped = ask.ErrCancelled
	w.fake.On("op", "whoami")
	w.fake.On("op", "item", "get", "A", "--vault", "vault", "--format", "json").Prints(`{"id":"a","title":"A","fields":[]}`)
	w.run(t, "secret", "add", "NEW_ONE", "--in", "tokens", "--field", "S/f")
	if !slices.Equal(w.typedFor, []string{"NEW_ONE  needs its value"}) {
		t.Errorf("asked for %q, want its value, once it knew where it goes", w.typedFor)
	}
}

// A block made in the shared file goes by a name no Mac's file has for
// another item: kit refuses it, naming that block, before anything's asked
// for or stored.
func TestAddASecretSharedByANameAMacHas(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "secrets op://vault/Item --as tokens", "TOKEN Section/token\n")
	w.writeSection(t, "studio", "secrets op://vault/Other --as tokens", "OTHER Section/other\n")
	w.stdin = "x"
	_, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--shared", "--in", "tokens", "--field", "Section/new", "--stdin")
	if code == 0 || !strings.Contains(errOut, "the config wouldn't read with it: studio/declarations:4: [secrets op://vault/Other --as tokens]: --as tokens is op://vault/Item in shared/declarations") || !strings.Contains(errOut, "a name stands for one item") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	if strings.Contains(w.read(t, filepath.Join(".config", "kit", "shared", "declarations")), "tokens") {
		t.Error("the shared file took a block going by tokens")
	}
}

// Nothing kit secret add would refuse to declare is asked for, at a
// terminal, or stored, without one: a line that wouldn't read back as the
// kind reads it, a name that isn't one or is declared already, a field the
// store wouldn't take, or a store that isn't signed in.
func TestAddASecretChecksAllBeforeTheValue(t *testing.T) {
	for _, c := range []struct {
		name, want string
		args       []string
		signedOut  bool
	}{
		{"--mode on a variable", "NEW_ONE: --mode is a file's", []string{"NEW_ONE", "--item", "op://vault/Item", "--field", "S/f", "--mode", "644"}, false},
		{"--mode that isn't one", "~/.x: --mode abc isn't a file's permissions, as in 600", []string{"~/.x", "--item", "op://vault/Item", "--field", "S/f", "--mode", "abc"}, false},
		{"--mode past 777", "~/.x: --mode 1000 isn't a file's permissions, as in 600", []string{"~/.x", "--item", "op://vault/Item", "--field", "S/f", "--mode", "1000"}, false},
		{"--ref with --mode on a variable", "NEW_ONE: --mode is a file's", []string{"NEW_ONE", "--ref", "op://vault/Item/S/f", "--mode", "644"}, false},
		{"declared already", "TOKEN is in laptop/declarations already", []string{"TOKEN", "--item", "op://vault/Item", "--field", "S/f"}, false},
		{"a name starting with a digit", `"9BAD" isn't a name`, []string{"9BAD", "--item", "op://vault/Item", "--field", "S/f"}, false},
		{"a name with a hyphen", `"bad-name" isn't a name`, []string{"bad-name", "--item", "op://vault/Item", "--field", "S/f"}, false},
		{"a field with no section", `"nosection" isn't where in 1Password it goes: <section>/<field>`, []string{"NEW_ONE", "--item", "op://vault/Item", "--field", "nosection"}, false},
		{"a field in full", `"op://vault/Other/x" isn't where in 1Password it goes`, []string{"NEW_ONE", "--item", "op://vault/Item", "--field", "op://vault/Other/x"}, false},
		{"signed out", "1Password isn't signed in", []string{"NEW_ONE", "--item", "op://vault/Item", "--field", "S/f"}, true},
	} {
		for _, terminal := range []bool{true, false} {
			w := secretsWorld(t)
			w.terminal, w.stdin = terminal, "the value"
			if c.signedOut {
				w.fake.On("op", "whoami").Exits(1)
				w.fake.On("op", "account", "list", "--format", "json").Prints("[]")
			} else {
				w.fake.On("op", "whoami")
			}
			args := append([]string{"secret", "add"}, c.args...)
			if !terminal && !slices.Contains(args, "--ref") {
				args = append(args, "--stdin")
			}
			_, errOut, code := w.run(t, args...)
			stored := slices.ContainsFunc(w.fake.Calls(), func(call string) bool { return strings.HasPrefix(call, "op item") })
			if code != 2 || !strings.Contains(ansi.Strip(errOut), c.want) || len(w.typedFor) > 0 || stored {
				t.Errorf("%s, at a terminal %v: printed %q, exit %d, asked for %q, ran %q; want it refused with %q, nothing asked or stored", c.name, terminal, errOut, code, w.typedFor, w.fake.Calls(), c.want)
			}
			if got := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations")); strings.Contains(got, "NEW_ONE") || strings.Contains(got, "~/.x") {
				t.Errorf("%s: laptop's declarations = %q, want nothing declared", c.name, got)
			}
		}
	}
}

// A name in the shared file can't go in this Mac's, nor in the shared file
// again: kit secret add refuses it before anything's asked for, stored,
// written or committed.
func TestAddASecretNamedInTheSharedFile(t *testing.T) {
	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"this Mac's", "TOKEN is in the shared declarations already", []string{"TOKEN"}},
		{"shared", "TOKEN is in shared/declarations already", []string{"TOKEN", "--shared"}},
	} {
		for _, terminal := range []bool{true, false} {
			w := laptopWorld(t)
			w.writeSection(t, "shared", itemSection, "TOKEN Section/token\n")
			w.writeSection(t, "studio", itemSection, "STUDIO Section/studio\n")
			before := map[string]string{}
			for _, scope := range []string{"shared", "laptop", "studio"} {
				before[scope] = w.read(t, filepath.Join(".config", "kit", scope, "declarations"))
			}
			w.terminal, w.stdin = terminal, "the value"
			w.fake.On("op", "whoami")
			args := append(append([]string{"secret", "add"}, c.args...), "--item", "op://vault/Item", "--field", "Section/new")
			if !terminal {
				args = append(args, "--stdin")
			}
			_, errOut, code := w.run(t, args...)
			ran := slices.DeleteFunc(w.fake.Calls(), func(call string) bool { return call == "op whoami" })
			if code != 2 || !strings.Contains(ansi.Strip(errOut), c.want) || len(w.typedFor) > 0 || len(ran) > 0 {
				t.Errorf("%s, at a terminal %v: printed %q, exit %d, asked for %q, ran %q; want it refused, nothing asked, stored or committed", c.name, terminal, errOut, code, w.typedFor, ran)
			}
			for scope, was := range before {
				if got := w.read(t, filepath.Join(".config", "kit", scope, "declarations")); got != was {
					t.Errorf("%s: %s's declarations = %q, want them untouched", c.name, scope, got)
				}
			}
		}
	}
}

// Declared for every Mac by the reference the Macs' lines give, short or in
// full, whatever block they're in, a secret comes out of their files in the
// same edit and commit, its shared line in the shared block for its item,
// keeping their note; nothing is stored.
func TestAddASecretSharedTakesItOutOfTheMacs(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "shared", "secrets op://vault/Item --as mac-secrets", "TOKEN Section/token\n")
	w.writeSection(t, "laptop", "secrets op://vault/Item --as mac-secrets", "FOO Section/foo   # a token\n")
	w.writeSection(t, "studio", "secrets", "FOO op://vault/Item/Section/foo\n")
	f := w.fake
	f.On("op", "whoami")
	optest.Holds(f, map[string]string{"op://vault/Item/Section/foo": "foo", "op://vault/Item/Section/token": "the value"})
	w.expectSync([]string{"laptop/declarations", "shared/declarations", "studio/declarations"}, "kit secret add FOO (laptop)")
	out, _, code := w.run(t, "secret", "add", "FOO", "--shared", "--ref", "op://vault/Item/Section/foo")
	if code != 0 || !strings.Contains(out, "declared in shared, out of laptop and studio, synced") {
		t.Fatalf("kit secret add --shared printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "secrets op://vault/Item --as mac-secrets"); got != "FOO Section/foo   # a token\nTOKEN Section/token\n" {
		t.Errorf("shared's [secrets op://vault/Item --as mac-secrets] = %q", got)
	}
	for _, mac := range []string{"laptop", "studio"} {
		if got := w.read(t, filepath.Join(".config", "kit", mac, "declarations")); strings.Contains(got, "FOO") {
			t.Errorf("%s's declarations = %q, want FOO out of them", mac, got)
		}
	}
	for _, call := range f.Calls() {
		if strings.HasPrefix(call, "op item") {
			t.Errorf("ran %s: a reference stores nothing", call)
		}
	}
	commits := slices.DeleteFunc(f.Calls(), func(call string) bool { return !strings.Contains(call, " commit ") })
	if len(commits) != 1 {
		t.Errorf("committed %q, want one commit", commits)
	}
}

// Declared for every Mac, a secret a Mac's line keeps elsewhere, or as a
// file of another mode, is refused before the store's asked anything, the
// files untouched, naming the line, and, for a value elsewhere, the --ref
// that shares it: whatever block the line is in, one going by the same name
// for another item too.
func TestAddASecretSharedRefusesWhatAMacSaysOtherwise(t *testing.T) {
	for _, c := range []struct {
		name, header, line, want string
		args                     []string
	}{
		{"a file of another mode", "secrets op://vault/Item --as mac-secrets", "~/.npmrc Section/npmrc --mode 644\n",
			"laptop/declarations:5 has ~/.npmrc with --mode 644, where this gives --mode 600: give the same, or take that line out first; --ref op://vault/Item/Section/npmrc --mode 644 shares the value it has",
			[]string{"~/.npmrc", "--ref", "op://vault/Item/Section/npmrc"}},
		{"a value at another field", "secrets op://vault/Item --as mac-secrets", "FOO Section/foo\n",
			"laptop/declarations:5 has FOO with op://vault/Item/Section/foo, where this gives op://vault/Item/Section/other: give the same, or take that line out first; --ref op://vault/Item/Section/foo shares the value it has",
			[]string{"FOO", "--item", "op://vault/Item", "--field", "Section/other", "--stdin"}},
		{"a block of the same name for another item", "", "",
			"studio/declarations:5 has FOO with op://vault/Other/Section/foo, where this gives op://vault/Item/Section/foo: give the same, or take that line out first; --ref op://vault/Other/Section/foo shares the value it has",
			[]string{"FOO", "--in", "mac-secrets", "--field", "Section/foo", "--stdin"}},
	} {
		w := laptopWorld(t)
		w.stdin = "new value"
		if c.header != "" {
			w.writeSection(t, "laptop", c.header, c.line)
		} else {
			w.writeSection(t, "laptop", "secrets op://vault/Item --as mac-secrets", "TOKEN Section/token\n")
			w.writeSection(t, "studio", "secrets op://vault/Other --as mac-secrets", "FOO Section/foo\n")
		}
		before := map[string]string{}
		for _, scope := range []string{"shared", "laptop", "studio"} {
			before[scope] = w.read(t, filepath.Join(".config", "kit", scope, "declarations"))
		}
		_, errOut, code := w.run(t, append([]string{"secret", "add", "--shared"}, c.args...)...)
		if code != 2 || !strings.Contains(ansi.Strip(errOut), c.want) {
			t.Errorf("%s: printed %q, exit %d; want it refused with %q", c.name, errOut, code, c.want)
		}
		if calls := w.fake.Calls(); len(calls) > 0 {
			t.Errorf("%s: ran %q, want the store asked nothing, nothing committed", c.name, calls)
		}
		for scope, was := range before {
			if got := w.read(t, filepath.Join(".config", "kit", scope, "declarations")); got != was {
				t.Errorf("%s: %s's declarations = %q, want them untouched", c.name, scope, got)
			}
		}
	}
}

// Where a new value goes must be free in its item, a field or an
// attachment, and a reference given must read: each found before anything's
// asked for, stored, declared or committed.
func TestAddASecretChecksTheStoreBeforeTheValue(t *testing.T) {
	from := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(from, []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"a field there already", "Section/token is in 1Password already", []string{"NEW_ONE", "--item", "op://vault/Item", "--field", "Section/token"}},
		{"an attachment there already", "token.json is in 1Password already", []string{"~/.config/tool/token.json", "--item", "op://vault/Item", "--from", from}},
		{"a reference that doesn't read", "op://vault/Item/Section/missing doesn't read from 1Password", []string{"NEW_ONE", "--ref", "op://vault/Item/Section/missing"}},
	} {
		for _, terminal := range []bool{true, false} {
			w := secretsWorld(t)
			before := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations"))
			w.terminal, w.stdin = terminal, "the value"
			w.fake.On("op", "whoami")
			w.fake.On("op", "item", "get", "Item", "--vault", "vault", "--format", "json").
				Prints(`{"id":"abc","title":"Item","sections":[{"id":"s1","label":"Section"}],"fields":[{"id":"f1","label":"token","section":{"id":"s1"}}],"files":[{"id":"x","name":"token.json"}]}`)
			w.fake.On("op", "read", "op://vault/Item/Section/missing").Exits(1)
			args := append([]string{"secret", "add"}, c.args...)
			if !terminal && !slices.Contains(args, "--ref") && !slices.Contains(args, "--from") {
				args = append(args, "--stdin")
			}
			_, errOut, code := w.run(t, args...)
			edited := slices.ContainsFunc(w.fake.Calls(), func(call string) bool { return strings.HasPrefix(call, "op item edit") })
			if code != 2 || !strings.Contains(ansi.Strip(errOut), c.want) || len(w.typedFor) > 0 || edited {
				t.Errorf("%s, at a terminal %v: printed %q, exit %d, asked for %q, ran %q; want it refused, nothing asked or stored", c.name, terminal, errOut, code, w.typedFor, w.fake.Calls())
			}
			if got := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations")); got != before {
				t.Errorf("%s: laptop's declarations = %q, want them untouched", c.name, got)
			}
		}
	}
}

func TestAddASecretSaysWhereItsValueComesFrom(t *testing.T) {
	w := secretsWorld(t)
	_, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--field", "Section/new")
	if code != 2 || !strings.Contains(errOut, "say where its value comes from: --stdin") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	w.stdin = "x"
	if _, errOut, _ := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--item", "op://vault/Item"); !strings.Contains(errOut, "say where in 1Password's item it goes: --field <section>/<field>, as in GitHub/token") {
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
	if code != 0 || !strings.Contains(out, "its value is outside every item a secrets block is for (op://vault/Elsewhere/key), so kit leaves it") {
		t.Errorf("kit secret remove printed\n%s exit %d", out, code)
	}
	for _, c := range w.fake.Commands() {
		if c.Name == "op" {
			t.Errorf("ran %s", c)
		}
	}
}

// An attachment's name with a period in it is escaped in op's assignments,
// which would read the period as a section's end; a reference reads it as
// it is.
func TestASecretFilesAttachmentNamedWithAPeriod(t *testing.T) {
	w := secretsWorld(t)
	from := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(from, []byte(`{"token":"x"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := w.fake
	f.On("op", "whoami")
	f.On("op", "item", "get", "Item", "--vault", "vault", "--format", "json").Prints(`{"id":"abc","title":"Item","files":[{"id":"x","name":"other.json"}]}`)
	f.On("op", "item", "edit", "Item", "--vault", "vault", `token\.json[file]=`+from)
	optest.Holds(f, map[string]string{"op://vault/Item/token.json": `{"token":"x"}` + "\n", "op://vault/Item/Section/token": "the value"})
	w.expectSync([]string{"laptop/declarations"}, "kit secret add ~/.config/tool/token.json (laptop)")
	if out, _, code := w.run(t, "secret", "add", "~/.config/tool/token.json", "--from", from, "--item", "op://vault/Item"); code != 0 {
		t.Fatalf("kit secret add printed\n%s exit %d", out, code)
	}
	if got := w.read(t, ".config/tool/token.json"); got != `{"token":"x"}`+"\n" {
		t.Errorf("the file holds %q", got)
	}
	f.On("op", "item", "edit", "Item", "--vault", "vault", `token\.json[delete]`)
	w.expectSync([]string{"laptop/declarations"}, "kit secret remove ~/.config/tool/token.json (laptop)")
	if out, _, code := w.run(t, "secret", "remove", "~/.config/tool/token.json", "--delete-value"); code != 0 || !strings.Contains(out, "its value deleted from 1Password") {
		t.Errorf("kit secret remove printed\n%s exit %d", out, code)
	}
}

// placesWorld is laptopWorld with secrets in three blocks: the shared
// file's, named, and this Mac's plain one and its own for another item.
func placesWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "shared", "secrets op://vault/Item --as tokens", "A Section/a\nB Section/b\n")
	w.writeSection(t, "laptop", "secrets", "KEY op://vault/Elsewhere/key\n")
	w.writeSection(t, "laptop", "secrets op://vault/Other", "C Section/c\n")
	return w
}

// kit secret places lists the blocks this Mac reads, a line each: its name,
// if it has one, its store, its item, its file and how many it holds.
func TestSecretPlaces(t *testing.T) {
	w := placesWorld(t)
	out, _, code := w.run(t, "secret", "places")
	want := "tokens  1Password · op://vault/Item · shared · 2 secrets\n" +
		"[secrets]  references in full · laptop · 1 secret\n" +
		"op://vault/Other  1Password · laptop · 1 secret\n"
	if code != 0 || out != want {
		t.Errorf("kit secret places printed\n%s\nexit %d, want\n%s", out, code, want)
	}
}

func TestSecretPlacesPretty(t *testing.T) {
	w := placesWorld(t)
	w.terminal, w.environ = true, []string{"TTY_FORCE=1", "TERM=xterm-256color"}
	out, _, code := w.run(t, "secret", "places")
	want := []string{
		"  ● tokens  1Password · op://vault/Item · shared · 2 secrets",
		"  ● [secrets]  references in full · laptop · 1 secret",
		"  ● op://vault/Other  1Password · laptop · 1 secret",
	}
	if got := strings.Split(strings.TrimRight(ansi.Strip(out), "\n"), "\n"); code != 0 || !slices.Equal(got, want) {
		t.Errorf("kit secret places printed\n%s\nexit %d, want\n%s", out, code, strings.Join(want, "\n"))
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("kit secret places printed %q, want it in colour at a terminal", out)
	}
}

func TestSecretPlacesJSON(t *testing.T) {
	w := placesWorld(t)
	out, _, code := w.run(t, "secret", "places", "--json")
	var doc struct {
		Schema  int    `json:"schema"`
		Machine string `json:"machine"`
		Places  []struct {
			Name, Store, Item, Scope, File string
			Line, Secrets                  int
		} `json:"places"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 {
		t.Fatalf("kit secret places --json printed\n%s\nexit %d: %v", out, code, err)
	}
	if doc.Schema != 1 || doc.Machine != "laptop" || len(doc.Places) != 3 {
		t.Fatalf("document = %+v", doc)
	}
	first := doc.Places[0]
	if first.Name != "tokens" || first.Store != "1Password" || first.Item != "op://vault/Item" || first.Scope != "shared" || first.File != "shared/declarations" || first.Secrets != 2 || first.Line == 0 {
		t.Errorf("places[0] = %+v", first)
	}
	if plain := doc.Places[1]; plain.Name != "" || plain.Store != "" || plain.Item != "" || plain.Scope != "laptop" || plain.Secrets != 1 {
		t.Errorf("places[1] = %+v, want the plain block, no store or item", plain)
	}
}

// Declared for every Mac by a reference in a Mac's block, with no block for
// its item in the shared file yet, a secret goes in a block made for that
// item, going by the Mac block's name, so the name isn't lost as the Mac's
// block empties.
func TestAddASecretSharedKeepsItsBlocksName(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "secrets op://vault/Item --as mac-secrets", "FOO Section/foo\n")
	f := w.fake
	f.On("op", "whoami")
	optest.Holds(f, map[string]string{"op://vault/Item/Section/foo": "foo"})
	w.expectSync([]string{"laptop/declarations", "shared/declarations"}, "kit secret add FOO (laptop)")
	if out, _, code := w.run(t, "secret", "add", "FOO", "--shared", "--ref", "op://vault/Item/Section/foo"); code != 0 {
		t.Fatalf("kit secret add --shared printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "secrets op://vault/Item --as mac-secrets"); got != "FOO Section/foo\n" {
		t.Errorf("shared's [secrets op://vault/Item --as mac-secrets] = %q", got)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations")); strings.Contains(got, "FOO") || strings.Contains(got, "mac-secrets") {
		t.Errorf("laptop's declarations = %q, want its block gone", got)
	}
}
