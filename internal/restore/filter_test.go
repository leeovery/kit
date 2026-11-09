package restore

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// owned is what's kit's in a test's home folder: a mix of each owner, in
// ~/.claude and beside it.
var owned = Owned{
	Linked:    []string{"~/.claude/CLAUDE.md", "~/.claude/skills/kit/SKILL.md", "~/.claude/odd[1].md", "~/.zshrc"},
	Prefs:     []string{"~/.claude/settings.json", "~/.claude/projects/*/memory", "~/.claude/teams/**/inbox", "~/Library/Application Support/Tool/*20[0-9][0-9].[0-9]/options"},
	Installed: []string{"~/.claude/skills/review"},
	Excluded:  []string{"~/.cache", "~/.claude/statsig/", "~/Library/Application Support/*/GPUCache"},
	Own:       []string{"~/.config/kit", "~/.local/state/kit"},
}

// claude is a line restoring ~/.claude, less what its skips name.
var claude = Line{Path: "~/.claude", Skips: []string{"cache", "plugins/*", "logs/**/*.log", "downloads/"}, Pos: "laptop/declarations:4"}

// A line's filter is a rule for each of kit's paths in its path, anchored at
// the path itself, as rsync copies a folder's contents into its place (/cache
// for ~/.claude/cache): a path as it is, its wildcards escaped; a glob as its
// readers take one, * never past a /. Then its skips, as written, anchored
// too; then Finder's files, wherever they are. What's elsewhere has no rule,
// so a listed file's filter has nothing of kit's.
func TestFilterWritesRsyncsRules(t *testing.T) {
	for _, tt := range []struct {
		line Line
		want string
	}{
		{claude, "- /CLAUDE.md\n- /skills/kit/SKILL.md\n- /odd\\[1].md\n- /settings.json\n- /projects/*/memory\n- /teams/*/inbox\n- /skills/review\n- /statsig\n" +
			"- /cache\n- /plugins/*\n- /logs/**/*.log\n- /downloads/\n- .DS_Store\n- .localized\n"},
		{Line{Path: "~/Library/Application Support/Tool"}, "- /*20[0-9][0-9].[0-9]/options\n- /GPUCache\n- .DS_Store\n- .localized\n"},
		{Line{Path: "~/Library/Application Support/Other"}, "- /GPUCache\n- .DS_Store\n- .localized\n"},
		{Line{Path: "~/.claude/projects"}, "- /*/memory\n- .DS_Store\n- .localized\n"},
		{Line{Path: "~/.local"}, "- /state/kit\n- .DS_Store\n- .localized\n"},
		{Line{Path: "~/.zsh_history"}, "- .DS_Store\n- .localized\n"},
	} {
		f, err := NewFilter(tt.line, owned)
		if err != nil {
			t.Fatalf("NewFilter(%s) = %v", tt.line.Path, err)
		}
		file := filepath.Join(t.TempDir(), "filter")
		if err := f.Write(file); err != nil {
			t.Fatal(err)
		}
		if got := read(t, file); got != tt.want {
			t.Errorf("%s's filter =\n%s\nwant\n%s", tt.line.Path, got, tt.want)
		}
	}
}

// A line whose path is kit's, or in one of kit's, is refused, naming the
// line and the owner: the restore never carries it.
func TestFilterRefusesKitsOwnPaths(t *testing.T) {
	for path, want := range map[string]string{
		"~/.zshrc":                         "kit links ~/.zshrc",
		"~/.claude/settings.json":          "prefs saves ~/.claude/settings.json",
		"~/.claude/projects/site/memory/a": "prefs saves ~/.claude/projects/*/memory",
		"~/.claude/skills/review":          "kit installs ~/.claude/skills/review",
		"~/.cache/go":                      "the backups leave out ~/.cache",
		"~/.config/kit":                    "kit keeps its own files in ~/.config/kit",
	} {
		_, err := NewFilter(Line{Path: path, Pos: "laptop/declarations:9"}, owned)
		if err == nil || err.Error() != "laptop/declarations:9: the restore never carries "+path+": "+want {
			t.Errorf("NewFilter(%s) = %v; want it refused, as %s", path, err, want)
		}
	}
}

