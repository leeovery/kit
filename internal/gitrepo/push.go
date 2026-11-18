package gitrepo

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// ErrNoRemote is returned by Push for a repository with nothing to push to.
var ErrNoRemote = errors.New("no remote to push to")

// ErrNowhere is in Push's refusal when it can't tell where to push: HEAD on
// no branch, or several remotes.
var ErrNowhere = errors.New("nowhere to push to")

// nowhere is one of Push's refusals for want of where to push, as its error
// says: ErrNowhere.
type nowhere struct{ error }

func (nowhere) Is(target error) bool { return target == ErrNowhere }

// stashMessage names what kit set aside to pull, should it be kept in git's
// list of stashes, as when it won't go back.
const stashMessage = "kit: your changes before pulling"

// Pull brings in the remote's changes, rebasing what's committed here onto
// the branch's upstream when it has commits HEAD hasn't. It refuses while
// git is part way through a merge, rebase, cherry-pick, revert or am, or a
// conflict waits to be resolved; when the remote's commits change a path
// not committed here, as putting it back would conflict; and when a file
// git tracks is a folder now, or a folder a file, as setting it aside
// would delete what's there. What's not committed is set aside while it
// rebases and put back as it was, staged or not, git's list of stashes
// left as it is unless what was set aside won't go back, when it's kept
// there, and the error says so. A rebase that stops on a conflict is
// aborted, leaving the repository as it was.
func (r Repo) Pull(ctx context.Context) error {
	_, _, err := r.pull(ctx)
	return err
}

// Push pulls the remote's changes (Pull), then pushes to the branch's
// upstream on the repository's one remote, the remote PushURLs reads,
// named, so git's pushRemote or pushDefault can't send it elsewhere:
// nothing when Pull refuses or fails. A branch with no upstream is pushed
// to the same branch there, which becomes its upstream.
func (r Repo) Push(ctx context.Context) error {
	remote, t, err := r.pull(ctx)
	if err != nil {
		return err
	}
	args := []string{"push", "--quiet", remote, "HEAD:" + t.merge}
	if t.up == "" {
		args = []string{"push", "--quiet", "-u", remote, "HEAD"}
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
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return err
	}
	dirty, err := r.settled(ctx, gitDir)
	if err != nil {
		return err
	}
	t, err := r.upstream(ctx)
	if err != nil {
		return err
	}
	if n, err := r.behind(ctx, t.up); err != nil || n == 0 {
		return nil
	}
	return r.holds(ctx, dirty, t.up)
}

// pull is Pull: the repository's remote, and the branch HEAD is on.
func (r Repo) pull(ctx context.Context) (remote string, t tracking, err error) {
	if remote, err = r.remote(ctx); err != nil {
		return "", tracking{}, err
	}
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return "", tracking{}, err
	}
	dirty, err := r.settled(ctx, gitDir)
	if err != nil {
		return "", tracking{}, err
	}
	if t, err = r.upstream(ctx); err != nil {
		return "", tracking{}, err
	}
	if err := r.fetch(ctx, remote, t); err != nil {
		return "", tracking{}, err
	}
	n, err := r.behind(ctx, t.up)
	if err != nil {
		return "", tracking{}, fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
	}
	if n == 0 {
		return remote, t, nil
	}
	if err := r.holds(ctx, dirty, t.up); err != nil {
		return "", tracking{}, err
	}
	// Begun, what's set aside is put back whatever becomes of the run.
	return remote, t, r.catchUp(context.WithoutCancel(ctx), gitDir, t.up)
}

// remote is the repository's one remote: ErrNoRemote without one, and a
// refusal (ErrNowhere) with several, as kit pushes only to one.
func (r Repo) remote(ctx context.Context) (string, error) {
	res, err := r.git(ctx, "remote")
	names := strings.Fields(string(res.Stdout))
	switch {
	case err != nil:
		return "", err
	case len(names) == 0:
		return "", ErrNoRemote
	case len(names) > 1:
		return "", nowhere{fmt.Errorf("the repository has %d remotes, so kit pushes nothing: kit pushes to one alone", len(names))}
	}
	return names[0], nil
}

