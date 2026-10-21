package gitrepo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// usersEntries is the user's index in a test's git folder, and
// resetEntries what git's reset makes of kit's copy of it.
const (
	usersEntries = "the user's entries"
	resetEntries = "the user's entries, kit's files as committed"
)

// indexMode and indexTime are the user's index's mode, one a shared
// repository's git gives it, and the time it was written; resetTime is
// when git's reset writes kit's copy of it.
var (
	indexMode = os.FileMode(0o664)
	indexTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	resetTime = time.Date(2026, 1, 2, 3, 4, 9, 0, time.UTC)
)

// gitFolder is a git folder holding the user's index, none before the
// first commit, and files, each an empty file at a name.
func gitFolder(t *testing.T, unborn bool, files ...string) string {
	t.Helper()
	gitDir := t.TempDir()
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(gitDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if unborn {
		return gitDir
	}
	index := filepath.Join(gitDir, "index")
	if err := errors.Join(
		os.WriteFile(index, []byte(usersEntries), 0o600),
		os.Chmod(index, indexMode),
		os.Chtimes(index, indexTime, indexTime),
	); err != nil {
		t.Fatal(err)
	}
	return gitDir
}

// checkGitFolder checks gitDir holds the user's index, as index says (none
// when ""), and others, and nothing else: no lock, nothing of kit's.
func checkGitFolder(t *testing.T, name, gitDir, index string, others ...string) {
	t.Helper()
	want := slices.Clone(others)
	if index != "" {
		want = append(want, "index")
	}
	slices.Sort(want)
	entries, _ := os.ReadDir(gitDir)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: the git folder holds %q, want %q", name, got, want)
	}
	if data, _ := os.ReadFile(filepath.Join(gitDir, "index")); index != "" && string(data) != index {
		t.Errorf("%s: the user's index holds %q, want %q", name, data, index)
	}
}

// sequence is the commands a test expects git to run, in order, as it
// scripts them.
type sequence struct {
	fake    *runnertest.Fake
	want    []call
	scripts map[string]*runnertest.Script
}

// expect scripts c, expected next: a command expected before answers again,
// as scripted now.
func (s *sequence) expect(c call) *runnertest.Script {
	s.want = append(s.want, c)
	key := strings.Join(c.args, "\x00")
	if script, ok := s.scripts[key]; ok {
		return script.Then()
	}
	if s.scripts == nil {
		s.scripts = map[string]*runnertest.Script{}
	}
	s.scripts[key] = s.fake.On("git", c.args...)
	return s.scripts[key]
}

func statusOf(paths ...string) []string {
	return git(slices.Concat([]string{"--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--"}, paths)...)
}

// The commands of a commit, after status.
var (
	gitDirOf = git("rev-parse", "--absolute-git-dir")
	headOf   = git("rev-parse", "--verify", "--quiet", "HEAD")
	stageIn  = git("update-index", "--add", "--remove", "--replace", "-z", "--stdin")
	stagedIn = git("diff", "--cached", "--name-only", "-z", "--no-renames")
	commitIn = git("commit", "--quiet", "-m", "kit test")
	parentOf = git("rev-parse", "--verify", "--quiet", "HEAD^")
	resetIn  = git("--literal-pathspecs", "reset", "--quiet", "--pathspec-from-file=-", "--pathspec-file-nul")
)

// commitCase is a commit of paths, git's answers to it as real git gives
// them, and what kit has git do: status of the paths; then, holding git's
// index, in an index of its own of the last commit's, the files it stages
// as they are on disk (stage) and those it commits as deleted (deleted),
// what's staged there (staged), and a commit when anything is, and the
// commit HEAD^ names after it (parent: the last commit, unless another git
// moved HEAD); then, in a copy of the user's index, the files that take the
// last commit's entries (reset), the copy then the user's index. Neither
// stage, deleted nor reset: nothing runs after status.
type commitCase struct {
	name                  string
	paths                 []string
	status                string
	unborn                bool
	stage, deleted, reset []string
	staged                string
	parent                string
	// err is what Commit's error says: none when "".
	err string
	// held, when set, checks the git folder too as each command runs
	// while kit holds the index.
	held func(t *testing.T, gitDir string)
}

