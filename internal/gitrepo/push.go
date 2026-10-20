package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNoRemote is returned by Push for a repository with nothing to push to.
var ErrNoRemote = errors.New("no remote to push to")

// stashMessage names what Push sets aside while it rebases.
const stashMessage = "kit: set aside while rebasing onto the remote"

// Push brings in the remote's changes, rebasing what's committed here onto
// them when there are any, then pushes. Anything uncommitted is set aside in
// git's stash while it rebases, and put back as it was, staged or not: a
// stash that won't go back is kept, and named. A rebase that stops on a
// conflict is aborted, leaving the repository as it was, and nothing is
// pushed.
func (r Repo) Push(ctx context.Context) error {
	res, err := r.git(ctx, "remote")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(res.Stdout)) == "" {
		return ErrNoRemote
	}
	if err := r.catchUp(ctx); err != nil {
		return err
	}
	if _, err := r.git(ctx, "push", "--quiet"); err != nil {
		return fmt.Errorf("couldn't push: %w", err)
	}
	return nil
}

// catchUp fetches the remote's changes, and rebases onto them when there
// are any, with what's not committed set aside.
func (r Repo) catchUp(ctx context.Context) error {
	behind, err := r.behind(ctx)
	if err != nil {
		return fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
	}
	if behind == 0 {
		return nil
	}
	stash, err := r.stash(ctx)
	if err != nil {
		return fmt.Errorf("couldn't set aside what's not committed, so nothing was pushed: %w", err)
	}
	if stash != "" {
		// Set aside: it's put back whatever becomes of the run.
		ctx = context.WithoutCancel(ctx)
	}
	settled, rebased := r.rebase(ctx)
	switch {
	case stash == "":
		return rebased
	case !settled:
		return errors.Join(rebased, kept(stash, errors.New("the rebase is still under way")))
	}
	if _, err := r.git(ctx, "stash", "pop", "--index", "--quiet"); err != nil {
		return errors.Join(rebased, kept(stash, err))
	}
	return rebased
}

// kept is the error for a stash that couldn't be put back, as err says.
func kept(stash string, err error) error {
	return fmt.Errorf("what wasn't committed is in git's stash, %s (%q), as it couldn't be put back, so nothing was pushed: %w", stash, stashMessage, err)
}

// behind fetches, and is how many of the upstream's commits HEAD hasn't.
func (r Repo) behind(ctx context.Context) (int, error) {
	if _, err := r.git(ctx, "fetch", "--quiet"); err != nil {
		return 0, err
	}
	res, err := r.git(ctx, "rev-list", "--count", "HEAD..@{u}")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
}

// stash sets aside what's not committed, staged or not, and is the stash
// made: "" with nothing to set aside, when git stash makes none.
func (r Repo) stash(ctx context.Context) (string, error) {
	before, err := r.commitAt(ctx, "refs/stash")
	if err != nil {
		return "", err
	}
	if _, err := r.git(ctx, "stash", "push", "--quiet", "--message", stashMessage); err != nil {
		return "", err
	}
	after, err := r.commitAt(ctx, "refs/stash")
	if err != nil || after == before {
		return "", err
	}
	return after, nil
}

// rebase rebases what's committed here onto the upstream, aborted if it
// stops: settled once it's rebased or back as it was, and not while the
// rebase is still under way.
func (r Repo) rebase(ctx context.Context) (settled bool, err error) {
	_, err = r.git(ctx, "rebase", "--quiet")
	if err == nil {
		return true, nil
	}
	if _, abortErr := r.git(ctx, "rebase", "--abort"); abortErr == nil {
		return true, fmt.Errorf("the remote's changes conflict with this Mac's, so nothing was pushed; the rebase was undone: %w", err)
	}
	return false, fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
}

// Unpushed is how many commits are waiting to be pushed to the branch's
// upstream: none without an upstream.
func (r Repo) Unpushed(ctx context.Context) int {
	res, err := r.git(ctx, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	return n
}
