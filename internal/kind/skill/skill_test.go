package skill_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/skill"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const record = `{
  "referodesign/refero_skill/refero-design": {"ref": "v1.0.3", "type": "skill", "constraint": "^1.0.2"},
  "github/gh-stack/gh-stack": {"ref": "v0.2.1", "type": "skill", "constraint": "^0.2.1"}
}`

// home is a home whose agntc record is rec, none for "".
func home(t *testing.T, rec string) string {
	t.Helper()
	dir := t.TempDir()
	if rec != "" {
		if err := os.MkdirAll(filepath.Join(dir, ".agntc"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".agntc", "manifest.json"), []byte(rec), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func declared(names ...string) config.List {
	l := config.List{Kind: "claude-skill"}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n, File: "claude skills"})
	}
	return l
}

// What's installed is agntc's record, read without asking the network; a
// Mac agntc has installed nothing on has none.
func TestInstalled(t *testing.T) {
	got, err := skill.New(runnertest.New(t), home(t, record)).Installed(t.Context())
	want := []kind.Installed{{Name: "github/gh-stack/gh-stack", Explicit: true}, {Name: "referodesign/refero_skill/refero-design", Explicit: true}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v; want %+v", got, err, want)
	}
	if got, err := skill.New(runnertest.New(t), home(t, "")).Installed(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("with no record, Installed() = %+v, %v; want none", got, err)
	}
	if _, err := skill.New(runnertest.New(t), home(t, "{")).Installed(t.Context()); err == nil {
		t.Error("with a record that doesn't read, Installed() = nil error")
	}
}

// A skill declared with a version matches on its key.
func TestCompare(t *testing.T) {
	got := kind.Compare(t.Context(), skill.New(runnertest.New(t), home(t, record)), declared("github/gh-stack/gh-stack@^0.2", "pbakaus/impeccable"))
	want := []check.Item{
		{ID: "claude-skill:pbakaus/impeccable", Name: "pbakaus/impeccable", State: kind.Missing, Action: kind.Install},
		{ID: "claude-skill:referodesign/refero_skill/refero-design", Name: "referodesign/refero_skill/refero-design", State: kind.Extra},
	}
	if got.Summary != "2 declared, 1 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

// A repo alone installs everything in it; a name after it, the skill of that
// name, its version kept; a longer path, the pack there. Each runs from home.
func TestInstall(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("agntc", "add", "-g", "pbakaus/impeccable", "--all", "--json").Prints(`{"ok": true}`)
	fake.On("agntc", "add", "-g", "github/gh-stack@^0.2", "--skill", "gh-stack", "--json").Prints(`{"ok": true}`)
	fake.On("agntc", "add", "-g", "acme/skills/packs/go", "--pack", "--json").Prints(`{"ok": true}`)
	dir := home(t, "")
	if err := skill.New(fake, dir).Install(t.Context(), []string{"pbakaus/impeccable", "github/gh-stack/gh-stack@^0.2", "acme/skills/packs/go"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range fake.Commands() {
		if c.Dir != dir {
			t.Errorf("ran %s in %q, want the home, never a project", c, c.Dir)
		}
	}
}

// A name after the repo that's no skill there is a pack at that path.
func TestInstallAPack(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("agntc", "add", "-g", "leeovery/agentic-skills@^0.2", "--skill", "laravel", "--json").
		Prints(`{"ok": false, "error": {"message": "leeovery/agentic-skills: no skill named \"laravel\""}}`).Exits(1)
	fake.On("agntc", "add", "-g", "leeovery/agentic-skills/laravel@^0.2", "--pack", "--json").Prints(`{"ok": true}`)
	if err := skill.New(fake, home(t, "")).Install(t.Context(), []string{"leeovery/agentic-skills/laravel@^0.2"}); err != nil {
		t.Fatal(err)
	}
}

// What went wrong is agntc's own words, from the document it prints; a name
// that's no repo isn't tried.
func TestInstallFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("agntc", "add", "-g", "github/gh-stack", "--skill", "gh-stack", "--json").
		Prints(`{"ok": false, "error": {"message": "gh-stack: files in the way: .claude/skills/gh-stack/", "flags": ["--overwrite"]}}`).Exits(1)
	err := skill.New(fake, home(t, "")).Install(t.Context(), []string{"github/gh-stack/gh-stack", "gh-stack"})
	want := "gh-stack: files in the way: .claude/skills/gh-stack/\ngh-stack isn't a GitHub repo, as owner/repo, and what in it"
	if err == nil || engine.Reason(err) != want {
		t.Errorf("Install() = %v; want %q", err, want)
	}
}

func TestRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("agntc", "remove", "-g", "github/gh-stack/gh-stack", "-y", "--json").Prints(`{"ok": true}`)
	if err := skill.New(fake, home(t, "")).Remove(t.Context(), []string{"github/gh-stack/gh-stack@^0.2"}); err != nil {
		t.Fatal(err)
	}
}
