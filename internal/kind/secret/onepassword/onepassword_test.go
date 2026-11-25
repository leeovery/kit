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
	"github.com/leeovery/kit/internal/runner"
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

// A reference short for its item names a section by what's before its first
// slash; a name alone names none.
func TestSection(t *testing.T) {
	d := onepassword.Driver{}
	for short, want := range map[string]string{"GitHub/token": "GitHub", "API Keys/key.pem": "API Keys", "token": "", "/token": ""} {
		if got, ok := d.Section(short); got != want || ok != (want != "") {
			t.Errorf("Section(%q) = %q, %v; want %q", short, got, ok, want)
		}
	}
}

// Free asks 1Password whether an item keeps something where a value would
// go, changing nothing, as op read would find it, so kit never makes a
// name op could read two ways: a name alone is taken by a field or an
// attachment of that name in any section, or none; a section's and a name
// by one in that section, by its label or id; each in any case.
func TestFree(t *testing.T) {
	run := runnertest.New(t)
	run.On("op", "item", "get", "Item", "--vault", "vault", "--format", "json").
		Prints(`{"sections":[{"id":"s1","label":"GitHub"}],"fields":[{"label":"token","section":{"id":"s1"}},{"label":"loose"}],"files":[{"name":"key.pem"},{"name":"id.pem","section":{"id":"s1","label":"GitHub"}},{"name":"loose.pem","section":{"id":"add more"}}]}`)
	d := onepassword.Driver{}
	for short, free := range map[string]bool{
		"GitHub/token": false, "github/TOKEN": false, "s1/token": false, "GitHub/other": true, "Other/token": true,
		"token": false, "LOOSE": false, "other": true,
		"key.pem": false, "other.pem": true, "GitHub/id.pem": false, "github/ID.PEM": false, "Other/id.pem": true, "id.pem": false, "loose.pem": false,
	} {
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

// op gets kit's own variables of op's, a password's session or a service
// account's token, as kit runs no other program with them.
func TestOpGetsKitsOwnVariablesOfOps(t *testing.T) {
	t.Setenv("OP_SESSION_my", "session-token")
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "ops_x")
	run := runnertest.New(t)
	run.On("op", "whoami")
	optest.Holds(run, map[string]string{"op://vault/item/a": "a"})
	d := onepassword.Driver{}
	if got := d.Session(t.Context(), run); got != store.Open {
		t.Fatalf("Session() = %v, want open", got)
	}
	if _, err := d.Read(t.Context(), run, "op://vault/item/a"); err != nil {
		t.Fatal(err)
	}
	for _, c := range run.Commands() {
		if !slices.Contains(c.Env, "OP_SESSION_my=session-token") || !slices.Contains(c.Env, "OP_SERVICE_ACCOUNT_TOKEN=ops_x") ||
			slices.ContainsFunc(c.Env, func(kv string) bool { return !strings.HasPrefix(kv, "OP_") }) {
			t.Errorf("%s ran with %q: want kit's variables of op's, and no other", c, c.Env)
		}
	}
}

// Signed in with a password's session, op runs with the app's approval
// off, as op otherwise asks the locked app and ignores the session; a value
// the person set wins, and without a session op gets none.
func TestOpWithASessionRunsWithTheAppsApprovalOff(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"session alone", map[string]string{"OP_SESSION_my": "token"}, "false"},
		{"session, approval on", map[string]string{"OP_SESSION_my": "token", "OP_BIOMETRIC_UNLOCK_ENABLED": "true"}, "true"},
		{"no session", nil, ""},
		{"approval off alone", map[string]string{"OP_BIOMETRIC_UNLOCK_ENABLED": "false"}, "false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			run := runnertest.New(t)
			run.On("op", "whoami")
			onepassword.Driver{}.Session(t.Context(), run)
			for _, cmd := range run.Commands() {
				got := slices.DeleteFunc(slices.Clone(cmd.Env), func(kv string) bool { return !strings.HasPrefix(kv, "OP_BIOMETRIC_UNLOCK_ENABLED=") })
				want := []string{}
				if c.want != "" {
					want = []string{"OP_BIOMETRIC_UNLOCK_ENABLED=" + c.want}
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s ran with %q, want %q", cmd, got, want)
				}
			}
		})
	}
}

