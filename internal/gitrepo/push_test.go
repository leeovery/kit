package gitrepo_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The commands of a pull and a push.
var (
	remoteOf    = git("remote")
	branchOf    = git("symbolic-ref", "-q", "HEAD")
	upstreamOf  = git("for-each-ref", "--format=%(upstream) %(upstream:remoteref)", "refs/heads/main")
	fetchIt     = git("fetch", "--prune", "--quiet", "origin")
	upstreamAt  = git("rev-parse", "--verify", "--quiet", upstreamRef)
	behindOf    = git("rev-list", "--count", "HEAD.."+upstreamRef)
	incomingOf  = git("diff", "--name-only", "-z", "--no-renames", "HEAD..."+upstreamRef)
	ignoredOf   = git("ls-files", "-z", "--others", "--ignored", "--exclude-standard")
	stashCreate = git("stash", "create", "kit: your changes before pulling")
	resetHard   = git("reset", "--hard", "--quiet")
	rebase      = git("rebase", "--no-fork-point", "--quiet", upstreamRef)
	abort       = git("rebase", "--abort")
	stashApply  = git("stash", "apply", "--index", "--quiet", kitsStash)
	stashStore  = git("stash", "store", "-m", "kit: your changes before pulling", kitsStash)
	pushIt      = git("push", "--quiet", "origin", "HEAD:refs/heads/main")
)

// tracks is what git prints of the branch's upstream, up, and the branch
// it is on the remote: up "" for none.
func tracks(up string) string {
	if up == "" {
		return " \n"
	}
	return up + " refs/heads/main\n"
}

// upstreamRef is the tests' upstream, origin's main, and upstream the
// commit it's at.
const (
	upstreamRef = "refs/remotes/origin/main"
	upstream    = "51cd7f5e2b6cbb1bd2ff3e2d02cd4f1bbd1c7a33"
)

// kitsStash is the stash git makes of what kit sets aside.
const kitsStash = "bfcfca69ab8231f56369d4652e7ff0a5ffc81478"

// offline is what real git prints when the remote, over ssh, can't be
// reached: why, above its last line.
const offline = "ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"

// conflicted is what real git prints on its standard error as a rebase
// stops on a conflict.
const conflicted = "error: could not apply a4fbd32... mine\nhint: Resolve all conflicts manually, mark them as resolved with\nhint: \"git add/rm <conflicted_files>\", then run \"git rebase --continue\".\nCould not apply a4fbd32... # mine\n"

// pulling scripts a pull as far as the remote's commits HEAD hasn't, as
// real git answers (pulls).
func pulling(fake *runnertest.Fake, gitDir, status, behind string) *sequence {
	s := &sequence{fake: fake}
	s.pulls(gitDir, status, behind)
	return s
}

// pulls expects a pull as far as the remote's commits HEAD hasn't, as real
// git answers: the one remote, origin; the git folder, gitDir; what's
// changed and not committed (status, -z); the branch, main, and its
// upstream, origin's; the fetch; then how many of the upstream's commits
// HEAD hasn't, behind.
func (s *sequence) pulls(gitDir, status, behind string) {
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(gitDir + "\n")
	s.expect(statusOf()...).Prints(status)
	s.expect(branchOf...).Prints("refs/heads/main\n")
	s.expect(upstreamOf...).Prints(tracks(upstreamRef))
	s.expect(fetchIt...)
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Prints(behind + "\n")
}

// rebasing has script, a rebase, stop part way, as real git does on a
// conflict, keeping its folder in gitDir.
func rebasing(t *testing.T, script *runnertest.Script, gitDir string) {
	script.Exits(1).PrintsToStderr(conflicted).Does(func() {
		if err := os.Mkdir(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
			t.Error(err)
		}
	})
}

func push(fake *runnertest.Fake) error {
	return gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
}

func pull(fake *runnertest.Fake) error {
	return gitrepo.Repo{Dir: dir, Run: fake}.Pull(context.Background())
}

// With nothing new on the remote, Push pushes, touching nothing else; Pull
// brings in as Push does, and pushes nothing. The push names the one
// remote, and the upstream's branch there, so nothing in git's config, as
// pushRemote or pushDefault, sends it anywhere else.
func TestPush(t *testing.T) {
	fake := runnertest.New(t)
	s := pulling(fake, t.TempDir(), " M laptop/a\x00", "0")
	s.expect(pushIt...)
	if err := push(fake); err != nil {
		t.Errorf("Push() = %v", err)
	}
	s.check(t, "push")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
	s.expect(statusOf()...)
	s.expect(branchOf...).Prints("refs/heads/main\n")
	s.expect(upstreamOf...).Prints(upstreamRef + " refs/heads/trunk\n")
	s.expect(fetchIt...)
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Prints("0\n")
	s.expect(git("push", "--quiet", "origin", "HEAD:refs/heads/trunk")...)
	if err := push(fake); err != nil {
		t.Errorf("Push(), the upstream another branch = %v", err)
	}
	s.check(t, "the upstream another branch")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), " M laptop/a\x00", "0")
	if err := pull(fake); err != nil {
		t.Errorf("Pull() = %v", err)
	}
	s.check(t, "pull")
}

