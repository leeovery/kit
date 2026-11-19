package gitrepo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/state"
)

// The commands bringing in the remote's commits adds to a pull's.
var (
	aheadOf = git("rev-list", "--count", upstreamRef+"..HEAD")
	moveOn  = git("merge", "--ff-only", "--no-overwrite-ignore", "--quiet", upstreamRef)
)

// bringing scripts BringIn as far as the remote's commits HEAD hasn't, as
// real git answers, as far as a pull's (pulls): behind of them.
func bringing(fake *runnertest.Fake, gitDir, status, behind string) *sequence {
	return pulling(fake, gitDir, status, behind)
}

// fetchingIn scripts BringIn as far as the fetch, which it leaves to the
// test.
func fetchingIn(fake *runnertest.Fake, gitDir, status string) *sequence {
	s := &sequence{fake: fake}
	s.fetches(gitDir, status)
	return s
}

func bringIn(ctx context.Context, fake *runnertest.Fake) (int, error) {
	return gitrepo.Repo{Dir: dir, Run: fake}.BringIn(ctx)
}

// With nothing committed here that the remote hasn't, the branch moves on
// to the remote's commits, git merge --ff-only, which writes only the files
// they change, whatever's changed and not committed here, staged or not,
// new or untracked, left exactly as it is: nothing set aside, nothing
// reset. How many came in is said. With nothing new, nothing moves. Real
// git, on scratch repositories, leaves an edit, a staged change and an
// untracked file as they were, the same inode and time, through such a
// merge.
func TestBringInMovesOnLeavingEditsAlone(t *testing.T) {
	fake := runnertest.New(t)
	s := bringing(fake, t.TempDir(), "M  shared/staged.txt\x00 M shared/config.toml\x00?? shared/untracked.txt\x00", "2")
	s.expect(incomingOf...).Prints("kit.toml\x00laptop/declarations\x00")
	s.expect(ignoredOf...)
	s.expect(aheadOf...).Prints("0\n")
	s.expect(moveOn...)
	if n, err := bringIn(context.Background(), fake); n != 2 || err != nil {
		t.Errorf("BringIn() = %d, %v; want 2 brought in", n, err)
	}
	s.check(t, "moving on")

	fake = runnertest.New(t)
	s = bringing(fake, t.TempDir(), " M shared/config.toml\x00", "0")
	if n, err := bringIn(context.Background(), fake); n != 0 || err != nil {
		t.Errorf("BringIn(), nothing new = %d, %v", n, err)
	}
	s.check(t, "nothing new")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
	s.expect(statusOf()...)
	s.expect(branchOf...).Prints("refs/heads/main\n")
	s.expect(upstreamOf...).Prints(tracks(""))
	if n, err := bringIn(context.Background(), fake); n != 0 || err != nil {
		t.Errorf("BringIn(), no upstream = %d, %v", n, err)
	}
	s.check(t, "no upstream")

}

