package secret_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/onepassword/optest"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// envFile is ~/.secrets.zsh under home.
func envFile(home string) string { return filepath.Join(home, ".secrets.zsh") }

// readEnv is what ~/.secrets.zsh under home holds.
func readEnv(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(envFile(home))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A variable put in place takes the place of its line, and every other line
// stays byte for byte, whatever it is: nothing's asked of a store.
func TestPutReplacesAVariablesLineAlone(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s, [2]string{"A", "Section/a"}, [2]string{"NEW", "Section/new   # a note"})
	was := "# a header of my own\nexport A='a'\n\n  # a comment, indented  \nexport NEW='old'   # an old note\nexport STRAY=by\\ hand\nnot an export"
	if err := os.WriteFile(envFile(home), []byte(was), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("NEW", "new value"); err != nil {
		t.Fatal(err)
	}
	want := "# a header of my own\nexport A='a'\n\n  # a comment, indented  \nexport NEW='new value'   # a note\nexport STRAY=by\\ hand\nnot an export"
	if got := readEnv(t, home); got != want {
		t.Errorf("~/.secrets.zsh =\n%q\nwant\n%q", got, want)
	}
	if info, _ := os.Stat(envFile(home)); info.Mode().Perm() != 0o600 {
		t.Errorf("~/.secrets.zsh is %v, want 600", info.Mode().Perm())
	}
	if calls := fake.Calls(); len(calls) > 0 {
		t.Errorf("ran %q: putting a value in hand asks no store", calls)
	}
}

// A variable new to ~/.secrets.zsh goes where a sync would write it: the
// next sync writes the same bytes, and finds nothing changed.
func TestPutInsertsAVariableWhereASyncWould(t *testing.T) {
	lines := [][2]string{{"FIRST", "Section/first"}, {"MIDDLE", "Section/middle   # a note"}, {"LAST", "Section/last"}}
	values := map[string]string{item + "/Section/first": "1", item + "/Section/middle": "2", item + "/Section/last": "3"}
	given := map[string]string{"FIRST": "1", "MIDDLE": "2", "LAST": "3"}
	for i, added := range lines {
		t.Run(added[0], func(t *testing.T) {
			home := t.TempDir()
			fake := runnertest.New(t)
			s := secret.New(fake, home)
			if err := os.WriteFile(envFile(home), []byte("export STRAY='no line declares it'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			fake.On("op", "whoami")
			optest.Holds(fake, values)
			var before [][2]string
			before = append(append(before, lines[:i]...), lines[i+1:]...)
			declare(t, s, before...)
			if _, err := s.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
			declare(t, s, lines...)
			calls := len(fake.Calls())
			if err := s.Put(added[0], given[added[0]]); err != nil {
				t.Fatal(err)
			}
			if ran := fake.Calls()[calls:]; len(ran) > 0 {
				t.Errorf("Put() ran %q: it asks no store", ran)
			}
			put := readEnv(t, home)
			report, err := s.Sync(t.Context())
			if err != nil || report.Changed != 0 || report.Synced != len(lines) {
				t.Errorf("the next sync = %+v, %v; want all synced, nothing changed", report, err)
			}
			if synced := readEnv(t, home); synced != put {
				t.Errorf("Put() wrote\n%s\nthe next sync\n%s\nwant the same", put, synced)
			}
		})
	}
}

// With no ~/.secrets.zsh, a variable put in place makes it, with the header
// a sync writes and that one line: the rest come with the next sync.
func TestPutMakesTheFileWithItsHeader(t *testing.T) {
	home := t.TempDir()
	s := secret.New(runnertest.New(t), home)
	declare(t, s, [2]string{"A", "Section/a"}, [2]string{"B", "Section/b"})
	if err := s.Put("B", "b"); err != nil {
		t.Fatal(err)
	}
	want := "# Written by kit from 1Password: kit-config's secrets sections. kit apply\n# and kit secret sync rewrite it whole; change a secret in its store, or with kit.\nexport B='b'\n"
	if got := readEnv(t, home); got != want {
		t.Errorf("~/.secrets.zsh =\n%q\nwant\n%q", got, want)
	}
	if info, _ := os.Stat(envFile(home)); info.Mode().Perm() != 0o600 {
		t.Errorf("~/.secrets.zsh is %v, want 600", info.Mode().Perm())
	}
}

// A file put in place is written alone, byte for byte, with its mode:
// ~/.secrets.zsh isn't touched.
func TestPutWritesAFileAlone(t *testing.T) {
	home := t.TempDir()
	s := secret.New(runnertest.New(t), home)
	declare(t, s, [2]string{"A", "Section/a"}, [2]string{"~/.config/tool/key", "key --mode 640"})
	if err := s.Put("~/.config/tool/key", "line one\nline two\n\n"); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(home, ".config", "tool", "key")
	if got, _ := os.ReadFile(key); string(got) != "line one\nline two\n\n" {
		t.Errorf("the file holds %q", got)
	}
	if info, _ := os.Stat(key); info.Mode().Perm() != 0o640 {
		t.Errorf("the file is %v, want 640", info.Mode().Perm())
	}
	if _, err := os.Stat(envFile(home)); !os.IsNotExist(err) {
		t.Errorf("~/.secrets.zsh: %v, want it never written", err)
	}
}

// A variable's value of many lines is refused, as a sync refuses it, the
// file untouched; the newlines a value ends with, and a file's lines, are
// fine.
func TestPutRefusesAVariableOfManyLines(t *testing.T) {
	home := t.TempDir()
	s := secret.New(runnertest.New(t), home)
	declare(t, s, [2]string{"A", "Section/a"})
	if err := s.Put("A", "line one\nline two"); err == nil || err.Error() != "a variable is one line: declare it as a file" {
		t.Errorf("Put() = %v, want it refused", err)
	}
	if _, err := os.Stat(envFile(home)); !os.IsNotExist(err) {
		t.Errorf("~/.secrets.zsh: %v, want it never written", err)
	}
	for _, c := range []struct{ name, value string }{{"A", "one line\n\n"}, {"~/.f", "line one\nline two"}} {
		if err := secret.OneLine(c.name, c.value); err != nil {
			t.Errorf("OneLine(%s, %q) = %v, want nil", c.name, c.value, err)
		}
	}
}

// Put puts a declared secret alone in place.
func TestPutRefusesWhatsNotDeclared(t *testing.T) {
	s := secret.New(runnertest.New(t), t.TempDir())
	declare(t, s, [2]string{"A", "Section/a"})
	if err := s.Put("B", "b"); err == nil || err.Error() != "B isn't declared" {
		t.Errorf("Put() = %v, want it refused", err)
	}
}