// With the remote ahead, what's committed here is rebased onto it before
// it's pushed. What's not committed in the files git tracks is set aside
// first in a stash of its own (git stash create), never in git's list of
// stashes, the files reset to the last commit, and put back by that stash
// once rebased, with --index, so what was staged is staged still: an edit
// staged, one not, a new file staged. Untracked files stay where they are.
// With nothing to set aside, git makes no stash, and nothing's reset or put
// back. What git prints is what real git does.
func TestPushRebasesOntoTheRemote(t *testing.T) {
	fake := runnertest.New(t)
	s := pulling(fake, t.TempDir(), "M  shared/staged.txt\x00 M shared/config.toml\x00A  shared/n\x00?? shared/untracked.txt\x00", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...)
	s.expect(rebase...)
	s.expect(stashApply...)
	s.expect(pushIt...)
	if err := push(fake); err != nil {
		t.Errorf("Push(), rebasing = %v", err)
	}
	s.check(t, "rebasing")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), "?? shared/untracked.txt\x00", "2")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...)
	s.expect(rebase...)
	s.expect(pushIt...)
	if err := push(fake); err != nil {
		t.Errorf("Push(), rebasing with nothing to set aside = %v", err)
	}
	s.check(t, "rebasing with nothing to set aside")
}

// With the remote's commits changing what's not committed here, staged or
// not, new or untracked, or in a repository nested here, nothing's set
// aside, rebased or pushed: put back, it would conflict, leaving git's
// markers in a file that may be linked into the home folder. Each is named,
// once. What git prints is what real git does.
func TestPushLeavesAClashAlone(t *testing.T) {
	for _, c := range []struct{ name, status, incoming, says string }{
		{"an edit", " M shared/config.toml\x00M  laptop/x\x00", "kit.toml\x00shared/config.toml\x00", ": commit or undo the change to shared/config.toml first"},
		{"an edit staged and not", "MM kit.toml\x00", "kit.toml\x00", "the change to kit.toml first"},
		{"a file the remote adds too", "?? laptop/new\x00", "laptop/new\x00", "the change to laptop/new first"},
		{"a nested repository", "?? laptop/nested/\x00", "laptop/nested/file\x00", "the change to laptop/nested/ first"},
		{"a file taken out and back", "D  twin\x00?? twin\x00 M a\x00", "twin\x00a\x00", "the change to twin, a first"},
	} {
		fake := runnertest.New(t)
		s := pulling(fake, t.TempDir(), c.status, "1")
		s.expect(incomingOf...).Prints(c.incoming)
		err := push(fake)
		if err == nil || !strings.HasPrefix(err.Error(), "the remote's commits change what's not committed here, so nothing was touched or pushed: ") || !strings.HasSuffix(err.Error(), c.says) {
			t.Errorf("Push(), %s = %v; want it refused, ending %q", c.name, err, c.says)
		}
		s.check(t, c.name)
	}
}

