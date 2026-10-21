package gitrepo_test

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const dir = "/Users/someone/.config/kit"

// base is the commit HEAD names in the tests' repositories, and made the
// commit kit makes onto it.
const (
	base = "0e0bf17a399109574ffb00e33300280a33f81ab7"
	made = "2f8f9a0d6c1b4e5a7d3c2b1a0f9e8d7c6b5a4f3e"
)

func git(args ...string) []string { return append([]string{"-C", dir}, args...) }

// The indexes a command runs in: the user's, kit's own of the last commit
// to commit from, the next of the user's, made while kit holds it, or one
// of kit's own only read, made in the temporary folder.
const (
	usersIndex = ""
	ownIndex   = "kit-index"
	nextIndex  = "kit-index.next"
	readIndex  = "read"
)

// call is a command kit ran: its arguments, what it read, and the index it
// ran in.
type call struct {
	args  []string
	input string
	index string
}

// checkCalls checks fake ran want, in order, each reading what it says and
// in the index it says: kit's own and the next of the user's in gitDir, one
// it only reads a file of the temporary folder's, gone now.
func checkCalls(t *testing.T, name string, fake *runnertest.Fake, gitDir string, want []call) {
	t.Helper()
	var ran []call
	var read string
	for _, cmd := range fake.Commands() {
		c := call{args: cmd.Args, input: cmd.Input}
		for _, e := range cmd.Env {
			path, ok := strings.CutPrefix(e, "GIT_INDEX_FILE=")
			switch {
			case !ok:
				continue
			case filepath.Dir(path) == gitDir:
				c.index = filepath.Base(path)
			case filepath.Dir(path) == filepath.Clean(os.TempDir()) && strings.HasPrefix(filepath.Base(path), "kit-read-index-"):
				c.index = readIndex
				if read == "" {
					read = path
				}
				if path != read {
					t.Errorf("%s: %s read the index %s, and another before it, %s", name, cmd, path, read)
				}
			default:
				t.Errorf("%s: %s ran in the index %s, none of kit's", name, cmd, path)
			}
		}
		ran = append(ran, c)
	}
	if !slices.EqualFunc(ran, want, func(a, b call) bool { return slices.Equal(a.args, b.args) && a.input == b.input && a.index == b.index }) {
		t.Errorf("%s: ran\n%s\n%v\nwant\n%v", name, strings.Join(fake.Calls(), "\n"), ran, want)
	}
	if read != "" {
		if _, err := os.Lstat(read); err == nil {
			t.Errorf("%s: left the index it read, %s", name, read)
		}
	}
}

// nul is paths, each ended by a NUL, as git reads them with -z.
func nul(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(p + "\x00")
	}
	return b.String()
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
