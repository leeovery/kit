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
// on the other. Anything not committed is set aside in git's stash while
// it rebases, and put back as it was, staged or not; but when the remote's
// commits change a file not committed here, staged or not, or one git
// ignores here, or a folder or file at one, nothing's set aside or
// rebased, as putting it back would conflict, or the rebase replace it. A
// stash that won't go back is kept, and named. A rebase that stops on a
// conflict is aborted, leaving the repository as it was. A branch with no
// upstream yet, or one the remote hasn't yet, as a new remote's, has
// nothing to bring in.
func (r Repo) Pull(ctx context.Context) error {
	_, err := r.pull(ctx)
	return err
}

// Push pulls the remote's changes (Pull), then pushes, nothing when Pull
// refuses or fails: to the branch's upstream, or, with none yet, to the
// same branch of the remote, which becomes its upstream.
func (r Repo) Push(ctx context.Context) error {
	to, err := r.pull(ctx)
	if err != nil {
		return err
	}
	args := []string{"push", "--quiet"}
	if to != "" {
		args = append(args, "-u", to, "HEAD")
	}
	if _, err := r.git(ctx, args...); err != nil {
		return fmt.Errorf("couldn't push: %w", err)
	}
	return nil
}

// Holding is why Push holds back now, as far as the remote's last fetch
// tells: one of Pull's refusals, or nil when there's none. It changes
// nothing.
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

// pull is Pull, and the remote to push to, making it the branch's
// upstream, when it has none: "" when it has.
func (r Repo) pull(ctx context.Context) (string, error) {
	res, err := r.git(ctx, "remote")
	if err != nil {
		return "", err
	}
	remotes := strings.Fields(string(res.Stdout))
	if len(remotes) == 0 {
		return "", ErrNoRemote
	}
	gitDir, dirty, err := r.settled(ctx)
	if err != nil {
		return "", err
	}
	if err := r.fetch(ctx); err != nil {
		return "", err
	}
	upstream, there, err := r.upstream(ctx)
	switch {
	case err != nil:
		return "", err
	case upstream == "":
		if slices.Contains(remotes, "origin") {
			return "origin", nil
		}
		return remotes[0], nil
	case !there:
		return "", nil
	}
	return "", r.catchUp(ctx, gitDir, dirty)
}

// settled is the repository's git folder, and what's changed and not
// committed, unless git is part way through one of its operations there,
// or a conflict waits to be resolved, which is an error.
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

// fetch brings in the remote's commits.
func (r Repo) fetch(ctx context.Context) error {
	if _, err := r.git(ctx, "fetch", "--quiet"); err != nil {
		if unreachable(err) {
			return errors.New("couldn't reach the remote, so nothing was pushed")
		}
		return fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
	}
	return nil
}

// upstream is the remote-tracking branch the branch HEAD is on pulls from
// and pushes to, as fetched, and whether it's there: none configured, as
// for a branch never pushed, or not there, as a new remote's.
func (r Repo) upstream(ctx context.Context) (string, bool, error) {
	res, err := r.git(ctx, "symbolic-ref", "-q", "HEAD")
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return "", false, errors.New("HEAD isn't on a branch, so nothing was pushed")
	}
	if err != nil {
		return "", false, err
	}
	if res, err = r.git(ctx, "for-each-ref", "--format=%(upstream)", strings.TrimSpace(string(res.Stdout))); err != nil {
		return "", false, err
	}
	upstream := strings.TrimSpace(string(res.Stdout))
	if upstream == "" {
		return "", false, nil
	}
	commit, err := r.commitAt(ctx, upstream)
	return upstream, commit != "", err
}

// catchUp rebases onto the upstream's commits HEAD hasn't, if there are
// any, with what's not committed, dirty, set aside, unless the remote
// changed some of it.
func (r Repo) catchUp(ctx context.Context, gitDir string, dirty []entry) error {
	behind, err := r.count(ctx, "HEAD..@{u}")
	if err != nil {
		return fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
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

// count is how many commits revs names, as git rev-list --count has them.
func (r Repo) count(ctx context.Context, revs string) (int, error) {
	res, err := r.git(ctx, "rev-list", "--count", revs)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
}

// unreachable reports whether git failed for want of the remote: no
// network, or the host not answering, as git's fatal line, or ssh's, says,
// above its last over ssh.
func unreachable(err error) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && slices.ContainsFunc(exit.Lines("fatal:", "ssh:"), func(line string) bool {
		return slices.ContainsFunc([]string{
			"Could not resolve host", "Failed to connect", "Connection refused", "Connection timed out",
			"Operation timed out", "Network is unreachable", "No route to host",
		}, func(s string) bool { return strings.Contains(line, s) })
	})
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
	// Paths that differ only by case are one on a disk that folds case, as
	// macOS's own do.
	fold, err := r.flag(ctx, "core.ignorecase")
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
	// A folder git ignores whole is listed as one, ending in a slash.
	res, err = r.git(ctx, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return err
	}
	var ignored []string
	for _, here := range names(res.Stdout) {
		for _, there := range incoming {
			hit := r.replaces(here, there, fold)
			if hit != "" && !slices.ContainsFunc(slices.Concat(changed, ignored), func(p string) bool { return same(p, hit, fold) }) {
				ignored = append(ignored, hit)
			}
		}
	}
	if len(changed)+len(ignored) == 0 {
		return nil
	}
	edited, staged, err := r.byDrift(ctx, changed)
	if err != nil {
		return err
	}
	return clash{edited: edited, staged: staged, ignored: ignored}
}

// replaces is what of here, a file git ignores or a folder it ignores
// whole, bringing in the remote's path there would replace: "" for
// nothing. A file is replaced at there, in a folder there's made a file,
// or where there needs a folder; a folder, where there's made a file of it
// or above it, and else only what's in it at there, or a file in it where
// there needs a folder, as real git checks out over files it ignores
// without a word, and adds a file to a folder it ignores harmlessly.
func (r Repo) replaces(here, there string, fold bool) string {
	switch {
	case !overlap(here, there, fold):
		return ""
	case !strings.HasSuffix(here, "/") || !within(there, here, fold):
		return here
	}
	parts := strings.Split(there[len(here):], "/")
	for i := range parts {
		path := here + strings.Join(parts[:i+1], "/")
		info, err := os.Lstat(filepath.Join(r.Dir, path))
		switch {
		case err != nil:
			return ""
		case i == len(parts)-1 || !info.IsDir():
			return path
		}
	}
	return ""
}

// overlap reports whether paths a and b, in the repository, are the same,
// or one is in the other's folder: a nested repository's, or a folder git
// ignores whole, ends in a slash. fold compares them whatever their case.
func overlap(a, b string, fold bool) bool {
	return same(a, b, fold) || within(a, b, fold) || within(b, a, fold)
}

// same reports whether paths a and b, in the repository, are one: a
// folder's ending in a slash or not, and, folding, whatever their case.
func same(a, b string, fold bool) bool {
	a, b = strings.TrimSuffix(a, "/"), strings.TrimSuffix(b, "/")
	return a == b || fold && strings.EqualFold(a, b)
}

// within reports whether path is in folder, a path in the repository,
// whether or not it ends in a slash: folding, whatever their case.
func within(path, folder string, fold bool) bool {
	folder = strings.TrimSuffix(folder, "/") + "/"
	if fold {
		path, folder = strings.ToLower(path), strings.ToLower(folder)
	}
	return strings.HasPrefix(path, folder)
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