// script scripts git's answers to c in fake, and is the commands c expects
// run, each checking kit holds git's index in gitDir from the git folder
// on.
func (c commitCase) script(t *testing.T, fake *runnertest.Fake, gitDir string) []call {
	t.Helper()
	held := func() {
		if _, err := os.Lstat(filepath.Join(gitDir, "index.lock")); err != nil {
			t.Errorf("%s: git ran without kit holding the index", c.name)
		}
		if c.held != nil {
			c.held(t, gitDir)
		}
	}
	s := &sequence{fake: fake}
	s.expect(call{args: statusOf(c.paths...)}).Prints(c.status)
	if c.stage == nil && c.deleted == nil && c.reset == nil {
		return s.want
	}
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	if head := s.expect(call{args: headOf}).Does(held); c.unborn {
		head.Exits(1)
	} else {
		head.Prints(base + "\n")
		s.expect(call{args: git("read-tree", base), index: ownIndex}).Does(held)
	}
	if c.deleted != nil {
		s.expect(call{args: git("update-index", "--force-remove", "-z", "--stdin"), input: nul(c.deleted), index: ownIndex}).Does(held)
	}
	if c.stage != nil {
		s.expect(call{args: stageIn, input: nul(c.stage), index: ownIndex}).Does(held)
	}
	s.expect(call{args: stagedIn, index: ownIndex}).Prints(c.staged).Does(held)
	if c.staged != "" {
		s.expect(call{args: commitIn, index: ownIndex}).Does(held)
		switch parent := s.expect(call{args: parentOf}).Does(held); {
		case c.parent != "":
			parent.Prints(c.parent + "\n")
		case c.unborn:
			parent.Exits(1)
		default:
			parent.Prints(base + "\n")
		}
	}
	if c.reset != nil {
		var lock os.FileInfo
		t.Cleanup(func() { c.checkReplaced(t, gitDir, lock) })
		s.expect(call{args: resetIn, input: nul(c.reset), index: nextIndex}).Does(func() {
			held()
			lock, _ = os.Stat(filepath.Join(gitDir, "index.lock"))
			c.checkCopy(t, filepath.Join(gitDir, nextIndex))
			// As git's reset writes it.
			next := filepath.Join(gitDir, nextIndex)
			if err := errors.Join(
				os.WriteFile(next, []byte(resetEntries), 0o600),
				os.Chtimes(next, resetTime, resetTime),
			); err != nil {
				t.Error(err)
			}
		})
	}
	return s.want
}

// checkCopy fails the test unless next, the copy of the user's index git
// resets, is the user's index, its time kept, so git's racy-git rule finds
// an entry changed within the second it was written; before the first
// commit there's none.
func (c commitCase) checkCopy(t *testing.T, next string) {
	t.Helper()
	data, err := os.ReadFile(next)
	switch {
	case c.unborn && err == nil:
		t.Errorf("%s: git reset a copy of an index there wasn't", c.name)
	case c.unborn:
	case err != nil || string(data) != usersEntries:
		t.Errorf("%s: git reset an index holding %q, %v; want a copy of the user's", c.name, data, err)
	default:
		if info, _ := os.Stat(next); !info.ModTime().Equal(indexTime) {
			t.Errorf("%s: the copy's time is %s, want the user's index's, %s", c.name, info.ModTime(), indexTime)
		}
	}
}

// checkReplaced fails the test unless the user's index is lock, the lock
// kit held, put in its place, with its mode and git's reset's time: the
// next index as git wrote it.
func (c commitCase) checkReplaced(t *testing.T, gitDir string, lock os.FileInfo) {
	t.Helper()
	index, err := os.Stat(filepath.Join(gitDir, "index"))
	switch {
	case lock == nil:
	case err != nil || !os.SameFile(lock, index):
		t.Errorf("%s: the user's index isn't the lock kit held, put in its place", c.name)
	case !index.ModTime().Equal(resetTime):
		t.Errorf("%s: the index's time is %s, want git's reset's, %s", c.name, index.ModTime(), resetTime)
	case !c.unborn && index.Mode().Perm() != indexMode:
		t.Errorf("%s: the index's mode is %v, want the user's index's, %v", c.name, index.Mode().Perm(), indexMode)
	}
}

