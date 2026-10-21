package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Commit commits paths in the repository with message, each whole as it is
// on disk: a file edited, new or deleted, staged or not, a rename's two
// sides, a folder's every file, a file become a folder or a folder a file.
// Anything else staged is left as it is, the commit made from an index of
// the last commit's and these alone, with git's plumbing: no commit hooks
// run, and it's signed as git commit would sign it. A file staged as
// deleted, on disk and ignored, is committed as deleted, as the user staged
// it. What git can't keep is left out, never failing the rest: an ignored
// file, an empty folder, a path gone git never kept, a repository nested in
// this one. It holds git's index from before it reads the last commit till
// the user's index holds the files committed as committed, as git commit
// does, so no other git changes either meanwhile, and kit's own lock on
// committing there all the while, so no other kit does; a file added then gone
// before it was committed (AD) leaves the index too. It refuses while git
// is part way through a merge, rebase, cherry-pick, revert or am, or a
// conflict in paths waits to be resolved. HEAD moved by another git
// meanwhile, it commits onto where HEAD is now, a few times at most, then
// gives up (ErrHeadMoving), HEAD and the user's index left as they were.
// What a run of kit's that stopped left it first deals with (recover). It
// reports what it did: with nothing to commit it doesn't, which is no
// error. An error once it has is the index's.
func (r Repo) Commit(ctx context.Context, message string, paths ...string) (Outcome, error) {
	if len(paths) == 0 {
		return Outcome{}, nil
	}
	changed, err := r.status(ctx, paths...)
	if err != nil {
		return Outcome{}, err
	}
	if e, ok := unmerged(changed); ok {
		return Outcome{}, fmt.Errorf("a conflict in %s is waiting to be resolved: kit's change waits till it's finished", e.path)
	}
	p := plan(changed)
	if p.empty() {
		return Outcome{}, nil
	}
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if err := underWay(gitDir); err != nil {
		return Outcome{}, err
	}
	commits, err := r.lockCommits(ctx, gitDir)
	if err != nil {
		return Outcome{}, err
	}
	var out Outcome
	if commits == nil {
		out.Notes = []string{unlocked}
	} else {
		defer func() { _ = commits.Close() }()
		if out.Notes, err = r.recover(ctx, gitDir); err != nil {
			return out, err
		}
	}
	lock, err := r.lockIndex(ctx, gitDir)
	if err != nil {
		return out, err
	}
	lock.commits = commits
	defer lock.release()
	if err := underWay(gitDir); err != nil {
		return out, err
	}
	out.Committed, err = r.commitLocked(ctx, lock, message, p)
	return out, err
}

// Outcome is what Commit did: whether it committed, and what else it did
// worth saying (Notes), as dealing with what a run of kit's that stopped
// left.
type Outcome struct {
	Committed bool
	Notes     []string
}

// ErrHeadMoving is Commit's error when other gits kept moving HEAD while
// kit committed, so nothing was: the change is left as it is on disk.
var ErrHeadMoving = errors.New("HEAD kept moving while kit committed, so nothing was committed")

// swapTries is how many times a commit is made, each anew onto where
// another git moved HEAD meanwhile, holding the index all the while.
const swapTries = 3

// commitLocked commits p holding git's index, the user's index then taking
// the files committed, and those added then gone, as committed. HEAD moved
// by another git meanwhile, it's made again onto where HEAD is now.
func (r Repo) commitLocked(ctx context.Context, lock *indexLock, message string, p commitPlan) (bool, error) {
	message = cleaned(message)
	base, err := r.commitAt(ctx, "HEAD")
	if err != nil {
		return false, err
	}
	for range swapTries {
		files, err := r.stage(ctx, lock, base, p)
		if err != nil {
			return false, err
		}
		if len(files) == 0 {
			return false, r.forget(ctx, lock, p.gone)
		}
		made, err := r.commitTree(ctx, lock, base, message)
		if err != nil {
			return false, err
		}
		// Made, the commit goes in whatever becomes of the run, unless
		// another git moved HEAD meanwhile.
		paths := slices.Concat(files, p.gone)
		head, err := r.swap(context.WithoutCancel(ctx), lock, base, made, message, paths)
		switch {
		case err != nil:
			return false, err
		case head == made:
			return true, r.settle(lock, paths)
		}
		base = head
	}
	return false, ErrHeadMoving
}

