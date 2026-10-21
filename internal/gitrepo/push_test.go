package gitrepo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The commands of a push.
var (
	wholeStatus = git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")
	incomingOf  = git("diff", "--name-only", "-z", "--no-renames", "HEAD...@{u}")
	foldsCase   = git("config", "--get", "--type=bool", "core.ignorecase")
	ignoredHere = git("ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	stashRef    = git("rev-parse", "--verify", "--quiet", "refs/stash")
	stashPush   = git("stash", "push", "--quiet", "--message", "kit: set aside while rebasing onto the remote")
	stashPop    = git("stash", "pop", "--index", "--quiet", "stash@{0}")
	rebase      = git("rebase", "--no-fork-point", "--quiet")
	abort       = git("rebase", "--abort")
	pushIt      = git("push", "--quiet")
)

// The stashes the tests' git names: the user's own, and the one kit makes.
const (
	theirStash = "1111111111111111111111111111111111111111"
	kitsStash  = "bfcfca69ab8231f56369d4652e7ff0a5ffc81478"
)

// offline is what real git prints when the remote, over ssh, can't be
// reached: why, above its last line.
const offline = "ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"

// pushing scripts a push as far as the rebase, as real git answers: the
// remote; the git folder, and what's changed and not committed (status,
// -z); the fetch, and the upstream's commits HEAD hasn't (behind).
func pushing(fake *runnertest.Fake, gitDir, status, behind string) *sequence {
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	s.expect(call{args: wholeStatus}).Prints(status)
	s.expect(call{args: git("fetch", "--quiet")})
	s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Prints(behind + "\n")
	return s
}

// incoming scripts the remote's changes as git names them: the paths its
// commits change (changed, -z), and, when there are any, git folding case,
// as on macOS's own disks, and the files it ignores here (ignored, -z).
func (s *sequence) incoming(changed, ignored string) {
	s.expect(call{args: incomingOf}).Prints(changed)
	if changed == "" {
		return
	}
	s.expect(call{args: foldsCase}).Prints("true\n")
	s.expect(call{args: ignoredHere}).Prints(ignored)
}

// drift scripts the files on disk that differ from the last commit
// (Changes): git's status, and its count of what differs (numstat, -z).
func (s *sequence) drift(status, numstat string) {
	s.expect(call{args: wholeStatus}).Prints(status)
	s.expect(call{args: headOf}).Prints(base + "\n")
	s.expect(call{args: git("read-tree", base), index: readIndex})
	s.expect(call{args: git("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD"), index: readIndex}).Prints(numstat)
}

// stashes scripts git stash push, git naming before as its latest stash
// before it, after after it, "" for none.
func (s *sequence) stashes(before, after string) {
	names(s.expect(call{args: stashRef}), before)
	s.expect(call{args: stashPush})
	names(s.expect(call{args: stashRef}), after)
}

// pops scripts the look at git's latest stash, latest, then, if it's kit's,
// its pop, and is that.
func (s *sequence) pops(latest string) *runnertest.Script {
	names(s.expect(call{args: stashRef}), latest)
	if latest != kitsStash {
		return nil
	}
	return s.expect(call{args: stashPop})
}

// names has rev-parse --verify --quiet name stash, or none when "", as it
// says by exiting 1.
func names(script *runnertest.Script, stash string) {
	if stash == "" {
		script.Exits(1)
		return
	}
	script.Prints(stash + "\n")
}

// rebasing has script start a rebase that stops part way, as git leaves
// one, its folder in gitDir; aborting, script undoes it.
func rebasing(script *runnertest.Script, gitDir string, stops bool) {
	script.Does(func() {
		path := filepath.Join(gitDir, "rebase-merge")
		if stops {
			_ = os.Mkdir(path, 0o700)
			return
		}
		_ = os.Remove(path)
	})
}

func push(t *testing.T, fake *runnertest.Fake) error {
	t.Helper()
	return gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
}

// pushOrPull is Push, or Pull when pull.
func pushOrPull(t *testing.T, fake *runnertest.Fake, pull bool) error {
	t.Helper()
	if pull {
		return gitrepo.Repo{Dir: dir, Run: fake}.Pull(context.Background())
	}
	return push(t, fake)
}

// With nothing new on the remote, Push pushes, touching nothing else: real
// git's staged work stays staged.
func TestPush(t *testing.T) {
	fake := runnertest.New(t)
	s := pushing(fake, t.TempDir(), "M  base\x00AM unst\x00", "0")
	s.expect(call{args: pushIt})
	if err := push(t, fake); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	checkCalls(t, "nothing new", fake, "", s.want)
}

// Pull brings in the remote's changes as Push does, and pushes nothing.
func TestPull(t *testing.T) {
	fake := runnertest.New(t)
	s := pushing(fake, t.TempDir(), "", "1")
	s.incoming("r\x00", "")
	s.expect(call{args: rebase})
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Pull(context.Background()); err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	checkCalls(t, "pull", fake, "", s.want)
}

// While git is part way through a merge, cherry-pick, revert, rebase or
// am, Push refuses before it fetches, as setting what's not committed
// aside would end it: real git's stash push takes away MERGE_HEAD and
// CHERRY_PICK_HEAD. A conflict waiting to be resolved is refused too. So
// does Pull, the prefs store's first step.
func TestPushRefusesWhileGitIsPartWay(t *testing.T) {
	for _, pull := range []bool{false, true} {
		for _, file := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
			gitDir := t.TempDir()
			if err := os.Mkdir(filepath.Join(gitDir, file), 0o700); err != nil {
				t.Fatal(err)
			}
			fake := runnertest.New(t)
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
			if err := pushOrPull(t, fake, pull); err == nil || !strings.Contains(err.Error(), "under way in the repository ("+file+"): kit's change waits till it's finished") {
				t.Errorf("with %s, pull %v = %v, want it refused", file, pull, err)
			}
			checkCalls(t, file, fake, "", s.want)
		}

		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints("origin\n")
		s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
		s.expect(call{args: wholeStatus}).Prints("UU f\x00")
		if err := pushOrPull(t, fake, pull); err == nil || err.Error() != "a conflict in f is waiting to be resolved: kit's change waits till it's finished" {
			t.Errorf("with a conflict waiting, pull %v = %v, want it refused", pull, err)
		}
		checkCalls(t, "a conflict waiting", fake, "", s.want)
	}
}

