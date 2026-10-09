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
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const item = "op://vault/Item"

// declare declares each line's secret: one in full in the plain section,
// the rest in item's.
func declare(t *testing.T, s *secret.Secrets, lines ...[2]string) config.List {
	t.Helper()
	var list config.List
	for i, l := range lines {
		value, note, _ := strings.Cut(l[1], "   # ")
		e := config.Entry{Name: l[0], Value: value, Note: note, Scope: "shared", Line: i + 2, Section: "secrets"}
		if !strings.HasPrefix(value, "op://") {
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
		{"", "GitHub/token", `"GitHub/token" is short for a field of an item: in a plain [secrets], give where 1Password keeps it in full`},
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
		[2]string{"BROKEN", "Section/broken"},
		[2]string{"~/.config/tool/token", "Section/plain"},
		[2]string{"ELSEWHERE", "op://vault/Other/field"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export BROKEN='old value'\nexport STRAY='kept'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	awkward := `it's $HOME, a ` + "`date`" + `, a \ and "quotes"`
	fake.On("op", "whoami")
	fake.On("op", "read", item+"/Section/plain").Prints("plain value\n")
	fake.On("op", "read", item+"/Section/awkward").Prints(awkward + "\n")
	fake.On("op", "read", item+"/Section/broken").Exits(1).PrintsToStderr("not found")
	fake.On("op", "read", "op://vault/Other/field").Prints("elsewhere\n")
	report, err := s.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Synced != 4 || len(report.Failed) != 1 || report.Failed["BROKEN"] == "" {
		t.Errorf("report = %+v", report)
	}
	for _, c := range fake.Commands() {
		if c.Name == "op" && c.Args[0] == "read" && !c.Secret {
			t.Errorf("%s: its output isn't kept from the log", c)
		}
	}
	token := filepath.Join(home, ".config/tool/token")
	if data, _ := os.ReadFile(token); string(data) != "plain value\n" {
		t.Errorf("the file holds %q", data)
	}
	if info, _ := os.Stat(token); info.Mode().Perm() != 0o600 {
		t.Errorf("the file is %v", info.Mode().Perm())
	}
	env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
	for _, want := range []string{"export PLAIN='plain value'   # a note\n", "export ELSEWHERE='elsewhere'\n", "export BROKEN='old value'\n", "export STRAY='kept'\n"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("~/.secrets.zsh =\n%s\nwant a line %q", env, want)
		}
	}
	out, err := exec.Command("/bin/zsh", "-f", "-c", `source "$1"; printf %s "$AWKWARD"`, "zsh", filepath.Join(home, ".secrets.zsh")).Output()
	if err != nil || string(out) != awkward {
		t.Errorf("zsh read back %q, %v; want %q", out, err, awkward)
	}
}

func TestSyncNeedsOnePasswordToAnswer(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s, [2]string{"PLAIN", "Section/plain"})
	fake.On("op", "whoami").Exits(1)
	fake.On("op", "account", "list", "--format", "json").Prints(`[{"url": "my.1password.com"}]`)
	fake.On("op", "vault", "list", "--format", "json").Exits(1)
	if _, err := s.Sync(t.Context()); err == nil || err.Error() != secret.SignIn {
		t.Errorf("Sync() = %v", err)
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
	if err == nil || err.Error() != secret.SignedOut {
		t.Errorf("Sync() = %v", err)
	}
	for _, c := range fake.Commands() {
		if slices.Contains(c.Args, "vault") {
			t.Error("it asked the app for a session: want nothing asked of anyone")
		}
	}
}

func TestSyncOpensASessionWhenNoneIsOpen(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s, [2]string{"PLAIN", "Section/plain"})
	fake.On("op", "whoami").Exits(1).PrintsToStderr("account is not signed in")
	fake.On("op", "account", "list", "--format", "json").Prints(`[{"url": "my.1password.com"}]`)
	fake.On("op", "vault", "list", "--format", "json").Prints("[]")
	fake.On("op", "read", item+"/Section/plain").Prints("v\n")
	if report, err := s.Sync(t.Context()); err != nil || report.Synced != 1 {
		t.Errorf("Sync() = %+v, %v; want the app asked for a session, and the sync done", report, err)
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
