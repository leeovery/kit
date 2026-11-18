package cli_test

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/cli"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/macapp"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// hostKey is the test Mac's SSH host key, plainly no real one, and
// localName its local hostname: the ones twoMacs records for laptop, and
// the world's Mac presents.
const (
	hostKey   = "ssh-ed25519 ZmFrZSBlZDI1NTE5IGhvc3Qga2V5"
	localName = "Someones-MacBook-Pro"
)

// twoMacs is a kit.toml knowing two Macs, laptop's host key and local
// hostname recorded.
const twoMacs = `format = 1
primary = "laptop"

[macs.laptop]
description = "MacBook Pro"
host_key = "` + hostKey + `"
local_hostname = "` + localName + `"

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
	// path and childEnv are what kit last made its runner with; added,
	// variables the runner gives programs besides childEnv's, as a
	// childEnv that passed them would.
	path     []string
	childEnv []string
	added    []string
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
	// shell, when set, is kit becoming the login shell.
	shell func() error
	// ended are the waits of a step by hand's card that end at once (after).
	ended []steps.Wait
	// ctx, when set, is what kit runs in, for a test to stop it; else the
	// test's own.
	ctx context.Context
	// sshAt is where the Mac's Remote Login answers, when a test has it.
	sshAt string
	// self is where kit runs from, when a test moves it: kitPath else.
	self string
	// gitHub is GitHub, as kit reaches it: on this machine, where nothing
	// answers, unless a test serves it.
	gitHub boot.GitHub
	// text answers what kit asks to be typed at a terminal, as its row
	// reads, given what's typed to start with: that, unless it's set.
	text func(question, typed string) string
}

// after is how long kit waits in the world, as time.After has it: no time
// at all for a wait in ended; never, for the rest, so that a step by hand's
// card goes up, and posts its notification, as a look finds it not done,
// and is looked at again only as it's answered.
func (w *world) after(wait steps.Wait, _ time.Duration) <-chan time.Time {
	if !slices.Contains(w.ended, wait) {
		return nil
	}
	end := make(chan time.Time, 1)
	end <- w.now
	return end
}

// newWorld makes a home holding a config repository of files, by name.
func newWorld(t *testing.T, files map[string]string) *world {
	t.Helper()
	w := &world{home: t.TempDir(), env: map[string]string{}, now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), fake: runnertest.New(t),
		gitHub: boot.GitHub{Client: &http.Client{}, Site: nowhere, API: nowhere}}
	// Without a terminal, Homebrew's installs ask whether sudo holds a
	// password: it doesn't, unless a test says so.
	w.fake.On("sudo", "-n", "true").Exits(1).PrintsToStderr("sudo: a password is required")
	// Every commit of kit-config's pushed: a sync committing nothing finds
	// none waiting.
	w.waiting("")
	w.write(t, filepath.Join("etc", "ssh", "ssh_host_ed25519_key.pub"), hostKey+" root@laptop\n")
	w.scheduled(t)
	for name, content := range files {
		w.write(t, filepath.Join(".config", "kit", name), content)
	}
	return w
}

// nowhere is an address on this machine nothing answers at.
const nowhere = "http://127.0.0.1:1"

// kitPath is where the world's kit is, from the home: on kit's PATH, the
// kit that runs, unless a test moves it, and the one the hourly launch runs.
var kitPath = filepath.Join(".local", "bin", "kit")

// launchAgent is the hourly launch's file, from the home.
var launchAgent = filepath.Join("Library", "LaunchAgents", steps.LaunchLabel+".plist")

// launchService is the hourly launch's service, as launchctl names it.
var launchService = fmt.Sprintf("gui/%d/%s", os.Getuid(), steps.LaunchLabel)

// kitApp is kit.app, in kit's data folder, from the home.
var kitApp = filepath.Join(".local", "share", "kit", "kit.app")

// scheduled has the world's Mac run kit by itself, as kit apply leaves a
// Mac: kit.app built from this kit's files and given Full Disk Access, the
// hourly launch in place and loaded, and an hourly run now.
func (w *world) scheduled(t *testing.T) {
	t.Helper()
	w.write(t, filepath.Join(kitApp, "Contents", "Resources", "built-from"), macapp.Fingerprint()+"\n")
	w.write(t, filepath.Join(kitApp, "Contents", "MacOS", "kit"), "")
	w.write(t, kitPath, "")
	if err := os.Chmod(filepath.Join(w.home, kitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	agent, err := steps.LaunchAgent(filepath.Join(w.home, kitApp), filepath.Join(w.home, kitPath))
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, launchAgent, string(agent))
	w.fake.On("launchctl", "print", launchService).Prints(steps.LaunchLabel + " = {\n}\n")
	w.ranHourly(t)
	if err := nightly.Update(w.stateDir(), func(r *nightly.Record) {
		r.Access = nightly.Access{At: w.now, App: macapp.Fingerprint(), Granted: true}
	}); err != nil {
		t.Fatal(err)
	}
}

// withoutKitApp takes kit.app away, as on a Mac kit can't build it on yet,
// without Swift's compiler: a card's notification goes through osascript,
// unless the test has kit build it.
func (w *world) withoutKitApp(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(w.home, kitApp)); err != nil {
		t.Fatal(err)
	}
}

// ranHourly records an hourly run now, as the hourly launch's would.
func (w *world) ranHourly(t *testing.T) {
	t.Helper()
	if err := nightly.Update(w.stateDir(), func(r *nightly.Record) {
		r.Hourly = nightly.Run{Started: w.now, Finished: w.now}
	}); err != nil {
		t.Fatal(err)
	}
}

// stateDir is kit's state folder in the world.
func (w *world) stateDir() string {
	return filepath.Join(w.home, ".local", "state", "kit")
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
		After:    w.after,
		Stdin:    strings.NewReader(w.stdin),
		Stdout:   &out,
		Stderr:   &errOut,
		Terminal: func(io.Writer) bool { return w.terminal },
		Width:    func(io.Writer) int { return cmp.Or(w.size[0], 80) },
		Height:   func(io.Writer) int { return cmp.Or(w.size[1], 40) },
		Listen:   w.listen,
		Runner: func(path, env []string) runner.Runner {
			w.path, w.childEnv = path, env
			w.fake.Env = slices.Concat(env, w.added)
			return w.fake
		},
		Self:      func() (string, error) { return cmp.Or(w.self, filepath.Join(w.home, kitPath)), nil },
		SudoLocal: filepath.Join(w.home, "etc", "sudo_local"),
		SSHDir:    filepath.Join(w.home, "etc", "ssh"),
		// Nothing listens there: a test waking Remote Login says where.
		SSHAddress: cmp.Or(w.sshAt, "127.0.0.1:1"),
		TCC:        filepath.Join(w.home, "TCC.db"),
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
		ReadText: func(_ context.Context, about look.Row, typed string) (string, error) {
			question := ansi.Strip(about.Name + "  " + about.Says)
			w.asked = append(w.asked, question)
			if w.text != nil {
				return w.text(question, typed), nil
			}
			return typed, nil
		},
		GitHub: func() boot.GitHub { return w.gitHub },
		Pick: func(_ context.Context, _ []string, lines []ask.Line, _ []look.Key) (string, error) {
			if w.pick == nil {
				t.Fatal("kit showed a list to pick from")
			}
			return w.pick(lines)
		},
		Become: func(_ string, args, _ []string) error {
			w.became = append(w.became, args)
			return nil
		},
		Shell: w.shell,
	})
	ctx := w.ctx
	if ctx == nil {
		ctx = t.Context()
	}
	status = cli.Execute(ctx, root, args)
	return out.String(), errOut.String(), status
}

// errNotFound is what the runner says of a program that isn't installed.
var errNotFound = runner.ErrNotFound

// commit is a commit of kit-config's a test expects kit to make, and git's
// answers to it, as real git gives them: kit commits paths with message;
// status is git's status of them (-z), the files new to git (??) among them
// entered in its index first; files are those committed, each whole as it
// is on disk. Unsigned, git is set to sign it and unable to; failing, git
// refuses it otherwise, saying fails: either way nothing's committed or
// pushed, and the new files are taken back out.
type commit struct {
	message      string
	paths, files []string
	status       string
	unsigned     bool
	fails        string
}

// edited is a commit of files, each a file edited and committed whole.
func edited(message string, files ...string) commit {
	c := commit{message: message, paths: files, files: files}
	for _, f := range files {
		c.status += " M " + f + "\x00"
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

// head is the commit HEAD names in kit-config, as git rev-parse prints it.
const head = "0e0bf17a399109574ffb00e33300280a33f81ab7"

// unsignedSays is what real git says when it can't sign a commit, as gpg
// has no key to sign it with.
const unsignedSays = "error: gpg failed to sign the data\nfatal: failed to write commit object\n"

// committing is git's command on paths in kit-config, as kit runs it: each
// path taken as it's written.
func (w *world) committing(args []string, paths ...string) []string {
	return slices.Concat([]string{"-C", filepath.Join(w.home, ".config", "kit"), "--literal-pathspecs"}, args, []string{"--"}, paths)
}

// expectCommit scripts git's answers to c, and the push after it, failing
// the test unless kit commits c.files, with c.message, entering the new
// ones first, and unless the status given is so of the files on disk once
// kit asks, when each of checks runs too. Unsigned, it fails the test
// unless kit commits nothing, and takes the new files back out.
func (w *world) expectCommit(t *testing.T, c commit, checks ...func()) {
	t.Helper()
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	asked := func() {
		onDisk(t, dir, c.status)
		for _, check := range checks {
			check()
		}
	}
	var added []string
	for rec := range strings.SplitSeq(c.status, "\x00") {
		if path, ok := strings.CutPrefix(rec, "?? "); ok {
			added = append(added, path)
		}
	}
	gitDir := t.TempDir()
	w.fake.On("git", w.committing([]string{"status", "--porcelain", "-z", "--untracked-files=all", "--no-renames"}, c.paths...)...).Prints(c.status).Does(asked)
	w.fake.On("git", git("rev-parse", "--absolute-git-dir")...).Prints(gitDir + "\n")
	signing := w.fake.On("git", git("config", "--get", "--type=bool", "commit.gpgsign")...).Exits(1)
	if len(added) > 0 {
		w.fake.On("git", w.committing([]string{"add", "--intent-to-add"}, added...)...)
	}
	w.fake.On("git", git("rev-parse", "--verify", "--quiet", "HEAD")...).Prints(head + "\n")
	w.fake.On("git", w.committing([]string{"diff", "--quiet", "--no-ext-diff", "HEAD"}, c.files...)...).Exits(1)
	var committed, takenBack atomic.Bool
	made := w.fake.On("git", w.committing([]string{"-c", "core.hooksPath=/dev/null", "commit", "--quiet", "--only", "-m", c.message}, c.files...)...)
	if c.unsigned {
		signing.Exits(0).Prints("true\n")
		c.fails = unsignedSays
	}
	failing := c.fails != ""
	if failing {
		made.Exits(128).PrintsToStderr(c.fails)
		if len(added) > 0 {
			w.fake.On("git", w.committing([]string{"rm", "--cached", "--quiet"}, added...)...).Does(func() { takenBack.Store(true) })
		}
	} else {
		made.Does(func() { committed.Store(true) })
		w.expectPush(t, gitDir)
	}
	t.Cleanup(func() {
		switch {
		case committed.Load() == failing:
			t.Errorf("kit committed %q: %v, git refusing it %v", c.message, committed.Load(), failing)
		case failing && len(added) > 0 && !takenBack.Load():
			t.Errorf("kit left the new files %q in git's index, committing nothing", added)
		}
	})
}

// waiting scripts git's answers as kit asks which of kit-config's commits
// wait to be pushed, as real git gives them: the one remote, origin; the
// branch, main, its upstream origin's, there; and when each commit its
// upstream hasn't was made, times (%ct), "" for none.
func (w *world) waiting(times string) {
	git := func(args ...string) []string {
		return append([]string{"-C", filepath.Join(w.home, ".config", "kit")}, args...)
	}
	w.fake.On("git", git("remote")...).Prints("origin\n")
	w.tracks()
	w.fake.On("git", git("log", "--format=%ct", "refs/remotes/origin/main..HEAD")...).Prints(times)
}

// tracks scripts git's answers as kit asks how kit-config's branch stands
// with its remote, as real git gives them: main, its upstream origin's
// main, there.
func (w *world) tracks() {
	git := func(args ...string) []string {
		return append([]string{"-C", filepath.Join(w.home, ".config", "kit")}, args...)
	}
	w.fake.On("git", git("symbolic-ref", "-q", "HEAD")...).Prints("refs/heads/main\n")
	w.fake.On("git", git("for-each-ref", "--format=%(upstream) %(upstream:remoteref)", "refs/heads/main")...).Prints("refs/remotes/origin/main refs/heads/main\n")
	w.fake.On("git", git("rev-parse", "--verify", "--quiet", "refs/remotes/origin/main")...).Prints(head + "\n")
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
	w.tracks()
	w.fake.On("git", git("fetch", "--prune", "--quiet", "origin")...)
	w.fake.On("git", git("rev-list", "--count", "HEAD..refs/remotes/origin/main")...).Prints("0\n")
	w.fake.On("git", git("push", "--quiet", "origin", "HEAD:refs/heads/main")...)
}

// edits scripts git's answers as kit asks for kit-config's edits, as real
// git gives them: its status (-z), and the lines each file adds and
// removes against the last commit (--numstat -z), as git's index has them.
func (w *world) edits(t *testing.T, status, numstat string) {
	t.Helper()
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	w.fake.On("git", git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")...).Prints(status)
	w.edited = true
	w.fake.On("git", git("rev-parse", "--verify", "--quiet", "HEAD")...).Prints(head + "\n")
	w.fake.On("git", git("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD")...).Prints(numstat)
}

// onDisk fails the test unless status, as git prints it (-z), is so of the
// files in dir: each it names deleted on disk is gone, and each other there
// but one deleted only in git's index, which is there when the user took it
// out of the index, kept on disk, and gone else.
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