// tracking is the branch HEAD is on, as in main, and its upstream: the
// remote-tracking ref, as in refs/remotes/origin/main, and the branch it
// is on the remote, as in refs/heads/main, both "" for none.
type tracking struct{ branch, up, merge string }

// upstream is the branch HEAD is on: a refusal (ErrNowhere) when it's on
// none.
func (r Repo) upstream(ctx context.Context) (tracking, error) {
	res, err := r.git(ctx, "symbolic-ref", "-q", "HEAD")
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return tracking{}, nowhere{errors.New("HEAD isn't on a branch, so kit pushes nothing")}
	}
	if err != nil {
		return tracking{}, err
	}
	ref := strings.TrimSpace(string(res.Stdout))
	if res, err = r.git(ctx, "for-each-ref", "--format=%(upstream) %(upstream:remoteref)", ref); err != nil {
		return tracking{}, err
	}
	up, merge, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), " ")
	return tracking{branch: strings.TrimPrefix(ref, "refs/heads/"), up: up, merge: merge}, nil
}

// settled is what's changed and not committed in the repository whose git
// folder is gitDir, unless git is part way through one of its operations
// there, or a conflict waits to be resolved, which is an error.
func (r Repo) settled(ctx context.Context, gitDir string) ([]entry, error) {
	if err := underWay(gitDir); err != nil {
		return nil, err
	}
	dirty, err := r.status(ctx)
	if err != nil {
		return nil, err
	}
	if e, ok := unmerged(dirty); ok {
		return nil, waitsFor(e)
	}
	return dirty, nil
}

// fetch brings in remote's commits, pruning its branches gone from it. A
// clone of one branch fetches that branch alone, and git fails the fetch
// while the remote hasn't it: in a repository whose branch may be missing
// (BranchMayBeMissing), t's upstream not there has nothing to bring in, as
// on a new, empty remote, and its remote-tracking ref, which the fetch
// couldn't prune, is deleted, so what's waiting is counted against the
// remote as it is.
func (r Repo) fetch(ctx context.Context, remote string, t tracking) error {
	_, err := r.git(ctx, "fetch", "--prune", "--quiet", remote)
	switch {
	case err == nil:
		return nil
	case r.BranchMayBeMissing && t.up != "" && notThere(err, t.merge):
		if _, err := r.git(ctx, "update-ref", "-d", t.up); err != nil {
			return fmt.Errorf("couldn't forget %s, gone from the remote, so nothing was pushed: %w", t.up, err)
		}
		return nil
	case unreachable(err):
		return errors.New("couldn't reach the remote, so nothing was pushed")
	}
	return fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
}

// notThere reports whether git failed a fetch for want of ref on the
// remote, as its fatal: line says.
func notThere(err error, ref string) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && slices.Contains(exit.Lines("fatal:"), "fatal: couldn't find remote ref "+ref)
}

// behind is how many of up's commits HEAD hasn't, up the branch's
// upstream: none for no upstream, or one not there, as on a remote with no
// branches yet.
func (r Repo) behind(ctx context.Context, up string) (int, error) {
	if up == "" {
		return 0, nil
	}
	if at, err := r.commitAt(ctx, up); err != nil || at == "" {
		return 0, err
	}
	return r.count(ctx, "HEAD.."+up)
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
// network, or the host not answering, as git's fatal: line says, curl's
// words in it over https, or ssh's own lines do.
func unreachable(err error) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && slices.ContainsFunc(exit.Lines(), func(line string) bool {
		if slices.ContainsFunc(sshUnreachable, func(s string) bool { return strings.HasPrefix(line, s) }) {
			return true
		}
		return strings.HasPrefix(line, "fatal:") && slices.ContainsFunc(offline, func(s string) bool { return strings.Contains(line, s) })
	})
}

// sshUnreachable start ssh's lines for a host it couldn't reach, the last
// with no prefix of ssh's; offline are the words for it in git's fatal:
// line.
var (
	sshUnreachable = []string{"ssh: Could not resolve hostname ", "ssh: connect to host ", "Connection timed out during banner exchange"}
	offline        = []string{
		"Could not resolve host", "Failed to connect", "Connection refused", "Connection timed out",
		"Operation timed out", "Network is unreachable", "No route to host",
	}
)

