package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/runner"
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
// conflict in paths waits to be resolved. HEAD moved by another git
// meanwhile, it commits onto where HEAD is now, a few times at most, then
// gives up (ErrHeadMoving), HEAD and the user's index left as they were.
// The index a run of kit's left in git's lock, stopped once HEAD had taken
// its commit, it first puts in place, as that run would have. It reports
// what it did: with nothing to commit it doesn't, which is no error. An
// error once it has is the index's.
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
	lock, err := r.lockIndex(ctx, gitDir)
	if err != nil {
		return Outcome{}, err
	}
	defer lock.release()
	out := Outcome{Finished: lock.finished}
	if err := underWay(gitDir); err != nil {
		return out, err
	}
	out.Committed, err = r.commitLocked(ctx, lock, message, p)
	return out, err
}

// Outcome is what Commit did: whether it committed, and whether it first
// put in place the index a run of kit's that stopped left (Finished).
type Outcome struct {
	Committed, Finished bool
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
// index's next contents, paths as made has them, in the lock first: once
// HEAD has moved, all that's left is the lock taking the index's place. It's
// HEAD after: made, or where another git moved it meanwhile.
func (r Repo) swap(ctx context.Context, lock *indexLock, base, made, message string, paths []string) (string, error) {
	if err := r.prepare(ctx, lock, made, paths); err != nil {
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

// lockedRef is git saying another git holds a ref's lock, the lock's path
// in it.
var lockedRef = regexp.MustCompile(`Unable to create '([^']+\.lock)': File exists`)

// refHeld is err, git failing to move HEAD, naming the lock another git
// holds on HEAD's branch, when that's why.
func refHeld(err error) error {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return err
	}
	for _, line := range exit.Lines("fatal:") {
		if m := lockedRef.FindStringSubmatch(line); m != nil {
			return heldBy("HEAD's branch", m[1])
		}
	}
	return err
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
		reset := r.command(slices.Concat([]string{"reset", "-q", "--"}, paths)...)
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
	// finished is whether the index a run of kit's left in the lock was
	// put in place before it was taken.
	finished bool
	// unlocked, when set, is called as kit lets go.
	unlocked func()
}

// lockIndex takes git's lock on the index, trying for a while if another git
// holds it, then refusing, naming the lock, which it leaves be, unless it's
// the index a run of kit's left, which it first puts in place (finish);
// kit's own files a run that stopped left are swept away once it has it.
func (r Repo) lockIndex(ctx context.Context, gitDir string) (*indexLock, error) {
	path := filepath.Join(gitDir, "index.lock")
	finished := false
	for try := 1; ; try++ {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
		if err == nil {
			l := &indexLock{gitDir: gitDir, f: f, finished: finished, unlocked: r.onUnlock}
			l.sweep()
			return l, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("couldn't lock git's index: %w", err)
		}
		if try >= lockTries {
			if !r.finish(ctx, gitDir, path) {
				return nil, heldBy("the repository's index", path)
			}
			finished = true
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.sleep(lockWait)
	}
}

// finish puts the lock at path in the index's place when it holds the index
// a run of kit's left, stopped once HEAD had taken its commit, before the
// lock took the index's place, as that run would have: HEAD's last move
// kit's, that commit's files as committed in the lock and not in the index,
// and the lock left alone for as long as kit waits for git's index, so no
// git is writing it, and unchanged as it's read. The index not having taken
// the commit tells it from a lock a git took since, as git commit -a's while
// its editor's open. It reports whether it did.
func (r Repo) finish(ctx context.Context, gitDir, path string) bool {
	before, err := os.Stat(path)
	if err != nil || time.Since(before.ModTime()) < lockTries*lockWait || !r.leftByKit(ctx, path) {
		return false
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		return false
	}
	return os.Rename(path, filepath.Join(gitDir, "index")) == nil
}

// leftByKit reports whether the index at path is one a run of kit's left:
// HEAD's last move kit's, and the files its commit changed as committed
// there, while the user's index holds one as it was.
func (r Repo) leftByKit(ctx context.Context, path string) bool {
	res, err := r.git(ctx, "log", "--walk-reflogs", "-1", "--no-show-signature", "--format=%gs", "HEAD")
	if err != nil || !strings.HasPrefix(string(res.Stdout), "kit: ") {
		return false
	}
	res, err = r.git(ctx, "diff-tree", "-r", "--root", "--no-commit-id", "--name-only", "-z", "--no-renames", "HEAD")
	if err != nil {
		return false
	}
	committed := names(res.Stdout)
	inLock, err := r.notAtHEAD(ctx, path)
	if err != nil || slices.ContainsFunc(committed, func(p string) bool { return slices.Contains(inLock, p) }) {
		return false
	}
	inIndex, err := r.notAtHEAD(ctx, "")
	return err == nil && slices.ContainsFunc(committed, func(p string) bool { return slices.Contains(inIndex, p) })
}

// notAtHEAD are the files the index at path, the user's for "", holds
// otherwise than HEAD does: an error when it's no index git can read.
func (r Repo) notAtHEAD(ctx context.Context, path string) ([]string, error) {
	var env []string
	if path != "" {
		env = []string{"GIT_INDEX_FILE=" + path}
	}
	res, err := r.gitIn(ctx, env, nil, "diff-index", "--cached", "--name-only", "-z", "--no-renames", "HEAD")
	return names(res.Stdout), err
}

// heldBy is the refusal while another git holds what's locked by the lock
// at path: how old it is, and, for one a git that stopped left, what to do.
func heldBy(what, path string) error {
	age := ""
	if info, err := os.Stat(path); err == nil {
		age = ", made " + ago(time.Since(info.ModTime())) + " ago"
	}
	return fmt.Errorf("another git holds %s, its lock %s%s, so kit's change waits till it's done; if no git is running, one that stopped left it: remove %s", what, path, age, path)
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

// fill writes the index at next into the lock, with mode, unless that's
// none, and next's time: the index's next contents, once the lock takes its
// place. Filled again, it holds only the latest.
func (l *indexLock) fill(next string, mode fs.FileMode) error {
	info, err := os.Stat(next)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(next)
	if err != nil {
		return err
	}
	if err := l.f.Truncate(0); err != nil {
		return err
	}
	if _, err := l.f.WriteAt(data, 0); err != nil {
		return err
	}
	if mode != 0 {
		if err := os.Chmod(l.path(), mode); err != nil {
			return err
		}
	}
	return os.Chtimes(l.path(), info.ModTime(), info.ModTime())
}

// commit makes the lock, filled, the index, kit's own files swept away
// first, while it's still the lock.
func (l *indexLock) commit() error {
	if err := l.f.Close(); err != nil {
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
// away first, unless commit made the lock the index.
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
