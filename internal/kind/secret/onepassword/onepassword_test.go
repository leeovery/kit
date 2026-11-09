package onepassword_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/kind/secret/onepassword"
	"github.com/leeovery/kit/internal/kind/secret/onepassword/optest"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// 1Password's references: a value's is a vault, an item and a field (a
// section's field as section/field), an item's a vault and an item, each
// part there.
func TestReferences(t *testing.T) {
	d := onepassword.Driver{}
	for _, c := range []struct {
		ref           string
		isRef, isItem bool
	}{
		{"op://vault/item/field", true, false},
		{"op://vault/Some Item/Section/field", true, false},
		{"op://vault/item", false, true},
		{"op://vault/", false, false},
		{"op://vault//field", false, false},
		{"op://vault/item/", false, false},
		{"vault/item/field", false, false},
		{"GitHub/token", false, false},
	} {
		if got := d.IsRef(c.ref); got != c.isRef {
			t.Errorf("IsRef(%q) = %v", c.ref, got)
		}
		if got := d.IsItem(c.ref); got != c.isItem {
			t.Errorf("IsItem(%q) = %v", c.ref, got)
		}
	}
	if got := d.Join("op://vault/item", "Section/field"); got != "op://vault/item/Section/field" {
		t.Errorf("Join() = %q", got)
	}
	if short, ok := d.Short("op://vault/item", "op://vault/item/Section/field"); !ok || short != "Section/field" {
		t.Errorf("Short() = %q, %v", short, ok)
	}
	if _, ok := d.Short("op://vault/item", "op://vault/other/field"); ok {
		t.Error("Short() of another item's value = ok")
	}
}

// A field is a section's name and a field's, an attachment a name with no
// /, or a section's name and its: each checked without asking 1Password, as
// Store and Attach take them.
func TestFieldsAndAttachments(t *testing.T) {
	d := onepassword.Driver{}
	for field, ok := range map[string]bool{"GitHub/token": true, "nosection": false, "/token": false, "GitHub/": false, "op://vault/item/x": false, "a/b/c": false} {
		if short, err := d.Field(field); (err == nil) != ok || ok && short != field {
			t.Errorf("Field(%q) = %q, %v", field, short, err)
		}
	}
	for name, ok := range map[string]bool{"token.json": true, "Section/token.json": true, "": false, "a/b/c": false, "/token.json": false, "Section/": false} {
		if short, err := d.Attachment(name); (err == nil) != ok || ok && short != name {
			t.Errorf("Attachment(%q) = %q, %v", name, short, err)
		}
	}
}

// Free asks 1Password whether an item keeps something where a value would
// go, a field of a section or an attachment, the item's own or a
// section's, changing nothing.
func TestFree(t *testing.T) {
	run := runnertest.New(t)
	run.On("op", "item", "get", "Item", "--vault", "vault", "--format", "json").
		Prints(`{"sections":[{"id":"s1","label":"GitHub"}],"fields":[{"label":"token","section":{"id":"s1"}},{"label":"loose"}],"files":[{"name":"key.pem"},{"name":"id.pem","section":{"id":"s1","label":"GitHub"}},{"name":"loose.pem","section":{"id":"add more"}}]}`)
	d := onepassword.Driver{}
	for short, free := range map[string]bool{"GitHub/token": false, "GitHub/other": true, "Other/token": true, "key.pem": false, "other.pem": true, "GitHub/id.pem": false, "github/ID.PEM": false, "Other/id.pem": true, "id.pem": true, "loose.pem": false} {
		if err := d.Free(t.Context(), run, "op://vault/Item", short); (err == nil) != free {
			t.Errorf("Free(%s) = %v, want free %v", short, err, free)
		}
	}
	if err := d.Free(t.Context(), run, "op://vault/Item", "GitHub/token"); err == nil || err.Error() != "GitHub/token is in 1Password already: change it there, or give it with --ref" {
		t.Errorf("Free() of a field there = %v", err)
	}
	for _, c := range run.Calls() {
		if c != "op item get Item --vault vault --format json" {
			t.Errorf("ran %q: want the item read alone", c)
		}
	}
}