// run scripts git's answers to c, runs Commit, and checks the commands it
// ran, in order, with what each read and in which index; that it committed
// when, and only when, it ran commit; and that the git folder holds the
// user's index, as the reset made it if it ran, the lock kit held put in
// its place, and no lock or file of kit's.
func (c commitCase) run(t *testing.T) {
	t.Helper()
	gitDir := gitFolder(t, c.unborn)
	fake := runnertest.New(t)
	want := c.script(t, fake, gitDir)
	committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", c.paths...)
	c.check(t, committed, err)
	checkCalls(t, c.name, fake, gitDir, want)
	c.checkFolder(t, gitDir)
}

func (c commitCase) check(t *testing.T, committed bool, err error) {
	t.Helper()
	if committed != (c.staged != "") || (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
		t.Errorf("%s: Commit() = %v, %v; want committed %v, error %q", c.name, committed, err, c.staged != "", c.err)
	}
}

func (c commitCase) checkFolder(t *testing.T, gitDir string, others ...string) {
	t.Helper()
	index := usersEntries
	switch {
	case c.reset != nil:
		index = resetEntries
	case c.unborn:
		index = ""
	}
	checkGitFolder(t, c.name, gitDir, index, others...)
}

// anEdit is a commit of a file edited.
var anEdit = commitCase{
	name: "an edit", paths: []string{"laptop/declarations"},
	status: " M laptop/declarations\x00", stage: []string{"laptop/declarations"},
	staged: "laptop/declarations\x00", reset: []string{"laptop/declarations"},
}

func TestCommit(t *testing.T) {
	anEdit.run(t)
}

// Every change under the paths goes in whole, file by file, whatever the
// user's config says: a new folder's every file, a link, a deletion staged
// or not, both sides of a rename, a file become a folder and a folder a
// file, names with spaces, " -> ", unicode or a pattern's characters, and
// paths given as dir/ or ./x. The commit's made in an index of kit's own,
// so anything else staged stays as it is, never named by status of the
// paths; the files committed then take the commit's entries in the user's.
// Each status is what real git prints.
func TestCommitTakesEachChangeWhole(t *testing.T) {
	for _, c := range []commitCase{
		{
			name:   "a new folder hidden by status.showUntrackedFiles=no",
			paths:  []string{"laptop/steps/tool"},
			status: "?? laptop/steps/tool/run\x00", stage: []string{"laptop/steps/tool/run"},
			staged: "laptop/steps/tool/run\x00", reset: []string{"laptop/steps/tool/run"},
		},
		{
			name:    "deletions staged and not, a new folder with a link, unicode, and a path unchanged",
			paths:   []string{"laptop/declarations", "laptop/steps/tool", "old", "shared/steps/tool", "studio/steps/tool"},
			status:  " M laptop/declarations\x00D  old/run\x00 D studio/steps/tool/run\x00?? shared/steps/tool/café\x00?? shared/steps/tool/link\x00?? shared/steps/tool/run\x00",
			stage:   []string{"laptop/declarations", "studio/steps/tool/run", "shared/steps/tool/café", "shared/steps/tool/link", "shared/steps/tool/run"},
			deleted: []string{"old/run"},
			staged:  "laptop/declarations\x00old/run\x00shared/steps/tool/café\x00shared/steps/tool/link\x00shared/steps/tool/run\x00studio/steps/tool/run\x00",
			reset:   []string{"laptop/declarations", "old/run", "shared/steps/tool/café", "shared/steps/tool/link", "shared/steps/tool/run", "studio/steps/tool/run"},
		},
		{
			name:   "a rename the user staged, both sides given, named with an arrow, status.renames on",
			paths:  []string{"p -> q", "r s"},
			status: "D  p -> q\x00A  r s\x00", stage: []string{"r s"}, deleted: []string{"p -> q"},
			staged: "p -> q\x00r s\x00", reset: []string{"p -> q", "r s"},
		},
		{
			name:   "a rename the user staged, its new side alone given",
			paths:  []string{"b"},
			status: "A  b\x00", stage: []string{"b"},
			staged: "b\x00", reset: []string{"b"},
		},
		{
			name:   "a name that's a pattern, beside a file it would match",
			paths:  []string{"a*"},
			status: " M a*\x00", stage: []string{"a*"},
			staged: "a*\x00", reset: []string{"a*"},
		},
		{
			name:   "a file become a folder",
			paths:  []string{"laptop"},
			status: " D laptop/conf\x00?? laptop/conf/inner\x00", stage: []string{"laptop/conf", "laptop/conf/inner"},
			staged: "laptop/conf\x00laptop/conf/inner\x00", reset: []string{"laptop/conf", "laptop/conf/inner"},
		},
		{
			name:   "a folder become a file",
			paths:  []string{"laptop"},
			status: " D laptop/conf/inner\x00?? laptop/conf\x00", stage: []string{"laptop/conf/inner", "laptop/conf"},
			staged: "laptop/conf\x00laptop/conf/inner\x00", reset: []string{"laptop/conf", "laptop/conf/inner"},
		},
		{
			name:   "paths given as dir/ and ./x",
			paths:  []string{"d/", "./x"},
			status: " M d/a\x00 M x\x00", stage: []string{"d/a", "x"},
			staged: "d/a\x00x\x00", reset: []string{"d/a", "x"},
		},
		{
			name:   "a file the user untracked, ignored, stays deleted",
			paths:  []string{"secret"},
			status: "D  secret\x00", deleted: []string{"secret"},
			staged: "secret\x00", reset: []string{"secret"},
		},
		{
			name:   "a file the user untracked, not ignored, the same on disk: nothing committed, the index as it was",
			paths:  []string{"kept"},
			status: "D  kept\x00?? kept\x00", stage: []string{"kept"},
		},
	} {
		c.run(t)
	}
}

