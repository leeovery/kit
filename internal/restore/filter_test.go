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
		"~/.claude/skills/review":          "kit puts ~/.claude/skills/review in place",
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

// The filter as written is the matcher's own rules, none lost or changed
// in the writing: walking a tree with the rules read back from the file
// finds the files the judgement counts as the folder's own. That rsync reads
// the file so is TestMatcherAgreesWithRsync's.
func TestWrittenFilterIsTheMatchersRules(t *testing.T) {
	home := t.TempDir()
	files := []string{
		"CLAUDE.md", "odd[1].md", "oddx.md", "history.jsonl", ".DS_Store",
		"projects/site/session.jsonl", "projects/site/memory/notes.md", "projects/site/.DS_Store", "projects/memory",
		"teams/a/inbox/m.json", "teams/a/config.json",
		"skills/kit/SKILL.md", "skills/kit/notes.md", "skills/review/SKILL.md", "skills/mine/SKILL.md",
		"statsig/x", "cache/a/b", "plugins/one/x", "plugins/list.json",
		"logs/a/b.log", "logs/b.log", "logs/a/b.txt", "downloads/x", "settings.json", "settings.json.bak",
	}
	for _, f := range files {
		write(t, filepath.Join(home, ".claude", f), later)
	}
	f, err := NewFilter(claude, owned)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "filter")
	if err := f.Write(file); err != nil {
		t.Fatal(err)
	}
	readBack := walkWithRules(t, filepath.Join(home, ".claude"), file)
	counted := counts(t, f, home, "~/.claude/")
	want := []string{"history.jsonl", "logs/a/b.txt", "logs/b.log", "oddx.md", "projects/memory", "projects/site/session.jsonl", "settings.json.bak", "skills/kit/notes.md", "skills/mine/SKILL.md", "teams/a/config.json"}
	if !slices.Equal(readBack, want) || !slices.Equal(counted, want) {
		t.Errorf("the rules read back leave %q\nthe judgement counts %q\nwant both %q", readBack, counted, want)
	}
}