// What's not committed with a file git tracks become a folder, or a folder
// a file, isn't set aside, rebased or pushed, whatever the remote's
// commits change: put back as last committed, the path would take the
// place of what git doesn't track there, deleting it. The path's named. A
// file become a folder is told by the folder on disk where git says it's
// deleted, or changed in type, as git lists no file in a repository of its
// own put there; a folder become a file by the file git doesn't track. A
// file deleted, or become a link, isn't swapped, nor is a path beside
// another's, its name longer. What git prints is what real git does.
func TestPushLeavesASwapAlone(t *testing.T) {
	for _, c := range []struct{ name, status, folder, file, says string }{
		{"a file become a folder", " D f\x00?? f/x\x00", "f", "", "f was a file git tracks and is a folder now, whose files pulling would delete"},
		{"a file taken out, a folder in", "D  f\x00?? f/x\x00", "f", "", "f was a file git tracks and is a folder now"},
		{"a file become a repository", " D laptop/f\x00", "laptop/f/.git", "", "laptop/f was a file git tracks and is a folder now"},
		{"a file become a repository with commits", " T laptop/f\x00", "laptop/f/.git", "", "laptop/f was a file git tracks and is a folder now"},
		{"a folder become a file", " D shared/config.toml\x00 D shared/keep.txt\x00?? shared\x00", "", "shared", "shared was a folder git tracks and is a file now, which pulling would delete"},
	} {
		top := t.TempDir()
		if c.folder != "" {
			if err := os.MkdirAll(filepath.Join(top, c.folder), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if c.file != "" {
			if err := os.WriteFile(filepath.Join(top, c.file), []byte("precious\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		fake := runnertest.New(t)
		s := &sequence{fake: fake, root: top}
		s.pulls(t.TempDir(), c.status, "1")
		err := gitrepo.Repo{Dir: top, Run: fake}.Push(context.Background())
		if err == nil || !strings.HasPrefix(err.Error(), c.says) || !strings.HasSuffix(err.Error(), ", so nothing was touched or pushed: commit or undo the change to "+strings.Fields(c.says)[0]+" first") {
			t.Errorf("Push(), %s = %v; want it refused, saying %q", c.name, err, c.says)
		}
		s.check(t, c.name)
	}

	top := t.TempDir()
	if err := os.Mkdir(filepath.Join(top, "fx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("fx", filepath.Join(top, "link")); err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	s := &sequence{fake: fake, root: top}
	s.pulls(t.TempDir(), " D f\x00?? fx/y\x00 T link\x00 D notes/a\x00?? note\x00", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...)
	s.expect(rebase...)
	s.expect(stashApply...)
	s.expect(pushIt...)
	if err := (gitrepo.Repo{Dir: top, Run: fake}).Push(context.Background()); err != nil {
		t.Errorf("Push(), nothing swapped = %v", err)
	}
	s.check(t, "nothing swapped")
}

// A rebase that stops on a conflict is aborted, by git's own account of it
// (its folder there), leaving the repository as it was, what was set aside
// put back, so nothing's lost, and nothing's pushed. One that won't abort
// is left as git has it, saying how to undo it, what was set aside kept in
// git's list of stashes. One that fails without starting, as when a file
// git doesn't track is in its way, is said as it is, what was set aside put
// back. One that stops as git, set to sign its commits, couldn't sign one
// is aborted so too, and is ErrUnsigned, saying what git said, not a
// conflict; not set to sign, git failing to write one is said as a stop.
// What git prints is what real git does.
func TestPushWhenTheRebaseStops(t *testing.T) {
	gitDir := t.TempDir()
	fake := runnertest.New(t)
	s := pulling(fake, gitDir, " M laptop/a\x00", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...)
	rebasing(t, s.expect(rebase...), gitDir)
	s.expect(abort...).Does(func() {
		if err := os.Remove(filepath.Join(gitDir, "rebase-merge")); err != nil {
			t.Error(err)
		}
	})
	s.expect(stashApply...)
	err := push(fake)
	if err == nil || !strings.Contains(err.Error(), "the remote's changes conflict with this Mac's, so nothing was pushed; the rebase was undone") || strings.Contains(err.Error(), "stash") {
		t.Errorf("Push(), the rebase stopping = %v; want it undone, all put back", err)
	}
	s.check(t, "the rebase stopping")

	gitDir = t.TempDir()
	fake = runnertest.New(t)
	s = pulling(fake, gitDir, " M laptop/a\x00", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...)
	rebasing(t, s.expect(rebase...), gitDir)
	s.expect(abort...).Exits(128).PrintsToStderr("fatal: could not move back to " + base + "\n")
	s.expect(stashStore...)
	err = push(fake)
	if err == nil || !strings.Contains(err.Error(), "the rebase couldn't be undone, so nothing was pushed: git rebase --abort undoes it") ||
		!strings.Contains(err.Error(), `what wasn't committed is kept in git's stash, as "kit: your changes before pulling"`) {
		t.Errorf("Push(), the rebase stuck = %v; want how to undo it, and where what wasn't committed is", err)
	}
	s.check(t, "the rebase stuck")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), " M laptop/a\x00", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...)
	s.expect(rebase...).Exits(1).PrintsToStderr("error: The following untracked working tree files would be overwritten by checkout:\n\tlaptop/b\nPlease move or remove them before you switch branches.\nAborting\n")
	s.expect(stashApply...)
	if err := push(fake); err == nil || !strings.Contains(err.Error(), "couldn't rebase onto the remote's changes, so nothing was pushed") {
		t.Errorf("Push(), the rebase not starting = %v", err)
	}
	s.check(t, "the rebase not starting")

	const unsignedRebase = "error: Couldn't load public key /Users/someone/.ssh/signing.pub: No such file or directory?\n\nerror: failed to write commit object\nCould not apply 894e6ae... # mine\n"
	for _, signs := range []bool{true, false} {
		gitDir := t.TempDir()
		fake := runnertest.New(t)
		s := pulling(fake, gitDir, " M laptop/a\x00", "1")
		s.expect(incomingOf...).Prints("kit.toml\x00")
		s.expect(ignoredOf...)
		s.expect(stashCreate...).Prints(kitsStash + "\n")
		s.expect(resetHard...)
		s.expect(rebase...).Exits(1).PrintsToStderr(unsignedRebase).Does(func() {
			if err := os.Mkdir(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
				t.Error(err)
			}
		})
		s.expect(abort...).Does(func() {
			if err := os.Remove(filepath.Join(gitDir, "rebase-merge")); err != nil {
				t.Error(err)
			}
		})
		if signs {
			s.expect(signing...).Prints("true\n")
		} else {
			s.expect(signing...).Exits(1)
		}
		s.expect(stashApply...)
		err := push(fake)
		const says = "git couldn't sign the commit rebased onto the remote's changes, so nothing was pushed; the rebase was undone: Couldn't load public key /Users/someone/.ssh/signing.pub: No such file or directory?"
		if signs && (!errors.Is(err, gitrepo.ErrUnsigned) || err.Error() != says) {
			t.Errorf("Push(), unable to sign a rebased commit = %v; want %q", err, says)
		}
		if !signs && (errors.Is(err, gitrepo.ErrUnsigned) || err == nil || !strings.HasPrefix(err.Error(), "the remote's changes conflict with this Mac's, so nothing was pushed; the rebase was undone: ")) {
			t.Errorf("Push(), a rebased commit not written, not set to sign = %v; want it said as a stop", err)
		}
		s.check(t, fmt.Sprintf("unable to write a rebased commit, set to sign %v", signs))
	}
}

// What was set aside that won't go back is kept in git's list of stashes,
// as kit's, so nothing's lost, and nothing's pushed, the error saying
// where; storing it failing too, the error names the stash itself. What
// git prints is what real git does.
func TestPushKeepsWhatWontGoBack(t *testing.T) {
	const wontApply = "error: patch failed: kit.toml:1\nerror: kit.toml: patch does not apply\nerror: conflicts in index. Try without --index.\n"
	fake := runnertest.New(t)
	s := pulling(fake, t.TempDir(), "M  kit.toml\x00", "1")
	s.expect(incomingOf...).Prints("laptop/a\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...)
	s.expect(rebase...)
	s.expect(stashApply...).Exits(128).PrintsToStderr(wontApply)
	s.expect(stashStore...)
	err := push(fake)
	if err == nil || !strings.HasPrefix(err.Error(), `nothing was pushed, and what wasn't committed is kept in git's stash, as "kit: your changes before pulling" (git stash apply --index puts it back): `) || !strings.Contains(err.Error(), "conflicts in index") {
		t.Errorf("Push(), what was set aside not going back = %v", err)
	}
	s.check(t, "not going back")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), "M  kit.toml\x00", "1")
	s.expect(incomingOf...).Prints("laptop/a\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...)
	s.expect(rebase...)
	s.expect(stashApply...).Exits(128).PrintsToStderr(wontApply)
	s.expect(stashStore...).Exits(1).PrintsToStderr("error: cannot update refs/stash\n")
	err = push(fake)
	if err == nil || !strings.HasPrefix(err.Error(), "nothing was pushed, and what wasn't committed is in git's stash "+kitsStash+" alone, not in its list (git stash apply --index "+kitsStash+" puts it back): ") {
		t.Errorf("Push(), not going back, nor kept = %v", err)
	}
	s.check(t, "nor kept")
}

// While git is part way through a merge, cherry-pick, revert, rebase or am,
// or a conflict waits to be resolved, Push and Pull refuse, touching
// nothing.
func TestPushRefusesWhileGitIsPartWay(t *testing.T) {
	gitDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitDir, "rebase-apply"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(gitDir + "\n")
	if err := pull(fake); err == nil || !strings.Contains(err.Error(), "a rebase or git am is under way") {
		t.Errorf("Pull(), a rebase under way = %v", err)
	}
	s.check(t, "a rebase under way")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
	s.expect(statusOf()...).Prints("UU kit.toml\x00")
	if err := push(fake); err == nil || !strings.Contains(err.Error(), "a conflict in kit.toml is waiting to be resolved") {
		t.Errorf("Push(), a conflict waiting = %v", err)
	}
	s.check(t, "a conflict waiting")
}

// A branch with no upstream is pushed to the same branch of the one
// remote, which becomes its upstream; with an upstream the remote hasn't
// yet, as a remote with no branches, there's nothing to bring in, and it's
// pushed there. Pull pushes nothing.
func TestPushToANewBranch(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
	s.expect(statusOf()...)
	s.expect(branchOf...).Prints("refs/heads/main\n")
	s.expect(upstreamOf...).Prints(tracks(""))
	s.expect(fetchIt...)
	s.expect(git("push", "--quiet", "-u", "origin", "HEAD")...)
	if err := push(fake); err != nil {
		t.Errorf("Push(), no upstream = %v", err)
	}
	s.check(t, "no upstream")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
	s.expect(statusOf()...)
	s.expect(branchOf...).Prints("refs/heads/main\n")
	s.expect(upstreamOf...).Prints(tracks(upstreamRef))
	s.expect(fetchIt...)
	s.expect(upstreamAt...).Exits(1)
	s.expect(pushIt...)
	if err := push(fake); err != nil {
		t.Errorf("Push(), the upstream not there yet = %v", err)
	}
	s.check(t, "the upstream not there yet")

}

// A clone of one branch whose branch may be missing from the remote, as the
// prefs store's before its first push, or once deleted there, finds git's
// fetch failing for want of it: its remote-tracking ref is forgotten, so
// nothing's brought in and it's pushed anew. Any other repository, as
// kit-config, its branch gone from the remote, pushes nothing, nor does one
// whose fetch fails for want of another branch.
func TestPushToABranchMissingFromTheRemote(t *testing.T) {
	missing := func(fake *runnertest.Fake, ref string) *sequence {
		s := &sequence{fake: fake}
		s.expect(remoteOf...).Prints("origin\n")
		s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
		s.expect(statusOf()...)
		s.expect(branchOf...).Prints("refs/heads/main\n")
		s.expect(upstreamOf...).Prints(tracks(upstreamRef))
		s.expect(fetchIt...).Exits(128).PrintsToStderr(missingRef(ref))
		return s
	}
	fake := runnertest.New(t)
	s := missing(fake, "refs/heads/main")
	s.expect(git("update-ref", "-d", upstreamRef)...)
	s.expect(upstreamAt...).Exits(1)
	s.expect(pushIt...)
	if err := (gitrepo.Repo{Dir: dir, Run: fake, BranchMayBeMissing: true}).Push(context.Background()); err != nil {
		t.Errorf("Push(), the branch missing, as it may be = %v", err)
	}
	s.check(t, "the branch missing, as it may be")

	for _, c := range []struct {
		name, ref string
		may       bool
	}{{"kit-config's branch missing", "refs/heads/main", false}, {"another branch missing", "refs/heads/trunk", true}} {
		fake := runnertest.New(t)
		s := missing(fake, c.ref)
		err := gitrepo.Repo{Dir: dir, Run: fake, BranchMayBeMissing: c.may}.Push(context.Background())
		if err == nil || !strings.HasPrefix(err.Error(), "couldn't bring in the remote's changes, so nothing was pushed: ") {
			t.Errorf("Push(), %s = %v; want nothing pushed", c.name, err)
		}
		s.check(t, c.name)
	}
}

// Two histories with no commit in common, as when two Macs each began the
// prefs store's branch before either pushed: git's diff of the remote's
// changes fails, so every path the remote has is what its commits change.
// With none of them changed here, the rebase replays this Mac's commits
// onto the remote's, and they're pushed; with one changed here, nothing
// is. Only the store's (BranchMayBeMissing): kit-config with no commit in
// common with its upstream, as when its main tracks the store's branch,
// pushes nothing, rebasing nothing onto it. What git prints is what real
// git does.
func TestPushOntoAnUnrelatedHistory(t *testing.T) {
	unrelated := func(fake *runnertest.Fake, status string) *sequence {
		s := pulling(fake, t.TempDir(), status, "1")
		s.expect(incomingOf...).Exits(128).PrintsToStderr("fatal: HEAD..." + upstreamRef + ": no merge base\n")
		return s
	}
	store := func(fake *runnertest.Fake, status string) *sequence {
		s := unrelated(fake, status)
		s.expect(git("merge-base", "HEAD", upstreamRef)...).Exits(1)
		s.expect(git("ls-tree", "-r", "--name-only", "-z", upstreamRef)...).Prints("laptop/domains/a.plist\x00laptop/owner\x00")
		return s
	}
	pushStore := func(fake *runnertest.Fake) error {
		return gitrepo.Repo{Dir: dir, Run: fake, BranchMayBeMissing: true}.Push(context.Background())
	}
	fake := runnertest.New(t)
	s := store(fake, " M studio/owner\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...)
	s.expect(rebase...)
	s.expect(stashApply...)
	s.expect(pushIt...)
	if err := pushStore(fake); err != nil {
		t.Errorf("Push(), onto an unrelated history = %v", err)
	}
	s.check(t, "onto an unrelated history")

	fake = runnertest.New(t)
	s = store(fake, "?? laptop/owner\x00")
	if err := pushStore(fake); err == nil || !strings.HasSuffix(err.Error(), "commit or undo the change to laptop/owner first") {
		t.Errorf("Push(), onto an unrelated history changing what's not committed here = %v", err)
	}
	s.check(t, "onto an unrelated history, a clash")

	fake = runnertest.New(t)
	s = unrelated(fake, " M kit.toml\x00")
	if err := push(fake); err == nil || !strings.HasPrefix(err.Error(), "couldn't read the remote's changes, so nothing was pushed: ") {
		t.Errorf("Push(), kit-config onto an unrelated history = %v; want nothing pushed", err)
	}
	s.check(t, "kit-config onto an unrelated history")
}

// missingRef is what real git prints when a clone of one branch, ref,
// fetches it from a remote that hasn't it, as on another branch of the
// remote's.
func missingRef(ref string) string {
	return "fatal: couldn't find remote ref " + ref + "\n"
}

// Push refuses, pushing nothing, without one remote to push to: none is
// ErrNoRemote; several, or HEAD on no branch, ErrNowhere.
func TestPushNowhere(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", remoteOf...)
	if err := push(fake); !errors.Is(err, gitrepo.ErrNoRemote) || len(fake.Calls()) != 1 {
		t.Errorf("Push() without a remote = %v, running %q", err, fake.Calls())
	}

	fake = runnertest.New(t)
	fake.On("git", remoteOf...).Prints("origin\nbackup\n")
	if err := push(fake); !errors.Is(err, gitrepo.ErrNowhere) || err.Error() != "the repository has 2 remotes, so kit pushes nothing: kit pushes to one alone" || len(fake.Calls()) != 1 {
		t.Errorf("Push() with two remotes = %v, running %q", err, fake.Calls())
	}

	fake = runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
	s.expect(statusOf()...)
	s.expect(branchOf...).Exits(1)
	if err := push(fake); !errors.Is(err, gitrepo.ErrNowhere) || err.Error() != "HEAD isn't on a branch, so kit pushes nothing" {
		t.Errorf("Push() on no branch = %v", err)
	}
	s.check(t, "on no branch")
}

// Git failing is an error, nothing pushed: the remote out of reach, as
// over ssh with no network; the fetch otherwise; counting what's new;
// reading the remote's changes; setting aside what's not committed, its
// stash made or the files reset, which leaves them as they were, so
// nothing's put in git's list of stashes; and the push. What git prints is
// what real git does.
func TestPushFails(t *testing.T) {
	fetching := func(fake *runnertest.Fake) *sequence {
		s := &sequence{fake: fake}
		s.expect(remoteOf...).Prints("origin\n")
		s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
		s.expect(statusOf()...).Prints(" M a\x00")
		s.expect(branchOf...).Prints("refs/heads/main\n")
		s.expect(upstreamOf...).Prints(tracks(upstreamRef))
		return s
	}
	fake := runnertest.New(t)
	s := fetching(fake)
	s.expect(fetchIt...).Exits(128).PrintsToStderr(offline)
	if err := push(fake); err == nil || err.Error() != "couldn't reach the remote, so nothing was pushed" {
		t.Errorf("Push(), offline = %v", err)
	}
	s.check(t, "offline")

	fake = runnertest.New(t)
	s = fetching(fake)
	s.expect(fetchIt...).Exits(128).PrintsToStderr("fatal: Authentication failed for 'https://github.com/someone/kit-config.git/'\n")
	if err := push(fake); err == nil || !strings.HasPrefix(err.Error(), "couldn't bring in the remote's changes, so nothing was pushed: ") {
		t.Errorf("Push(), the fetch failing = %v", err)
	}
	s.check(t, "the fetch failing")

	fake = runnertest.New(t)
	s = fetching(fake)
	s.expect(fetchIt...).Exits(128).PrintsToStderr(missingRef("refs/heads/trunk"))
	if err := push(fake); err == nil || !strings.HasPrefix(err.Error(), "couldn't bring in the remote's changes, so nothing was pushed: ") {
		t.Errorf("Push(), a branch not the upstream's not on the remote = %v", err)
	}
	s.check(t, "a branch not the upstream's not on the remote")

	fake = runnertest.New(t)
	s = fetching(fake)
	s.expect(fetchIt...)
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Exits(128).PrintsToStderr("fatal: bad revision\n")
	if err := push(fake); err == nil || !strings.HasPrefix(err.Error(), "couldn't bring in the remote's changes, so nothing was pushed: ") {
		t.Errorf("Push(), counting failing = %v", err)
	}
	s.check(t, "counting failing")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), " M a\x00", "1")
	s.expect(incomingOf...).Exits(128).PrintsToStderr("fatal: bad object " + upstreamRef + "\n")
	s.expect(git("merge-base", "HEAD", upstreamRef)...).Prints(upstream + "\n")
	if err := (gitrepo.Repo{Dir: dir, Run: fake, BranchMayBeMissing: true}).Push(context.Background()); err == nil || !strings.HasPrefix(err.Error(), "couldn't read the remote's changes, so nothing was pushed: ") {
		t.Errorf("Push(), reading the remote's changes failing = %v", err)
	}
	s.check(t, "reading the remote's changes failing")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), " M a\x00", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Exits(128).PrintsToStderr("fatal: index file corrupt\n")
	if err := push(fake); err == nil || !strings.HasPrefix(err.Error(), "couldn't set aside what's not committed, so it's left as it was and nothing was pushed: ") {
		t.Errorf("Push(), setting aside failing = %v", err)
	}
	s.check(t, "setting aside failing")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), "M  kit.toml\x00", "1")
	s.expect(incomingOf...).Prints("laptop/a\x00")
	s.expect(ignoredOf...)
	s.expect(stashCreate...).Prints(kitsStash + "\n")
	s.expect(resetHard...).Exits(128).PrintsToStderr("fatal: Unable to create '/Users/someone/.config/kit/.git/index.lock': File exists.\n")
	if err := push(fake); err == nil || !strings.HasPrefix(err.Error(), "couldn't set aside what's not committed, so it's left as it was and nothing was pushed: ") {
		t.Errorf("Push(), unable to reset = %v", err)
	}
	s.check(t, "unable to reset")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), "", "0")
	s.expect(pushIt...).Exits(1).PrintsToStderr("error: failed to push some refs to 'github.com:someone/kit-config.git'\n")
	if err := push(fake); err == nil || !strings.HasPrefix(err.Error(), "couldn't push: ") {
		t.Errorf("Push(), the push failing = %v", err)
	}
	s.check(t, "the push failing")
}

