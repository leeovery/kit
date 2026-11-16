package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// Commit commits paths in the repository with message, each whole as it is
// on disk: a file edited, new or deleted, a folder's every file. Anything
// else, staged or not, is left as it is (git commit --only), and only new
// files are entered in git's index first, as to be added (git add -N), so a
// git commit by anyone else meanwhile takes none of kit's files. No hooks
// run, and it's signed as git commit signs, by the user's own config: git
// unable to sign it, nothing's committed (ErrUnsigned). What git can't keep
// is left out: an ignored file, a path gone git never kept, a repository
// nested in this one, a file added then gone before it was committed. It
// refuses while git is part way through a merge, rebase, cherry-pick,
// revert or am, or a conflict in paths waits to be resolved. It reports
// whether it committed: with nothing to commit it doesn't, which is no
// error. Committing nothing, it takes the new files back out of git's
// index.
func (r Repo) Commit(ctx context.Context, message string, paths ...string) (bool, error) {
	if len(paths) == 0 {
		return false, nil
	}
	changed, err := r.status(ctx, paths...)
	if err != nil {
		return false, err
	}
	if e, ok := unmerged(changed); ok {
		return false, waitsFor(e)
	}
	files, added := toCommit(changed)
	if len(files) == 0 {
		return false, nil
	}
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return false, err
	}
	if err := underWay(gitDir); err != nil {
		return false, err
	}
	signs, err := r.flag(ctx, "commit.gpgsign")
	if err != nil {
		return false, err
	}
	if len(added) > 0 {
		if _, err := r.locked(ctx, []string{"add", "--intent-to-add"}, added...); err != nil {
			return false, err
		}
	}
	committed, err := r.commitOnly(ctx, message, signs, files)
	if !committed && len(added) > 0 {
		_, undo := r.locked(context.WithoutCancel(ctx), []string{"rm", "--cached", "--quiet"}, added...)
		err = errors.Join(err, undo)
	}
	return committed, err
}

// toCommit are the files of changed, git's status of the paths committed,
// each once, that a commit takes, and those of them new to git: never a
// repository nested in this one, or a file added then gone, in neither the
// last commit nor on disk.
func toCommit(changed []entry) (files, added []string) {
	for _, e := range changed {
		if e.nested() || e.code == addedThenGone {
			continue
		}
		if e.code == "??" {
			added = append(added, e.path)
		}
		if !slices.Contains(files, e.path) {
			files = append(files, e.path)
		}
	}
	return files, added
}

// commitOnly commits files alone, each as it is on disk, signed when signs,
// as git's config says: nothing when they're on disk as last committed,
// their difference only in git's index. A file git tracks become a folder
// it refuses, as git commit --only does, naming it.
func (r Repo) commitOnly(ctx context.Context, message string, signs bool, files []string) (bool, error) {
	if same, err := r.unchanged(ctx, files); same || err != nil {
		return false, err
	}
	// Begun, the commit finishes whatever becomes of the run: git stopped
	// once HEAD has moved leaves its index behind it.
	_, err := r.locked(context.WithoutCancel(ctx), []string{"-c", "core.hooksPath=/dev/null", "commit", "--quiet", "--only", "-m", message}, files...)
	if signs && unwritten(err) {
		return false, fmt.Errorf("%w: %s", ErrUnsigned, signingSaid(err))
	}
	if path := folderNow(err); path != "" {
		return false, fmt.Errorf("%s was a file git tracks and is a folder now, which kit doesn't commit: commit the change by hand", path)
	}
	return err == nil, err
}

// folderNow is the path git, failing as err to commit it, found a folder
// where it tracks a file, as it says: "" for none.
func folderNow(err error) string {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return ""
	}
	for _, line := range exit.Lines("error: '") {
		if path, ok := strings.CutSuffix(strings.TrimPrefix(line, "error: '"), "' does not have a commit checked out"); ok {
			return path
		}
	}
	return ""
}

// unchanged reports whether files are on disk as last committed, as git
// reads them once new ones are entered in its index: never before the
// first commit.
func (r Repo) unchanged(ctx context.Context, files []string) (bool, error) {
	head, err := r.commitAt(ctx, "HEAD")
	if err != nil || head == "" {
		return false, err
	}
	_, err = r.gitOn(ctx, []string{"diff", "--quiet", "--no-ext-diff", "HEAD"}, files...)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return false, nil
	}
	return err == nil, err
}

// lockTries and lockWait are how long kit waits for another git to let go
// of the index: as long as a git status takes, never a git commit waiting
// on its editor.
const (
	lockTries = 20
	lockWait  = 100 * time.Millisecond
)

// locked runs git's args on paths (gitOn), again for a while when another
// git holds the index, then failing as git does, naming its lock.
func (r Repo) locked(ctx context.Context, args []string, paths ...string) (runner.Result, error) {
	for try := 1; ; try++ {
		res, err := r.gitOn(ctx, args, paths...)
		exit, ok := errors.AsType[*runner.ExitError](err)
		switch {
		case !ok || !strings.Contains(exit.FullStderr, "index.lock': File exists"):
			return res, err
		case try == lockTries || ctx.Err() != nil:
			return res, fmt.Errorf("another git holds the repository's index, so kit's change waits till it's done (%s): %w", strings.Join(exit.Lines("fatal:"), " "), err)
		}
		r.sleep(lockWait)
	}
}

// ErrUnsigned is Commit's error when git, set to sign its commits, made
// none: it couldn't sign it, as when the key it signs with isn't on the Mac
// yet, the error saying what git said of it. Nothing's committed.
var ErrUnsigned = errors.New("git couldn't sign the commit")

// noCommit is git's line for a commit it didn't write, as when it couldn't
// sign it: fatal: from git commit, error: from git rebase.
const noCommit = "failed to write commit object"

// unwritten reports whether err is git failing to write a commit.
func unwritten(err error) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && len(exit.Lines("fatal: "+noCommit, "error: "+noCommit)) > 0
}

// signingSaid is what git said of failing to sign a commit, err: the error
// lines its signing program left, above git's line that only says no
// commit was written; else the last line it printed.
func signingSaid(err error) string {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return err.Error()
	}
	var said []string
	for _, line := range exit.Lines("error:") {
		if line != "error: "+noCommit {
			said = append(said, strings.TrimSpace(strings.TrimPrefix(line, "error:")))
		}
	}
	if len(said) == 0 {
		return exit.Stderr
	}
	return strings.Join(said, "; ")
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

// waitsFor is the refusal while e's conflict waits to be resolved.
func waitsFor(e entry) error {
	return fmt.Errorf("a conflict in %s is waiting to be resolved: kit's change waits till it's finished", e.path)
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
