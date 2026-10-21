package gitrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// ownIndex and nextIndex are the files in the git folder a commit works in
// while it holds git's index: an index of its own, then the next of the
// user's. Every file whose name starts with ownIndex is kit's, swept away
// only while kit holds the repository's commit lock, so no other kit is
// using one. marked is kit's mark (mark), which outlasts a sweep, as a run
// that finds it reads it first; nextMark the file it's written in before
// it takes its place, swept with kit's other files.
const (
	ownIndex  = "kit-index"
	nextIndex = "kit-index.next"
	nextMark  = "kit-index.mark"
	marked    = "kit-mark"
)

// commitLock is the file in the git folder whose kernel lock kit holds for
// a whole commit, so no other kit commits there meanwhile, the kernel
// letting go should the run die. It's never swept or removed: a kit waiting
// for it would go on to lock a file no longer there. Its name doesn't end
// in .lock, which cleaners of git's stale locks remove.
const commitLock = "kit-commits"

// lockTries and lockWait are how long a commit waits for another git to let
// go of the index, or another kit of the repository: as long as a git
// status takes, never a git commit waiting on its editor.
const (
	lockTries = 20
	lockWait  = 100 * time.Millisecond
)

// errLockTaken is why kit left git's lock on the index be as it came to put
// it in place; errCommitsTaken why it committed nothing, as it came to move
// HEAD.
var (
	errLockTaken    = errors.New("kit's lock on git's index was removed or replaced by another git meanwhile, so kit left it be")
	errCommitsTaken = errors.New("kit's lock on committing in the repository was removed or replaced meanwhile, so nothing was committed, as another kit may be committing")
)

// lockCommits takes kit's lock on committing in the repository whose git
// folder is gitDir, trying for a while if another kit holds it, then
// refusing; closing the file lets go of it. A file a cleaner removed or
// replaced meanwhile, it locks the one there now. It's nil, with no error,
// where the git folder's filesystem takes no such lock (NFS mounted without
// locks, some FUSE filesystems).
func (r Repo) lockCommits(ctx context.Context, gitDir string) (*os.File, error) {
	path := filepath.Join(gitDir, commitLock)
	var f *os.File
	unlockable := false
	got, err := r.wait(ctx, func() (bool, error) {
		if f == nil {
			var err error
			if f, err = os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644); err != nil {
				return false, fmt.Errorf("couldn't lock the repository for kit's commit: %w", err)
			}
		}
		err := r.lockFile(f)
		switch {
		case errors.Is(err, syscall.EWOULDBLOCK):
			return false, nil
		case errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, syscall.ENOLCK):
			unlockable = true
			return true, nil
		case err != nil:
			return false, fmt.Errorf("couldn't lock the repository for kit's commit: %w", err)
		case !stillAt(f, path):
			_ = f.Close()
			f = nil
			return false, nil
		}
		return true, nil
	})
	if err == nil && !got {
		err = errors.New("another kit is committing in the repository, so kit's change waits till it's done")
	}
	if err != nil || unlockable {
		if f != nil {
			_ = f.Close()
		}
		return nil, err
	}
	return f, nil
}

// lockFile takes the kernel's lock on f, never waiting.
func (r Repo) lockFile(f *os.File) error {
	if r.flock != nil {
		return r.flock(int(f.Fd()))
	}
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// stillAt reports whether f, open, is the file at path.
func stillAt(f *os.File, path string) bool {
	held, err := f.Stat()
	if err != nil {
		return false
	}
	now, err := os.Lstat(path)
	return err == nil && os.SameFile(held, now)
}

// indexLock is git's lock on the index, index.lock in the git folder, held
// as git holds it: made only when it isn't there, and either removed or
// renamed over the index with the index's next contents.
type indexLock struct {
	gitDir string
	f      *os.File
	// made is the lock's file as kit made it, to tell it from another's in
	// its place.
	made fs.FileInfo
	// sum is the hash of what kit filled it with.
	sum string
	// commits is kit's lock on committing, held: nil without it, where no
	// mark is left, as no run would read it.
	commits *os.File
	// marked is whether this run left its mark (mark), the only one it
	// removes.
	marked bool
	done   bool
	// unlocked, when set, is called as kit lets go.
	unlocked func()
}

// lockIndex takes git's lock on the index, trying for a while if another
// git holds it, then refusing, naming the lock, which it leaves be; kit's
// own files a run that stopped left are swept away once it has it.
func (r Repo) lockIndex(ctx context.Context, gitDir string) (*indexLock, error) {
	path := filepath.Join(gitDir, "index.lock")
	var f *os.File
	got, err := r.wait(ctx, func() (bool, error) {
		var err error
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
		switch {
		case errors.Is(err, fs.ErrExist):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("couldn't lock git's index: %w", err)
		}
		return true, nil
	})
	if err == nil && !got {
		err = heldBy("the repository's index", path)
	}
	if err != nil {
		return nil, err
	}
	made, err := f.Stat()
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("couldn't lock git's index: %w", err)
	}
	l := &indexLock{gitDir: gitDir, f: f, made: made, unlocked: r.onUnlock}
	l.sweep()
	return l, nil
}