// Holding is why Push would hold back now, as far as the last fetch tells,
// fetching nothing and changing nothing: the remote's commits changing
// what's not committed here, a folder become a file, or git part way
// through something; nil with nothing new, no upstream, or nothing
// clashing.
func TestHolding(t *testing.T) {
	holding := func(fake *runnertest.Fake, gitDir, status string) *sequence {
		s := &sequence{fake: fake}
		s.expect(gitDirOf...).Prints(gitDir + "\n")
		s.expect(statusOf()...).Prints(status)
		s.expect(branchOf...).Prints("refs/heads/main\n")
		return s
	}
	hold := func(fake *runnertest.Fake) error {
		return gitrepo.Repo{Dir: dir, Run: fake}.Holding(context.Background())
	}

	fake := runnertest.New(t)
	s := holding(fake, t.TempDir(), " M kit.toml\x00")
	s.expect(upstreamOf...).Prints(tracks(upstreamRef))
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Prints("1\n")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	if err := hold(fake); err == nil || !strings.Contains(err.Error(), "kit.toml") {
		t.Errorf("Holding(), a clash = %v", err)
	}
	s.check(t, "a clash")

	fake = runnertest.New(t)
	s = holding(fake, t.TempDir(), " D shared/config.toml\x00?? shared\x00")
	s.expect(upstreamOf...).Prints(tracks(upstreamRef))
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Prints("1\n")
	if err := hold(fake); err == nil || !strings.HasPrefix(err.Error(), "shared was a folder git tracks and is a file now") {
		t.Errorf("Holding(), a folder become a file = %v", err)
	}
	s.check(t, "a folder become a file")

	fake = runnertest.New(t)
	s = holding(fake, t.TempDir(), " M laptop/a\x00")
	s.expect(upstreamOf...).Prints(tracks(upstreamRef))
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Prints("1\n")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	if err := hold(fake); err != nil {
		t.Errorf("Holding(), nothing clashing = %v", err)
	}
	s.check(t, "nothing clashing")

	fake = runnertest.New(t)
	s = holding(fake, t.TempDir(), " M kit.toml\x00")
	s.expect(upstreamOf...).Prints(tracks(upstreamRef))
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Prints("0\n")
	if err := hold(fake); err != nil {
		t.Errorf("Holding(), nothing new = %v", err)
	}
	s.check(t, "nothing new")

	fake = runnertest.New(t)
	s = holding(fake, t.TempDir(), " M kit.toml\x00")
	s.expect(upstreamOf...).Prints(tracks(""))
	if err := hold(fake); err != nil {
		t.Errorf("Holding(), no upstream = %v", err)
	}
	s.check(t, "no upstream")

	gitDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(gitDirOf...).Prints(gitDir + "\n")
	if err := hold(fake); err == nil || !strings.Contains(err.Error(), "a rebase is under way") {
		t.Errorf("Holding(), a rebase under way = %v", err)
	}
	s.check(t, "a rebase under way")
}

