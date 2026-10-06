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

func declare(t *testing.T, s *secret.Secrets, lines ...[2]string) config.List {
	t.Helper()
	var list config.List
	for i, l := range lines {
		value, note, _ := strings.Cut(l[1], "   # ")
		list.Entries = append(list.Entries, config.Entry{Name: l[0], Value: value, Note: note, Scope: "shared", Line: i + 2})
	}
	if _, err := s.Values(list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestLinesRead(t *testing.T) {
	s := secret.New(runnertest.New(t), t.TempDir(), "")
	for value, want := range map[string]string{
		"GitHub/token":            `"GitHub/token" is short for a field of the item kit.toml's secrets_item names`,
		"op://vault/a/b --mode":   "--mode needs its value",
		"op://vault/a/b --mode 9": "--mode 9 isn't a file's permissions",
		"op://vault/a/b --flag x": "--flag isn't an option",
	} {
		_, err := s.Values(config.List{Entries: []config.Entry{{Name: "~/.npmrc", Value: value, Scope: "shared", Line: 2}}})
		if err == nil || !strings.Contains(err.Error(), "shared/declarations:2: ~/.npmrc: "+want) {
			t.Errorf("%q: error = %v", value, err)
		}
	}
}

func TestCheckingReadsWhatsInPlace(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home, item)
	list := declare(t, s,
		[2]string{"GH_TOKEN", "GitHub/token"},
		[2]string{"MISSING", "GitHub/other"},
		[2]string{"~/.npmrc", "npmrc"},
		[2]string{"~/.ssh/key.pub", "op://vault/Key/public --mode 644"},
		[2]string{"CICD_PAT", "GitHub/cicd --github someone/one,someone/two"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export GH_TOKEN='x'\nexport STRAY='y'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake.On("gh", "secret", "list", "--repo", "someone/one", "--json", "name").Prints(`[{"name":"CICD_PAT"}]`)
	fake.On("gh", "secret", "list", "--repo", "someone/two", "--json", "name").Prints(`[]`)
	var got []string
	for _, it := range kind.Compare(t.Context(), s, list).Items {
		got = append(got, it.Name+" "+it.State)
	}
	want := []string{"CICD_PAT missing", "MISSING missing", "~/.npmrc missing", "~/.ssh/key.pub missing", "STRAY extra"}
	if !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q (the npmrc's mode is wrong; one repository lacks the PAT)", got, want)
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
	s := secret.New(fake, home, item)
	declare(t, s,
		[2]string{"PLAIN", "Section/plain   # a note"},
		[2]string{"AWKWARD", "Section/awkward"},
		[2]string{"BROKEN", "Section/broken"},
		[2]string{"~/.config/tool/token", "Section/plain"},
		[2]string{"PAT", "Section/pat --github someone/repo"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export BROKEN='old value'\nexport STRAY='kept'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	awkward := `it's $HOME, a ` + "`date`" + `, a \ and "quotes"`
	fake.On("op", "whoami")
	fake.On("op", "read", item+"/Section/plain").Prints("plain value\n")
	fake.On("op", "read", item+"/Section/awkward").Prints(awkward + "\n")
	fake.On("op", "read", item+"/Section/broken").Exits(1).PrintsToStderr("not found")
	fake.On("op", "read", item+"/Section/pat").Prints("pat value\n")
	fake.On("gh", "secret", "set", "PAT", "--repo", "someone/repo")
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
		if c.Name == "gh" && c.Input != "pat value" {
			t.Errorf("gh got %q on its input", c.Input)
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
	for _, want := range []string{"export PLAIN='plain value'   # a note\n", "export BROKEN='old value'\n", "export STRAY='kept'\n"} {
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
	s := secret.New(fake, t.TempDir(), item)
	declare(t, s, [2]string{"PLAIN", "Section/plain"})
	fake.On("op", "whoami").Exits(1)
	if _, err := s.Sync(t.Context()); err == nil || err.Error() != secret.SignIn {
		t.Errorf("Sync() = %v", err)
	}
}

func TestRemove(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home, item)
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