// With the remote's commits changing what's not committed here, nothing's
// brought in, and what's changed is named: moving on would overwrite it,
// as real git refuses to. Nor is it when they add a file git ignores
// that's here: real git's merge overwrites one without a word, unless told
// not to (--no-overwrite-ignore), when it refuses, as the rebase never
// does. A merge git refuses anyway is said, nothing brought in.
func TestBringInLeavesAClashAlone(t *testing.T) {
	fake := runnertest.New(t)
	s := bringing(fake, t.TempDir(), " M shared/config.toml\x00?? laptop/new\x00 M laptop/a\x00", "1")
	s.expect(incomingOf...).Prints("shared/config.toml\x00laptop/new\x00")
	n, err := bringIn(context.Background(), fake)
	if n != 0 || err == nil || err.Error() != "the remote's commits change what's not committed here, so nothing was touched or brought in: commit or undo the change to shared/config.toml, laptop/new first" {
		t.Errorf("BringIn(), a clash = %d, %v", n, err)
	}
	s.check(t, "a clash")

	fake = runnertest.New(t)
	s = bringing(fake, t.TempDir(), "", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00shared/cache.tmp\x00")
	s.expect(ignoredOf...).Prints("shared/cache.tmp\x00laptop/cache.tmp\x00")
	n, err = bringIn(context.Background(), fake)
	if n != 0 || err == nil || err.Error() != "the remote's commits add shared/cache.tmp, which git ignores here, so nothing was touched or brought in: move it aside first" {
		t.Errorf("BringIn(), an ignored file the remote adds = %d, %v", n, err)
	}
	s.check(t, "an ignored file the remote adds")

	fake = runnertest.New(t)
	s = bringing(fake, t.TempDir(), "", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(aheadOf...).Prints("0\n")
	s.expect(moveOn...).Exits(1).PrintsToStderr("error: The following untracked working tree files would be overwritten by merge:\n\tkit.tmp\nPlease move or remove them before you merge.\nAborting\n")
	if n, err := bringIn(context.Background(), fake); n != 0 || err == nil || !strings.HasPrefix(err.Error(), "couldn't move on to the remote's commits, so nothing was brought in: ") {
		t.Errorf("BringIn(), the merge refused = %d, %v", n, err)
	}
	s.check(t, "the merge refused")
}

// Commits waiting to be pushed are rebased onto the remote's, as Pull
// rebases them, while nothing git tracks is changed and not committed:
// files it doesn't track stay where they are. Something it tracks changed,
// staged or not, nothing's brought in, the commits kept as they are, as
// rebasing would set it aside and put it back, writing it twice. So too
// with git set to sign its commits: the rebase would ask for each with
// nobody there, and real git, unable to sign, stops it part way. A rebase
// that stops on a conflict is aborted, leaving the commits as they were.
func TestBringInRebasesWhatsWaiting(t *testing.T) {
	fake := runnertest.New(t)
	s := bringing(fake, t.TempDir(), "?? shared/untracked.txt\x00", "2")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(aheadOf...).Prints("1\n")
	s.expect(signing...).Exits(1)
	s.expect(rebase...)
	if n, err := bringIn(context.Background(), fake); n != 2 || err != nil {
		t.Errorf("BringIn(), commits waiting = %d, %v; want 2 brought in", n, err)
	}
	s.check(t, "commits waiting")

	for _, status := range []string{" M laptop/a\x00", "M  laptop/a\x00?? laptop/b\x00"} {
		fake := runnertest.New(t)
		s := bringing(fake, t.TempDir(), status, "1")
		s.expect(incomingOf...).Prints("kit.toml\x00")
		s.expect(ignoredOf...)
		s.expect(aheadOf...).Prints("1\n")
		s.expect(signing...).Exits(1)
		n, err := bringIn(context.Background(), fake)
		if n != 0 || err == nil || !strings.HasSuffix(err.Error(), "so nothing was brought in: commit or undo the change to laptop/a first") {
			t.Errorf("BringIn(), commits waiting and %q not committed = %d, %v; want nothing brought in", status, n, err)
		}
		s.check(t, "commits waiting and an edit")
	}

	fake = runnertest.New(t)
	s = bringing(fake, t.TempDir(), "", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(aheadOf...).Prints("1\n")
	s.expect(signing...).Prints("true\n")
	n, err := bringIn(context.Background(), fake)
	if n != 0 || err == nil || !strings.HasSuffix(err.Error(), "so nothing was brought in: the next push, by hand, rebases and signs them") {
		t.Errorf("BringIn(), commits waiting, git signing = %d, %v; want nothing brought in", n, err)
	}
	s.check(t, "commits waiting, git signing")

	gitDir := t.TempDir()
	fake = runnertest.New(t)
	s = bringing(fake, gitDir, "", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(ignoredOf...)
	s.expect(aheadOf...).Prints("1\n")
	s.expect(signing...).Exits(1)
	rebasing(t, s.expect(rebase...), gitDir)
	s.expect(abort...).Does(func() {
		if err := os.Remove(filepath.Join(gitDir, "rebase-merge")); err != nil {
			t.Error(err)
		}
	})
	n, err = bringIn(context.Background(), fake)
	if n != 0 || err == nil || !strings.HasPrefix(err.Error(), "the remote's changes conflict with this Mac's, so nothing was brought in; the rebase was undone: ") {
		t.Errorf("BringIn(), the rebase stopping = %d, %v", n, err)
	}
	s.check(t, "the rebase stopping")
}

// The commits real git names as another Mac rewrites the remote's history:
// the one it dropped, which this Mac had pulled, and where the two
// histories meet, before it.
const (
	dropped = "8d2c41f0b6a94e1f2d7c3b5a6e9f0a1b2c3d4e5f"
	before  = "3a7b9c1d5e2f4a6b8c0d1e3f5a7b9c2d4e6f8a0b"
)

// rewriting scripts git's answers, once fetched, as HEAD holds a commit
// the remote's history dropped, as real git gives them, whether behind,
// ahead or both: the upstream's reflog has HEAD fork from it at the
// dropped commit, past where the two meet now.
func (s *sequence) rewriting() {
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(forkPointOf...).Prints(dropped + "\n")
	s.expect(mergeBaseOf...).Prints(before + "\n")
}

// While HEAD holds a commit the remote's rewritten history dropped, as
// another Mac's forced push does, nothing's brought in or pushed, nothing
// touched: rebasing onto it, or pushing, would put the commit back. The
// upstream's reflog keeps the commit, so real git's answers are the same
// on the first run after the rewrite, on the next, its fetch bringing
// nothing new, and after a fetch by hand, which the reflog outlives: every
// one is refused. Pull and Push refuse as BringIn does. Once HEAD holds
// the commit no more, as after git reset --hard to the upstream, or a
// rebase --onto it leaving the commit out, real git's fork point and
// merge base agree, and runs carry on; as they do with no fork point
// (exit 1), as once the reflog's record of the commit has expired.
func TestARewrittenHistoryIsLeftAlone(t *testing.T) {
	const refusal = "the remote's history was rewritten, dropping a commit this Mac has, so nothing was %s: bring it in by hand"
	for _, run := range []string{"the first run after the rewrite", "the next run", "after a fetch by hand"} {
		fake := runnertest.New(t)
		s := fetchingIn(fake, t.TempDir(), "")
		s.expect(fetchIt...)
		s.rewriting()
		n, err := bringIn(context.Background(), fake)
		if n != 0 || err == nil || err.Error() != strings.ReplaceAll(refusal, "%s", "brought in") {
			t.Errorf("BringIn(), %s = %d, %v", run, n, err)
		}
		s.check(t, run)
	}

	for name, f := range map[string]func(*runnertest.Fake) error{"Push": push, "Pull": pull} {
		fake := runnertest.New(t)
		s := fetchingIn(fake, t.TempDir(), "")
		s.expect(fetchIt...)
		s.rewriting()
		if err := f(fake); err == nil || err.Error() != strings.ReplaceAll(refusal, "%s", "pushed") {
			t.Errorf("%s(), the remote's history rewritten = %v", name, err)
		}
		s.check(t, name+", the remote's history rewritten")
	}

	fake := runnertest.New(t)
	s := fetchingIn(fake, t.TempDir(), "")
	s.expect(fetchIt...)
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(forkPointOf...).Prints(upstream + "\n")
	s.expect(mergeBaseOf...).Prints(upstream + "\n")
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Prints("0\n")
	s.expect(pushIt...)
	if err := push(fake); err != nil {
		t.Errorf("Push(), healed by a reset to the upstream = %v", err)
	}
	s.check(t, "healed")

	fake = runnertest.New(t)
	s = fetchingIn(fake, t.TempDir(), "")
	s.expect(fetchIt...)
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(forkPointOf...).Exits(1)
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(behindOf...).Prints("0\n")
	if n, err := bringIn(context.Background(), fake); n != 0 || err != nil {
		t.Errorf("BringIn(), no fork point = %d, %v", n, err)
	}
	s.check(t, "no fork point")

	fake = runnertest.New(t)
	s = fetchingIn(fake, t.TempDir(), "")
	s.expect(fetchIt...)
	s.expect(upstreamAt...).Prints(upstream + "\n")
	s.expect(forkPointOf...).Exits(128).PrintsToStderr("fatal: No such ref: '" + upstreamRef + "'\n")
	if n, err := bringIn(context.Background(), fake); n != 0 || err == nil || !strings.HasPrefix(err.Error(), "couldn't tell whether the remote's history was rewritten, so nothing was brought in: ") {
		t.Errorf("BringIn(), unable to tell = %d, %v", n, err)
	}
	s.check(t, "unable to tell")
}

// Offline, or with the remote not answering by ctx's deadline, nothing's
// brought in, and that's said; as it is while git's part way through
// something, which nothing touches. No remote, or several, are Push's
// errors, ErrNoRemote and ErrNowhere.
func TestBringInWhenItCant(t *testing.T) {
	fake := runnertest.New(t)
	s := fetchingIn(fake, t.TempDir(), "")
	s.expect(fetchIt...).Exits(128).PrintsToStderr(offline)
	if _, err := bringIn(context.Background(), fake); err == nil || err.Error() != "couldn't reach the remote, so nothing was brought in" {
		t.Errorf("BringIn(), offline = %v", err)
	}
	s.check(t, "offline")

	fake = runnertest.New(t)
	s = fetchingIn(fake, t.TempDir(), "")
	s.expect(fetchIt...).Hangs()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := bringIn(ctx, fake); err == nil || err.Error() != "the remote didn't answer in time, so nothing was brought in" {
		t.Errorf("BringIn(), the remote slow = %v", err)
	}
	s.check(t, "the remote slow")

	gitDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(gitDir + "\n")
	if _, err := bringIn(context.Background(), fake); err == nil || !strings.Contains(err.Error(), "a rebase is under way") {
		t.Errorf("BringIn(), a rebase under way = %v", err)
	}
	s.check(t, "a rebase under way")

	fake = runnertest.New(t)
	fake.On("git", remoteOf...)
	if _, err := bringIn(context.Background(), fake); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("BringIn(), no remote = %v", err)
	}
	fake = runnertest.New(t)
	fake.On("git", remoteOf...).Prints("origin\nbackup\n")
	if _, err := bringIn(context.Background(), fake); !errors.Is(err, gitrepo.ErrNowhere) {
		t.Errorf("BringIn(), two remotes = %v", err)
	}
}

// Two kits at once take turns, by a lock in the git folder that Commit,
// Push, Pull and BringIn all hold: each waits while another holds it, then
// goes on, Push holding it through the push itself, so no pull slips in
// between; past ctx's deadline, BringIn gives up, saying so, having
// fetched nothing.
func TestKitsTakeTurns(t *testing.T) {
	gitDir := t.TempDir()
	lock := filepath.Join(gitDir, "kit.lock")
	hold := func() func() {
		t.Helper()
		held, err := state.Lock(context.Background(), lock, func() {})
		if err != nil {
			t.Fatal(err)
		}
		return held
	}
	// waiting is a Repo whose waits for the lock are counted in waited, the
	// first letting held go, and none of which come once the fake ran
	// anything past the git folder.
	waiting := func(fake *runnertest.Fake, held func(), waited *int) gitrepo.Repo {
		return gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) {
			*waited++
			if calls := fake.Calls(); len(calls) > 2 {
				t.Errorf("ran %q while another kit held the lock", calls)
			}
			held()
		}}
	}

	fake := runnertest.New(t)
	s := bringing(fake, gitDir, "", "0")
	waited := 0
	if n, err := waiting(fake, hold(), &waited).BringIn(context.Background()); n != 0 || err != nil || waited != 1 {
		t.Errorf("BringIn(), another kit at it = %d, %v, waited %d times; want it to wait its turn", n, err, waited)
	}
	s.check(t, "bringing in, waiting its turn")

	fake = runnertest.New(t)
	s = pulling(fake, gitDir, "", "0")
	s.expect(pushIt...).Does(func() {
		done, cancel := context.WithCancel(context.Background())
		cancel()
		if unlock, err := state.Lock(done, lock, func() {}); err == nil {
			unlock()
			t.Error("Push() let the lock go before it pushed")
		}
	})
	waited = 0
	if err := waiting(fake, hold(), &waited).Push(context.Background()); err != nil || waited != 1 {
		t.Errorf("Push(), another kit at it = %v, waited %d times; want it to wait its turn, then push", err, waited)
	}
	s.check(t, "pushing, waiting its turn")

	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(gitDirOf...).Prints(gitDir + "\n")
	s.expect(statusOf("laptop")...)
	waited = 0
	if committed, err := waiting(fake, hold(), &waited).Commit(context.Background(), message, "laptop"); committed || err != nil || waited != 1 {
		t.Errorf("Commit(), another kit at it = %v, %v, waited %d times; want it to wait its turn", committed, err, waited)
	}
	s.check(t, "committing, waiting its turn")

	defer hold()()
	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(gitDir + "\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	repo := gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) { time.Sleep(time.Millisecond) }}
	if _, err := repo.BringIn(ctx); err == nil || err.Error() != "another kit was pulling or pushing all the while, so nothing was brought in" {
		t.Errorf("BringIn(), another kit at it throughout = %v", err)
	}
	s.check(t, "another kit at it throughout")
}