// Signing in with a password runs op signin at kit's terminal, op asking
// for the password there itself, the app's approval off, what it prints
// kept from every report: the session is the variable op prints for a
// shell to set, as NAME=token. A sign-in op refuses, or that prints none,
// fails, never saying what op printed.
func TestSignInWithAPassword(t *testing.T) {
	t.Setenv("OP_SESSION_OLD", "still-open")
	for _, c := range []struct {
		name, prints string
		exits        int
		want         string
	}{
		{"signed in", "export OP_SESSION_ABC123=\"tok-en\"\n# This command is meant to be used with your shell's eval function.\n", 0, "OP_SESSION_ABC123=tok-en"},
		{"no session", "# Run 'eval $(op signin)' to sign in\n", 0, ""},
		{"refused", "export OP_SESSION_ABC123=\"tok-en\"\n", 1, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := runnertest.New(t)
			run.On("op", "signin").Prints(c.prints).Exits(c.exits)
			got, err := onepassword.Driver{}.SignIn(t.Context(), run)
			if got != c.want || (err == nil) != (c.want != "") || err != nil && strings.Contains(err.Error(), "tok-en") {
				t.Errorf("SignIn() = %q, %v; want %q", got, err, c.want)
			}
			cmd := run.Commands()[0]
			if biometric, _ := runner.LookupEnv(cmd.Env, "OP_BIOMETRIC_UNLOCK_ENABLED"); !cmd.Terminal || !cmd.Secret || cmd.SharesSession || biometric != "false" {
				t.Errorf("%s: at the terminal %v, its output a secret %v, sharing kit's session %v, the app's approval %q; want op at the terminal, the app's approval off, its output kept from reports",
					cmd, cmd.Terminal, cmd.Secret, cmd.SharesSession, biometric)
			}
			shell, _ := runner.LookupEnv(cmd.Env, "SHELL")
			if _, old := runner.LookupEnv(cmd.Env, "OP_SESSION_OLD"); shell != "/bin/sh" || old {
				t.Errorf("%s ran with SHELL %q, given kit's session %v; want sh's words, and no session to keep it from printing one", cmd, shell, old)
			}
		})
	}
}

// Whether a session lasts is op whoami's to say, run in it: done, it's
// open; exiting otherwise, it's over; not finishing, op hasn't answered.
func TestASessionLasts(t *testing.T) {
	for _, c := range []struct {
		name   string
		script func(*runnertest.Script)
		lasts  bool
		fails  bool
	}{
		{"open", func(*runnertest.Script) {}, true, false},
		{"over", func(s *runnertest.Script) { s.Exits(1).PrintsToStderr("[ERROR] You are not currently signed in.") }, false, false},
		{"no answer", func(s *runnertest.Script) { s.Fails(fmt.Errorf("op whoami: %w", context.DeadlineExceeded)) }, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := runnertest.New(t)
			c.script(run.On("op", "whoami"))
			lasts, err := onepassword.Driver{}.Lasts(t.Context(), run, "OP_SESSION_ABC123=tok-en")
			if lasts != c.lasts || (err != nil) != c.fails {
				t.Errorf("Lasts() = %v, %v; want %v, failing %v", lasts, err, c.lasts, c.fails)
			}
			if session, _ := runner.LookupEnv(run.Commands()[0].Env, "OP_SESSION_ABC123"); session != "tok-en" {
				t.Errorf("op whoami ran in the session %q", session)
			}
		})
	}
}