// holds is the refusal when dirty, what's not committed, can't be set
// aside to rebase onto up: a path swapped (swapped), or one up's commits
// change (clashes).
func (r Repo) holds(ctx context.Context, dirty []entry, up string) error {
	if err := r.swapped(dirty); err != nil {
		return err
	}
	return r.clashes(ctx, dirty, up)
}

// swapped is the refusal when dirty, what's not committed, has a path git
// tracks that's something else now: a file become a folder, by the folder
// on disk where git says the file's deleted or changed in type, as git
// lists no file in a repository of its own put there; or a folder become a
// file, by a path git doesn't track over one it does that's gone. Set
// aside, the path's put back as last committed, which deletes what's in
// its place. Nothing's touched.
func (r Repo) swapped(dirty []entry) error {
	for _, gone := range dirty {
		if gone.code == addedThenGone || !strings.ContainsAny(gone.code, "DT") {
			continue
		}
		if info, err := os.Lstat(filepath.Join(r.Dir, gone.path)); err == nil && info.IsDir() {
			return keptBack(gone.path, "was a file git tracks and is a folder now, whose files")
		}
		if i := slices.IndexFunc(dirty, func(e entry) bool { return e.code == "??" && strings.HasPrefix(gone.path, e.path+"/") }); i >= 0 {
			return keptBack(dirty[i].path, "was a folder git tracks and is a file now, which")
		}
	}
	return nil
}

// keptBack is swapped's refusal for path, which was says what became of.
func keptBack(path, was string) error {
	return fmt.Errorf("%s %s pulling would delete, so nothing was touched or pushed: commit or undo the change to %s first", path, was, path)
}

