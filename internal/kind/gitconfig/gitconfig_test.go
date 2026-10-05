package gitconfig_test

import (
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/gitconfig"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// list is what git config --global --list -z prints for settings, given as
// key, value pairs.
func list(pairs ...string) string {
	var b strings.Builder
	for i := 0; i < len(pairs); i += 2 {
		b.WriteString(pairs[i] + "\n" + pairs[i+1] + "\x00")
	}
	return b.String()
}

func declared(entries ...config.Entry) config.List {
	for i := range entries {
		entries[i].Scope, entries[i].Line = "shared", i+2
	}
	return config.List{Kind: "git", Entries: entries}
}

func TestCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", "config", "--global", "--list", "-z").Prints(list(
		"user.name", "Someone",
		"commit.gpgsign", "true",
		"core.editor", "vim",
		"credential.https://example.com.helper", "",
		"credential.https://example.com.helper", "!gh auth git-credential",
		"delta.pager", "less",
	))
	g := gitconfig.New(fake)
	l, err := g.Values(declared(
		config.Entry{Name: "user.name", Value: `"Someone"`},
		config.Entry{Name: "commit.gpgSign", Value: "true"},
		config.Entry{Name: "core.editor", Value: "micro"},
		config.Entry{Name: "credential.https://example.com.helper", Value: `"!gh auth git-credential"`},
		config.Entry{Name: "init.defaultBranch", Value: "main"},
	))
	if err != nil {
		t.Fatal(err)
	}
	res := kind.Compare(t.Context(), g, l)
	var got []string
	for _, it := range res.Items {
		got = append(got, it.Name+" "+it.State+" "+it.Detail+" "+it.Action)
	}
	want := []string{
		"init.defaultBranch missing  install",
		"core.editor diverged set to vim ",
		"credential.https://example.com.helper diverged set 2 times ",
		"delta.pager extra  ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") || res.State != check.Attention {
		t.Errorf("items =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestValuesAreOneWord(t *testing.T) {
	g := gitconfig.New(runnertest.New(t))
	if _, err := g.Values(declared(config.Entry{Name: "user.name", Value: "Some One"})); err == nil || !strings.Contains(err.Error(), "shared/declarations:2: user.name: one value after the key, in quotes when it holds spaces") {
		t.Errorf("Values() error = %v", err)
	}
}

func TestInstallValueAndRemove(t *testing.T) {
	fake := runnertest.New(t)
	g := gitconfig.New(fake)
	if _, err := g.Values(declared(config.Entry{Name: "alias.st", Value: `"status -sb"`})); err != nil {
		t.Fatal(err)
	}
	fake.On("git", "config", "--global", "--replace-all", "alias.st", "status -sb")
	if err := g.Install(t.Context(), []string{"alias.st"}); err != nil {
		t.Errorf("Install() = %v", err)
	}
	fake.On("git", "config", "--global", "--list", "-z").Prints(list("alias.st", "status --short"))
	if v, err := g.Value(t.Context(), "alias.st"); err != nil || v != `"status --short"` {
		t.Errorf("Value() = %q, %v", v, err)
	}
	fake.On("git", "config", "--global", "--unset-all", "delta.pager").Exits(5)
	if err := g.Remove(t.Context(), []string{"delta.pager"}); err != nil {
		t.Errorf("Remove() of a setting not set = %v, want no error", err)
	}
}

func TestNoGlobalConfigYet(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", "config", "--global", "--list", "-z").Exits(1)
	installed, err := gitconfig.New(fake).Installed(t.Context())
	if err != nil || len(installed) != 0 {
		t.Errorf("Installed() = %v, %v; want nothing set", installed, err)
	}
}