// What git can't keep is left out, never failing the rest: an ignored file
// and an empty folder, which git status never names; a path gone that git
// never kept; and a repository nested in this one. A file added, then gone
// before it was committed (AD), leaves the user's index, which takes the
// last commit's entry, none, committed or not. With nothing committed, the
// user's staged edits stay as they are.
func TestCommitLeavesOutWhatGitCantKeep(t *testing.T) {
	for _, c := range []commitCase{
		{
			name:   "an ignored file and an empty folder",
			paths:  []string{"emptyd", "ign", "other"},
			status: "?? other\x00", stage: []string{"other"},
			staged: "other\x00", reset: []string{"other"},
		},
		{
			name:   "a path gone that git never kept",
			paths:  []string{"gone", "other"},
			status: "?? other\x00", stage: []string{"other"},
			staged: "other\x00", reset: []string{"other"},
		},
		{
			name:  "an ignored file alone",
			paths: []string{"ign"},
		},
		{
			name:   "a file added then gone, beside an edit",
			paths:  []string{"decl", "steps/tool"},
			status: " M decl\x00AD steps/tool/run\x00", stage: []string{"decl"},
			staged: "decl\x00", reset: []string{"decl", "steps/tool/run"},
		},
		{
			name:   "a file added then gone, its declaration staged then put back as committed",
			paths:  []string{"decl", "steps/tool"},
			status: "MM decl\x00AD steps/tool/run\x00", stage: []string{"decl"}, reset: []string{"steps/tool/run"},
		},
		{
			name:   "a file added then gone alone",
			paths:  []string{"steps/tool"},
			status: "AD steps/tool/run\x00", reset: []string{"steps/tool/run"},
		},
		{
			name:   "an edit staged, then put back as committed: the staged edit stays",
			paths:  []string{"decl"},
			status: "MM decl\x00", stage: []string{"decl"},
		},
		{
			name:   "a nested repository",
			paths:  []string{"laptop"},
			status: "?? laptop/sub/\x00",
		},
		{
			name:   "a nested repository beside an edit",
			paths:  []string{"laptop"},
			status: " M laptop/decl\x00?? laptop/sub/\x00", stage: []string{"laptop/decl"},
			staged: "laptop/decl\x00", reset: []string{"laptop/decl"},
		},
	} {
		c.run(t)
	}
}

// Before the repository's first commit, kit's own index starts empty, and
// so does the user's next, git having made none.
func TestCommitBeforeTheFirst(t *testing.T) {
	commitCase{
		name: "a Mac's first capture", paths: []string{"laptop"}, unborn: true,
		status: "?? laptop/.gitignore\x00?? laptop/domains/a.plist\x00", stage: []string{"laptop/.gitignore", "laptop/domains/a.plist"},
		staged: "laptop/.gitignore\x00laptop/domains/a.plist\x00", reset: []string{"laptop/.gitignore", "laptop/domains/a.plist"},
	}.run(t)
}