// clashes is the refusal when up's commits, those HEAD hasn't, change what's
// not committed here, dirty, staged or not, a nested repository's folder
// by any path in it: put back after the rebase, it would conflict, leaving
// git's markers in a file that may be linked into the home folder.
// Nothing's touched.
func (r Repo) clashes(ctx context.Context, dirty []entry, up string) error {
	incoming, err := r.incoming(ctx, up)
	if err != nil {
		return fmt.Errorf("couldn't read the remote's changes, so nothing was pushed: %w", err)
	}
	var hit []string
	for _, e := range dirty {
		meets := func(p string) bool { return p == e.path || e.nested() && strings.HasPrefix(p, e.path) }
		if slices.ContainsFunc(incoming, meets) && !slices.Contains(hit, e.path) {
			hit = append(hit, e.path)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return fmt.Errorf("the remote's commits change what's not committed here, so nothing was touched or pushed: commit or undo the change to %s first", strings.Join(hit, ", "))
}

// incoming are the paths up's commits change, those HEAD hasn't: since the
// two parted, or, with no commit in common, as when two Macs each began the
// store's branch, every path up has, as the rebase then replays every
// commit HEAD has onto up. Git's diff of HEAD...up fails without a commit
// in common, which alone has it ask for up's tree.
func (r Repo) incoming(ctx context.Context, up string) ([]string, error) {
	res, err := r.git(ctx, "diff", "--name-only", "-z", "--no-renames", "HEAD..."+up)
	if err != nil {
		if related, rerr := r.related(ctx, up); rerr != nil || related {
			return nil, err
		}
		if res, err = r.git(ctx, "ls-tree", "-r", "--name-only", "-z", up); err != nil {
			return nil, err
		}
	}
	return names(res.Stdout), nil
}

// related reports whether HEAD and up have a commit in common, as git
// merge-base says, exiting 1 for none.
func (r Repo) related(ctx context.Context, up string) (bool, error) {
	_, err := r.git(ctx, "merge-base", "HEAD", up)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return false, nil
	}
	return err == nil, err
}

// catchUp rebases what's committed here onto up, a remote-tracking ref, what
// isn't set aside meanwhile (setAside) and put back (putBack): kept in git's
// list of stashes when the rebase couldn't be undone.
func (r Repo) catchUp(ctx context.Context, gitDir, up string) error {
	stash, err := r.setAside(ctx)
	if err != nil {
		return err
	}
	rebased := r.rebase(ctx, gitDir, up)
	switch {
	case stash == "":
		return rebased
	case rebasing(gitDir):
		return errors.Join(rebased, r.keep(ctx, stash, errors.New("the rebase is still under way")))
	}
	return errors.Join(rebased, r.putBack(ctx, stash))
}

// setAside sets aside the changes not committed to files git tracks, staged
// or not, in a stash of their own, never in git's list of stashes (git
// stash create), the files then put back as last committed: the stash, ""
// with nothing to set aside. Files git doesn't track stay as they are, and
// failing, so does everything.
func (r Repo) setAside(ctx context.Context) (string, error) {
	res, err := r.git(ctx, "stash", "create", stashMessage)
	stash := strings.TrimSpace(string(res.Stdout))
	if err == nil && stash != "" {
		_, err = r.git(ctx, "reset", "--hard", "--quiet")
	}
	if err != nil {
		return "", fmt.Errorf("couldn't set aside what's not committed, so it's left as it was and nothing was pushed: %w", err)
	}
	return stash, nil
}

// rebase rebases what's committed here onto up, a remote-tracking ref,
// aborted if it stops part way, so back as it was: on a conflict, or when
// git, set to sign its commits, couldn't sign one it rebased, which is
// ErrUnsigned, as Commit's is.
func (r Repo) rebase(ctx context.Context, gitDir, up string) error {
	// Not by fork-point, which leaves out a commit the upstream had once,
	// before it was pushed over, so the rebase changes more than the
	// remote's commits, as clashes reads them.
	_, err := r.git(ctx, "rebase", "--no-fork-point", "--quiet", up)
	switch {
	case err == nil:
		return nil
	case !rebasing(gitDir):
		return fmt.Errorf("couldn't rebase onto the remote's changes, so nothing was pushed: %w", err)
	}
	then := "so nothing was pushed; the rebase was undone"
	if _, abort := r.git(ctx, "rebase", "--abort"); abort != nil || rebasing(gitDir) {
		then = "and the rebase couldn't be undone, so nothing was pushed: git rebase --abort undoes it"
	}
	if unwritten(err) {
		if signs, _ := r.flag(ctx, "commit.gpgsign"); signs {
			return fmt.Errorf("%w rebased onto the remote's changes, %s: %s", ErrUnsigned, then, signingSaid(err))
		}
	}
	return fmt.Errorf("the remote's changes conflict with this Mac's, %s: %w", then, err)
}

// rebasing reports whether a rebase is under way in the repository whose
// git folder is gitDir, by the folder git keeps while it is.
func rebasing(gitDir string) bool {
	return slices.ContainsFunc([]string{"rebase-merge", "rebase-apply"}, func(name string) bool {
		_, err := os.Lstat(filepath.Join(gitDir, name))
		return err == nil
	})
}

// putBack puts stash, what setAside set aside, back as it was, staged or
// not: kept in git's list of stashes when it won't go back.
func (r Repo) putBack(ctx context.Context, stash string) error {
	if _, err := r.git(ctx, "stash", "apply", "--index", "--quiet", stash); err != nil {
		return r.keep(ctx, stash, err)
	}
	return nil
}

// keep puts stash in git's list of stashes, as stashMessage, so nothing in
// it's lost, as why says it couldn't be put back, and is the error saying
// where it is.
func (r Repo) keep(ctx context.Context, stash string, why error) error {
	if _, err := r.git(ctx, "stash", "store", "-m", stashMessage, stash); err != nil {
		return fmt.Errorf("nothing was pushed, and what wasn't committed is in git's stash %s alone, not in its list (git stash apply --index %s puts it back): %w", stash, stash, errors.Join(why, err))
	}
	return fmt.Errorf("nothing was pushed, and what wasn't committed is kept in git's stash, as %q (git stash apply --index puts it back): %w", stashMessage, why)
}

// Waiting is when each commit waiting to be pushed was made: those the
// upstream hasn't, or, with it not there, those on none of the remote's
// branches. ErrNoRemote without a remote.
func (r Repo) Waiting(ctx context.Context) ([]time.Time, error) {
	remote, err := r.remote(ctx)
	if err != nil {
		return nil, err
	}
	t, err := r.upstream(ctx)
	if err != nil {
		return nil, err
	}
	revs := []string{"HEAD", "--not", "--remotes=" + remote}
	if t.up != "" {
		at, err := r.commitAt(ctx, t.up)
		if err != nil {
			return nil, err
		}
		if at != "" {
			revs = []string{t.up + "..HEAD"}
		}
	}
	res, err := r.git(ctx, slices.Concat([]string{"log", "--format=%ct"}, revs)...)
	if err != nil {
		return nil, err
	}
	var made []time.Time
	for f := range strings.FieldsSeq(string(res.Stdout)) {
		unix, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, err
		}
		made = append(made, time.Unix(unix, 0))
	}
	return made, nil
}

