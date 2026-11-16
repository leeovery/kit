package gitrepo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// message is what the tests' commits say.
const message = "kit test"

// The commands of a commit, in the order kit runs them: status of the
// paths; the git folder, and whether git signs; the new files entered as to
// be added; whether the files are on disk as last committed, HEAD first;
// then the commit, or, committing nothing, the new files taken back out.
var (
	gitDirOf = git("rev-parse", "--absolute-git-dir")
	signing  = git("config", "--get", "--type=bool", "commit.gpgsign")
	headOf   = git("rev-parse", "--verify", "--quiet", "HEAD")
)

func statusOf(paths ...string) []string {
	return git(slices.Concat([]string{"--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--"}, paths)...)
}

func intend(paths ...string) []string {
	return git(slices.Concat([]string{"--literal-pathspecs", "add", "--intent-to-add", "--"}, paths)...)
}

func asCommitted(paths ...string) []string {
	return git(slices.Concat([]string{"--literal-pathspecs", "diff", "--quiet", "--no-ext-diff", "HEAD", "--"}, paths)...)
}

func commitOnly(paths ...string) []string {
	return git(slices.Concat([]string{"--literal-pathspecs", "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "--only", "-m", message, "--"}, paths)...)
}

func unintend(paths ...string) []string {
	return git(slices.Concat([]string{"--literal-pathspecs", "rm", "--cached", "--quiet", "--"}, paths)...)
}

// lockHeld is what real git says when another git holds the index.
const lockHeld = "fatal: Unable to create '" + dir + "/.git/index.lock': File exists.\n\nAnother git process seems to be running in this repository, or the lock file may be stale\n"

// unsigned is what real git says when it can't sign a commit, its signing
// key not on the Mac.
const unsigned = "error: Couldn't load public key /Users/someone/.ssh/signing.pub: No such file or directory?\n\nfatal: failed to write commit object\n"

// begins scripts a commit's commands up to the new files, added, entered in
// git's index: status of paths, status git's answer, then the git folder,
// and whether git signs, signs.
func (s *sequence) begins(t *testing.T, status string, signs bool, added []string, paths ...string) {
	t.Helper()
	s.expect(statusOf(paths...)...).Prints(status)
	s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
	if signs {
		s.expect(signing...).Prints("true\n")
	} else {
		s.expect(signing...).Exits(1)
	}
	if len(added) > 0 {
		s.expect(intend(added...)...)
	}
}

// differs scripts git saying files differ on disk from the last commit.
func (s *sequence) differs(files ...string) {
	s.expect(headOf...).Prints(base + "\n")
	s.expect(asCommitted(files...)...).Exits(1)
}

func commit(fake *runnertest.Fake, paths ...string) (bool, error) {
	return gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) {}}.Commit(context.Background(), message, paths...)
}

// Commit commits each of the paths whole, as it's on disk, and nothing
// else: git's own commit of those files alone (--only), the user's staged
// and unstaged changes to any other file left as they are, never named. A
// file new to git is entered in its index as to be added (git add -N) first,
// and it alone, never git add -A, so a git commit by anyone else meanwhile
// takes none of kit's files; one already in git's index, staged as new or
// to be, is entered by nothing. A folder is its every file. What git can't
// keep is left out: a file added then gone before it was committed, in
// neither the last commit nor on disk, and a repository nested in this one.
// A file git lists twice, taken out of its index and back untracked, is
// committed once. No hook runs (core.hooksPath=/dev/null: --no-verify
// still runs prepare-commit-msg and post-commit). Each status is what real
// git prints.
func TestCommit(t *testing.T) {
	for _, c := range []struct {
		name   string
		paths  []string
		status string
		// added are the files kit enters in git's index as new, and files
		// those it commits.
		added, files []string
	}{
		{
			name:   "an edit, a deletion, and new files in a new folder",
			paths:  []string{"laptop"},
			status: " M laptop/declarations\x00 D laptop/gone.txt\x00?? laptop/new.txt\x00?? laptop/steps/thing/run\x00",
			added:  []string{"laptop/new.txt", "laptop/steps/thing/run"},
			files:  []string{"laptop/declarations", "laptop/gone.txt", "laptop/new.txt", "laptop/steps/thing/run"},
		},
		{
			name:   "files edited, staged, partly staged or not",
			paths:  []string{"a", "b", "c"},
			status: "M  a\x00MM b\x00 M c\x00",
			files:  []string{"a", "b", "c"},
		},
		{
			name:   "deletions, staged or not",
			paths:  []string{"a", "b"},
			status: "D  a\x00 D b\x00",
			files:  []string{"a", "b"},
		},
		{
			name:   "new files already in git's index",
			paths:  []string{"laptop"},
			status: "A  laptop/n\x00 A laptop/ita\x00AM laptop/m\x00",
			files:  []string{"laptop/n", "laptop/ita", "laptop/m"},
		},
		{
			name:   "what git can't keep left out",
			paths:  []string{"laptop"},
			status: " M laptop/a\x00AD laptop/gone\x00?? laptop/nested/\x00",
			files:  []string{"laptop/a"},
		},
		{
			name:   "a file taken out of git's index, back untracked and changed",
			paths:  []string{"kept"},
			status: "D  kept\x00?? kept\x00",
			added:  []string{"kept"},
			files:  []string{"kept"},
		},
		{
			name:   "a name git would take for a pattern",
			paths:  []string{"a[1].txt"},
			status: "?? a[1].txt\x00",
			added:  []string{"a[1].txt"},
			files:  []string{"a[1].txt"},
		},
	} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.begins(t, c.status, false, c.added, c.paths...)
		s.differs(c.files...)
		s.expect(commitOnly(c.files...)...)
		if committed, err := commit(fake, c.paths...); !committed || err != nil {
			t.Errorf("Commit(), %s = %v, %v; want committed", c.name, committed, err)
		}
		s.check(t, c.name)
	}
}