func TestCommitWithNothingToCommit(t *testing.T) {
	commitCase{name: "nothing changed", paths: []string{"brew"}}.run(t)
	fake := runnertest.New(t)
	if committed, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit test"); committed || err != nil || len(fake.Calls()) != 0 {
		t.Errorf("Commit() of no path = %v, %v, running %q; want nothing run", committed, err, fake.Calls())
	}
}

// A commit made while git is part way through one of its own would finish
// it or be lost to it, so kit refuses, before staging anything, saying
// what's under way, by the file git keeps in its folder while it is. Real
// git keeps each of these: a merge, cherry-pick or revert stopped on a
// conflict, a cherry-pick of several commits, a rebase, and git am.
func TestCommitRefusesWhileGitIsPartWay(t *testing.T) {
	for _, op := range []struct {
		file, name string
		folder     bool
	}{
		{"MERGE_HEAD", "merge", false},
		{"CHERRY_PICK_HEAD", "cherry-pick", false},
		{"REVERT_HEAD", "revert", false},
		{"rebase-merge", "rebase", true},
		{"rebase-apply", "rebase or git am", true},
		{"sequencer", "cherry-pick or revert", true},
	} {
		gitDir := gitFolder(t, false)
		var err error
		if op.folder {
			err = os.Mkdir(filepath.Join(gitDir, op.file), 0o700)
		} else {
			err = os.WriteFile(filepath.Join(gitDir, op.file), []byte(base+"\n"), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
		s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
		committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", "brew")
		if want := "a " + op.name + " is under way in the repository (" + op.file + "): kit's change waits till it's finished"; committed || err == nil || err.Error() != want {
			t.Errorf("Commit() with %s = %v, %v; want %q", op.file, committed, err, want)
		}
		checkCalls(t, op.file, fake, gitDir, s.want)
		checkGitFolder(t, op.file, gitDir, usersEntries, op.file)
	}
}

// A file whose conflict is waiting to be resolved, as git stash pop leaves
// one with nothing in the git folder to say so, is refused too, naming it,
// before kit takes git's index. Real git prints UU, AA and UD for these;
// the rest are git's other codes for a conflict.
func TestCommitRefusesAConflict(t *testing.T) {
	for _, code := range []string{"UU", "AA", "DD", "AU", "UA", "DU", "UD"} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: statusOf("decl", "f")}).Prints(" M decl\x00" + code + " f\x00")
		committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", "decl", "f")
		if want := "a conflict in f is waiting to be resolved: kit's change waits till it's finished"; committed || err == nil || err.Error() != want {
			t.Errorf("Commit() with %s f = %v, %v; want %q", code, committed, err, want)
		}
		checkCalls(t, code, fake, "", s.want)
	}
}

// While another git holds the index, kit tries again for a moment, then
// refuses, naming the lock and how old it is, and what to do should no git
// be running, but leaves it as it is: nothing of the folder's touched, not
// even kit's own files from a run that stopped.
func TestCommitWaitsForAnotherGit(t *testing.T) {
	gitDir := gitFolder(t, false, "kit-index-123")
	lock := filepath.Join(gitDir, "index.lock")
	if err := os.WriteFile(lock, []byte("another git's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if made := time.Now().Add(-3*time.Hour - time.Minute); os.Chtimes(lock, made, made) != nil {
		t.Fatal("couldn't date the lock")
	}
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	var tries int
	var waited time.Duration
	repo := gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(d time.Duration) { tries, waited = tries+1, waited+d }}
	committed, err := repo.Commit(context.Background(), "kit test", "brew")
	if want := "another git holds the repository's index, its lock " + lock + ", made 3 hours ago, so kit's change waits till it's done; if no git is running, one that stopped left it: remove " + lock; committed || err == nil || err.Error() != want {
		t.Errorf("Commit() with the index locked = %v, %v; want %q", committed, err, want)
	}
	if tries < 2 || waited > 5*time.Second {
		t.Errorf("waited %d times, %s in all; want a few tries, briefly", tries, waited)
	}
	if data, _ := os.ReadFile(lock); string(data) != "another git's" {
		t.Errorf("the other git's lock holds %q", data)
	}
	checkCalls(t, "the index locked", fake, gitDir, s.want)
	checkGitFolder(t, "the index locked", gitDir, usersEntries, "index.lock", "kit-index-123")
}