// wait has take try to take what kit waits for, again for a while till it
// does, and reports whether it did: an error is take's, or the run ending.
func (r Repo) wait(ctx context.Context, take func() (bool, error)) (bool, error) {
	for try := 1; ; try++ {
		if got, err := take(); got || err != nil {
			return got, err
		}
		if try == lockTries {
			return false, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		r.sleep(lockWait)
	}
}

// lockedRef is git saying another git holds a ref's lock, the lock's path
// in it.
var lockedRef = regexp.MustCompile(`Unable to create '([^']+\.lock)': File exists`)

// refHeld is err, git failing to move HEAD, naming the lock another git
// holds, HEAD's own or its branch's, when that's why.
func refHeld(err error) error {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return err
	}
	for _, line := range exit.Lines("fatal:") {
		m := lockedRef.FindStringSubmatch(line)
		switch {
		case m == nil:
			continue
		case filepath.Base(m[1]) == "HEAD.lock":
			return heldBy("HEAD", m[1])
		}
		return heldBy("HEAD's branch", m[1])
	}
	return err
}

// heldBy is the refusal while another git holds what's locked by the lock
// at path: how old it is, by when it last changed, as a lock of kit's
// carries the index's time, and, for one a git that stopped left, what to
// do.
func heldBy(what, path string) error {
	age := ""
	if info, err := os.Stat(path); err == nil {
		age = ", made " + since(time.Since(changed(info)))
	}
	return fmt.Errorf("another git holds %s, its lock %s%s, so kit's change waits till it's done; if no git is running, one that stopped left it: remove %s", what, path, age, path)
}

// changed is when the file info describes last changed, its contents or
// its times.
func changed(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctimespec.Unix())
	}
	return info.ModTime()
}

// since says roughly how long ago it was, d ago: just now, or as in 3 hours
// ago.
func since(d time.Duration) string {
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return count(int(d.Seconds()), "second") + " ago"
	case d < time.Hour:
		return count(int(d.Minutes()), "minute") + " ago"
	case d < 48*time.Hour:
		return count(int(d.Hours()), "hour") + " ago"
	}
	return count(int(d.Hours()/24), "day") + " ago"
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

// mine reports whether the lock is still the file kit made: not removed,
// nor another git's in its place.
func (l *indexLock) mine() bool {
	now, err := os.Lstat(l.path())
	return err == nil && os.SameFile(l.made, now)
}

// sweep clears the git folder of kit's own index files.
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
		if err := l.f.Chmod(mode); err != nil {
			return err
		}
	}
	sum := sha256.Sum256(data)
	l.sum = hex.EncodeToString(sum[:])
	at := syscall.NsecToTimeval(info.ModTime().UnixNano())
	return syscall.Futimes(int(l.f.Fd()), []syscall.Timeval{at, at})
}

// mark leaves, before HEAD moves from base onto made, kit's mark of the
// lock as filled, and of what the user's index held for each path the
// commit takes (read), for a run that finds it left should this one stop
// (recover), once it's sure it still holds its lock on committing, so no
// other kit reads it meanwhile: none without that lock. It's written whole
// or not at all (writeWhole).
func (l *indexLock) mark(base, made string, read map[string]string) error {
	switch {
	case l.commits == nil:
		return nil
	case !stillAt(l.commits, l.file(commitLock)):
		return errCommitsTaken
	}
	m := mark{base: base, made: made, sum: l.sum, file: fileID(l.made), read: read}
	if err := writeWhole(l.file(marked), l.file(nextMark), m.String()); err != nil {
		return err
	}
	l.marked = true
	return nil
}

// writeWhole writes data to the file at path in one step, so a stop or a
// power loss leaves it whole or not there: into the file at next first,
// synced to the disk, then renamed over it. A next a failure leaves is one
// of kit's own files, which the sweep takes.
func writeWhole(path, next, data string) error {
	f, err := os.OpenFile(next, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(data)
	if err == nil {
		err = f.Sync()
	}
	if closed := f.Close(); err == nil {
		err = closed
	}
	if err != nil {
		return err
	}
	return os.Rename(next, path)
}

// unmark removes this run's mark, when it left one.
func (l *indexLock) unmark() {
	if l.marked {
		_ = os.Remove(l.file(marked))
	}
}

// commit makes the lock, filled, the index, then sweeps kit's own files
// away, and this run's mark; a lock no longer kit's it leaves be.
func (l *indexLock) commit() error {
	if err := l.f.Close(); err != nil {
		return err
	}
	if !l.mine() {
		l.letGo()
		return errLockTaken
	}
	if err := os.Rename(l.path(), l.file("index")); err != nil {
		return err
	}
	l.sweep()
	l.unmark()
	l.letGo()
	return nil
}

// release lets go of the lock, the index as it was, kit's own files and
// this run's mark swept away, unless commit made the lock the index; a lock
// no longer kit's it leaves be, and kit's files with it.
func (l *indexLock) release() {
	if l.done {
		return
	}
	_ = l.f.Close()
	if l.mine() {
		l.sweep()
		_ = os.Remove(l.path())
		l.unmark()
	}
	l.letGo()
}

func (l *indexLock) letGo() {
	l.done = true
	if l.unlocked != nil {
		l.unlocked()
	}
}