// Waiting is when each commit waiting to be pushed was made: those the
// upstream hasn't; with no upstream, or one not there yet, those on none
// of the remote's branches. Unpushed counts them, none when that can't be
// told, as with two remotes.
func TestWaiting(t *testing.T) {
	waiting := func(fake *runnertest.Fake, up string) *sequence {
		s := &sequence{fake: fake}
		s.expect(remoteOf...).Prints("origin\n")
		s.expect(branchOf...).Prints("refs/heads/main\n")
		s.expect(upstreamOf...).Prints(tracks(up))
		return s
	}
	fake := runnertest.New(t)
	s := waiting(fake, upstreamRef)
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(git("log", "--format=%ct", upstreamRef+"..HEAD")...).Prints("1767323045\n1767236645\n")
	got, err := gitrepo.Repo{Dir: dir, Run: fake}.Waiting(context.Background())
	if want := []time.Time{time.Unix(1767323045, 0), time.Unix(1767236645, 0)}; err != nil || !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("Waiting() = %v, %v; want %v", got, err, want)
	}
	s.check(t, "waiting")

	for _, c := range []struct{ name, up string }{{"no upstream", ""}, {"the upstream not there yet", upstreamRef}} {
		fake := runnertest.New(t)
		s := waiting(fake, c.up)
		if c.up != "" {
			s.expect(upstreamAt...).Exits(1)
		}
		s.expect(git("log", "--format=%ct", "HEAD", "--not", "--remotes=origin")...).Prints("1767323045\n")
		if n := (gitrepo.Repo{Dir: dir, Run: fake}).Unpushed(context.Background()); n != 1 {
			t.Errorf("Unpushed(), %s = %d, want 1", c.name, n)
		}
		s.check(t, c.name)
	}

	fake = runnertest.New(t)
	fake.On("git", remoteOf...).Prints("origin\nbackup\n")
	if n := (gitrepo.Repo{Dir: dir, Run: fake}).Unpushed(context.Background()); n != 0 {
		t.Errorf("Unpushed() with two remotes = %d, want 0", n)
	}
}

