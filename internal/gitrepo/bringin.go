package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// pullLock is the file in the git folder whose lock a pull holds, Push's
// and BringIn's, so two kits at once, as the hourly run's and one by hand,
// take turns.
const pullLock = "kit-pull.lock"

// lockPulls takes the lock on pulling in the repository whose git folder
// is gitDir, waiting while another kit holds it, till ctx is done: unlock
// lets it go. Failing, it says what was undone, as nothing was pushed.
func (r Repo) lockPulls(ctx context.Context, gitDir, undone string) (unlock func(), err error) {
	unlock, err = state.Lock(ctx, filepath.Join(gitDir, pullLock), func() { r.sleep(lockWait) })
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("another kit was bringing in the remote's commits all the while, so nothing was %s", undone)
	case err != nil:
		return nil, fmt.Errorf("couldn't lock the repository to bring in the remote's commits, so nothing was %s: %w", undone, err)
	}
	return unlock, nil
}

// BringIn brings in the remote's commits HEAD hasn't, as a run starts, so
// the run reads what another Mac changed: how many came in. What isn't
// committed is left exactly as it is, or nothing's brought in. With no
// commit here the upstream hasn't, the branch moves on to it (git merge
// --ff-only), which writes only the files the remote's commits change,
// never one git ignores; with commits waiting to be pushed, they're
// rebased onto the remote's, as Pull does, only while no file git tracks
// is changed and not committed, which rebasing would set aside and put
// back, and git doesn't sign its commits, which nobody may be there to
// approve. It refuses, touching nothing, as Pull does, while git is part
// way through one of its operations or a conflict waits, and when the
// remote's commits change a path not committed here, or add one git
// ignores; and when the remote's history was rewritten, the upstream's
// last commit no longer in it, as rebasing would put back what another Mac
// took out. A rebase that stops is aborted. With no remote it's
// ErrNoRemote, and ErrNowhere with several or HEAD on no branch. ctx's
// deadline ends the wait for another kit pulling (lockPulls), and the
// fetch; once fetched, the rest is git's on this Mac, and finishes.
func (r Repo) BringIn(ctx context.Context) (int, error) {
	remote, err := r.remote(ctx)
	if err != nil {
		return 0, err
	}
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return 0, err
	}
	unlock, err := r.lockPulls(ctx, gitDir, broughtIn)
	if err != nil {
		return 0, err
	}
	defer unlock()
	dirty, err := r.settled(ctx, gitDir)
	if err != nil {
		return 0, err
	}
	t, err := r.upstream(ctx)
	if err != nil || t.up == "" {
		return 0, err
	}
	was, err := r.commitAt(ctx, t.up)
	if err != nil {
		return 0, err
	}
	if err := r.fetch(ctx, remote, t, broughtIn); err != nil {
		return 0, err
	}
	ctx = context.WithoutCancel(ctx)
	if err := r.unrewritten(ctx, was, t.up); err != nil {
		return 0, err
	}
	n, err := r.behind(ctx, t.up)
	if err != nil {
		return 0, fmt.Errorf("couldn't count the remote's commits, so nothing was %s: %w", broughtIn, err)
	}
	if n == 0 {
		return 0, nil
	}
	if err := r.clashing(ctx, dirty, t.up, broughtIn); err != nil {
		return 0, err
	}
	if err := r.moveOn(ctx, gitDir, t.up, dirty); err != nil {
		return 0, err
	}
	return n, nil
}

// unrewritten is the refusal when the commit was, where up stood before
// the fetch, isn't in up's history now, as git merge-base --is-ancestor
// says, exiting 1: the remote's history was rewritten. None was, nil.
func (r Repo) unrewritten(ctx context.Context, was, up string) error {
	if was == "" {
		return nil
	}
	_, err := r.git(ctx, "merge-base", "--is-ancestor", was, up)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return fmt.Errorf("the remote's history was rewritten, so nothing was %s: bring it in by hand", broughtIn)
	}
	if err != nil {
		return fmt.Errorf("couldn't tell whether the remote's history was rewritten, so nothing was %s: %w", broughtIn, err)
	}
	return nil
}

// moveOn has the branch take in up's commits, as BringIn does: up's alone
// when it has every commit HEAD has, else what's committed here rebased
// onto them, only while git signs nothing and nothing git tracks is
// changed, dirty saying what is.
func (r Repo) moveOn(ctx context.Context, gitDir, up string, dirty []entry) error {
	ahead, err := r.count(ctx, up+"..HEAD")
	if err != nil {
		return fmt.Errorf("couldn't tell what's waiting to be pushed, so nothing was %s: %w", broughtIn, err)
	}
	if ahead == 0 {
		if _, err := r.git(ctx, "merge", "--ff-only", "--no-overwrite-ignore", "--quiet", up); err != nil {
			return fmt.Errorf("couldn't move on to the remote's commits, so nothing was %s: %w", broughtIn, err)
		}
		return nil
	}
	signs, err := r.flag(ctx, "commit.gpgsign")
	switch {
	case err != nil:
		return fmt.Errorf("couldn't tell whether git signs its commits, so nothing was %s: %w", broughtIn, err)
	case signs:
		return fmt.Errorf("commits here wait to be pushed, and git signs its commits, which rebasing them onto the remote's would ask for with nobody to answer, so nothing was %s: the next push, by hand, rebases and signs them", broughtIn)
	}
	if edited := tracked(dirty); len(edited) > 0 {
		return fmt.Errorf("commits here wait to be pushed, and rebasing them onto the remote's would set aside what's not committed, so nothing was %s: commit or undo the change to %s first", broughtIn, strings.Join(edited, ", "))
	}
	return r.rebase(ctx, gitDir, up, broughtIn)
}

// tracked are the paths of dirty, what's changed and not committed, that
// git tracks, or is told to: all but new files git doesn't track yet.
func tracked(dirty []entry) []string {
	var paths []string
	for _, e := range dirty {
		if e.code != "??" && !slices.Contains(paths, e.path) {
			paths = append(paths, e.path)
		}
	}
	return paths
}