// A field goes in the item's last section of its label, when several have
// it.
func TestStoreInTheLastSectionOfItsLabel(t *testing.T) {
	run := runnertest.New(t)
	run.On("op", "item", "get", "Item", "--vault", "vault", "--format", "json").
		Prints(`{"sections":[{"id":"s1","label":"GitHub"},{"id":"s2","label":"GitHub"}],"fields":[]}`)
	run.On("op", "item", "edit", "Item", "--vault", "vault")
	if _, err := (onepassword.Driver{}).Store(t.Context(), run, "op://vault/Item", "GitHub/token", "the value"); err != nil {
		t.Fatal(err)
	}
	cmds := run.Commands()
	if edit := cmds[len(cmds)-1]; !strings.Contains(edit.Input, `"section":{"id":"s2"}`) {
		t.Error("Store() didn't put the field in the last GitHub section")
	}
}

// A read waits for the person to approve it in 1Password as long as they
// take: only a run of op that's hung is ended.
func TestReadsWaitForThePersonsApproval(t *testing.T) {
	run := runnertest.New(t)
	optest.Holds(run, map[string]string{"op://vault/item/a": "a", "op://vault/item/b": "b"})
	d := onepassword.Driver{}
	if _, err := d.ReadMany(t.Context(), run, []string{"op://vault/item/a", "op://vault/item/b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(t.Context(), run, "op://vault/item/a"); err != nil {
		t.Fatal(err)
	}
	for _, c := range run.Commands() {
		if c.Timeout < 10*time.Minute {
			t.Errorf("%s ends after %s: want it to wait ten minutes for the person", c, c.Timeout)
		}
	}
}

// Every run of op shares kit's session, where the person's approval in
// that terminal holds: once a terminal, not once a run. Signed in with a
// password, none do, as op might ask for it at the terminal.
func TestOpSharesKitsSessionUnlessSignedInWithAPassword(t *testing.T) {
	for _, env := range [][2]string{{"", ""}, {"OP_BIOMETRIC_UNLOCK_ENABLED", "false"}, {"OP_SESSION_my", "token"}} {
		t.Run(env[0], func(t *testing.T) {
			if env[0] != "" {
				t.Setenv(env[0], env[1])
			}
			run := runnertest.New(t)
			optest.Holds(run, map[string]string{"op://vault/item/a": "a"})
			run.On("op", "item", "get", "Item", "--vault", "vault", "--format", "json").Prints(`{"fields":[]}`)
			run.On("op", "item", "edit", "Item", "--vault", "vault")
			run.On("op", "whoami").Exits(1)
			run.On("op", "account", "list", "--format", "json").Prints(`[{}]`)
			run.On("op", "vault", "list", "--format", "json").Prints(`[]`)
			d := onepassword.Driver{}
			if got := d.Session(t.Context(), run); got != store.SignedIn {
				t.Fatalf("Session() = %v, want signed in", got)
			}
			if err := d.Unlock(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			if _, err := d.ReadMany(t.Context(), run, []string{"op://vault/item/a"}); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Read(t.Context(), run, "op://vault/item/a"); err != nil {
				t.Fatal(err)
			}
			if err := d.Free(t.Context(), run, "op://vault/Item", "GitHub/token"); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Store(t.Context(), run, "op://vault/Item", "GitHub/token", "value"); err != nil {
				t.Fatal(err)
			}
			for _, c := range run.Commands() {
				if c.SharesSession != (env[0] == "") {
					t.Errorf("%s shares kit's session %v", c, c.SharesSession)
				}
			}
		})
	}
}

// A read that fails names, as store.Unread, the references op quotes in its
// errors, each with the line it quotes it in, and those alone: one that's
// the start of another quoted isn't named by it. A failure that quotes
// none, or where op didn't run to the end, names none.
func TestAReadThatFailsNamesWhatOpQuotes(t *testing.T) {
	refs := []string{"op://vault/item/token", "op://vault/item/token2", "op://vault/item/other"}
	d := onepassword.Driver{}
	for _, c := range []struct {
		name   string
		script func(*runnertest.Script)
		want   store.Unread
	}{
		{"one quoted", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("[ERROR] could not read secret 'op://vault/item/token2': no such field\n")
		}, store.Unread{"op://vault/item/token2": "[ERROR] could not read secret 'op://vault/item/token2': no such field"}},
		{"two quoted", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("first: op://vault/item/token\nthen: op://vault/item/other, gone\n")
		}, store.Unread{"op://vault/item/token": "first: op://vault/item/token", "op://vault/item/other": "then: op://vault/item/other, gone"}},
		{"none quoted", func(s *runnertest.Script) { s.Exits(1).PrintsToStderr("authorization prompt dismissed\n") }, nil},
		{"quoted, not approved", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("[ERROR] 2026/11/05 05:52:13 could not read secret 'op://vault/item/token': error initializing client: authorization prompt dismissed, please try again\n")
		}, nil},
		{"quoted, not in time", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("[ERROR] 2026/11/05 05:52:13 could not read secret 'op://vault/item/other': error initializing client: authorization timeout\n")
		}, nil},
		{"quoted, signed out", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("[ERROR] op://vault/item/other: You are not currently signed in. Please run `op signin --help` for instructions\n")
		}, nil},
		{"ended", func(s *runnertest.Script) { s.Fails(fmt.Errorf("op inject: %w", context.DeadlineExceeded)) }, nil},
	} {
		run := runnertest.New(t)
		c.script(run.On("op", "inject"))
		_, err := d.ReadMany(t.Context(), run, refs)
		got, named := errors.AsType[store.Unread](err)
		switch {
		case err == nil:
			t.Errorf("%s: ReadMany() didn't fail", c.name)
		case c.want == nil && named:
			t.Errorf("%s: ReadMany() named %v, want none", c.name, got)
		case c.want != nil && !maps.Equal(got, c.want):
			t.Errorf("%s: ReadMany() = %v, want %v named", c.name, err, c.want)
		}
	}
}