// PushURLs are where Push pushes, as git push has them: the remote's push
// URLs, else its URL, each with git's insteadOf applied. ErrNoRemote
// without a remote; an error that's ErrNowhere with several.
func (r Repo) PushURLs(ctx context.Context) ([]string, error) {
	remote, err := r.remote(ctx)
	if err != nil {
		return nil, err
	}
	res, err := r.git(ctx, "remote", "get-url", "--push", "--all", remote)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(strings.Split(string(res.Stdout), "\n"), func(url string) bool { return url == "" }), nil
}

// RemoteURL is the URL the repository's one remote is fetched from, as URL
// has it: ErrNoRemote without a remote; an error that's ErrNowhere with
// several.
func (r Repo) RemoteURL(ctx context.Context) (string, error) {
	remote, err := r.remote(ctx)
	if err != nil {
		return "", err
	}
	return r.URL(ctx, remote)
}

// SetURL is the URL the repository's one remote has set, before git's
// insteadOf and pushInsteadOf rewrite it, as a clone given it, under the
// same rules, fetches and pushes where this one does. ErrNoRemote without
// a remote; an error that's ErrNowhere with several.
func (r Repo) SetURL(ctx context.Context) (string, error) {
	remote, err := r.remote(ctx)
	if err != nil {
		return "", err
	}
	return r.setting(ctx, "remote."+remote+".url")
}

// UnderWay is the refusal while one of git's operations is part way
// through in the repository, as a rebase cut short: nil while none is.
func (r Repo) UnderWay(ctx context.Context) error {
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return err
	}
	return underWay(gitDir)
}

// URL is the URL the remote called remote is fetched from, as git takes it:
// "" when there's no such remote, as git says by exiting 2.
func (r Repo) URL(ctx context.Context, remote string) (string, error) {
	res, err := r.git(ctx, "remote", "get-url", remote)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 2 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), err
}

// Unpushed is how many commits are waiting to be pushed (Waiting): none
// when that can't be told.
func (r Repo) Unpushed(ctx context.Context) int {
	made, _ := r.Waiting(ctx)
	return len(made)
}

// Fetched is the file at path, from the repository's top, as the remote
// has it on the branch's upstream, or its branch of the same name, fetched
// now: nothing merged, the working tree and the branch left as they are.
func (r Repo) Fetched(ctx context.Context, path string) ([]byte, error) {
	remote, err := r.remote(ctx)
	if err != nil {
		return nil, err
	}
	t, err := r.upstream(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := r.git(ctx, "fetch", "--prune", "--quiet", remote); err != nil {
		if unreachable(err) {
			return nil, fmt.Errorf("couldn't reach %s", remote)
		}
		return nil, fmt.Errorf("couldn't fetch %s: %w", remote, err)
	}
	up := cmp.Or(t.up, "refs/remotes/"+remote+"/"+t.branch)
	res, err := r.git(ctx, "show", up+":"+path)
	if err != nil {
		return nil, fmt.Errorf("read %s as %s has it: %w", path, remote, err)
	}
	return res.Stdout, nil
}
