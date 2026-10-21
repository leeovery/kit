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
	"time"
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
// does, so no other git changes either meanwhile; a file added then gone
// before it was committed (AD) leaves the index too. It refuses while git
// is part way through a merge, rebase, cherry-pick, revert or am, or a
// conflict in paths waits to be resolved. It reports whether it committed:
// with nothing to commit it doesn't, which is no error; nor when another
// git moved HEAD meanwhile, which is, HEAD and the user's index left as
// they were. An error once it has is the index's.
func (r Repo) Commit(ctx context.Context, message string, paths ...string) (bool, error) {
	if len(paths) == 0 {
		return false, nil
	}
	changed, err := r.status(ctx, paths...)
	if err != nil {
		return false, err
	}
	if e, ok := unmerged(changed); ok {
		return false, fmt.Errorf("a conflict in %s is waiting to be resolved: kit's change waits till it's finished", e.path)
	}
	p := plan(changed)
	if p.empty() {
		return false, nil
	}
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return false, err
	}
	lock, err := r.lockIndex(ctx, gitDir)
	if err != nil {
		return false, err
	}
	defer lock.release()
	if err := underWay(gitDir); err != nil {
		return false, err
	}
	return r.commitLocked(ctx, lock, message, p)
}

// commitLocked commits p holding git's index, then has the index take the
// files committed, and those added then gone, as last committed.
func (r Repo) commitLocked(ctx context.Context, lock *indexLock, message string, p commitPlan) (bool, error) {
	base, err := r.commitAt(ctx, "HEAD")
	if err != nil {
		return false, err
	}
	files, err := r.stage(ctx, lock, base, p)
	if err != nil {
		return false, err
	}
	committed := len(files) > 0
	if committed {
		if err := r.commitTree(ctx, lock, base, message); err != nil {
			return false, err
		}
		// What follows is the index catching up with a commit made: it's
		// done whatever becomes of the run.
		ctx = context.WithoutCancel(ctx)
	}
	if err := r.resetIndex(ctx, lock, slices.Concat(files, p.gone)); err != nil {
		return committed, fmt.Errorf("git's index still shows them as they were: %w", err)
	}
	return committed, nil
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
// message, as git's plumbing does: its tree written, a commit of it made,
// signed when git commit would sign it (commit.gpgsign), and HEAD moved
// onto it only from base, so a commit another git made meanwhile is never
// undone: HEAD moved, nothing's committed, the commit made left unused.
func (r Repo) commitTree(ctx context.Context, lock *indexLock, base, message string) error {
	res, err := r.gitIn(ctx, []string{"GIT_INDEX_FILE=" + lock.file(ownIndex)}, nil, "write-tree")
	if err != nil {
		return err
	}
	args := []string{"commit-tree", strings.TrimSpace(string(res.Stdout))}
	if base != "" {
		args = append(args, "-p", base)
	}
	signs, err := r.flag(ctx, "commit.gpgsign")
	if err != nil {
		return err
	}
	if signs {
		args = append(args, "-S")
	}
	if res, err = r.git(ctx, append(args, "-m", message)...); err != nil {
		return err
	}
	made := strings.TrimSpace(string(res.Stdout))
	subject, _, _ := strings.Cut(message, "\n")
	// The last commit, base, as HEAD's old value: "" before the first,
	// which has git refuse should another git have made one meanwhile.
	if _, err := r.git(ctx, "update-ref", "-m", "kit: "+subject, "HEAD", made, base); err != nil {
		return r.unmoved(ctx, made, base, err)
	}
	return nil
}

// unmoved is the error for HEAD not taking made, the commit kit made onto
// base, as err says: none when it did all the same, the run stopped as git
// moved it; HEAD moved, when another git moved it off base meanwhile.
func (r Repo) unmoved(ctx context.Context, made, base string, err error) error {
	head, headErr := r.commitAt(context.WithoutCancel(ctx), "HEAD")
	switch {
	case headErr == nil && head == made:
		return nil
	case headErr == nil && head != base:
		return errors.New("HEAD moved while kit committed, so nothing was committed: run kit again")
	}
	return errors.Join(err, headErr)
}

// resetIndex has git's index take files as last committed: a copy of it
// takes them, and is written into the lock, which then takes the index's
// place, as git does.
func (r Repo) resetIndex(ctx context.Context, lock *indexLock, files []string) error {
	if len(files) == 0 {
		return nil
	}
	next := lock.file(nextIndex)
	mode, err := copyIndex(lock.file("index"), next)
	if err != nil {
		return err
	}
	env := []string{"GIT_INDEX_FILE=" + next}
	if _, err := r.gitIn(ctx, env, files, "--literal-pathspecs", "reset", "--quiet", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
		return err
	}
	return lock.replace(next, mode)
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

// ownIndex and nextIndex are the files in the git folder a commit works in
// while it holds git's index: an index of its own, then the next of the
// user's. Every file whose name starts with ownIndex is kit's, swept away
// while kit holds the index, never after: by then another git may hold it,
// another kit working in its own.
const (
	ownIndex  = "kit-index"
	nextIndex = "kit-index.next"
)

// lockTries and lockWait are how long a commit waits for another git to let
// go of the index: as long as a git status takes, never a git commit
// waiting on its editor.
const (
	lockTries = 20
	lockWait  = 100 * time.Millisecond
)

// indexLock is git's lock on the index, index.lock in the git folder, held
// as git holds it: made only when it isn't there, and either removed or
// renamed over the index with the index's next contents.
type indexLock struct {
	gitDir string
	f      *os.File
	done   bool
	// unlocked, when set, is called as kit lets go.
	unlocked func()
}

// lockIndex takes git's lock on the index, trying for a while if another git
// holds it, then refusing, naming the lock, which it leaves be; kit's own
// files a run that stopped left are swept away once it has it.
func (r Repo) lockIndex(ctx context.Context, gitDir string) (*indexLock, error) {
	path := filepath.Join(gitDir, "index.lock")
	for try := 1; ; try++ {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
		if err == nil {
			l := &indexLock{gitDir: gitDir, f: f, unlocked: r.onUnlock}
			l.sweep()
			return l, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("couldn't lock git's index: %w", err)
		}
		if try == lockTries {
			return nil, heldBy(path)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.sleep(lockWait)
	}
}

// heldBy is the refusal while another git holds the index's lock at path:
// how old it is, and, for one a git that stopped left, what to do.
func heldBy(path string) error {
	age := ""
	if info, err := os.Stat(path); err == nil {
		age = ", made " + ago(time.Since(info.ModTime())) + " ago"
	}
	return fmt.Errorf("another git holds the repository's index, its lock %s%s, so kit's change waits till it's done; if no git is running, one that stopped left it: remove %s", path, age, path)
}

// ago says roughly how long d is.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return count(int(d.Seconds()), "second")
	case d < time.Hour:
		return count(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return count(int(d.Hours()), "hour")
	}
	return count(int(d.Hours()/24), "day")
}

// count is n of unit, as in 1 hour or 3 hours.
func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func (r Repo) sleep(d time.Duration) {
	if r.Sleep == nil {
		time.Sleep(d)
		return
	}
	r.Sleep(d)
}

// file is name's path in the git folder.
func (l *indexLock) file(name string) string {
	return filepath.Join(l.gitDir, name)
}

// path is the lock's.
func (l *indexLock) path() string {
	return l.f.Name()
}

// sweep clears the git folder of kit's own index files: only while kit
// holds the index, so none is in use.
func (l *indexLock) sweep() {
	entries, _ := os.ReadDir(l.gitDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ownIndex) {
			_ = os.Remove(l.file(e.Name()))
		}
	}
}

// replace makes the index at next the index, with mode, unless that's
// none: written into the lock, with next's time, which takes the index's
// place, kit's own files swept away first, while it's still the lock.
func (l *indexLock) replace(next string, mode fs.FileMode) error {
	info, err := os.Stat(next)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(next)
	if err != nil {
		return err
	}
	if _, err := l.f.Write(data); err != nil {
		return err
	}
	if err := l.f.Close(); err != nil {
		return err
	}
	if mode != 0 {
		if err := os.Chmod(l.path(), mode); err != nil {
			return err
		}
	}
	if err := os.Chtimes(l.path(), info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	l.sweep()
	if err := os.Rename(l.path(), l.file("index")); err != nil {
		return err
	}
	l.letGo()
	return nil
}

// release lets go of the lock, the index as it was, kit's own files swept
// away first, unless replace made the lock the index.
func (l *indexLock) release() {
	if l.done {
		return
	}
	_ = l.f.Close()
	l.sweep()
	_ = os.Remove(l.path())
	l.letGo()
}

// letGo marks the lock let go of, nothing of the git folder kit's again.
func (l *indexLock) letGo() {
	l.done = true
	if l.unlocked != nil {
		l.unlocked()
	}
}
