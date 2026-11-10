package restore

import (
	"reflect"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

// entry is a [restore] line of the laptop's declarations, at line n.
func entry(n int, path, options string) config.Entry {
	return config.Entry{Name: path, Value: options, Scope: "laptop", Section: "restore", Line: n}
}

// A line is its path, from ~ and cleaned, and what in it to skip, each as
// written, globs and all, as rsync takes them.
func TestParseLine(t *testing.T) {
	for _, tt := range []struct {
		path, options string
		want          Line
	}{
		{"~/Code", "", Line{Path: "~/Code"}},
		{"~/Library/Application Support/Some App/", "", Line{Path: "~/Library/Application Support/Some App"}},
		{"~/.zsh_history", "", Line{Path: "~/.zsh_history"}},
		{"~/.claude", "--skip cache --skip 'plugins/*' --skip \"logs/**/*.log\" --skip downloads/", Line{Path: "~/.claude", Skips: []string{"cache", "plugins/*", "logs/**/*.log", "downloads/"}}},
		{"~/a/./b//c", "", Line{Path: "~/a/b/c"}},
	} {
		got, err := ParseLine(entry(3, tt.path, tt.options))
		tt.want.Pos = "laptop/declarations:3"
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseLine(%s %s) = %+v, %v; want %+v", tt.path, tt.options, got, err, tt.want)
		}
	}
}

// A line that doesn't read is refused, naming it: a path not in the home
// folder; a --skip absolute, leading out of its path, or naming nothing in
// it; an option kit doesn't know, or a --skip with nothing after it.
func TestParseLineRefuses(t *testing.T) {
	for _, tt := range []struct{ path, options, want string }{
		{"/Users/someone/Code", "", "/Users/someone/Code isn't in the home folder"},
		{"Code", "", "Code isn't in the home folder"},
		{"~", "", "~ isn't in the home folder"},
		{"~/", "", "~/ isn't in the home folder"},
		{"~/../other", "", "~/../other isn't in the home folder"},
		{"~/Code/../..", "", "~/Code/../.. isn't in the home folder"},
		{"~/Code", "--skip /tmp", "--skip /tmp: what to leave out is relative to the line's path, never absolute"},
		{"~/Code", "--skip ../other", "--skip ../other: what to leave out is in the line's path, and .. leads out of it"},
		{"~/Code", "--skip a/../../b", "--skip a/../../b: what to leave out is in the line's path, and .. leads out of it"},
		{"~/Code", "--skip .", `--skip ".": what to leave out is a path in the line's`},
		{"~/Code", "--skip ''", `--skip "": what to leave out is a path in the line's`},
		{"~/Code", "--skip a//b", `--skip "a//b": what to leave out is a path in the line's`},
		{"~/Code", "--exclude vendor", `"--exclude" isn't one of a restore line's options: --skip`},
		{"~/Code", "--skip=vendor", `"--skip=vendor" isn't one of a restore line's options: --skip`},
		{"~/Code", "--skip", "--skip needs what to leave out after it"},
		{"~/Code", "--skip --skip a", "--skip needs what to leave out after it"},
		{"~/Code", "--skip 'vendor", "a quote isn't closed"},
	} {
		_, err := ParseLine(entry(7, tt.path, tt.options))
		if err == nil || !strings.HasPrefix(err.Error(), "laptop/declarations:7: ") || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("ParseLine(%s %s) = %v; want laptop/declarations:7 saying %q", tt.path, tt.options, err, tt.want)
		}
	}
}

// A list's lines read in order; a path is restored by one line, so one
// that's another's, or in another's, case aside, is refused, naming both.
func TestLines(t *testing.T) {
	list := config.List{Entries: []config.Entry{entry(2, "~/Code", ""), entry(3, "~/.claude", "--skip cache"), entry(4, "~/.claude-other", "")}}
	got, err := Lines(list)
	want := []Line{
		{Path: "~/Code", Pos: "laptop/declarations:2"},
		{Path: "~/.claude", Skips: []string{"cache"}, Pos: "laptop/declarations:3"},
		{Path: "~/.claude-other", Pos: "laptop/declarations:4"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Lines() = %+v, %v; want %+v", got, err, want)
	}
	for _, tt := range []struct {
		entries []config.Entry
		want    string
	}{
		{[]config.Entry{entry(2, "~/.claude", ""), entry(5, "~/.claude/projects", "")}, "laptop/declarations:5: ~/.claude/projects and ~/.claude (laptop/declarations:2) overlap"},
		{[]config.Entry{entry(2, "~/.claude/projects", ""), entry(5, "~/.claude", "")}, "laptop/declarations:5: ~/.claude and ~/.claude/projects (laptop/declarations:2) overlap"},
		{[]config.Entry{entry(2, "~/Code", ""), entry(5, "~/Code/", "")}, "laptop/declarations:5: ~/Code and ~/Code (laptop/declarations:2) overlap"},
		{[]config.Entry{entry(2, "~/Code", ""), entry(5, "~/code", "")}, "laptop/declarations:5: ~/code and ~/Code (laptop/declarations:2) overlap"},
		{[]config.Entry{entry(2, "~/.Claude", ""), entry(5, "~/.claude/projects", "")}, "laptop/declarations:5: ~/.claude/projects and ~/.Claude (laptop/declarations:2) overlap"},
		{[]config.Entry{entry(2, "~/Code", ""), entry(5, "~/Docs", "--skip /x")}, "laptop/declarations:5: --skip /x"},
	} {
		if _, err := Lines(config.List{Entries: tt.entries}); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Lines() = %v; want %q", err, tt.want)
		}
	}
}