// Another git that lets go of the index while kit waits, as a second kit
// run does once it's committed, leaves kit to commit, its last commit read
// once it holds the index.
func TestCommitOnceAnotherGitLetsGo(t *testing.T) {
	gitDir := gitFolder(t, false, "index.lock")
	fake := runnertest.New(t)
	want := anEdit.script(t, fake, gitDir)
	letGo := func(time.Duration) { _ = os.Remove(filepath.Join(gitDir, "index.lock")) }
	committed, err := gitrepo.Repo{Dir: dir, Run: fake, Sleep: letGo}.Commit(context.Background(), "kit test", anEdit.paths...)
	anEdit.check(t, committed, err)
	checkCalls(t, "once another git lets go", fake, gitDir, want)
	anEdit.checkFolder(t, gitDir)
}

// The files of kit's own a run that stopped part way left in the git
// folder, older kits' named at random among them, go once kit holds the
// index, before it runs anything in it.
func TestCommitSweepsWhatAStoppedRunLeft(t *testing.T) {
	left := []string{"kit-index", "kit-index.lock", "kit-index.next", "kit-index.next.lock", "kit-index-123"}
	gitDir := gitFolder(t, false, left...)
	for _, name := range left {
		if err := os.WriteFile(filepath.Join(gitDir, name), []byte("left by a run that stopped"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := anEdit
	c.name = "swept"
	c.held = func(t *testing.T, gitDir string) {
		entries, _ := os.ReadDir(gitDir)
		for _, e := range entries {
			if data, _ := os.ReadFile(filepath.Join(gitDir, e.Name())); string(data) == "left by a run that stopped" {
				t.Errorf("%s is left once kit holds the index", e.Name())
			}
		}
	}
	fake := runnertest.New(t)
	want := c.script(t, fake, gitDir)
	committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", c.paths...)
	c.check(t, committed, err)
	checkCalls(t, c.name, fake, gitDir, want)
	c.checkFolder(t, gitDir)
}

// Once kit lets go of the index, nothing of the git folder is kit's: the
// next git to take the index, another kit working in its own index there,
// finds its files as it left them, whether kit committed or had nothing to
// commit.
func TestCommitSweepsNothingOnceItLetsGo(t *testing.T) {
	nothing := commitCase{name: "nothing to commit", paths: []string{"decl"}, status: "MM decl\x00", stage: []string{"decl"}}
	for _, c := range []commitCase{anEdit, nothing} {
		gitDir := gitFolder(t, false)
		fake := runnertest.New(t)
		want := c.script(t, fake, gitDir)
		next := func() {
			for _, name := range []string{"index.lock", "kit-index"} {
				if err := os.WriteFile(filepath.Join(gitDir, name), []byte("the next kit's"), 0o600); err != nil {
					t.Error(err)
				}
			}
		}
		committed, err := gitrepo.OnUnlock(gitrepo.Repo{Dir: dir, Run: fake}, next).Commit(context.Background(), "kit test", c.paths...)
		c.check(t, committed, err)
		checkCalls(t, c.name, fake, gitDir, want)
		for _, name := range []string{"index.lock", "kit-index"} {
			if data, _ := os.ReadFile(filepath.Join(gitDir, name)); string(data) != "the next kit's" {
				t.Errorf("%s: the next kit's %s holds %q once kit let go", c.name, name, data)
			}
		}
	}
}

// HEAD moved by another git while kit committed, as git reset --soft can
// while the index is held, leaves a commit that may undo the other's: kit
// says so, naming both commits and how to put it right, once the user's
// index takes the files committed, the error one that's never pushed.
func TestCommitWhenHEADMoved(t *testing.T) {
	const moved = "154906a47f12246099602d1bee6b1c558d3409c9"
	c := anEdit
	c.name, c.parent = "HEAD moved", moved
	c.err = "HEAD moved while kit committed: its commit went onto " + moved + ", not " + base + ", which it was made from, so it may undo what that commit changed, and isn't pushed: undo it with git revert HEAD, or git reset --soft HEAD~ and run kit again"
	gitDir := gitFolder(t, false)
	fake := runnertest.New(t)
	want := c.script(t, fake, gitDir)
	committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", c.paths...)
	c.check(t, committed, err)
	if !errors.Is(err, gitrepo.ErrHeadMoved) {
		t.Errorf("Commit() error = %v, want ErrHeadMoved", err)
	}
	checkCalls(t, c.name, fake, gitDir, want)
	c.checkFolder(t, gitDir)
}

// Once git has committed, the user's index catches up whatever becomes of
// the run: a run cancelled then still checks the commit's parent and resets
// the index.
func TestCommitFinishesOnceCommitted(t *testing.T) {
	gitDir := gitFolder(t, false)
	fake := runnertest.New(t)
	want := anEdit.script(t, fake, gitDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &cancelling{run: fake, cancel: cancel}
	committed, err := gitrepo.Repo{Dir: dir, Run: w}.Commit(ctx, "kit test", anEdit.paths...)
	anEdit.check(t, committed, err)
	if len(w.dead) != 0 {
		t.Errorf("ran on a cancelled run: %q", w.dead)
	}
	checkCalls(t, "cancelled once committed", fake, gitDir, want)
	anEdit.checkFolder(t, gitDir)
}

// A run stopped while git commits, once git has made the commit, as while
// a post-commit hook runs, has committed: HEAD gone onto a commit whose
// parent is the last one, holding kit's own index's files, the user's index
// takes the files committed, on a run never stopped. HEAD where it was, it
// hasn't; nor when HEAD went onto another's commit, as a hook can make,
// which git commit then refuses to follow, as real git does.
func TestCommitStoppedOnceGitCommitted(t *testing.T) {
	const made = "2f8f9a0d6c1b4e5a7d3c2b1a0f9e8d7c6b5a4f3e"
	gitDir := gitFolder(t, false)
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	s.expect(call{args: headOf}).Prints(base + "\n")
	s.expect(call{args: git("read-tree", base), index: ownIndex})
	s.expect(call{args: stageIn, input: "brew\x00", index: ownIndex})
	s.expect(call{args: stagedIn, index: ownIndex}).Prints("brew\x00")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.expect(call{args: commitIn, index: ownIndex}).Fails(context.Canceled).Does(cancel)
	s.expect(call{args: headOf}).Prints(made + "\n")
	s.expect(call{args: parentOf}).Prints(base + "\n")
	s.expect(call{args: git("diff", "--cached", "--quiet", "HEAD"), index: ownIndex})
	s.expect(call{args: parentOf}).Prints(base + "\n")
	s.expect(call{args: resetIn, input: "brew\x00", index: nextIndex}).Does(func() {
		_ = os.WriteFile(filepath.Join(gitDir, nextIndex), []byte(resetEntries), 0o600)
	})
	w := &cancelling{run: fake, cancel: func() {}}
	committed, err := gitrepo.Repo{Dir: dir, Run: w}.Commit(ctx, "kit test", "brew")
	if !committed || err != nil {
		t.Errorf("Commit(), stopped once git committed = %v, %v; want committed", committed, err)
	}
	if len(w.dead) != 0 {
		t.Errorf("ran on the stopped run: %q", w.dead)
	}
	checkCalls(t, "stopped once git committed", fake, gitDir, s.want)
	checkGitFolder(t, "stopped once git committed", gitDir, resetEntries)

	const theirs = "8dcfc6d2dbad007a2413a27223728fd223f83569"
	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	s.expect(call{args: headOf}).Prints(base + "\n")
	s.expect(call{args: git("read-tree", base), index: ownIndex})
	s.expect(call{args: stageIn, input: "brew\x00", index: ownIndex})
	s.expect(call{args: stagedIn, index: ownIndex}).Prints("brew\x00")
	s.expect(call{args: commitIn, index: ownIndex}).Exits(128).PrintsToStderr("fatal: cannot lock ref 'HEAD': is at " + theirs + " but expected " + base)
	s.expect(call{args: headOf}).Prints(theirs + "\n")
	s.expect(call{args: parentOf}).Prints(base + "\n")
	s.expect(call{args: git("diff", "--cached", "--quiet", "HEAD"), index: ownIndex}).Exits(1)
	if committed, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit test", "brew"); committed || err == nil || !strings.Contains(err.Error(), "cannot lock ref 'HEAD'") {
		t.Errorf("Commit(), HEAD moved by a hook = %v, %v; want git's refusal", committed, err)
	}
	checkCalls(t, "HEAD moved by a hook", fake, gitDir, s.want)
}

// cancelling cancels the run once git commit has run, noting each command
// run after on a cancelled context.
type cancelling struct {
	run    runner.Runner
	cancel func()
	dead   []string
}

func (c *cancelling) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	if ctx.Err() != nil {
		c.dead = append(c.dead, cmd.String())
	}
	res, err := c.run.Run(ctx, cmd)
	if slices.Contains(cmd.Args, "commit") {
		c.cancel()
	}
	return res, err
}

// A commit that fails, as a hook or signing does, HEAD where it was, leaves
// the user's index as it was, and nothing of kit's: a hook that reached for
// git's own index, which kit holds, says so, never as git says it, a lock
// to remove. Committed, then the index not caught up, it's committed all
// the same, the error the index's; and a repository git names no git
// folder for is an error. What git prints is what real git does.
func TestCommitFails(t *testing.T) {
	gitDir := gitFolder(t, false)
	lock := filepath.Join(gitDir, "index.lock")
	for _, c := range []struct{ name, stderr, want string }{
		{"signing", "error: gpg failed to sign the data", "error: gpg failed to sign the data"},
		{"a hook reaching for the index", "fatal: Unable to create '" + lock + "': File exists.\n\nAnother git process seems to be running in this repository, or the lock file may be stale\n",
			"a hook of the commit's failed: it reached for git's own index, which kit holds while it commits, rather than the one git gives hooks (GIT_INDEX_FILE)"},
	} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
		s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
		s.expect(call{args: headOf}).Prints(base + "\n")
		s.expect(call{args: git("read-tree", base), index: ownIndex})
		s.expect(call{args: stageIn, input: "brew\x00", index: ownIndex})
		s.expect(call{args: stagedIn, index: ownIndex}).Prints("brew\x00")
		s.expect(call{args: commitIn, index: ownIndex}).Exits(1).PrintsToStderr(c.stderr)
		s.expect(call{args: headOf}).Prints(base + "\n")
		if committed, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit test", "brew"); committed || err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "stale") {
			t.Errorf("%s: Commit() = %v, %v; want %q", c.name, committed, err, c.want)
		}
		checkCalls(t, c.name, fake, gitDir, s.want)
		checkGitFolder(t, c.name, gitDir, usersEntries)
	}

	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	s.expect(call{args: headOf}).Prints(base + "\n")
	s.expect(call{args: git("read-tree", base), index: ownIndex})
	s.expect(call{args: stageIn, input: "brew\x00", index: ownIndex})
	s.expect(call{args: stagedIn, index: ownIndex}).Prints("brew\x00")
	s.expect(call{args: commitIn, index: ownIndex})
	s.expect(call{args: parentOf}).Prints(base + "\n")
	s.expect(call{args: resetIn, input: "brew\x00", index: nextIndex}).Exits(128).PrintsToStderr("fatal: index file corrupt")
	if committed, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit test", "brew"); !committed || err == nil || !strings.Contains(err.Error(), "git's index still shows them as they were") {
		t.Errorf("Commit(), the reset failing = %v, %v; want committed, the index's error", committed, err)
	}
	checkCalls(t, "the reset failing", fake, gitDir, s.want)
	checkGitFolder(t, "the reset failing", gitDir, usersEntries)

	for _, c := range []struct {
		name   string
		prints string
		exit   int
	}{{"not a repository", "", 128}, {"no folder named", "\n", 0}, {"a relative folder", ".git\n", 0}} {
		fake := runnertest.New(t)
		fake.On("git", statusOf("brew")...).Prints(" M brew\x00")
		fake.On("git", gitDirOf...).Prints(c.prints).Exits(c.exit)
		if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit test", "brew"); err == nil {
			t.Errorf("Commit(), %s: no error", c.name)
		}
		if got := len(fake.Calls()); got != 2 {
			t.Errorf("Commit(), %s, ran %q; want nothing after the git folder", c.name, fake.Calls())
		}
	}
}