// A reference a template can't take, with a $ or }}, is read alone, with op
// read, beside the rest's op inject.
func TestAnAwkwardReferenceIsReadAlone(t *testing.T) {
	awkward := "op://vault/Item $HOME/field}}"
	run := runnertest.New(t)
	optest.Holds(run, map[string]string{"op://vault/item/a": "a", awkward: "awkward"})
	values, err := (onepassword.Driver{}).ReadMany(t.Context(), run, []string{"op://vault/item/a", awkward})
	if err != nil || values["op://vault/item/a"] != "a" || values[awkward] != "awkward" {
		t.Fatalf("ReadMany() = %v, %v", values, err)
	}
	if want := []string{"op inject", "op read '" + awkward + "'"}; !slices.Equal(run.Calls(), want) {
		t.Errorf("ran %q, want %q", run.Calls(), want)
	}
}

// op read quotes the reference in saying the person didn't approve it:
// that names none, and says what op said.
func TestAReadNotApprovedNamesNone(t *testing.T) {
	said := "[ERROR] 2026/11/05 05:52:13 could not read secret 'op://vault/item/a': error initializing client: authorization prompt dismissed, please try again"
	run := runnertest.New(t)
	run.On("op", "read", "op://vault/item/a").Exits(1).PrintsToStderr(said + "\n")
	_, err := (onepassword.Driver{}).Read(t.Context(), run, "op://vault/item/a")
	if _, named := errors.AsType[store.Unread](err); named || err == nil || err.Error() != said {
		t.Errorf("Read() = %v, want op's words, naming nothing", err)
	}
}

// A reference read alone, as a template can't take it, that op names as
// one it couldn't read fails alone: what op inject read is given back
// beside it.
func TestAnAwkwardReferenceThatWontReadKeepsTheRest(t *testing.T) {
	awkward := "op://vault/Item $HOME/field}}"
	run := runnertest.New(t)
	optest.Holds(run, map[string]string{"op://vault/item/a": "a"})
	run.On("op", "read", awkward).Exits(1).PrintsToStderr("[ERROR] could not read secret '" + awkward + "': no such field\n")
	values, err := (onepassword.Driver{}).ReadMany(t.Context(), run, []string{"op://vault/item/a", awkward})
	named, ok := errors.AsType[store.Unread](err)
	if !ok || len(named) != 1 || named[awkward] == "" || values["op://vault/item/a"] != "a" {
		t.Errorf("ReadMany() = %v, %v; want a read, the awkward one named", values, err)
	}
}

