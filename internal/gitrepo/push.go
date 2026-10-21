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

// Pull brings in the remote's changes, rebasing what's committed here onto
// them when there are any. It refuses while git is part way through a
// merge, rebase, cherry-pick, revert or am, or a conflict waits to be
// resolved: setting what's not committed aside would end the one, and fail
// on the other; and while a commit kit held back is in the branch (held),
// which a rebase would make anew, unmarked. Anything not committed is set
// aside in git's stash while it rebases, and put back as it was, staged or
// not; but when the remote's commits change a file not committed here,
// staged or not, or one git ignores here, or a folder or file at one,
// nothing's set aside or rebased, as putting it back would conflict, or
// the rebase replace it. A stash that won't go back is kept, and named. A
// rebase that stops on a conflict is aborted, leaving the repository as it
// was.
func (r Repo) Pull(ctx context.Context) error {
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
	return r.catchUp(ctx, gitDir, dirty)
}

// Push pulls the remote's changes (Pull), then pushes; nothing when Pull
// refuses or fails.
func (r Repo) Push(ctx context.Context) error {
	if err := r.Pull(ctx); err != nil {
		return err
	}
	if _, err := r.git(ctx, "push", "--quiet"); err != nil {
		return fmt.Errorf("couldn't push: %w", err)
	}
	return nil
}

// Holding is why Push holds back now, as far as the remote's last fetch
// tells: one of Pull's refusals, or nil when there's none.
func (r Repo) Holding(ctx context.Context) error {
	_, dirty, err := r.settled(ctx)
	if err != nil {
		return err
	}
	if behind, err := r.count(ctx, "HEAD..@{u}"); err != nil || behind == 0 {
		return nil
	}
	return r.clashes(ctx, dirty)
}

// settled is the repository's git folder, and what's changed and not
// committed, unless git is part way through one of its operations there, a
// conflict waits to be resolved, or a commit kit held back is in the
// branch, which is an error.
func (r Repo) settled(ctx context.Context) (gitDir string, dirty []entry, err error) {
	if gitDir, err = r.gitDir(ctx); err != nil {
		return "", nil, err
	}
	if err := underWay(gitDir); err != nil {
		return "", nil, err
	}
	if err := r.held(ctx, gitDir); err != nil {
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
	n, err := r.count(ctx, "HEAD..@{u}")
	if err != nil {
		return 0, fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
	}
	return n, nil
}

// count is how many commits revs names, as git rev-list --count has them.
func (r Repo) count(ctx context.Context, revs string) (int, error) {
	res, err := r.git(ctx, "rev-list", "--count", revs)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
}

// unreachable reports whether git failed for want of the remote: no
// network, or the host not answering. git says why above its last line,
// over ssh.
func unreachable(err error) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && slices.ContainsFunc([]string{
		"Could not resolve host", "Failed to connect", "Connection refused", "Connection timed out",
		"Operation timed out", "Network is unreachable", "No route to host",
	}, func(s string) bool { return strings.Contains(exit.FullStderr, s) })
}

// clashes errors when the remote's commits change what's not committed
// here: a path changed here, dirty, staged or not; one git ignores, which
// the rebase would replace; a folder or a file at one of them. Put back
// after the rebase, a change would conflict, leaving git's markers in a
// file that may be linked into the home folder. Nothing's touched.
func (r Repo) clashes(ctx context.Context, dirty []entry) error {
	res, err := r.git(ctx, "diff", "--name-only", "-z", "--no-renames", "HEAD...@{u}")
	if err != nil {
		return fmt.Errorf("couldn't read the remote's changes, so nothing was pushed: %w", err)
	}
	incoming := names(res.Stdout)
	if len(incoming) == 0 {
		return nil
	}
	fold, err := r.foldsCase(ctx)
	if err != nil {
		return err
	}
	meets := func(here string) bool {
		return slices.ContainsFunc(incoming, func(there string) bool { return overlap(here, there, fold) })
	}
	var changed []string
	for _, e := range dirty {
		if meets(e.path) && !slices.Contains(changed, e.path) {
			changed = append(changed, e.path)
		}
	}
	res, err = r.git(ctx, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return err
	}
	ignored := slices.DeleteFunc(names(res.Stdout), func(path string) bool { return !meets(path) })
	if len(changed)+len(ignored) == 0 {
		return nil
	}
	edited, staged, err := r.byDrift(ctx, changed)
	if err != nil {
		return err
	}
	return clash{edited: edited, staged: staged, ignored: ignored}
}

// foldsCase reports whether the repository's paths differ only by case on
// its disk, as on macOS's own, by git's core.ignorecase.
func (r Repo) foldsCase(ctx context.Context) (bool, error) {
	res, err := r.git(ctx, "config", "--get", "--type=bool", "core.ignorecase")
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		// Not set.
		return false, nil
	}
	return strings.TrimSpace(string(res.Stdout)) == "true", err
}

// overlap reports whether paths a and b, in the repository, are the same,
// or one is in the other's folder: a nested repository's, or a folder git
// ignores whole, ends in a slash. fold compares them whatever their case.
func overlap(a, b string, fold bool) bool {
	a, b = strings.TrimSuffix(a, "/"), strings.TrimSuffix(b, "/")
	if fold {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// byDrift splits paths into those changed on disk, drift (Changes), and
// those whose change is only staged, in git's index, the user's own.
func (r Repo) byDrift(ctx context.Context, paths []string) (edited, staged []string, err error) {
	if len(paths) == 0 {
		return nil, nil, nil
	}
	changes, err := r.Changes(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range paths {
		if slices.ContainsFunc(changes, func(c Change) bool { return c.Path == path }) {
			edited = append(edited, path)
		} else {
			staged = append(staged, path)
		}
	}
	return edited, staged, nil
}

// clash is Pull's refusal when the remote's commits change what's not
// committed here: files changed on disk, edited; changes only staged; and
// files git ignores here.
type clash struct {
	edited, staged, ignored []string
}

func (c clash) Error() string {
	var said []string
	if len(c.edited) > 0 {
		said = append(said, strings.Join(c.edited, ", ")+" changed here: commit or undo the change with kit reconcile")
	}
	if len(c.staged) > 0 {
		said = append(said, strings.Join(c.staged, ", ")+" staged here, its file on disk as last committed: git's index is yours, to commit or unstage (git restore --staged "+strings.Join(c.staged, " ")+")")
	}
	if len(c.ignored) > 0 {
		said = append(said, strings.Join(c.ignored, ", ")+" ignored here, which the remote's would replace: move it aside")
	}
	return "the remote's commits change what's not committed here, so nothing was touched or pushed: " + strings.Join(said, "; ")
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
	// Not by fork-point, which leaves out a commit the upstream had once,
	// before it was pushed over, so the rebase changes more than the
	// remote's commits, as clashes reads them.
	_, err = r.git(ctx, "rebase", "--no-fork-point", "--quiet")
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
	n, _ := r.count(ctx, "@{u}..HEAD")
	return n
}