// PushURLs are where Push pushes, as git push has them: the one remote's
// push URLs, else its URL, insteadOf applied by git. Without a remote it's
// ErrNoRemote; with several, ErrNowhere, as Push pushes to none.
func TestPushURLs(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(git("remote", "get-url", "--push", "--all", "origin")...).Prints("git@github.com:someone/kit-config.git\nhttps://github.com/someone/kit-config.git\n")
	got, err := gitrepo.Repo{Dir: dir, Run: fake}.PushURLs(context.Background())
	if want := []string{"git@github.com:someone/kit-config.git", "https://github.com/someone/kit-config.git"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("PushURLs() = %q, %v; want %q", got, err, want)
	}
	s.check(t, "push URLs")

	fake = runnertest.New(t)
	fake.On("git", remoteOf...)
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).PushURLs(context.Background()); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("PushURLs() without a remote = %v", err)
	}
	fake = runnertest.New(t)
	fake.On("git", remoteOf...).Prints("origin\nbackup\n")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).PushURLs(context.Background()); !errors.Is(err, gitrepo.ErrNowhere) {
		t.Errorf("PushURLs() with two remotes = %v", err)
	}
}

// RemoteURL is the one remote's fetch URL, as git takes it: without a
// remote it's ErrNoRemote; with several, ErrNowhere.
func TestRemoteURL(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(remoteOf...).Prints("upstream\n")
	s.expect(git("remote", "get-url", "upstream")...).Prints("https://github.com/someone/kit-config.git\n")
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).RemoteURL(context.Background()); got != "https://github.com/someone/kit-config.git" || err != nil {
		t.Errorf("RemoteURL() = %q, %v", got, err)
	}
	s.check(t, "the one remote")

	for remotes, want := range map[string]error{"": gitrepo.ErrNoRemote, "origin\nbackup\n": gitrepo.ErrNowhere} {
		fake := runnertest.New(t)
		fake.On("git", remoteOf...).Prints(remotes)
		if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).RemoteURL(context.Background()); got != "" || !errors.Is(err, want) {
			t.Errorf("RemoteURL(), remotes %q = %q, %v; want %v", remotes, got, err, want)
		}
	}
}

