package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// ErrNoRemote is returned by Push for a repository with nothing to push to.
var ErrNoRemote = errors.New("no remote to push to")

// stashMessage names what Push sets aside while it rebases.
const stashMessage = "kit: set aside while rebasing onto the remote"

// Push brings in the remote's changes, rebasing what's committed here onto
// them when there are any, then pushes. It refuses while git is part way
// through a merge, rebase, cherry-pick, revert or am, or a conflict waits
// to be resolved: setting what's not committed aside would end the one, and
// fail on the other. Anything not committed is set aside in git's stash
// while it rebases, and put back as it was, staged or not; but when the
// remote changed a file changed here, nothing's set aside or rebased, as
// putting it back would conflict. A stash that won't go back is kept, and
// named. A rebase that stops on a conflict is aborted, leaving the
// repository as it was, and nothing is pushed.
func (r Repo) Push(ctx context.Context) error {
	res, err := r.git(ctx, "remote")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(res.Stdout)) == "" {
		return ErrNoRemote
	}
	gitDir, dirty, err := r.settled(ctx)
	if err != nil {
		return err
	}
	if err := r.catchUp(ctx, gitDir, dirty); err != nil {
		return err
	}
	if _, err := r.git(ctx, "push", "--quiet"); err != nil {
		return fmt.Errorf("couldn't push: %w", err)
	}
	return nil
}

// settled is the repository's git folder, and what's changed and not
// committed, unless git is part way through one of its operations there or
// a conflict waits to be resolved, which is an error.
func (r Repo) settled(ctx context.Context) (gitDir string, dirty []entry, err error) {
	if gitDir, err = r.gitDir(ctx); err != nil {
		return "", nil, err
	}
	if err := underWay(gitDir); err != nil {
		return "", nil, err
	}
	if dirty, err = r.status(ctx); err != nil {
		return "", nil, err
	}
	if e, ok := unmerged(dirty); ok {
		return "", nil, fmt.Errorf("a conflict in %s is waiting to be resolved: kit's change waits till it's finished", e.path)
	}
	return gitDir, dirty, nil
}

// catchUp fetches the remote's changes, and rebases onto them when there
// are any, with what's not committed, dirty, set aside, unless the remote
// changed some of it.
func (r Repo) catchUp(ctx context.Context, gitDir string, dirty []entry) error {
	behind, err := r.behind(ctx)
	if err != nil {
		return err
	}
	if behind == 0 {
		return nil
	}
	if err := r.clashes(ctx, dirty); err != nil {
		return err
	}
	stash := ""
	if slices.ContainsFunc(dirty, func(e entry) bool { return e.code != "??" }) {
		if stash, err = r.stash(ctx); err != nil {
			return fmt.Errorf("couldn't set aside what's not committed, so nothing was pushed: %w", err)
		}
	}
	if stash != "" {
		// Set aside: it's put back whatever becomes of the run.
		ctx = context.WithoutCancel(ctx)
	}
	settled, rebased := r.rebase(ctx, gitDir)
	switch {
	case stash == "":
		return rebased
	case !settled:
		return errors.Join(rebased, kept(stash, errors.New("the rebase is still under way")))
	}
	return errors.Join(rebased, r.unstash(ctx, stash))
}

// behind fetches, and is how many of the upstream's commits HEAD hasn't.
func (r Repo) behind(ctx context.Context) (int, error) {
	if _, err := r.git(ctx, "fetch", "--quiet"); err != nil {
		if unreachable(err) {
			return 0, errors.New("couldn't reach the remote, so nothing was pushed")
		}
		return 0, fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
	}
	res, err := r.git(ctx, "rev-list", "--count", "HEAD..@{u}")
	if err != nil {
		return 0, fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
	}
	return strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
}

// unreachable reports whether git failed for want of the remote: no
// network, or the host not answering.
func unreachable(err error) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && slices.ContainsFunc([]string{
		"Could not resolve host", "Failed to connect", "Connection refused", "Connection timed out",
		"Operation timed out", "Network is unreachable", "No route to host",
	}, func(s string) bool { return strings.Contains(exit.Stderr, s) })
}

