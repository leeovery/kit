package secret_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/onepassword"
	"github.com/leeovery/kit/internal/kind/secret/secrettest"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const item = "op://vault/Item"

// onePassword is what kit knows of 1Password.
var onePassword = onepassword.Driver{}.About()

// declare declares each line's secret: one in full in the plain section,
// quoted or not, the rest in item's.
func declare(t *testing.T, s *secret.Secrets, lines ...[2]string) config.List {
	t.Helper()
	var list config.List
	for i, l := range lines {
		value, note, _ := strings.Cut(l[1], "   # ")
		e := config.Entry{Name: l[0], Value: value, Note: note, Scope: "shared", Line: i + 2, Section: "secrets"}
		if !strings.HasPrefix(strings.TrimPrefix(value, "'"), "op://") {
			e.Item, e.Section = item, "secrets "+item
		}
		list.Entries = append(list.Entries, e)
	}
	if _, err := s.Values(list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestLinesRead(t *testing.T) {
	s := secret.New(runnertest.New(t), t.TempDir())
	for _, c := range []struct{ item, value, want string }{
		{"", "GitHub/token", `"GitHub/token" isn't a reference kit knows: in a plain [secrets], give where its store keeps it in full (1Password's, as in op://vault/item/field), or put it in its item's section`},
		{item, "op://vault/a/b", "op://vault/a/b is in full: in [secrets " + item + "] a line names a field or attachment of its item"},
		{"", "op://vault/a/b --mode", "--mode needs its value"},
		{item, "a/b --mode 9", "--mode 9 isn't a file's permissions"},
		{"", "op://vault/a/b --github x/y", "--github isn't an option"},
	} {
		e := config.Entry{Name: "~/.npmrc", Value: c.value, Scope: "shared", Line: 2, Item: c.item, Section: strings.TrimSpace("secrets " + c.item)}
		_, err := s.Values(config.List{Entries: []config.Entry{e}})
		if err == nil || !strings.Contains(err.Error(), "shared/declarations:2: ~/.npmrc: "+c.want) {
			t.Errorf("%q in [%s]: error = %v", c.value, e.Section, err)
		}
	}
}

func TestCheckingReadsWhatsInPlace(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	list := declare(t, s,
		[2]string{"GH_TOKEN", "GitHub/token"},
		[2]string{"MISSING", "GitHub/other"},
		[2]string{"~/.npmrc", "npmrc"},
		[2]string{"~/.ssh/key.pub", "op://vault/Key/public --mode 644"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export GH_TOKEN='x'\nexport STRAY='y'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range kind.Compare(t.Context(), s, list).Items {
		got = append(got, it.Name+" "+it.State)
	}
	want := []string{"MISSING missing", "~/.npmrc missing", "~/.ssh/key.pub missing", "STRAY extra"}
	if !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q (the npmrc's mode is wrong)", got, want)
	}
	for _, c := range fake.Calls() {
		if strings.HasPrefix(c, "op ") {
			t.Errorf("checking ran %s: it never asks 1Password", c)
		}
	}
}

func TestSyncPutsEachInPlace(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s,
		[2]string{"PLAIN", "Section/plain   # a note"},
		[2]string{"AWKWARD", "Section/awkward"},
		[2]string{"~/.config/tool/token", "Section/plain"},
		[2]string{"~/.config/tool/key", "key-file --mode 640"},
		[2]string{"ELSEWHERE", "op://vault/Other/field"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export STRAY='kept'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	awkward := `it's $HOME, a ` + "`date`" + `, a \ and "quotes"`
	fake.On("op", "whoami")
	secrettest.Holds(fake, map[string]string{
		item + "/Section/plain":   "plain value",
		item + "/Section/awkward": awkward,
		item + "/key-file":        "line one\nline two\n",
		"op://vault/Other/field":  "elsewhere",
	})
	report, err := s.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Synced != 5 || report.Changed != 5 || len(report.Failed) != 0 {
		t.Errorf("report = %+v", report)
	}
	token := filepath.Join(home, ".config/tool/token")
	if data, _ := os.ReadFile(token); string(data) != "plain value\n" {
		t.Errorf("the file holds %q", data)
	}
	if info, _ := os.Stat(token); info.Mode().Perm() != 0o600 {
		t.Errorf("the file is %v", info.Mode().Perm())
	}
	key := filepath.Join(home, ".config/tool/key")
	if data, _ := os.ReadFile(key); string(data) != "line one\nline two\n" {
		t.Errorf("the file of many lines holds %q", data)
	}
	if info, _ := os.Stat(key); info.Mode().Perm() != 0o640 {
		t.Errorf("the file of many lines is %v", info.Mode().Perm())
	}
	env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
	for _, want := range []string{"export PLAIN='plain value'   # a note\n", "export ELSEWHERE='elsewhere'\n", "export STRAY='kept'\n"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("~/.secrets.zsh =\n%s\nwant a line %q", env, want)
		}
	}
	out, err := exec.Command("/bin/zsh", "-f", "-c", `source "$1"; printf %s "$AWKWARD"`, "zsh", filepath.Join(home, ".secrets.zsh")).Output()
	if err != nil || string(out) != awkward {
		t.Errorf("zsh read back %q, %v; want %q", out, err, awkward)
	}
	if report, err := s.Sync(t.Context()); err != nil || report.Changed != 0 {
		t.Errorf("a second sync = %+v, %v; want nothing changed", report, err)
	}
}

// Each run of op that reads a value has the person approve it, as each is
// in a session of its own: a sync reads every value in one, op inject, its
// references on its standard input and its output kept from the log, after
// asking, of no one, whether 1Password is signed in.
func TestSyncReadsEveryValueInOneRunOfOp(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s,
		[2]string{"ONE", "Section/one"},
		[2]string{"TWO", "Section/two"},
		[2]string{"~/.ssh/key", "'op://vault/Key Item/private key'"},
		[2]string{"ALSO_ONE", "Section/one"},
	)
	fake.On("op", "whoami").Exits(1).PrintsToStderr("account is not signed in")
	fake.On("op", "account", "list", "--format", "json").Prints(`[{"url": "my.1password.com"}]`)
	secrettest.Holds(fake, map[string]string{item + "/Section/one": "1", item + "/Section/two": "2", "op://vault/Key Item/private key": "-----BEGIN KEY-----\nabc\n-----END KEY-----"})
	if report, err := s.Sync(t.Context()); err != nil || report.Synced != 4 {
		t.Fatalf("Sync() = %+v, %v", report, err)
	}
	want := []string{"op whoami", "op account list --format json", "op inject"}
	if got := fake.Calls(); !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q: one run of op reading the values", got, want)
	}
	inject := fake.Commands()[2]
	if !inject.Secret || strings.Count(inject.Input, "{{") != 3 || !strings.Contains(inject.Input, "{{ op://vault/Key Item/private key }}") {
		t.Errorf("op inject read %q (secret: %v): want each reference once, its output kept from the log", inject.Input, inject.Secret)
	}
}

// A reference that won't read fails op inject whole: once 1Password
// answers, the rest are read in halves, and only what won't read is read
// alone, keeping its previous value.
func TestSyncReadsAroundAReferenceThatWontRead(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s,
		[2]string{"A", "Section/a"},
		[2]string{"B", "Section/b"},
		[2]string{"BROKEN", "Section/broken"},
		[2]string{"D", "Section/d"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export BROKEN='old value'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake.On("op", "whoami").Exits(1)
	fake.On("op", "account", "list", "--format", "json").Prints(`[{"url": "my.1password.com"}]`)
	fake.On("op", "vault", "list", "--format", "json").Prints("[]")
	secrettest.Holds(fake, map[string]string{item + "/Section/a": "a", item + "/Section/b": "b", item + "/Section/d": "d"})
	fake.On("op", "read", item+"/Section/broken").Exits(1).PrintsToStderr("not found")
	report, err := s.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Synced != 3 || len(report.Failed) != 1 || report.Failed["BROKEN"] == "" {
		t.Errorf("report = %+v", report)
	}
	var reads []string
	for _, c := range fake.Calls() {
		if strings.HasPrefix(c, "op read ") || c == "op inject" {
			reads = append(reads, c)
		}
	}
	want := []string{"op inject", "op inject", "op inject", "op read " + item + "/Section/broken", "op read " + item + "/Section/d"}
	if !slices.Equal(reads, want) {
		t.Errorf("read with %q, want %q: all, each half, then the half that failed, each alone", reads, want)
	}
	env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
	for _, line := range []string{"export A='a'\n", "export D='d'\n", "export BROKEN='old value'\n"} {
		if !strings.Contains(string(env), line) {
			t.Errorf("~/.secrets.zsh =\n%s\nwant a line %q", env, line)
		}
	}
}

// A template would read a $ in a reference as a variable, and }} as its
// end: such a reference is read alone, never through op inject.
func TestSyncReadsAnAwkwardReferenceAlone(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	awkward := "op://vault/Item $HOME/field}}"
	declare(t, s, [2]string{"PLAIN", "Section/plain"}, [2]string{"AWKWARD", "'" + awkward + "'"})
	fake.On("op", "whoami")
	secrettest.Holds(fake, map[string]string{item + "/Section/plain": "plain", awkward: "awkward"})
	if report, err := s.Sync(t.Context()); err != nil || report.Synced != 2 {
		t.Fatalf("Sync() = %+v, %v", report, err)
	}
	for _, c := range fake.Commands() {
		if c.String() == "op inject" && strings.Contains(c.Input, "$HOME") {
			t.Errorf("op inject read %q", c.Input)
		}
	}
	if !slices.Contains(fake.Calls(), "op read '"+awkward+"'") {
		t.Errorf("ran %q: want the awkward reference read alone", fake.Calls())
	}
}

// A variable's value is read back from ~/.secrets.zsh as a sync wrote it,
// whatever's in it, and without asking 1Password; one written otherwise
// isn't guessed at.
func TestVariableReadsWhatASyncWrote(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s,
		[2]string{"PLAIN", "Section/plain   # a note"},
		[2]string{"AWKWARD", "Section/awkward"},
		[2]string{"~/.config/tool/token", "Section/plain"},
	)
	awkward := `it's $HOME, ''a ` + "`date`" + `, a \ and "quotes" # not a note'`
	fake.On("op", "whoami")
	secrettest.Holds(fake, map[string]string{item + "/Section/plain": "plain value", item + "/Section/awkward": awkward})
	if _, err := s.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	calls := len(fake.Calls())
	for name, want := range map[string]string{"PLAIN": "plain value", "AWKWARD": awkward} {
		if got, ok, err := s.Variable(name); err != nil || !ok || got != want {
			t.Errorf("Variable(%s) = %v, %v: want what the sync wrote", name, ok, err)
		}
	}
	if _, ok, err := s.Variable("MISSING"); ok || err != nil {
		t.Errorf("Variable(MISSING) = %v, %v; want none", ok, err)
	}
	if !s.Has(t.Context(), "AWKWARD") || !s.Has(t.Context(), "~/.config/tool/token") || s.Has(t.Context(), "MISSING") {
		t.Error("Has() didn't say what's in place")
	}
	if len(fake.Calls()) != calls {
		t.Errorf("reading back ran %q: it never asks 1Password", fake.Calls()[calls:])
	}
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export PLAIN=unquoted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Variable("PLAIN"); err == nil || err.Error() != "~/.secrets.zsh holds PLAIN otherwise than kit writes it: run kit secret sync" {
		t.Errorf("Variable() of a line kit didn't write = %v", err)
	}
}

// When the one run of op that reads every value fails for want of the
// person's approval, the sync asks once more, then gives up, reading
// nothing alone.
func TestSyncNeedsOnePasswordToAnswer(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s, [2]string{"PLAIN", "Section/plain"}, [2]string{"OTHER", "Section/other"})
	fake.On("op", "whoami").Exits(1)
	fake.On("op", "account", "list", "--format", "json").Prints(`[{"url": "my.1password.com"}]`)
	fake.On("op", "inject").Exits(1).PrintsToStderr("authorization prompt dismissed")
	fake.On("op", "vault", "list", "--format", "json").Exits(1)
	if _, err := s.Sync(t.Context()); err == nil || err.Error() != onePassword.NoAnswer {
		t.Errorf("Sync() = %v", err)
	}
	if n := len(fake.Calls()); n != 6 {
		t.Errorf("ran %q: want no value read alone once 1Password doesn't answer", fake.Calls())
	}
}

// Listing no account, 1Password isn't signed in on this Mac, or its CLI
// isn't on: the sync says so at once, not yet rather than failed, and
// asks the app for nothing.
func TestSyncWhenOnePasswordIsntSignedIn(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s, [2]string{"PLAIN", "Section/plain"})
	fake.On("op", "whoami").Exits(1)
	fake.On("op", "account", "list", "--format", "json").Prints("[]")
	_, err := s.Sync(t.Context())
	if err == nil || err.Error() != onePassword.SignedOut {
		t.Errorf("Sync() = %v", err)
	}
	if want := []string{"op whoami", "op account list --format json"}; !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q, want %q: nothing asked of anyone", fake.Calls(), want)
	}
}

func TestRemove(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s, [2]string{"KEEP", "a/b"}, [2]string{"GONE", "a/c"}, [2]string{"~/.token", "a/d"})
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("# header\nexport KEEP='1'\nexport GONE='2'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".token"), []byte("t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(t.Context(), []string{"GONE", "~/.token"}); err != nil {
		t.Fatal(err)
	}
	if env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh")); string(env) != "# header\nexport KEEP='1'\n" {
		t.Errorf("~/.secrets.zsh = %q", env)
	}
	if _, err := os.Stat(filepath.Join(home, ".token")); !os.IsNotExist(err) {
		t.Errorf("the file: %v, want it gone", err)
	}
}