// cleaned is message as git commit -m leaves it: each line without its
// trailing whitespace, runs of blank lines one, and none first or last.
func cleaned(message string) string {
	var lines []string
	blank := false
	for line := range strings.Lines(message) {
		if line = strings.TrimRight(line, " \t\n\v\f\r"); line == "" {
			blank = len(lines) > 0
			continue
		}
		if blank {
			lines = append(lines, "")
		}
		lines, blank = append(lines, line), false
	}
	return strings.Join(lines, "\n")
}

// stage stages p in an index of base's and p's files alone, kit's own
// while it holds lock, and returns the files that differ from base there:
// none when a commit would change nothing.
func (r Repo) stage(ctx context.Context, lock *indexLock, base string, p commitPlan) ([]string, error) {
	env := []string{"GIT_INDEX_FILE=" + lock.file(ownIndex)}
	if base != "" {
		if _, err := r.gitIn(ctx, env, nil, "read-tree", base); err != nil {
			return nil, err
		}
	}
	if len(p.deleted) > 0 {
		if _, err := r.gitIn(ctx, env, p.deleted, "update-index", "--force-remove", "-z", "--stdin"); err != nil {
			return nil, err
		}
	}
	if len(p.stage) > 0 {
		if _, err := r.gitIn(ctx, env, p.stage, "update-index", "--add", "--remove", "--replace", "-z", "--stdin"); err != nil {
			return nil, err
		}
	}
	res, err := r.gitIn(ctx, env, nil, "diff", "--cached", "--name-only", "-z", "--no-renames")
	if err != nil {
		return nil, err
	}
	return names(res.Stdout), nil
}