// walkWithRules are the files under root the rules in the filter file
// leave in, a folder one leaves out never looked in, as rsync does.
func walkWithRules(t *testing.T, root, filter string) []string {
	t.Helper()
	var rules []rule
	for line := range strings.Lines(read(t, filter)) {
		text, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "- ")
		if !ok {
			t.Fatalf("%q isn't an exclude rule", line)
		}
		rules = append(rules, newRule(text))
	}
	var in []string
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
			in = append(in, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// counts are the files the judgement counts as f's line's path's own, in
// home, each from the path (prefix): all of them, each written in set-up's
// window.
func counts(t *testing.T, f *Filter, home, prefix string) []string {
	t.Helper()
	written, older, err := f.holds(home, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || older {
		t.Fatalf("holds() = %v, older %v", err, older)
	}
	out := make([]string, len(written))
	for i, w := range written {
		out[i] = strings.TrimPrefix(w, prefix)
	}
	return out
}

// The tree the real rsync ran on, by hand, with filter-fixture.rules: its
// files, and links to folders by their targets, under ~/Some App.
var (
	fixtureFiles = []string{
		"notes [x].md", "notes x.md", ".hidden", ".DS_Store", "sub/.localized", "sub/y.bak", "x.bak", ".config/settings.json",
		"projects/site one/memory/a.md", "projects/site one/session.jsonl", "projects/.dot/memory/b.md", "projects/memory",
		"skills/review/SKILL.md", "skills/mine/SKILL.md", "Cache Data/blob", "logs/a/b.log", "logs/b.log", "logs/a/b.txt",
		"tmp/x", "tmpfile", "cache/123abc", "cache/abc", "deep/er/most/x", "deep/er/other", "media",
	}
	fixtureLinks = map[string]string{"shortcut": "projects", "linkdir": "deep"}
	fixtureLine  = Line{Path: "~/Some App", Skips: []string{"logs/**/*.log", "tmp/***", "cache/[[:digit:]]*", "deep/er/most", "media/", "shortcut/", "*.bak"}, Pos: "laptop/declarations:6"}
	fixtureOwned = Owned{
		Linked:    []string{"~/Some App/notes [x].md", "~/.zshrc"},
		Prefs:     []string{"~/Some App/projects/*/memory"},
		Installed: []string{"~/Some App/skills/review"},
		Excluded:  []string{"~/Some App/Cache Data"},
		Own:       []string{"~/.config/kit"},
	}
)

// The matcher agrees with the real rsync: the filter kit writes for the
// fixture is the one rsync 3.5.2 ran with, and the files rsync copied with
// it (rsync -a -n -i, its output in testdata) are those the judgement counts
// as the path's own. The fixture holds names with spaces, dot files and
// folders, a linked file's [x] escaped, prefs' */memory matching a dot
// folder, a skip of a nested folder, **, ***, a named class, a skip of a
// folder that's a file here, and links to folders, which a folder's rule
// never matches.
func TestMatcherAgreesWithRsync(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Some App")
	for _, f := range fixtureFiles {
		write(t, filepath.Join(root, f), later)
	}
	for link, target := range fixtureLinks {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	f, err := NewFilter(fixtureLine, fixtureOwned)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "filter")
	if err := f.Write(file); err != nil {
		t.Fatal(err)
	}
	if got, want := read(t, file), read(t, filepath.Join("testdata", "filter-fixture.rules")); got != want {
		t.Fatalf("the filter =\n%s\nrsync ran with\n%s", got, want)
	}
	copied := rsyncCopied(t, filepath.Join("testdata", "filter-fixture.rsync-3.5.2.txt"))
	counted := counts(t, f, home, "~/Some App/")
	slices.Sort(copied)
	slices.Sort(counted)
	if !slices.Equal(counted, copied) {
		t.Errorf("the judgement counts %q\nrsync copied %q", counted, copied)
	}
}

// rsyncCopied are the files and links rsync's itemized output says it
// copied: its folders, and lines that aren't an item's, left out.
func rsyncCopied(t *testing.T, path string) []string {
	t.Helper()
	var copied []string
	for line := range strings.Lines(read(t, path)) {
		code, name, _ := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		switch {
		case len(code) != 11:
		case code[1] == 'f':
			copied = append(copied, name)
		case code[1] == 'L':
			name, _, _ = strings.Cut(name, " -> ")
			copied = append(copied, name)
		}
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

// A rule matches as rsync's rule_matches has it: one starting / the whole
// path from the transfer's root, one without a / the path's last part; one
// ending / folders alone; one ending /*** a folder and all in it; one
// without *, ? or [ as it is, its \ and all.
func TestRuleMatches(t *testing.T) {
	for _, tt := range []struct {
		rule, name string
		dir, want  bool
	}{
		{"/cache", "cache", true, true},
		{"/cache", "cache", false, true},
		{"/cache", "a/cache", true, false},
		{"/cache/", "cache", true, true},
		{"/cache/", "cache", false, false},
		{"/projects/*/memory", "projects/site/memory", true, true},
		{"/projects/*/memory", "projects/a/b/memory", true, false},
		{"/logs/***", "logs", true, true},
		{"/logs/***", "logs", false, false},
		{"/logs/***", "logs/a/b", false, true},
		{".DS_Store", ".DS_Store", false, true},
		{".DS_Store", "a/b/.DS_Store", false, true},
		{".DS_Store", "a/.DS_Store.old", false, false},
		{`/a\*b`, "a*b", false, true},
		{`/a\*b`, "axb", false, false},
		{`/a\b`, `a\b`, false, true},
	} {
		if got := newRule(tt.rule).matches(tt.name, tt.dir); got != tt.want {
			t.Errorf("rule %q matches %q (folder %v) = %v; want %v", tt.rule, tt.name, tt.dir, got, tt.want)
		}
	}
}
