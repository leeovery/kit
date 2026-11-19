package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/leeovery/kit/internal/state"
)

// repoLock is the file in the git folder whose lock kit holds while it
// changes the repository's history, committing (Commit), pulling and
// pushing (Push, Pull, BringIn), so two kits at once, as the hourly run's
// and one by hand, take turns. Each takes it once, at its outset: flock's
// locks are the open file's, so a second taken in the same process would
// wait on the first.
const repoLock = "kit.lock"

// lockRepo takes the lock on the repository whose git folder is gitDir,
// waiting while another kit holds it, till ctx is done: unlock lets it
// go. Failing, it says what was undone, as nothing was pushed.
func (r Repo) lockRepo(ctx context.Context, gitDir, undone string) (unlock func(), err error) {
	unlock, err = state.Lock(ctx, filepath.Join(gitDir, repoLock), func() { r.sleep(lockWait) })
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("another kit was pulling or pushing all the while, so nothing was %s", undone)
	case err != nil:
		return nil, fmt.Errorf("couldn't lock the repository, so nothing was %s: %w", undone, err)
	}
	return unlock, nil
}

// exclusively runs f, given the repository's one remote and its git
// folder, while kit holds the lock on it (lockRepo): with no remote it's
// ErrNoRemote, and ErrNowhere with several.
func (r Repo) exclusively(ctx context.Context, undone string, f func(remote, gitDir string) error) error {
	remote, err := r.remote(ctx)
	if err != nil {
		return err
	}
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return err
	}
	unlock, err := r.lockRepo(ctx, gitDir, undone)
	if err != nil {
		return err
	}
	defer unlock()
	return f(remote, gitDir)
}