// op read, through a pipe, ends a value with a newline unless it ends with
// one already: Read takes that newline off, so a value ending with its own
// reads without it, the pipe telling the two apart no further. op inject
// gives the value exactly.
func TestReadTakesOffTheNewlineOpAdds(t *testing.T) {
	run := runnertest.New(t)
	optest.Holds(run, map[string]string{"op://vault/item/a": "a", "op://vault/item/b": "b\n"})
	d := onepassword.Driver{}
	for ref, want := range map[string]string{"op://vault/item/a": "a", "op://vault/item/b": "b"} {
		if got, err := d.Read(t.Context(), run, ref); err != nil || got != want {
			t.Errorf("Read(%s) = %q, %v; want %q", ref, got, err, want)
		}
	}
	values, err := d.ReadMany(t.Context(), run, []string{"op://vault/item/a", "op://vault/item/b"})
	if err != nil || values["op://vault/item/a"] != "a" || values["op://vault/item/b"] != "b\n" {
		t.Errorf("ReadMany() = %q, %v; want each value exactly", values, err)
	}
}

// ReadFile reads an attachment, the item's own or a section's, byte for
// byte with op read --out-file, into a folder of its own, removed after,
// never through a pipe, which can't tell a final newline of its own from
// op's, nor op inject, which turns each byte that isn't UTF-8 into U+FFFD;
// a field's text it ends with one newline, in place of those it ends with,
// the item saying which each is.
func TestReadFile(t *testing.T) {
	blob := "\xff\xfe\x00kit\xc3\x28\x80\n\n"
	run := runnertest.New(t)
	optest.Holds(run, map[string]string{
		"op://vault/Item/blob":          blob,
		"op://vault/Item/text":          "one line\n",
		"op://vault/Item/loose":         "loose\n\n",
		"op://vault/Item/Section/field": "field",
		"op://vault/Item/Section/key":   "key\n",
	})
	optest.Attaches(run, "op://vault/Item", "blob", "text", "Section/key", "Other/field")
	d := onepassword.Driver{}
	for ref, want := range map[string]string{
		"op://vault/Item/blob":          blob,
		"op://vault/Item/text":          "one line\n",
		"op://vault/Item/loose":         "loose\n",
		"op://vault/Item/Section/field": "field\n",
		"op://vault/Item/Section/key":   "key\n",
	} {
		if got, err := d.ReadFile(t.Context(), run, ref); err != nil || got != want {
			t.Errorf("ReadFile(%s) = %q, %v; want %q", ref, got, err, want)
		}
	}
	calls := strings.Join(run.Calls(), "\n")
	if got := strings.Count(calls, "op item get"); got != 5 {
		t.Errorf("ran %q: want the item asked of each reference", run.Calls())
	}
	if got := strings.Count(calls, "--out-file"); got != 3 || strings.Contains(calls, "op inject") {
		t.Errorf("ran %q: want each attachment read with --out-file, and no op inject", run.Calls())
	}
	folders(t, run)
	values, err := d.ReadMany(t.Context(), run, []string{"op://vault/Item/blob"})
	if err != nil || values["op://vault/Item/blob"] == blob {
		t.Errorf("ReadMany() of bytes that aren't UTF-8 = %v: want them turned into U+FFFD, as op inject does", err)
	}
}

