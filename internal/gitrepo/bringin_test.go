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
	moveOn  = git("merge", "--ff-only", "--quiet", upstreamRef)
)

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
	s := pulling(fake, t.TempDir(), "M  shared/staged.txt\x00 M shared/config.toml\x00?? shared/untracked.txt\x00", "2")
	s.expect(incomingOf...).Prints("kit.toml\x00laptop/declarations\x00")
	s.expect(aheadOf...).Prints("0\n")
	s.expect(moveOn...)
	if n, err := bringIn(context.Background(), fake); n != 2 || err != nil {
		t.Errorf("BringIn() = %d, %v; want 2 brought in", n, err)
	}
	s.check(t, "moving on")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), " M shared/config.toml\x00", "0")
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
// as real git refuses to. A merge git refuses anyway is said, nothing
// brought in.
func TestBringInLeavesAClashAlone(t *testing.T) {
	fake := runnertest.New(t)
	s := pulling(fake, t.TempDir(), " M shared/config.toml\x00?? laptop/new\x00 M laptop/a\x00", "1")
	s.expect(incomingOf...).Prints("shared/config.toml\x00laptop/new\x00")
	n, err := bringIn(context.Background(), fake)
	if n != 0 || err == nil || err.Error() != "the remote's commits change what's not committed here, so nothing was touched or brought in: commit or undo the change to shared/config.toml, laptop/new first" {
		t.Errorf("BringIn(), a clash = %d, %v", n, err)
	}
	s.check(t, "a clash")

	fake = runnertest.New(t)
	s = pulling(fake, t.TempDir(), "", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(aheadOf...).Prints("0\n")
	s.expect(moveOn...).Exits(128).PrintsToStderr("fatal: Unable to create '/Users/someone/.config/kit/.git/index.lock': File exists.\n")
	if n, err := bringIn(context.Background(), fake); n != 0 || err == nil || !strings.HasPrefix(err.Error(), "couldn't move on to the remote's commits, so nothing was brought in: ") {
		t.Errorf("BringIn(), the merge refused = %d, %v", n, err)
	}
	s.check(t, "the merge refused")
}

// Commits waiting to be pushed are rebased onto the remote's, as Pull
// rebases them, while nothing git tracks is changed and not committed:
// files it doesn't track stay where they are. Something it tracks changed,
// staged or not, nothing's brought in, the commits kept as they are, as
// rebasing would set it aside and put it back, writing it twice. A rebase
// that stops on a conflict is aborted, leaving the commits as they were.
func TestBringInRebasesWhatsWaiting(t *testing.T) {
	fake := runnertest.New(t)
	s := pulling(fake, t.TempDir(), "?? shared/untracked.txt\x00", "2")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(aheadOf...).Prints("1\n")
	s.expect(rebase...)
	if n, err := bringIn(context.Background(), fake); n != 2 || err != nil {
		t.Errorf("BringIn(), commits waiting = %d, %v; want 2 brought in", n, err)
	}
	s.check(t, "commits waiting")

	for _, status := range []string{" M laptop/a\x00", "M  laptop/a\x00?? laptop/b\x00"} {
		fake := runnertest.New(t)
		s := pulling(fake, t.TempDir(), status, "1")
		s.expect(incomingOf...).Prints("kit.toml\x00")
		s.expect(aheadOf...).Prints("1\n")
		n, err := bringIn(context.Background(), fake)
		if n != 0 || err == nil || !strings.HasSuffix(err.Error(), "so nothing was brought in: commit or undo the change to laptop/a first") {
			t.Errorf("BringIn(), commits waiting and %q not committed = %d, %v; want nothing brought in", status, n, err)
		}
		s.check(t, "commits waiting and an edit")
	}

	gitDir := t.TempDir()
	fake = runnertest.New(t)
	s = pulling(fake, gitDir, "", "1")
	s.expect(incomingOf...).Prints("kit.toml\x00")
	s.expect(aheadOf...).Prints("1\n")
	rebasing(t, s.expect(rebase...), gitDir)
	s.expect(abort...).Does(func() {
		if err := os.Remove(filepath.Join(gitDir, "rebase-merge")); err != nil {
			t.Error(err)
		}
	})
	n, err := bringIn(context.Background(), fake)
	if n != 0 || err == nil || !strings.HasPrefix(err.Error(), "the remote's changes conflict with this Mac's, so nothing was brought in; the rebase was undone: ") {
		t.Errorf("BringIn(), the rebase stopping = %d, %v", n, err)
	}
	s.check(t, "the rebase stopping")
}

// Offline, or with the remote not answering by ctx's deadline, nothing's
// brought in, and that's said; as it is while git's part way through
// something, which nothing touches. No remote, or several, are Push's
// errors, ErrNoRemote and ErrNowhere.
func TestBringInWhenItCant(t *testing.T) {
	fetching := func(fake *runnertest.Fake) *sequence {
		s := &sequence{fake: fake}
		s.expect(remoteOf...).Prints("origin\n")
		s.expect(gitDirOf...).Prints(t.TempDir() + "\n")
		s.expect(statusOf()...)
		s.expect(branchOf...).Prints("refs/heads/main\n")
		s.expect(upstreamOf...).Prints(tracks(upstreamRef))
		return s
	}
	fake := runnertest.New(t)
	s := fetching(fake)
	s.expect(fetchIt...).Exits(128).PrintsToStderr(offline)
	if _, err := bringIn(context.Background(), fake); err == nil || err.Error() != "couldn't reach the remote, so nothing was brought in" {
		t.Errorf("BringIn(), offline = %v", err)
	}
	s.check(t, "offline")

	fake = runnertest.New(t)
	s = fetching(fake)
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

// Two kits at once take turns, by a lock in the git folder: one waits
// while another holds it, then brings in what's left; past ctx's
// deadline, it gives up, saying so, having fetched nothing.
func TestBringInTakesTurns(t *testing.T) {
	gitDir := t.TempDir()
	held, err := state.Lock(context.Background(), filepath.Join(gitDir, "kit-pull.lock"), func() {})
	if err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	s := pulling(fake, gitDir, "", "0")
	waited := 0
	repo := gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) {
		waited++
		held()
	}}
	if n, err := repo.BringIn(context.Background()); n != 0 || err != nil || waited != 1 {
		t.Errorf("BringIn(), another kit at it = %d, %v, waited %d times; want it to wait its turn", n, err, waited)
	}
	s.check(t, "waiting its turn")

	held, err = state.Lock(context.Background(), filepath.Join(gitDir, "kit-pull.lock"), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(remoteOf...).Prints("origin\n")
	s.expect(gitDirOf...).Prints(gitDir + "\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	repo = gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) { time.Sleep(time.Millisecond) }}
	if _, err := repo.BringIn(ctx); err == nil || err.Error() != "another kit was bringing in the remote's commits all the while, so nothing was brought in" {
		t.Errorf("BringIn(), another kit at it throughout = %v", err)
	}
	s.check(t, "another kit at it throughout")
}