// A commit kit held back, as it went onto another commit than it was made
// from, holds back every push and pull while it's in the branch, before
// anything's fetched or rebased, as a rebase would make it anew, unmarked:
// saying how to take it out, keeping its change. Once it isn't in the
// branch, as after git reset --soft, or isn't in the repository at all, the
// mark's cleared, and the push goes on; git failing otherwise clears
// nothing. What git says is what real git does.
func TestPushHoldsBackACommitKitHeld(t *testing.T) {
	const later = "6c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d"
	for _, c := range []struct{ name, head, want string }{
		{"the last commit", made, "kit's commit " + made + " went onto another commit than it was made from, so it may undo what that one changed, and nothing's pushed or brought in while it's in the branch: git reset --soft HEAD~ takes it out, keeping its change, then run kit again"},
		{"under a later commit", later, "git reset --soft " + made + "~ takes it and the commits since out, keeping their changes, then run kit again"},
	} {
		for _, pull := range []bool{false, true} {
			gitDir := t.TempDir()
			hold(t, gitDir)
			fake := runnertest.New(t)
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
			s.expect(call{args: git("merge-base", "--is-ancestor", made, "HEAD")})
			s.expect(call{args: headOf}).Prints(c.head + "\n")
			if err := pushOrPull(t, fake, pull); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s, pull %v = %v, want %q", c.name, pull, err, c.want)
			}
			checkCalls(t, c.name, fake, "", s.want)
			checkGitFolder(t, c.name, gitDir, "", "kit-held")
		}
	}

	for _, c := range []struct {
		name, stderr string
		exit         int
	}{
		{"taken out", "", 1},
		{"gone from the repository", "fatal: Not a valid commit name " + made + "\n", 128},
	} {
		gitDir := t.TempDir()
		hold(t, gitDir)
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints("origin\n")
		s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
		s.expect(call{args: git("merge-base", "--is-ancestor", made, "HEAD")}).Exits(c.exit).PrintsToStderr(c.stderr)
		s.expect(call{args: wholeStatus})
		s.expect(call{args: git("fetch", "--quiet")})
		s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Prints("0\n")
		s.expect(call{args: pushIt})
		if err := push(t, fake); err != nil {
			t.Errorf("%s: Push() error = %v", c.name, err)
		}
		checkCalls(t, c.name, fake, "", s.want)
		checkGitFolder(t, c.name, gitDir, "")
	}

	// git not running at all says nothing of the commit: the mark stays.
	gitDir := t.TempDir()
	hold(t, gitDir)
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	s.expect(call{args: git("merge-base", "--is-ancestor", made, "HEAD")}).Fails(errors.New("git: signal: killed"))
	if err := push(t, fake); err == nil || !strings.Contains(err.Error(), "killed") {
		t.Errorf("Push(), git killed = %v, want its error", err)
	}
	checkCalls(t, "git killed", fake, "", s.want)
	checkGitFolder(t, "git killed", gitDir, "", "kit-held")
}

