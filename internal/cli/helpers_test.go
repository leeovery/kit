package cli_test

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/cli"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// twoMacs is a kit.toml knowing two Macs.
const twoMacs = `format = 1
primary = "laptop"

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"
`

// world is a home of a test's own, with kit's config repository in it, a
// clock, and a stand-in for every program kit runs.
type world struct {
	home string
	env  map[string]string
	now  time.Time
	fake *runnertest.Fake
	// edited is whether git's status of kit-config whole is scripted, as
	// edits does.
	edited bool
	// environ is kit's environment, whole, beside env's lookups; stdin is
	// what kit reads on its standard input.
	environ []string
	stdin   string
	// typed is what a person types when kit asks for a value unshown;
	// typedFor, what each time it was asked for, as its row reads.
	typed    string
	typedFor []string
	// notTyped, when set, is how asking for a value unshown ends instead;
	// typing, when set, is what the person does before typing it.
	notTyped error
	typing   func()
	// path and childEnv are what kit last made its runner with.
	path     []string
	childEnv []string
	// terminal is whether kit's output is a terminal, size its columns and
	// lines (80 by 40 unless set), and listen reads its keys, for
	// applying's screen.
	terminal bool
	size     [2]int
	listen   func() (<-chan ask.Key, func())
	// choose answers kit's questions at a terminal; asked notes each.
	choose func(question string, options []string) (int, error)
	asked  []string
	// pick picks from a list kit shows at a terminal, as its lines read,
	// plain: the value of the line picked.
	pick func(lines []ask.Line) (string, error)
	// became is each command kit became, as its arguments.
	became [][]string
}

// newWorld makes a home holding a config repository of files, by name.
func newWorld(t *testing.T, files map[string]string) *world {
	t.Helper()
	w := &world{home: t.TempDir(), env: map[string]string{}, now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), fake: runnertest.New(t)}
	// Without a terminal, Homebrew's installs ask whether sudo holds a
	// password: it doesn't, unless a test says so.
	w.fake.On("sudo", "-n", "true").Exits(1).PrintsToStderr("sudo: a password is required")
	// Every commit of kit-config's pushed: a sync committing nothing finds
	// none waiting.
	w.fake.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "rev-list", "--count", "@{u}..HEAD").Prints("0\n")
	for name, content := range files {
		w.write(t, filepath.Join(".config", "kit", name), content)
	}
	return w
}