// SetURL is the one remote's URL as set, before git's insteadOf rewrites
// it: without a remote it's ErrNoRemote; with several, ErrNowhere.
func TestSetURL(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(remoteOf...).Prints("upstream\n")
	s.expect(git("config", "--get", "remote.upstream.url")...).Prints("https://github.com/someone/kit-config.git\n")
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).SetURL(context.Background()); got != "https://github.com/someone/kit-config.git" || err != nil {
		t.Errorf("SetURL() = %q, %v", got, err)
	}
	s.check(t, "the one remote's URL as set")

	for remotes, want := range map[string]error{"": gitrepo.ErrNoRemote, "origin\nbackup\n": gitrepo.ErrNowhere} {
		fake := runnertest.New(t)
		fake.On("git", remoteOf...).Prints(remotes)
		if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).SetURL(context.Background()); got != "" || !errors.Is(err, want) {
			t.Errorf("SetURL(), remotes %q = %q, %v; want %v", remotes, got, err, want)
		}
	}
}

// UnderWay is the refusal while a rebase, or another of git's operations,
// is part way through, as when one was cut short: nil with none.
func TestUnderWay(t *testing.T) {
	gitDir := t.TempDir()
	fake := runnertest.New(t)
	fake.On("git", gitDirOf...).Prints(gitDir + "\n")
	repo := gitrepo.Repo{Dir: dir, Run: fake}
	if err := repo.UnderWay(context.Background()); err != nil {
		t.Errorf("UnderWay(), nothing under way = %v", err)
	}
	if err := os.Mkdir(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := repo.UnderWay(context.Background()); err == nil || !strings.Contains(err.Error(), "under way in the repository (rebase-merge)") {
		t.Errorf("UnderWay(), a rebase cut short = %v", err)
	}
}

// URL is a remote's fetch URL, as git takes it, none without that remote.
func TestURL(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote", "get-url", "origin")...).Prints("git@github.com:someone/kit-config.git\n")
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).URL(context.Background(), "origin"); got != "git@github.com:someone/kit-config.git" || err != nil {
		t.Errorf("URL() = %q, %v", got, err)
	}
	fake = runnertest.New(t)
	fake.On("git", git("remote", "get-url", "origin")...).Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).URL(context.Background(), "origin"); got != "" || err != nil {
		t.Errorf("URL() of no such remote = %q, %v", got, err)
	}
}