// An attachment goes in a section as op's assignments name one, section
// then name, each escaped, and is deleted the same way; the item's own goes
// by its name alone.
func TestAttachAndDeleteInASection(t *testing.T) {
	from := filepath.Join(t.TempDir(), "id.pem")
	if err := os.WriteFile(from, []byte("key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := runnertest.New(t)
	run.On("op", "item", "edit", "Item", "--vault", "vault", `Git\.Hub.id\.pem[file]=`+from)
	run.On("op", "item", "edit", "Item", "--vault", "vault", `id\.pem[file]=`+from)
	run.On("op", "item", "edit", "Item", "--vault", "vault", `Git\.Hub.id\.pem[delete]`)
	d := onepassword.Driver{}
	for _, name := range []string{"Git.Hub/id.pem", "id.pem"} {
		if short, err := d.Attach(t.Context(), run, "op://vault/Item", name, from); err != nil || short != name {
			t.Errorf("Attach(%s) = %q, %v", name, short, err)
		}
	}
	if err := d.Delete(t.Context(), run, "op://vault/Item", "Git.Hub/id.pem"); err != nil {
		t.Errorf("Delete() = %v", err)
	}
	if _, err := d.Attach(t.Context(), run, "op://vault/Item", "a/b/c", from); err == nil {
		t.Error("Attach() of a name with two /s didn't fail")
	}
}

// folders checks the runs of op read --out-file on run: each into a folder
// of its own, its output kept from the log, the folder gone after.
func folders(t *testing.T, run *runnertest.Fake) {
	t.Helper()
	for _, c := range run.Commands() {
		i := slices.Index(c.Args, "--out-file")
		if i < 0 {
			continue
		}
		path := c.Args[i+1]
		if !c.Secret || !filepath.IsAbs(path) || filepath.Dir(path) == filepath.Clean(os.TempDir()) {
			t.Errorf("%s: want an absolute path in a folder of its own, its output kept from the log (secret %v)", c, c.Secret)
		}
		if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s left its folder: %v", c, err)
		}
	}
}

// ReadFile finds an attachment as op read does: its name or its section's
// in any case, or by id. A name with no / is the item's own alone: a field
// of that name stays a field beside a section's attachment of it.
func TestReadFileFindsAnAttachmentAsOpDoes(t *testing.T) {
	run := runnertest.New(t)
	optest.Holds(run, map[string]string{
		"op://vault/Item/npmrc":        "\xffbin",
		"op://vault/Item/keys/id.pem":  "a\n\n",
		"op://vault/Item/file2":        "by id\n\n",
		"op://vault/Item/s-Keys/other": "\xffother",
		"op://vault/Item/password":     "pw\n\n",
	})
	optest.Attaches(run, "op://vault/Item", "npmrc", "Keys/id.pem", "byid", "Keys/other", "Keys/password")
	d := onepassword.Driver{}
	for ref, want := range map[string]string{
		"op://vault/Item/NPMRC":        "\xffbin",
		"op://vault/Item/keys/id.pem":  "a\n\n",
		"op://vault/Item/file2":        "by id\n\n",
		"op://vault/Item/s-Keys/other": "\xffother",
		"op://vault/Item/password":     "pw\n",
	} {
		if got, err := d.ReadFile(t.Context(), run, ref); err != nil || got != want {
			t.Errorf("ReadFile(%s) = %q, %v; want %q", ref, got, err, want)
		}
	}
	folders(t, run)
}

// op read --out-file that fails, or ends without writing the file, fails
// the read, its folder removed all the same.
func TestReadFileThatFails(t *testing.T) {
	for name, script := range map[string]func(*runnertest.Script){
		"exits non-zero":     func(s *runnertest.Script) { s.Exits(1).PrintsToStderr("[ERROR] something went wrong") },
		"writes nothing":     func(*runnertest.Script) {},
		"writes, then fails": func(s *runnertest.Script) { s.Writes("--out-file", "part").Exits(1) },
	} {
		t.Run(name, func(t *testing.T) {
			run := runnertest.New(t)
			optest.Holds(run, map[string]string{"op://vault/Item/file": "bytes"})
			optest.Attaches(run, "op://vault/Item", "file")
			script(run.OnMatch("op", func(args []string) bool { return slices.Contains(args, "--out-file") }))
			if got, err := (onepassword.Driver{}).ReadFile(t.Context(), run, "op://vault/Item/file"); err == nil || got != "" {
				t.Errorf("ReadFile() = %q, %v; want it failed", got, err)
			} else if name == "writes nothing" && !strings.HasPrefix(err.Error(), "op read wrote no file") {
				t.Errorf("ReadFile() = %v; want that op wrote no file", err)
			}
			folders(t, run)
		})
	}
}