// Nothing to commit is no error, and commits nothing: no paths; none
// changed; only what git can't keep; files whose difference is only in
// git's index, as the user staged it, on disk as last committed; and a file
// taken out of git's index and back untracked as it was, whose entry as to
// be added is taken back out (git rm --cached), so git's index is as kit
// found it. What git prints is what real git does.
func TestCommitWithNothingToCommit(t *testing.T) {
	fake := runnertest.New(t)
	if committed, err := commit(fake); committed || err != nil || len(fake.Calls()) != 0 {
		t.Errorf("Commit() of no paths = %v, %v, running %q", committed, err, fake.Calls())
	}

	for _, c := range []struct{ name, status string }{
		{"none changed", ""},
		{"only what git can't keep", "AD laptop/gone\x00?? laptop/nested/\x00"},
	} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(statusOf("laptop")...).Prints(c.status)
		if committed, err := commit(fake, "laptop"); committed || err != nil {
			t.Errorf("Commit(), %s = %v, %v", c.name, committed, err)
		}
		s.check(t, c.name)
	}

	fake = runnertest.New(t)
	s := &sequence{fake: fake}
	s.begins(t, "MM same\x00", false, nil, "same")
	s.expect(headOf...).Prints(base + "\n")
	s.expect(asCommitted("same")...)
	if committed, err := commit(fake, "same"); committed || err != nil {
		t.Errorf("Commit(), only staged = %v, %v", committed, err)
	}
	s.check(t, "only staged")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, "D  twin\x00?? twin\x00", false, []string{"twin"}, "twin")
	s.expect(headOf...).Prints(base + "\n")
	s.expect(asCommitted("twin")...)
	s.expect(unintend("twin")...)
	if committed, err := commit(fake, "twin"); committed || err != nil {
		t.Errorf("Commit(), back untracked as it was = %v, %v", committed, err)
	}
	s.check(t, "back untracked as it was")
}

// Before the repository's first commit there's no last commit to compare
// with: the files are committed.
func TestCommitBeforeTheFirst(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.begins(t, "?? laptop/declarations\x00", false, []string{"laptop/declarations"}, "laptop")
	s.expect(headOf...).Exits(1)
	s.expect(commitOnly("laptop/declarations")...)
	if committed, err := commit(fake, "laptop"); !committed || err != nil {
		t.Errorf("Commit() before the first = %v, %v; want committed", committed, err)
	}
	s.check(t, "before the first")
}