// What a filter leaves out is what a rule matches, or anything in a folder
// one matches.
func TestFilterExcludes(t *testing.T) {
	f, err := NewFilter(claude, owned)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		rel       string
		dir, want bool
	}{
		{"CLAUDE.md", false, true},
		{"odd[1].md", false, true},
		{"oddx.md", false, false},
		{"projects/site/memory", true, true},
		{"projects/site/memory/notes.md", false, true},
		{"projects/site/session.jsonl", false, false},
		{"teams/a/inbox/m.json", false, true},
		{"skills/kit/SKILL.md", false, true},
		{"skills/kit/notes.md", false, false},
		{"skills/review/SKILL.md", false, true},
		{"statsig/x", false, true},
		{"cache/x/y", false, true},
		{"plugins", true, false},
		{"plugins/one/x", false, true},
		{"logs/a/b.log", false, true},
		{"logs/b.log", false, false},
		{"downloads/x", false, true},
		{"downloads", false, false},
		{"history.jsonl", false, false},
		{"projects/site/.DS_Store", false, true},
		{".localized", false, true},
	} {
		if got := f.Excludes(tt.rel, tt.dir); got != tt.want {
			t.Errorf("Excludes(%s, folder %v) = %v; want %v", tt.rel, tt.dir, got, tt.want)
		}
	}
}

// What the copy carries, rsync reading the filter as written, anchored at
// the listed folder its transfer is rooted at, is what the judgement counts
// as the folder's own, the matcher reading the same rules from the home
// folder: a tree walked both ways finds the same files.
func TestFilterAndMatcherAgree(t *testing.T) {
	home := t.TempDir()
	files := []string{
		"CLAUDE.md", "odd[1].md", "oddx.md", "history.jsonl", ".DS_Store",
		"projects/site/session.jsonl", "projects/site/memory/notes.md", "projects/site/.DS_Store", "projects/memory",
		"teams/a/inbox/m.json", "teams/a/config.json",
		"skills/kit/SKILL.md", "skills/kit/notes.md", "skills/review/SKILL.md", "skills/mine/SKILL.md",
		"statsig/x", "cache/a/b", "plugins/one/x", "plugins/list.json",
		"logs/a/b.log", "logs/b.log", "logs/a/b.txt", "downloads/x", "settings.json", "settings.json.bak",
	}
	began := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	for _, f := range files {
		write(t, filepath.Join(home, ".claude", f), began.Add(time.Hour))
	}
	f, err := NewFilter(claude, owned)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "filter")
	if err := f.Write(file); err != nil {
		t.Fatal(err)
	}
	copied := rsyncCopies(t, filepath.Join(home, ".claude"), file)
	written, older, err := f.holds(home, began)
	if err != nil || older {
		t.Fatalf("holds() = %v, older %v", err, older)
	}
	counted := make([]string, len(written))
	for i, w := range written {
		counted[i] = strings.TrimPrefix(w, "~/.claude/")
	}
	want := []string{"history.jsonl", "logs/a/b.txt", "logs/b.log", "oddx.md", "projects/memory", "projects/site/session.jsonl", "settings.json.bak", "skills/kit/notes.md", "skills/mine/SKILL.md", "teams/a/config.json"}
	if !slices.Equal(copied, want) || !slices.Equal(counted, want) {
		t.Errorf("rsync would copy %q\nthe judgement counts %q\nwant both %q", copied, counted, want)
	}
}

// rsyncCopies are the files under root rsync copies with the rules in the
// filter file as written, read as rsync reads them: a folder a rule leaves
// out is never looked in.
func rsyncCopies(t *testing.T, root, filter string) []string {
	t.Helper()
	var rules []rule
	for line := range strings.Lines(read(t, filter)) {
		text, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "- ")
		if !ok {
			t.Fatalf("%q isn't an exclude rule", line)
		}
		rules = append(rules, newRule(text))
	}
	var copied []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+"/"))
		if slices.ContainsFunc(rules, func(r rule) bool { return r.matches(rel, d.IsDir()) }) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			copied = append(copied, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return copied
}

// write writes a file at path, its folders made, last written at when.
func write(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