// commitTree commits kit's own index, which lock holds, onto base with
// message, as git's plumbing does: its tree written, and a commit of it
// made, signed when git commit would sign it (commit.gpgsign). It's the
// commit made.
func (r Repo) commitTree(ctx context.Context, lock *indexLock, base, message string) (string, error) {
	res, err := r.gitIn(ctx, []string{"GIT_INDEX_FILE=" + lock.file(ownIndex)}, nil, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", strings.TrimSpace(string(res.Stdout))}
	if base != "" {
		args = append(args, "-p", base)
	}
	signs, err := r.flag(ctx, "commit.gpgsign")
	if err != nil {
		return "", err
	}
	if signs {
		args = append(args, "-S")
	}
	if res, err = r.git(ctx, append(args, "-m", message)...); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// swap moves HEAD onto made, the commit kit made onto base, only from base,
// so a commit another git made meanwhile is never undone, the user's
// index's next contents, paths as made has them, in the lock first, and
// kit's mark of it beside it: once HEAD has moved, all that's left is the
// lock taking the index's place, which a run that finds it left does
// (finish). It's HEAD after: made, or where another git moved it meanwhile.
func (r Repo) swap(ctx context.Context, lock *indexLock, base, made, message string, paths []string) (string, error) {
	if err := r.prepare(ctx, lock, made, paths); err != nil {
		return "", err
	}
	if err := lock.mark(base, made); err != nil {
		return "", err
	}
	subject, _, _ := strings.Cut(message, "\n")
	// HEAD's old value is base: "" before the first commit, which has git
	// refuse should another git have made one meanwhile.
	_, err := r.git(ctx, "update-ref", "-m", "kit: "+subject, "HEAD", made, base)
	if err == nil {
		return made, nil
	}
	head, headErr := r.commitAt(ctx, "HEAD")
	if headErr == nil && head != base {
		return head, nil
	}
	return "", errors.Join(refHeld(err), headErr)
}

// forget has the user's index let go of gone, files added then gone, with
// nothing committed.
func (r Repo) forget(ctx context.Context, lock *indexLock, gone []string) error {
	if len(gone) == 0 {
		return nil
	}
	if err := r.prepare(ctx, lock, "", gone); err != nil {
		return err
	}
	return lock.commit()
}

// prepare writes the user's index's next contents into the lock: a copy of
// the index, paths in it as rev has them, HEAD for "", which git's reset of
// paths makes so, never moving HEAD.
func (r Repo) prepare(ctx context.Context, lock *indexLock, rev string, paths []string) error {
	next := lock.file(nextIndex)
	mode, err := copyIndex(lock.file("index"), next)
	if err != nil {
		return err
	}
	args := []string{"--literal-pathspecs", "reset", "--quiet"}
	if rev != "" {
		args = append(args, rev)
	}
	if _, err := r.gitIn(ctx, []string{"GIT_INDEX_FILE=" + next}, paths, append(args, "--pathspec-from-file=-", "--pathspec-file-nul")...); err != nil {
		return err
	}
	return lock.fill(next, mode)
}

// settle has the lock take the index's place, HEAD having taken kit's
// commit: failing, it says how to bring the index up to date, as a git
// commit from it would undo kit's.
func (r Repo) settle(lock *indexLock, paths []string) error {
	if err := lock.commit(); err != nil {
		reset := r.command(slices.Concat([]string{"--literal-pathspecs", "reset", "-q", "--"}, paths)...)
		return fmt.Errorf("git's index still shows them as they were, so a git commit would undo them: %s brings it up to date: %w", reset, err)
	}
	return nil
}

// copyIndex copies the index at from to to, and is its mode: none when
// there's no index yet, git making one. The copy keeps the index's time, so
// git still finds an entry whose file changed within the second the index
// was written racy, and reads the file (git's racy-git rule): one newer
// would have git trust the entry, and miss the change.
func copyIndex(from, to string) (fs.FileMode, error) {
	info, err := os.Stat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		return 0, err
	}
	return info.Mode().Perm(), os.Chtimes(to, info.ModTime(), info.ModTime())
}

// commitPlan is what a commit does with the files status names: those to
// stage as they are on disk; those to commit as deleted, staged so and on
// disk with no untracked twin, as an ignored file has none; and those added
// then gone, which only leave the index. A nested repository it leaves
// alone.
type commitPlan struct {
	stage, deleted, gone []string
}

func plan(changed []entry) commitPlan {
	var p commitPlan
	seen := map[string]bool{}
	for _, e := range changed {
		if e.nested() || seen[e.path] {
			continue
		}
		seen[e.path] = true
		switch {
		case e.code == addedThenGone:
			p.gone = append(p.gone, e.path)
		case deletedAlone(e, changed):
			p.deleted = append(p.deleted, e.path)
		default:
			p.stage = append(p.stage, e.path)
		}
	}
	return p
}

func (p commitPlan) empty() bool {
	return len(p.stage)+len(p.deleted)+len(p.gone) == 0
}

// unmergedCodes are status's codes for a file whose conflict is waiting to
// be resolved.
var unmergedCodes = []string{"UU", "AA", "DD", "AU", "UA", "DU", "UD"}

// unmerged is the first of entries waiting to be resolved.
func unmerged(entries []entry) (entry, bool) {
	i := slices.IndexFunc(entries, func(e entry) bool { return slices.Contains(unmergedCodes, e.code) })
	if i < 0 {
		return entry{}, false
	}
	return entries[i], true
}

// operations are git's that can stop part way, each by the file in the git
// folder that's there till it's finished: a commit made meanwhile would
// finish it, or be lost to it.
var operations = []struct{ file, name string }{
	{"MERGE_HEAD", "merge"},
	{"CHERRY_PICK_HEAD", "cherry-pick"},
	{"REVERT_HEAD", "revert"},
	{"rebase-merge", "rebase"},
	{"rebase-apply", "rebase or git am"},
	{"sequencer", "cherry-pick or revert"},
}

// underWay errors when one of git's operations is part way through in the
// repository whose git folder is gitDir.
func underWay(gitDir string) error {
	for _, op := range operations {
		if _, err := os.Lstat(filepath.Join(gitDir, op.file)); err == nil {
			return fmt.Errorf("a %s is under way in the repository (%s): kit's change waits till it's finished", op.name, op.file)
		}
	}
	return nil
}