// hold marks made held back in gitDir, as Commit does.
func hold(t *testing.T, gitDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(gitDir, "kit-held"), []byte(made+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// With the remote ahead, what's committed here is rebased onto it, by the
// upstream's commits since the two parted, never git's fork-point, which
// leaves out a commit the upstream once had, so the rebase changes no more
// than the remote's commits do. Work not committed, none of it changed by
// them, is set aside in a stash of its own first and put back after, by
// that stash, staged or not, as real git keeps it; untracked files alone,
// which git stash leaves, set nothing aside; a stash of the user's own,
// there before, is never touched.
func TestPushRebasesOntoTheRemote(t *testing.T) {
	for _, c := range []struct {
		name, status  string
		before, after string
		stashes       bool
	}{
		{"nothing changed here", "", "", "", false},
		{"work set aside and put back", "M  base\x00 M shared\x00", "", kitsStash, true},
		{"untracked files alone", "?? notes\x00", "", "", false},
		{"the user's own stash, work set aside and put back", "M  base\x00", theirStash, kitsStash, true},
		{"the user's own stash, nothing git stashes", "MM base\x00", theirStash, theirStash, true},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), c.status, "1")
		s.incoming("r\x00", "")
		if c.stashes {
			s.stashes(c.before, c.after)
		}
		s.expect(call{args: rebase})
		if c.after == kitsStash {
			s.pops(kitsStash)
		}
		s.expect(call{args: pushIt})
		if err := push(t, fake); err != nil {
			t.Errorf("%s: Push() error = %v", c.name, err)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// With the remote's commits changing what's not committed here, nothing's
// set aside or rebased, nothing pushed, and Push names the files, and what
// to do with each: a file edited here, new here, or in a repository nested
// here, which would come back with git's markers in it, kit reconcile's to
// commit or undo; a change only staged, its file on disk as last
// committed, which drift doesn't show, the user's to commit or unstage; a
// file, or a folder, git ignores here, which the rebase would replace, as
// real git does without a word; and a folder of the remote's where a file
// is here, or a file where a folder is, which git would refuse to check
// out. What git prints is what real git does.
func TestPushLeavesAClashAlone(t *testing.T) {
	const reconcile = " changed here: commit or undo the change with kit reconcile"
	for _, c := range []struct {
		name, status, incoming, ignored, numstat, want string
	}{
		{name: "a file edited here", status: " M f\x00?? notes\x00", incoming: "f\x00g\x00", numstat: "1\t1\tf\x00", want: "the remote's commits change what's not committed here, so nothing was touched or pushed: f" + reconcile},
		{name: "a file new here", status: "?? r\x00", incoming: "r\x00", want: ": r" + reconcile},
		{name: "a nested repository", status: "?? vendor/tool/\x00", incoming: "vendor/tool/x\x00", want: ": vendor/tool/" + reconcile},
		{name: "a change only staged", status: "MM notes\x00", incoming: "notes\x00", want: ": notes staged here, its file on disk as last committed: git's index is yours, to commit or unstage (git restore --staged notes)"},
		{name: "a file git ignores", incoming: ".gitignore\x00secret.env\x00", ignored: "cache/\x00secret.env\x00", want: ": secret.env ignored here, which the remote's would replace: move it aside"},
		{name: "a folder git ignores", incoming: "cache/blob\x00", ignored: "cache/\x00", want: ": cache/ ignored here"},
		{name: "a file here, the remote's folder", status: "?? x\x00", incoming: "x/y\x00", want: ": x" + reconcile},
		{name: "a folder's file here, the remote's file", status: "?? d/new\x00", incoming: "d\x00", want: ": d/new" + reconcile},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), c.status, "1")
		s.incoming(c.incoming, c.ignored)
		if c.status != "" {
			s.drift(c.status, c.numstat)
		}
		if err := push(t, fake); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// Paths that differ only by case are one where git folds case, as on
// macOS's own disks, and checking out the remote's would meet the file
// here; two where it doesn't, and the rebase goes on.
func TestPushFoldsCaseAsTheDiskDoes(t *testing.T) {
	fake := runnertest.New(t)
	s := pushing(fake, t.TempDir(), "?? notes\x00", "1")
	s.incoming("Notes\x00", "")
	s.drift("?? notes\x00", "")
	if err := push(t, fake); err == nil || !strings.Contains(err.Error(), "notes changed here") {
		t.Errorf("Push() folding case = %v, want notes named", err)
	}
	checkCalls(t, "folding case", fake, "", s.want)

	// Unset, git exits 1; set false, it says so.
	for _, folds := range []struct {
		name, prints string
		exit         int
	}{{"unset", "", 1}, {"set false", "false\n", 0}} {
		fake = runnertest.New(t)
		s = pushing(fake, t.TempDir(), "?? notes\x00", "1")
		s.expect(call{args: incomingOf}).Prints("Notes\x00")
		s.expect(call{args: foldsCase}).Prints(folds.prints).Exits(folds.exit)
		s.expect(call{args: ignoredHere})
		s.expect(call{args: rebase})
		s.expect(call{args: pushIt})
		if err := push(t, fake); err != nil {
			t.Errorf("Push(), core.ignorecase %s = %v", folds.name, err)
		}
		checkCalls(t, "core.ignorecase "+folds.name, fake, "", s.want)
	}
}

// A stash that won't go back is kept, never dropped, and nothing's pushed:
// one real git leaves whole, the tree clean, as when it can't put back
// what was staged, said to be wholly in the stash; one it put back with
// conflicts, its markers in a file, said so, naming the file and what to
// run; one no longer git's latest left be, never popped.
func TestPushKeepsAStashThatWontGoBack(t *testing.T) {
	for _, c := range []struct {
		name, latest, after string
		exit                int
		stderr, want        string
	}{
		{"its staged part won't go back", kitsStash, "", 128, "error: conflicts in index. Try without --index.\n", `what wasn't committed is wholly in git's stash, ` + kitsStash + ` ("kit: set aside while rebasing onto the remote"), as it couldn't be put back, so nothing was pushed: git stash apply ` + kitsStash + ` puts it back`},
		{"back with conflicts", kitsStash, "UU f\x00 M g\x00", 1, "Auto-merging f\nCONFLICT (content): Merge conflict in f\nOn branch main\nUnmerged paths:\n\tboth modified:   f\nIndex was not unstashed.\nThe stash entry is kept in case you need it again.\n", "what wasn't committed came back with conflicts in f: each holds the remote's version and this Mac's, between git's <<<<<<< Updated upstream and >>>>>>> Stashed changes lines, so nothing was pushed: make each what it should be, git add it, then git stash drop, as git's stash, " + kitsStash + ", still holds it all"},
		{"no longer git's latest", theirStash, "", 0, "", "what wasn't committed is wholly in git's stash, " + kitsStash},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), " M f\x00 M g\x00", "1")
		s.incoming("r\x00", "")
		s.stashes("", kitsStash)
		s.expect(call{args: rebase})
		if pop := s.pops(c.latest); pop != nil {
			pop.Exits(c.exit).PrintsToStderr(c.stderr)
			// What's left once it won't go back.
			s.expect(call{args: wholeStatus}).Prints(c.after)
		}
		if err := push(t, fake); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// A rebase that stops on a conflict is aborted, by git's own account of it
// under way, the work set aside put back, and nothing pushed. One git
// couldn't abort, still under way, keeps the stash, named, never putting it
// back onto it; one that never started, as when a pre-rebase hook refuses,
// or a file's in the way the clashes missed, leaves nothing to abort, and
// the work goes back. What git prints is what real git does.
func TestPushWhenTheRebaseStops(t *testing.T) {
	const conflict = "Auto-merging f\nCONFLICT (content): Merge conflict in f\nerror: could not apply 7ff9573... kit\n"
	for _, c := range []struct {
		name, stderr    string
		starts, aborted bool
		want            string
	}{
		{"a conflict", conflict, true, true, "the remote's changes conflict with this Mac's, so nothing was pushed; the rebase was undone"},
		{"a conflict git couldn't abort", conflict, true, false, "git rebase --abort undoes it"},
		{"refused by a hook", "pre-rebase says no\nerror: The pre-rebase hook refused to rebase.\n", false, false, "couldn't rebase onto the remote's changes, so nothing was pushed: git -C " + dir + " rebase --no-fork-point --quiet exited 1: error: The pre-rebase hook refused to rebase."},
		{"a file in the way", "error: The following untracked working tree files would be overwritten by checkout:\n\tnew\nPlease move or remove them before you switch branches.\nAborting\nerror: could not detach HEAD\n", false, false, "couldn't rebase onto the remote's changes, so nothing was pushed: git -C " + dir + " rebase --no-fork-point --quiet exited 1: error: could not detach HEAD"},
	} {
		gitDir := t.TempDir()
		fake := runnertest.New(t)
		s := pushing(fake, gitDir, " M f\x00", "1")
		s.incoming("r\x00", "")
		s.stashes("", kitsStash)
		rebasing(s.expect(call{args: rebase}).Exits(1).PrintsToStderr(c.stderr), gitDir, c.starts)
		if c.starts {
			ab := s.expect(call{args: abort})
			if c.aborted {
				rebasing(ab, gitDir, false)
			} else {
				ab.Exits(128).PrintsToStderr("error: Unable to create '" + gitDir + "/index.lock': File exists.\n\nAnother git process seems to be running in this repository, or the lock file may be stale\nfatal: could not move back to 22f46b0\n")
			}
		}
		if c.aborted || !c.starts {
			s.pops(kitsStash)
		}
		err := push(t, fake)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		if kept := strings.Contains(err.Error(), kitsStash); kept == (c.aborted || !c.starts) {
			t.Errorf("%s: Push() error = %v; the stash named %v", c.name, err, kept)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

func TestPushFails(t *testing.T) {
	fetchFails := func(stderr string) func(*runnertest.Fake) *sequence {
		return func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
			s.expect(call{args: wholeStatus})
			s.expect(call{args: git("fetch", "--quiet")}).Exits(128).PrintsToStderr(stderr)
			return s
		}
	}
	for _, c := range []struct {
		name, want string
		script     func(fake *runnertest.Fake) *sequence
	}{
		{"offline, over https", "couldn't reach the remote, so nothing was pushed", fetchFails("fatal: unable to access 'https://github.com/someone/kit-config.git/': Could not resolve host: github.com\n")},
		{"offline, over ssh", "couldn't reach the remote, so nothing was pushed", fetchFails(offline)},
		{"the remote gone", "couldn't bring in the remote's changes, so nothing was pushed", fetchFails("fatal: 'someone/kit-config.git' does not appear to be a git repository\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n")},
		{"no upstream", "couldn't bring in the remote's changes, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
			s.expect(call{args: wholeStatus})
			s.expect(call{args: git("fetch", "--quiet")})
			s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Exits(128).PrintsToStderr("fatal: no upstream configured for branch 'main'\n")
			return s
		}},
		{"nothing set aside", "couldn't set aside what's not committed, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := pushing(fake, t.TempDir(), " M f\x00", "1")
			s.incoming("r\x00", "")
			s.expect(call{args: stashRef}).Exits(1)
			s.expect(call{args: stashPush}).Exits(1).PrintsToStderr("fatal: Unable to create '/Users/someone/.config/kit/.git/index.lock': File exists.\n\nAnother git process seems to be running in this repository, or the lock file may be stale\n")
			return s
		}},
		{"the push refused", "couldn't push", func(fake *runnertest.Fake) *sequence {
			s := pushing(fake, t.TempDir(), "", "0")
			s.expect(call{args: pushIt}).Exits(1).PrintsToStderr("To github.com:someone/kit-config.git\n ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs to 'github.com:someone/kit-config.git'\n")
			return s
		}},
	} {
		fake := runnertest.New(t)
		s := c.script(fake)
		err := push(t, fake)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.HasPrefix(c.name, "offline") && strings.Contains(err.Error(), "fatal") {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

func TestPushWithoutARemote(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...)
	if err := push(t, fake); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("Push() error = %v, want ErrNoRemote", err)
	}
}

// Holding is why Push would hold back now, as far as the last fetch tells,
// fetching nothing: a clash, or git part way through; nothing when the
// remote's last known commits are all here.
func TestHolding(t *testing.T) {
	holding := func(fake *runnertest.Fake) error {
		return gitrepo.Repo{Dir: dir, Run: fake}.Holding(context.Background())
	}
	settled := func(fake *runnertest.Fake, status, behind string) *sequence {
		s := &sequence{fake: fake}
		s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
		s.expect(call{args: wholeStatus}).Prints(status)
		s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Prints(behind + "\n")
		return s
	}

	fake := runnertest.New(t)
	s := settled(fake, "MM notes\x00", "0")
	if err := holding(fake); err != nil {
		t.Errorf("Holding(), nothing new = %v", err)
	}
	checkCalls(t, "nothing new", fake, "", s.want)

	fake = runnertest.New(t)
	s = settled(fake, "MM notes\x00", "1")
	s.incoming("notes\x00", "")
	s.drift("MM notes\x00", "")
	if err := holding(fake); err == nil || !strings.Contains(err.Error(), "notes staged here") {
		t.Errorf("Holding(), a staged change the remote changed = %v", err)
	}
	checkCalls(t, "a clash", fake, "", s.want)

	gitDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitDir, "MERGE_HEAD"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake = runnertest.New(t)
	fake.On("git", gitDirOf...).Prints(gitDir + "\n")
	if err := holding(fake); err == nil || !strings.Contains(err.Error(), "a merge is under way") {
		t.Errorf("Holding(), mid-merge = %v", err)
	}
}

// Unpushed counts the commits waiting for the upstream: none without one,
// as git says when there isn't.
func TestUnpushed(t *testing.T) {
	for _, c := range []struct {
		prints string
		exit   int
		want   int
	}{{"1\n", 0, 1}, {"0\n", 0, 0}, {"", 128, 0}} {
		fake := runnertest.New(t)
		fake.On("git", git("rev-list", "--count", "@{u}..HEAD")...).Prints(c.prints).Exits(c.exit).PrintsToStderr(map[bool]string{true: "fatal: no upstream configured for branch 'main'\n"}[c.exit != 0])
		if got := (gitrepo.Repo{Dir: dir, Run: fake}).Unpushed(context.Background()); got != c.want {
			t.Errorf("Unpushed() with git printing %q, exit %d = %d, want %d", c.prints, c.exit, got, c.want)
		}
	}
}
