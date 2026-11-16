package gitrepo_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const dir = "/Users/someone/.config/kit"

// base is the commit HEAD names in the tests' repositories.
const base = "0e0bf17a399109574ffb00e33300280a33f81ab7"

func git(args ...string) []string { return append([]string{"-C", dir}, args...) }

// call is a command kit ran: its arguments, and what it read.
type call struct {
	args  []string
	input string
}

// checkCalls checks fake ran want, in order, each reading what it says and
// in the C locale alone, as kit reads what git says: git's own index, never
// one of kit's.
func checkCalls(t *testing.T, name string, fake *runnertest.Fake, want []call) {
	t.Helper()
	var ran []call
	for _, cmd := range fake.Commands() {
		if !slices.Equal(cmd.Env, []string{"LC_ALL=C"}) {
			t.Errorf("%s: %s ran with %q, want the C locale alone", name, cmd, cmd.Env)
		}
		ran = append(ran, call{args: cmd.Args, input: cmd.Input})
	}
	if !slices.EqualFunc(ran, want, func(a, b call) bool { return slices.Equal(a.args, b.args) && a.input == b.input }) {
		t.Errorf("%s: ran\n%s\nwant\n%v", name, strings.Join(fake.Calls(), "\n"), want)
	}
}

// sequence is the commands a test expects git to run, in order, as it
// scripts them.
type sequence struct {
	fake *runnertest.Fake
	// root is the repository's folder, when it isn't dir's.
	root    string
	want    []call
	scripts map[string]*runnertest.Script
}

// expect scripts the command git runs with args, expected next: a command
// expected before answers again, as scripted now.
func (s *sequence) expect(args ...string) *runnertest.Script {
	if s.root != "" {
		args = slices.Concat([]string{"-C", s.root}, args[2:])
	}
	s.want = append(s.want, call{args: args})
	key := strings.Join(args, "\x00")
	if script, ok := s.scripts[key]; ok {
		return script.Then()
	}
	if s.scripts == nil {
		s.scripts = map[string]*runnertest.Script{}
	}
	s.scripts[key] = s.fake.On("git", args...)
	return s.scripts[key]
}

// check checks git ran what s expects, in order, and nothing else.
func (s *sequence) check(t *testing.T, name string) {
	t.Helper()
	checkCalls(t, name, s.fake, s.want)
}

// Ignored names each path a rule ignores, with the rule, as check-ignore
// prints it: from the repository's .gitignore or the user's global one;
// a rule that keeps a path (!) isn't one ignoring it; none matching exits 1.
func TestIgnored(t *testing.T) {
	paths := []string{"laptop/home/.vscode/settings.json", "laptop/home/keep.jar", "laptop/home/tool.jar", "laptop/home/.zshrc"}
	fake := runnertest.New(t)
	fake.On("git", git("check-ignore", "--verbose", "--stdin", "-z")...).
		Prints(".gitignore\x001\x00.vscode\x00laptop/home/.vscode/settings.json\x00.gitignore\x002\x00!keep.jar\x00laptop/home/keep.jar\x00/Users/someone/.gitignore_global\x001\x00*.jar\x00laptop/home/tool.jar\x00")
	got, err := gitrepo.Repo{Dir: dir, Run: fake}.Ignored(context.Background(), paths...)
	want := map[string]gitrepo.Rule{
		"laptop/home/.vscode/settings.json": {Pattern: ".vscode", Source: ".gitignore", Line: 1},
		"laptop/home/tool.jar":              {Pattern: "*.jar", Source: "/Users/someone/.gitignore_global", Line: 1},
	}
	if err != nil || !maps.Equal(got, want) {
		t.Errorf("Ignored() = %v, %v; want %v", got, err, want)
	}
	if in := fake.Commands()[0].Input; in != strings.Join(paths, "\x00")+"\x00" {
		t.Errorf("check-ignore read %q", in)
	}

	fake = runnertest.New(t)
	fake.On("git", git("check-ignore", "--verbose", "--stdin", "-z")...).Exits(1)
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).Ignored(context.Background(), "laptop/home/.zshrc"); err != nil || len(got) != 0 {
		t.Errorf("Ignored() of a path no rule matches = %v, %v", got, err)
	}
	fake = runnertest.New(t)
	fake.On("git", git("check-ignore", "--verbose", "--stdin", "-z")...).Exits(128).PrintsToStderr("fatal: not a git repository")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Ignored(context.Background(), "laptop/home/.zshrc"); err == nil {
		t.Error("Ignored() with git failing: no error")
	}
}

// A setting is set when git config --get prints a value for it: unset, as
// it exits 1, or unreadable, it isn't.
func TestIsSet(t *testing.T) {
	for _, tt := range []struct {
		name   string
		prints string
		exits  int
		want   bool
	}{
		{name: "set", prints: "Someone\n", want: true},
		{name: "unset", exits: 1},
		{name: "unreadable", exits: 128},
	} {
		fake := runnertest.New(t)
		fake.On("git", git("config", "--get", "user.name")...).Prints(tt.prints).Exits(tt.exits)
		if got := (gitrepo.Repo{Dir: dir, Run: fake}).IsSet(context.Background(), "user.name"); got != tt.want {
			t.Errorf("IsSet(user.name), %s = %v, want %v", tt.name, got, tt.want)
		}
	}
}