// Fetched is a file as the remote has it on the branch's upstream, or,
// with none, its branch of the same name, fetched now, nothing merged: the
// new Mac reads the old one's recorded host key so. The remote out of
// reach, or the file not there, is an error.
func TestFetched(t *testing.T) {
	fetched := func(fake *runnertest.Fake, up string) *sequence {
		s := &sequence{fake: fake}
		s.expect(remoteOf...).Prints("origin\n")
		s.expect(branchOf...).Prints("refs/heads/main\n")
		s.expect(upstreamOf...).Prints(tracks(up))
		return s
	}
	read := func(fake *runnertest.Fake) ([]byte, error) {
		return gitrepo.Repo{Dir: dir, Run: fake}.Fetched(context.Background(), "kit.toml")
	}
	for _, up := range []string{upstreamRef, ""} {
		fake := runnertest.New(t)
		s := fetched(fake, up)
		s.expect(fetchIt...)
		s.expect(git("show", upstreamRef+":kit.toml")...).Prints("format = 1\n")
		if got, err := read(fake); string(got) != "format = 1\n" || err != nil {
			t.Errorf("Fetched(), upstream %q = %q, %v", up, got, err)
		}
		s.check(t, "fetched")
	}

	fake := runnertest.New(t)
	s := fetched(fake, upstreamRef)
	s.expect(fetchIt...).Exits(128).PrintsToStderr(offline)
	if _, err := read(fake); err == nil || err.Error() != "couldn't reach origin" {
		t.Errorf("Fetched(), offline = %v", err)
	}
	s.check(t, "offline")

	fake = runnertest.New(t)
	s = fetched(fake, upstreamRef)
	s.expect(fetchIt...)
	s.expect(git("show", upstreamRef+":kit.toml")...).Exits(128).PrintsToStderr("fatal: path 'kit.toml' does not exist in 'refs/remotes/origin/main'\n")
	if _, err := read(fake); err == nil || !strings.HasPrefix(err.Error(), "read kit.toml as origin has it: ") {
		t.Errorf("Fetched(), not there = %v", err)
	}
	s.check(t, "not there")
}