// The commit's signed as git commit signs it, by the user's own config: kit
// adds nothing to the command. Set to sign and unable to, git commits
// nothing, which is ErrUnsigned, saying what git said, as when the key
// isn't on the Mac yet, the new files taken back out of git's index; a
// failure with nothing set to sign isn't. What git prints is what real git
// does.
func TestCommitSigns(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.begins(t, " M a\x00?? n\x00", true, []string{"n"}, "a", "n")
	s.differs("a", "n")
	s.expect(commitOnly("a", "n")...)
	if committed, err := commit(fake, "a", "n"); !committed || err != nil {
		t.Errorf("Commit(), signed = %v, %v; want committed", committed, err)
	}
	s.check(t, "signed")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, " M a\x00?? n\x00", true, []string{"n"}, "a", "n")
	s.differs("a", "n")
	s.expect(commitOnly("a", "n")...).Exits(128).PrintsToStderr(unsigned)
	s.expect(unintend("n")...)
	committed, err := commit(fake, "a", "n")
	if committed || !errors.Is(err, gitrepo.ErrUnsigned) || err.Error() != "git couldn't sign the commit: Couldn't load public key /Users/someone/.ssh/signing.pub: No such file or directory?" {
		t.Errorf("Commit(), unable to sign = %v, %v; want it unsigned, saying git's error", committed, err)
	}
	s.check(t, "unable to sign")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, " M a\x00", true, nil, "a")
	s.differs("a")
	s.expect(commitOnly("a")...).Exits(128).PrintsToStderr("fatal: failed to write commit object\n")
	if _, err := commit(fake, "a"); !errors.Is(err, gitrepo.ErrUnsigned) || err.Error() != "git couldn't sign the commit: fatal: failed to write commit object" {
		t.Errorf("Commit(), unable to sign, without an error line = %v; want it unsigned, saying git's last line", err)
	}

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, " M a\x00", false, nil, "a")
	s.differs("a")
	s.expect(commitOnly("a")...).Exits(128).PrintsToStderr("fatal: failed to write commit object\n")
	if _, err := commit(fake, "a"); err == nil || errors.Is(err, gitrepo.ErrUnsigned) {
		t.Errorf("Commit(), failing, not set to sign = %v; want git's error, not unsigned", err)
	}
}

// Another git holding the index, as git status does a moment, kit tries
// again a while, its new files entered and the commit made once it lets
// go. Held throughout, kit gives up, saying git's words, and commits
// nothing, the new files taken back out of git's index. What git prints is
// what real git does.
func TestCommitWaitsForGitsLock(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.begins(t, " M a\x00?? n\x00", false, nil, "a", "n")
	s.expect(intend("n")...).Exits(128).PrintsToStderr(lockHeld)
	s.expect(intend("n")...).Exits(128).PrintsToStderr(lockHeld)
	s.expect(intend("n")...)
	s.differs("a", "n")
	s.expect(commitOnly("a", "n")...).Exits(128).PrintsToStderr(lockHeld)
	s.expect(commitOnly("a", "n")...)
	var slept []time.Duration
	repo := gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(d time.Duration) { slept = append(slept, d) }}
	if committed, err := repo.Commit(context.Background(), message, "a", "n"); !committed || err != nil {
		t.Errorf("Commit(), the lock let go = %v, %v; want committed", committed, err)
	}
	s.check(t, "the lock let go")
	if want := slices.Repeat([]time.Duration{100 * time.Millisecond}, 3); !slices.Equal(slept, want) {
		t.Errorf("Commit() slept %v while git held the index, want %v", slept, want)
	}

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, " M a\x00?? n\x00", false, []string{"n"}, "a", "n")
	s.differs("a", "n")
	for range 20 {
		s.expect(commitOnly("a", "n")...).Exits(128).PrintsToStderr(lockHeld)
	}
	s.expect(unintend("n")...)
	committed, err := commit(fake, "a", "n")
	if committed || err == nil || !strings.Contains(err.Error(), "another git holds the repository's index, so kit's change waits till it's done (fatal: Unable to create '"+dir+"/.git/index.lock': File exists.)") {
		t.Errorf("Commit(), the lock held = %v, %v; want nothing committed, naming the lock", committed, err)
	}
	s.check(t, "the lock held")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, "?? n\x00", false, nil, "n")
	for range 20 {
		s.expect(intend("n")...).Exits(128).PrintsToStderr(lockHeld)
	}
	if committed, err := commit(fake, "n"); committed || err == nil || !strings.Contains(err.Error(), "index.lock") {
		t.Errorf("Commit(), the lock held as it enters new files = %v, %v; want nothing committed, naming the lock", committed, err)
	}
	s.check(t, "the lock held as it enters new files")
}