// Using the session kit keeps, every run of op has the session's variable,
// the app's approval off whatever kit's own variables say, outside kit's
// session, as there's no approval to share; signing out runs op signout in
// that session.
func TestOpUsesTheSessionKitKeeps(t *testing.T) {
	t.Setenv("OP_BIOMETRIC_UNLOCK_ENABLED", "true")
	run := runnertest.New(t)
	run.On("op", "whoami")
	run.On("op", "signout")
	optest.Holds(run, map[string]string{"op://vault/item/a": "a"})
	d := onepassword.Driver{}.Using("OP_SESSION_ABC123=tok-en")
	if got := d.Session(t.Context(), run); got != store.Open {
		t.Fatalf("Session() = %v, want open", got)
	}
	if _, err := d.Read(t.Context(), run, "op://vault/item/a"); err != nil {
		t.Fatal(err)
	}
	if err := (onepassword.Driver{}).SignOut(t.Context(), run, "OP_SESSION_ABC123=tok-en"); err != nil {
		t.Fatal(err)
	}
	if got := run.Calls(); !slices.Equal(got, []string{"op whoami", "op read op://vault/item/a", "op signout"}) {
		t.Errorf("ran %q", got)
	}
	for _, c := range run.Commands() {
		session, _ := runner.LookupEnv(c.Env, "OP_SESSION_ABC123")
		biometric, _ := runner.LookupEnv(c.Env, "OP_BIOMETRIC_UNLOCK_ENABLED")
		if session != "tok-en" || biometric != "false" || c.SharesSession {
			t.Errorf("%s ran with the session %q, the app's approval %q, sharing kit's session %v", c, session, biometric, c.SharesSession)
		}
	}
	run.On("op", "signout").Exits(1)
	if err := (onepassword.Driver{}).SignOut(t.Context(), run, "OP_SESSION_ABC123=tok-en"); err == nil {
		t.Error("SignOut(), op failing = nil")
	}
}