// write writes a file at path, from the home, making its directories.
func (w *world) write(t *testing.T, path, content string) {
	t.Helper()
	full := filepath.Join(w.home, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// read reads the file at path, from the home: "" when there isn't one.
func (w *world) read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(w.home, path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// run runs kit with args in the world, returning what it printed and the
// status it exits with.
func (w *world) run(t *testing.T, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := cli.NewRootCommand(cli.Deps{
		Version:  "0.1.0",
		Getenv:   func(name string) string { return w.env[name] },
		Environ:  func() []string { return w.environ },
		HomeDir:  func() (string, error) { return w.home, nil },
		Now:      func() time.Time { return w.now },
		Stdin:    strings.NewReader(w.stdin),
		Stdout:   &out,
		Stderr:   &errOut,
		Terminal: func(io.Writer) bool { return w.terminal },
		Width:    func(io.Writer) int { return cmp.Or(w.size[0], 80) },
		Height:   func(io.Writer) int { return cmp.Or(w.size[1], 40) },
		Listen:   w.listen,
		Runner: func(path, env []string) runner.Runner {
			w.path, w.childEnv = path, env
			return w.fake
		},
		SudoLocal: filepath.Join(w.home, "etc", "sudo_local"),
		TCC:       filepath.Join(w.home, "TCC.db"),
		Choose: func(_ context.Context, q ask.Question) (int, error) {
			return w.answer(t, q)
		},
		Walk: func(_ context.Context, _ string, qs []ask.Question) ([]int, error) {
			var answers []int
			for _, q := range qs {
				i, err := w.answer(t, q)
				if err != nil {
					return answers, err
				}
				answers = append(answers, i)
			}
			return answers, nil
		},
		ReadSecret: func(_ context.Context, about look.Row) (string, error) {
			w.typedFor = append(w.typedFor, ansi.Strip(about.Name+"  "+about.Says))
			if w.typing != nil {
				w.typing()
			}
			if w.notTyped != nil {
				return "", w.notTyped
			}
			return w.typed, nil
		},
		Pick: func(_ context.Context, _ []string, lines []ask.Line, _ []look.Key) (string, error) {
			if w.pick == nil {
				t.Fatal("kit showed a list to pick from")
			}
			return w.pick(lines)
		},
		Become: func(args []string) error {
			w.became = append(w.became, args)
			return nil
		},
	})
	status = cli.Execute(t.Context(), root, args)
	return out.String(), errOut.String(), status
}

// errNotFound is what the runner says of a program that isn't installed.
var errNotFound = runner.ErrNotFound

// commit is a commit of kit-config's a test expects kit to make, and git's
// answers to it, as real git gives them: kit commits paths with message;
// status is git's status of them (-z); kit stages update, each file as it
// is on disk, in an index of its own; staged is what git says is staged
// there (-z); and the user's index takes the files staged as committed.
// The commit goes onto head, unless moved, HEAD moved by another git
// meanwhile, when git refuses it, and nothing's committed or pushed.
type commit struct {
	message        string
	paths, update  []string
	status, staged string
	moved          bool
}

// edited is a commit of files, each a file edited and committed whole.
func edited(message string, files ...string) commit {
	c := commit{message: message, paths: files, update: files}
	for _, f := range files {
		c.status += " M " + f + "\x00"
		c.staged += f + "\x00"
	}
	return c
}

// expectSync expects kit to commit files, each a file there before the run
// and edited by it, with message, and push: each must hold something else
// once kit asks git's status than it held when this was called. A file the
// run makes is new to git (??), and a file edited before the run is dirty
// before it: either is expectCommit's, its status stated.
func (w *world) expectSync(t *testing.T, files []string, message string) {
	t.Helper()
	dir := filepath.Join(w.home, ".config", "kit")
	was := make(map[string]string, len(files))
	for _, f := range files {
		if _, err := os.Lstat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expectSync: %s isn't there before the run: git says ?? of a file the run makes; state it with expectCommit", f)
		}
		was[f] = w.read(t, filepath.Join(".config", "kit", f))
	}
	w.expectCommit(t, edited(message, files...), func() {
		for _, f := range files {
			if data, err := os.ReadFile(filepath.Join(dir, f)); err != nil || string(data) == was[f] {
				t.Errorf("kit commits %s as edited, and the run didn't edit it: %v", f, err)
			}
		}
	})
}

// head is the commit HEAD names in kit-config, as git rev-parse prints it;
// tree the tree git writes of kit's own index of it, made the commit kit
// makes of that onto it, and theirs another git's, HEAD moved onto it.
const (
	head   = "0e0bf17a399109574ffb00e33300280a33f81ab7"
	tree   = "af9055821923c351c404eaaff97d82a5552a1ad5"
	made   = "2f8f9a0d6c1b4e5a7d3c2b1a0f9e8d7c6b5a4f3e"
	theirs = "154906a47f12246099602d1bee6b1c558d3409c9"
)

// expectCommit scripts git's answers to c, and the push after it, failing
// the test unless kit commits, unless what it stages is update and what it
// hands back to the user's index the files staged, the user's index then
// the one git made, and unless the status given is so of the files on disk
// once kit asks, when each of checks runs too. HEAD moved, it fails the
// test unless kit commits nothing, and leaves the git folder as it was.
func (w *world) expectCommit(t *testing.T, c commit, checks ...func()) {
	t.Helper()
	dir := filepath.Join(w.home, ".config", "kit")
	gitDir := t.TempDir()
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	asked := func() {
		onDisk(t, dir, c.status)
		for _, check := range checks {
			check()
		}
	}
	reads := func(what, want string) func(string) (string, error) {
		return func(in string) (string, error) {
			if in != want {
				t.Errorf("%s read %q, want %q", what, in, want)
			}
			return "", nil
		}
	}
	w.fake.On("git", git(slices.Concat([]string{"--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--"}, c.paths)...)...).Prints(c.status).Does(asked)
	w.fake.On("git", git("rev-parse", "--absolute-git-dir")...).Prints(gitDir + "\n")
	var tried, committed atomic.Bool
	w.fake.On("git", git("rev-parse", "--verify", "--quiet", "HEAD")...).Filters(func(string) (string, error) {
		if tried.Load() {
			return theirs + "\n", nil
		}
		return head + "\n", nil
	})
	w.fake.On("git", git("read-tree", head)...)
	w.fake.On("git", git("update-index", "--add", "--remove", "--replace", "-z", "--stdin")...).Filters(reads("update-index", strings.Join(c.update, "\x00")+"\x00"))
	w.fake.On("git", git("diff", "--cached", "--name-only", "-z", "--no-renames")...).Prints(c.staged)
	w.fake.On("git", git("write-tree")...).Prints(tree + "\n")
	w.fake.On("git", git("config", "--get", "--type=bool", "commit.gpgsign")...).Exits(1)
	w.fake.On("git", git("commit-tree", tree, "-p", head, "-m", c.message)...).Prints(made + "\n")
	subject, _, _ := strings.Cut(c.message, "\n")
	moveHEAD := w.fake.On("git", git("update-ref", "-m", "kit: "+subject, "HEAD", made, head)...)
	const reset = "the user's index, kit's files as committed"
	if c.moved {
		moveHEAD.Exits(128).PrintsToStderr("fatal: update_ref failed for ref 'HEAD': cannot lock ref 'HEAD': is at " + theirs + " but expected " + head + "\n").Does(func() { tried.Store(true) })
	} else {
		moveHEAD.Does(func() { committed.Store(true) })
		w.fake.On("git", git("--literal-pathspecs", "reset", "--quiet", "--pathspec-from-file=-", "--pathspec-file-nul")...).Filters(reads("reset", c.staged)).Does(func() {
			if err := os.WriteFile(filepath.Join(gitDir, "kit-index.next"), []byte(reset), 0o600); err != nil {
				t.Error(err)
			}
		})
		w.expectPush(t, gitDir)
	}
	t.Cleanup(func() {
		entries, _ := os.ReadDir(gitDir)
		data, _ := os.ReadFile(filepath.Join(gitDir, "index"))
		switch {
		case committed.Load() == c.moved:
			t.Errorf("kit committed %q: %v, HEAD moved %v", c.message, committed.Load(), c.moved)
		case c.moved && len(entries) != 0:
			t.Errorf("the git folder holds %v; want it left as it was, empty", entries)
		case !c.moved && (len(entries) != 1 || string(data) != reset):
			t.Errorf("the git folder holds %v, its index %q; want the index git reset", entries, data)
		}
	})
}

// expectPush scripts a push of kit-config's, whose git folder is gitDir,
// the remote with nothing new: nothing else changed, unless edits says what
// is.
func (w *world) expectPush(t *testing.T, gitDir string) {
	t.Helper()
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	w.fake.On("git", git("remote")...).Prints("origin\n")
	w.fake.On("git", git("rev-parse", "--absolute-git-dir")...).Prints(gitDir + "\n")
	if !w.edited {
		w.fake.On("git", git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")...)
	}
	w.fake.On("git", git("fetch", "--quiet")...)
	w.fake.On("git", git("symbolic-ref", "-q", "HEAD")...).Prints("refs/heads/main\n")
	w.fake.On("git", git("for-each-ref", "--format=%(upstream)", "refs/heads/main")...).Prints("refs/remotes/origin/main\n")
	w.fake.On("git", git("rev-parse", "--verify", "--quiet", "refs/remotes/origin/main")...).Prints(head + "\n")
	w.fake.On("git", git("rev-list", "--count", "HEAD..@{u}")...).Prints("0\n")
	w.fake.On("git", git("push", "--quiet")...)
}

// edits scripts git's answers as kit asks for kit-config's edits, as real
// git gives them: its status (-z), and the lines each file adds and
// removes against the last commit (--numstat -z), counted in kit's own
// index of it.
func (w *world) edits(t *testing.T, status, numstat string) {
	t.Helper()
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	w.fake.On("git", git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")...).Prints(status)
	w.edited = true
	w.fake.On("git", git("rev-parse", "--verify", "--quiet", "HEAD")...).Prints(head + "\n")
	w.fake.On("git", git("read-tree", head)...)
	w.fake.On("git", git("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD")...).Prints(numstat)
}

// onDisk fails the test unless status, as git prints it (-z), is so of the
// files in dir: each it names deleted on disk is gone, and each other there
// but one deleted only in git's index, which is there when the user
// untracked it, kept as git ignores it, and gone else.
func onDisk(t *testing.T, dir, status string) {
	for rec := range strings.SplitSeq(status, "\x00") {
		if len(rec) < 4 {
			continue
		}
		code, path := rec[:2], rec[3:]
		if code == "D " {
			continue
		}
		_, err := os.Lstat(filepath.Join(dir, path))
		if there := err == nil; there == (code[1] == 'D') {
			t.Errorf("git status has %q %s, and it's there: %v", code, path, there)
		}
	}
}

// writeSection sets the section headed header, in scope's declarations
// file, to lines, keeping the file's other sections; the section is added at
// the end when the file hasn't one.
func (w *world) writeSection(t *testing.T, scope, header, lines string) {
	t.Helper()
	path := filepath.Join(".config", "kit", config.DeclFile(scope))
	type section struct{ header, body string }
	var sections []section
	for line := range strings.Lines(w.read(t, path)) {
		if strings.HasPrefix(line, "[") {
			sections = append(sections, section{header: strings.TrimSpace(line)})
		} else if len(sections) > 0 {
			sections[len(sections)-1].body += line
		}
	}
	want := "[" + header + "]"
	i := slices.IndexFunc(sections, func(s section) bool { return s.header == want })
	if i < 0 {
		sections = append(sections, section{header: want})
		i = len(sections) - 1
	}
	sections[i].body = lines
	var b strings.Builder
	for j, s := range sections {
		if j > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s.header + "\n" + strings.TrimRight(s.body, "\n") + "\n")
	}
	w.write(t, path, b.String())
}

// readSection reads the lines of the section headed header in scope's
// declarations file: "" when there's no such section.
func (w *world) readSection(t *testing.T, scope, header string) string {
	t.Helper()
	var b strings.Builder
	in := false
	for line := range strings.Lines(w.read(t, filepath.Join(".config", "kit", config.DeclFile(scope)))) {
		if strings.HasPrefix(line, "[") {
			in = strings.TrimSpace(line) == "["+header+"]"
			continue
		}
		if in {
			b.WriteString(line)
		}
	}
	if text := strings.TrimRight(b.String(), "\n"); text != "" {
		return text + "\n"
	}
	return ""
}

// answer answers a question kit asks, as the test's choose says, by its
// plain words and its answers' labels.
func (w *world) answer(t *testing.T, q ask.Question) (int, error) {
	t.Helper()
	text := q.Text()
	if len(q.More) > 0 {
		text += "\n\n" + ansi.Strip(strings.Join(q.More, "\n"))
	}
	w.asked = append(w.asked, text)
	if w.choose == nil {
		t.Errorf("kit asked %q, which this test doesn't answer", text)
		return 0, errors.New("unanswered")
	}
	return w.choose(text, q.Labels())
}

// sudoAsks is sudo -S's prompt, as kit has sudo give it, and askedFor, the
// call that asks for the password through it, as calls read.
const (
	sudoAsks = "[kit: sudo wants the password]"
	askedFor = "sudo -S -p '" + sudoAsks + "' -v"
)

// expectPassword scripts sudo taking the password kit asks for in its field,
// typed as the world's typed, once.
func (w *world) expectPassword() *runnertest.Script {
	if w.typed == "" {
		w.typed = "hunter2"
	}
	return w.fake.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(1)
}