// While git is part way through a merge, cherry-pick, revert, rebase or am,
// a commit would finish it, or be lost to it: kit refuses, naming what's
// under way, by the file git keeps in its folder while it is, and runs
// nothing more.
func TestCommitRefusesWhileGitIsPartWay(t *testing.T) {
	for _, c := range []struct{ file, says string }{
		{"MERGE_HEAD", "a merge is under way"},
		{"CHERRY_PICK_HEAD", "a cherry-pick is under way"},
		{"REVERT_HEAD", "a revert is under way"},
		{"rebase-merge", "a rebase is under way"},
		{"rebase-apply", "a rebase or git am is under way"},
		{"sequencer", "a cherry-pick or revert is under way"},
	} {
		gitDir := t.TempDir()
		if err := os.Mkdir(filepath.Join(gitDir, c.file), 0o700); err != nil {
			t.Fatal(err)
		}
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(statusOf("a")...).Prints(" M a\x00")
		s.expect(gitDirOf...).Prints(gitDir + "\n")
		if committed, err := commit(fake, "a"); committed || err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("Commit(), %s there = %v, %v; want %q", c.file, committed, err, c.says)
		}
		s.check(t, c.file)
	}
}

// A conflict in the paths waiting to be resolved is refused, naming it,
// before anything else runs. Real git prints UU, AA and UD for these; the
// rest are git's other codes for a conflict.
func TestCommitRefusesAConflict(t *testing.T) {
	for _, code := range []string{"UU", "AA", "DD", "AU", "UA", "DU", "UD"} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(statusOf("laptop")...).Prints(" M laptop/a\x00" + code + " laptop/b\x00")
		if committed, err := commit(fake, "laptop"); committed || err == nil || !strings.Contains(err.Error(), "a conflict in laptop/b is waiting to be resolved") {
			t.Errorf("Commit(), %s = %v, %v; want the conflict named", code, committed, err)
		}
		s.check(t, code)
	}
}

// ctxs is a runner that notes whether the context each git command ran in
// was cancelled, by git's command: its first argument after git's own
// options.
type ctxs struct {
	*runnertest.Fake
	mu        sync.Mutex
	cancelled map[string]bool
}

func (c *ctxs) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	args := cmd.Args[2:]
	if i := slices.IndexFunc(args, func(arg string) bool { return !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") }); i >= 0 {
		c.mu.Lock()
		c.cancelled[args[i]] = ctx.Err() != nil
		c.mu.Unlock()
	}
	return c.Fake.Run(ctx, cmd)
}