// A read that fails names its reference, as store.Unread, when op quotes it
// in its errors, with the line it quotes it in. A failure that quotes it
// about the person's approval or the session, or quotes nothing, or where
// op didn't run to the end, names none.
func TestAReadThatFailsNamesWhatOpQuotes(t *testing.T) {
	ref := "op://vault/item/token"
	d := onepassword.Driver{}
	for _, c := range []struct {
		name   string
		script func(*runnertest.Script)
		want   store.Unread
	}{
		{"quoted", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("[ERROR] could not read secret 'op://vault/item/token': no such field\n")
		}, store.Unread{ref: "[ERROR] could not read secret 'op://vault/item/token': no such field"}},
		{"none quoted", func(s *runnertest.Script) { s.Exits(1).PrintsToStderr("authorization prompt dismissed\n") }, nil},
		{"quoted, not approved", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("[ERROR] 2026/11/05 05:52:13 could not read secret 'op://vault/item/token': error initializing client: authorization prompt dismissed, please try again\n")
		}, nil},
		{"quoted, not in time", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("[ERROR] 2026/11/05 05:52:13 could not read secret 'op://vault/item/token': error initializing client: authorization timeout\n")
		}, nil},
		{"quoted, signed out", func(s *runnertest.Script) {
			s.Exits(1).PrintsToStderr("[ERROR] op://vault/item/token: You are not currently signed in. Please run `op signin --help` for instructions\n")
		}, nil},
		{"ended", func(s *runnertest.Script) { s.Fails(fmt.Errorf("op read: %w", context.DeadlineExceeded)) }, nil},
	} {
		run := runnertest.New(t)
		c.script(run.On("op", "read", ref))
		_, err := d.Read(t.Context(), run, ref)
		got, named := errors.AsType[store.Unread](err)
		switch {
		case err == nil:
			t.Errorf("%s: Read() didn't fail", c.name)
		case c.want == nil && named:
			t.Errorf("%s: Read() named %v, want none", c.name, got)
		case c.want != nil && !maps.Equal(got, c.want):
			t.Errorf("%s: Read() = %v, want %v named", c.name, err, c.want)
		}
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

// op read, through a pipe, ends a value with a newline unless it ends with
// one already: Read takes that newline off, so a value ending with its own
// reads without it, the pipe telling the two apart no further.
func TestReadTakesOffTheNewlineOpAdds(t *testing.T) {
	run := runnertest.New(t)
	optest.Holds(run, map[string]string{"op://vault/item/a": "a", "op://vault/item/b": "b\n"})
	d := onepassword.Driver{}
	for ref, want := range map[string]string{"op://vault/item/a": "a", "op://vault/item/b": "b"} {
		if got, err := d.Read(t.Context(), run, ref); err != nil || got != want {
			t.Errorf("Read(%s) = %q, %v; want %q", ref, got, err, want)
		}
	}
}

// ReadFile reads an attachment, the item's own or a section's, byte for
// byte with op read --out-file, into a folder of its own, removed after,
// never through a pipe, which can't tell a final newline of its own from
// op's; a field's text it ends with one newline, in place of those it ends
// with, the item saying which each is.
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
	if got := strings.Count(calls, "--out-file"); got != 3 {
		t.Errorf("ran %q: want each attachment read with --out-file", run.Calls())
	}
	folders(t, run)
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

// ReadFile finds an attachment as op read does: a name alone finds one in
// any section, or none; a section's and a name, one in that section; each
// by its name or id, in any case. A reference op would find a field and an
// attachment at fails alone, as which it reads is unknown.
func TestReadFileFindsAnAttachmentAsOpDoes(t *testing.T) {
	run := runnertest.New(t)
	optest.Holds(run, map[string]string{
		"op://vault/Item/npmrc":        "\xffbin",
		"op://vault/Item/connections":  "\xffconnections\n\n",
		"op://vault/Item/keys/id.pem":  "a\n\n",
		"op://vault/Item/file3":        "by id\n\n",
		"op://vault/Item/s-Keys/other": "\xffother",
		"op://vault/Item/Keys/token":   "\xfftoken",
		"op://vault/Item/Login/token":  "tok\n\n",
		"op://vault/Item/token":        "either",
	})
	optest.Keeps(run, "op://vault/Item", []string{"Login/token"}, []string{"npmrc", "TablePlus/connections", "Keys/id.pem", "byid", "Keys/other", "Keys/token"})
	d := onepassword.Driver{}
	for ref, want := range map[string]string{
		"op://vault/Item/NPMRC":        "\xffbin",
		"op://vault/Item/connections":  "\xffconnections\n\n",
		"op://vault/Item/keys/id.pem":  "a\n\n",
		"op://vault/Item/file3":        "by id\n\n",
		"op://vault/Item/s-Keys/other": "\xffother",
		"op://vault/Item/Keys/token":   "\xfftoken",
		"op://vault/Item/Login/token":  "tok\n",
	} {
		if got, err := d.ReadFile(t.Context(), run, ref); err != nil || got != want {
			t.Errorf("ReadFile(%s) = %q, %v; want %q", ref, got, err, want)
		}
	}
	_, err := d.ReadFile(t.Context(), run, "op://vault/Item/token")
	if named, ok := errors.AsType[store.Unread](err); !ok || named["op://vault/Item/token"] != "op://vault/Item/token names both a field and a file in op://vault/Item: give its section, or rename one" {
		t.Errorf("ReadFile() of a name of both = %v; want it failed alone, saying so", err)
	}
	folders(t, run)
}

// op read --out-file that fails, or ends without writing the file, fails
// the read, its folder removed all the same.
func TestReadFileThatFails(t *testing.T) {
	for name, script := range map[string]func(*runnertest.Script){
		"exits non-zero":     func(s *runnertest.Script) { s.Exits(1).PrintsToStderr("[ERROR] something went wrong") },
		"writes nothing":     func(*runnertest.Script) {},
		"writes, then fails": func(s *runnertest.Script) { s.WritesPartly("--out-file", "part").Exits(1) },
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