// clashes errors when the remote's commits change a file changed here and
// not committed, dirty, or within a nested repository's folder: put back
// after the rebase, it would conflict, leaving git's markers in a file
// that may be linked into the home folder. Nothing's touched.
func (r Repo) clashes(ctx context.Context, dirty []entry) error {
	if len(dirty) == 0 {
		return nil
	}
	res, err := r.git(ctx, "diff", "--name-only", "-z", "--no-renames", "HEAD...@{u}")
	if err != nil {
		return fmt.Errorf("couldn't read the remote's changes, so nothing was pushed: %w", err)
	}
	var both []string
	for _, path := range names(res.Stdout) {
		if slices.ContainsFunc(dirty, func(e entry) bool { return e.path == path || e.nested() && strings.HasPrefix(path, e.path) }) {
			both = append(both, path)
		}
	}
	if len(both) == 0 {
		return nil
	}
	return fmt.Errorf("changed on another Mac, and changed here, not committed: %s; so nothing was touched or pushed: commit those changes or put them aside, and kit pushes then", strings.Join(both, ", "))
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
// stops part way: settled once it's rebased, back as it was, or never
// started, as when a hook refuses it or a file git doesn't keep is in its
// way; not while git still has it under way.
func (r Repo) rebase(ctx context.Context, gitDir string) (settled bool, err error) {
	_, err = r.git(ctx, "rebase", "--quiet")
	switch {
	case err == nil:
		return true, nil
	case !rebasing(gitDir):
		return true, fmt.Errorf("couldn't rebase onto the remote's changes, so nothing was pushed: %w", err)
	}
	_, _ = r.git(ctx, "rebase", "--abort")
	if rebasing(gitDir) {
		return false, fmt.Errorf("the remote's changes conflict with this Mac's, and the rebase couldn't be undone, so nothing was pushed: git rebase --abort undoes it: %w", err)
	}
	return true, fmt.Errorf("the remote's changes conflict with this Mac's, so nothing was pushed; the rebase was undone: %w", err)
}

// rebasing reports whether a rebase is under way in the repository whose
// git folder is gitDir, by the folder git keeps while it is.
func rebasing(gitDir string) bool {
	return slices.ContainsFunc([]string{"rebase-merge", "rebase-apply"}, func(name string) bool {
		_, err := os.Lstat(filepath.Join(gitDir, name))
		return err == nil
	})
}

// unstash puts stash back as it was, staged or not: popped only while it's
// still git's latest. One that won't go back is kept, never dropped, and
// the error says where what wasn't committed is: in the stash whole, or
// back in its files but for conflicts git marked in some.
func (r Repo) unstash(ctx context.Context, stash string) error {
	if latest, err := r.commitAt(ctx, "refs/stash"); err != nil || latest != stash {
		return kept(stash, errors.New("it isn't git's latest stash now, so kit left it be"))
	}
	_, err := r.git(ctx, "stash", "pop", "--index", "--quiet", "stash@{0}")
	if err == nil {
		return nil
	}
	entries, _ := r.status(ctx)
	var marked []string
	for _, e := range entries {
		if slices.Contains(unmergedCodes, e.code) {
			marked = append(marked, e.path)
		}
	}
	if len(marked) == 0 {
		return kept(stash, err)
	}
	return fmt.Errorf("what wasn't committed came back with conflicts in %s: each holds the remote's version and this Mac's, between git's <<<<<<< Updated upstream and >>>>>>> Stashed changes lines, so nothing was pushed: make each what it should be, git add it, then git stash drop, as git's stash, %s, still holds it all", strings.Join(marked, ", "), stash)
}

// kept is the error for a stash that couldn't be put back, as err says,
// what wasn't committed wholly in it.
func kept(stash string, err error) error {
	return fmt.Errorf("what wasn't committed is wholly in git's stash, %s (%q), as it couldn't be put back, so nothing was pushed: git stash apply %s puts it back: %w", stash, stashMessage, stash, err)
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