// Begun, the commit's made whatever becomes of the run: a run cancelled
// still commits, as git stopped once HEAD has moved leaves its index behind
// it. Taking the new files back out goes on too.
func TestCommitFinishesOnceBegun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.begins(t, "?? n\x00", false, []string{"n"}, "n")
	s.differs("n")
	s.expect(commitOnly("n")...)
	run := &ctxs{Fake: fake, cancelled: map[string]bool{}}
	if committed, err := (gitrepo.Repo{Dir: dir, Run: run}).Commit(ctx, message, "n"); !committed || err != nil {
		t.Errorf("Commit(), cancelled = %v, %v; want committed", committed, err)
	}
	if !run.cancelled["status"] || run.cancelled["commit"] {
		t.Errorf("Commit(), cancelled, ran status cancelled %v and the commit cancelled %v; want the commit made regardless", run.cancelled["status"], run.cancelled["commit"])
	}

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, "?? n\x00", false, []string{"n"}, "n")
	s.differs("n")
	s.expect(commitOnly("n")...).Exits(128).PrintsToStderr("fatal: something went wrong\n")
	s.expect(unintend("n")...)
	run = &ctxs{Fake: fake, cancelled: map[string]bool{}}
	if _, err := (gitrepo.Repo{Dir: dir, Run: run}).Commit(ctx, message, "n"); err == nil || run.cancelled["rm"] {
		t.Errorf("Commit(), cancelled and failing = %v, the new file taken back out cancelled %v; want an error, taken out regardless", err, run.cancelled["rm"])
	}
}

// Git failing at any point is an error, nothing committed: its status, the
// git folder, its signing setting, entering the new files or comparing them
// with the last commit, the new files then taken back out; and its commit,
// as real git's of a tracked file become a folder, which it refuses,
// saying so, a failure to take the new files back out said too. What git
// prints is what real git does.
func TestCommitFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", statusOf("a")...).Exits(128).PrintsToStderr("fatal: not a git repository (or any of the parent directories): .git\n")
	if committed, err := commit(fake, "a"); committed || err == nil {
		t.Errorf("Commit(), status failing = %v, %v", committed, err)
	}

	for _, c := range []struct {
		name   string
		prints string
		exit   int
	}{{"not a repository", "", 128}, {"no folder named", "\n", 0}, {"a relative folder", ".git\n", 0}} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(statusOf("a")...).Prints(" M a\x00")
		s.expect(gitDirOf...).Prints(c.prints).Exits(c.exit)
		if committed, err := commit(fake, "a"); committed || err == nil {
			t.Errorf("Commit(), %s = %v, %v", c.name, committed, err)
		}
		s.check(t, c.name)
	}

	fake = runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(statusOf("a")...).Prints(" M a\x00")
	s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
	s.expect(signing...).Exits(3).PrintsToStderr("fatal: bad config line 1 in file .git/config\n")
	if committed, err := commit(fake, "a"); committed || err == nil {
		t.Errorf("Commit(), the signing setting unreadable = %v, %v", committed, err)
	}
	s.check(t, "the signing setting unreadable")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, "?? n\x00", false, nil, "n")
	s.expect(intend("n")...).Exits(128).PrintsToStderr("fatal: index file corrupt\n")
	if committed, err := commit(fake, "n"); committed || err == nil || !strings.Contains(err.Error(), "index file corrupt") {
		t.Errorf("Commit(), entering the new files failing = %v, %v", committed, err)
	}
	s.check(t, "entering the new files failing")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, "?? n\x00", false, []string{"n"}, "n")
	s.expect(headOf...).Prints(base + "\n")
	s.expect(asCommitted("n")...).Exits(128).PrintsToStderr("fatal: unable to read 0e0bf17a\n")
	s.expect(unintend("n")...)
	if committed, err := commit(fake, "n"); committed || err == nil || !strings.Contains(err.Error(), "unable to read") {
		t.Errorf("Commit(), comparing failing = %v, %v", committed, err)
	}
	s.check(t, "comparing failing")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.begins(t, " D f\x00?? f/x\x00", false, []string{"f/x"}, "f", "f/x")
	s.differs("f", "f/x")
	s.expect(commitOnly("f", "f/x")...).Exits(128).PrintsToStderr("error: 'f' does not have a commit checked out\nerror: unable to index file 'f'\nfatal: updating files failed\n")
	s.expect(unintend("f/x")...).Exits(128).PrintsToStderr("fatal: index file corrupt\n")
	committed, err := commit(fake, "f", "f/x")
	if committed || err == nil || !strings.Contains(err.Error(), "updating files failed") || !strings.Contains(err.Error(), "index file corrupt") {
		t.Errorf("Commit(), a file become a folder = %v, %v; want git's refusal, and its failure to take the new file back out", committed, err)
	}
	s.check(t, "a file become a folder")
}
