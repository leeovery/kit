package gitrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
// user's; marked is the mark it leaves before HEAD moves. Every file whose
// name starts with ownIndex is kit's, swept away only while kit holds the
// repository's commit lock, so no other kit is using one.
const (
	ownIndex  = "kit-index"
	nextIndex = "kit-index.next"
	marked    = "kit-index.made"
)

// commitLock is the file in the git folder whose kernel lock kit holds for
// a whole commit, so no other kit commits there meanwhile, the kernel
// letting go should the run die. It's never swept or removed: a kit waiting
// for it would go on to lock a file no longer there.
const commitLock = "kit-commit.lock"

// lockTries and lockWait are how long a commit waits for another git to let
// go of the index, or another kit of the repository: as long as a git
// status takes, never a git commit waiting on its editor.
const (
	lockTries = 20
	lockWait  = 100 * time.Millisecond
)

// errLockTaken is why kit left git's lock on the index be as it came to put
// it in place.
var errLockTaken = errors.New("kit's lock on git's index was removed or replaced by another git meanwhile, so kit left it be")

// lockCommits takes kit's lock on committing in the repository whose git
// folder is gitDir, trying for a while if another kit holds it, then
// refusing. Closing the file lets go of it.
func (r Repo) lockCommits(ctx context.Context, gitDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(gitDir, commitLock), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("couldn't lock the repository for kit's commit: %w", err)
	}
	got, err := r.wait(ctx, func() (bool, error) {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("couldn't lock the repository for kit's commit: %w", err)
		}
		return true, nil
	})
	if err == nil && !got {
		err = errors.New("another kit is committing in the repository, so kit's change waits till it's done")
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
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
	sum  string
	done bool
	// unlocked, when set, is called as kit lets go.
	unlocked func()
}

// lockIndex takes git's lock on the index, holding kit's commit lock,
// trying for a while if another git holds it, then refusing, naming the
// lock, which it leaves be; kit's own files a run that stopped left are
// swept away once it has it. A lock holding the index a run of kit's left,
// it first puts in place (finish), and reports it did.
func (r Repo) lockIndex(ctx context.Context, gitDir string) (*indexLock, bool, error) {
	path := filepath.Join(gitDir, "index.lock")
	finished := r.finish(ctx, gitDir, path)
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
		return nil, finished, err
	}
	made, err := f.Stat()
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, finished, fmt.Errorf("couldn't lock git's index: %w", err)
	}
	l := &indexLock{gitDir: gitDir, f: f, made: made, unlocked: r.onUnlock}
	l.sweep()
	return l, finished, nil
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

// finish puts the lock at path in the index's place when it holds the index
// a run of kit's left, stopped once HEAD had taken its commit, before the
// lock took the index's place, as that run would have. Kit holding the
// commit lock, that run is over; its mark says the rest: HEAD is the commit
// it names, and the lock the very file it filled, by its hash. It reports
// whether it did.
func (r Repo) finish(ctx context.Context, gitDir, path string) bool {
	mark := filepath.Join(gitDir, marked)
	data, err := os.ReadFile(mark)
	if err != nil {
		return false
	}
	made, filled, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	info, sum, err := hashed(path)
	if err != nil || sum != filled {
		return false
	}
	if head, err := r.commitAt(ctx, "HEAD"); err != nil || head != made {
		return false
	}
	if now, err := os.Lstat(path); err != nil || !os.SameFile(info, now) || os.Rename(path, filepath.Join(gitDir, "index")) != nil {
		return false
	}
	_ = os.Remove(mark)
	return true
}

// hashed is the file at path, and the hash of what it holds.
func hashed(path string) (fs.FileInfo, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, "", err
	}
	return info, hex.EncodeToString(h.Sum(nil)), nil
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
// at path: how old it is, and, for one a git that stopped left, what to do.
func heldBy(what, path string) error {
	age := ""
	if info, err := os.Stat(path); err == nil {
		age = ", made " + since(time.Since(info.ModTime()))
	}
	return fmt.Errorf("another git holds %s, its lock %s%s, so kit's change waits till it's done; if no git is running, one that stopped left it: remove %s", what, path, age, path)
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
		if err := os.Chmod(l.path(), mode); err != nil {
			return err
		}
	}
	sum := sha256.Sum256(data)
	l.sum = hex.EncodeToString(sum[:])
	return os.Chtimes(l.path(), info.ModTime(), info.ModTime())
}

// mark leaves, before HEAD moves onto made, the mark of the lock as filled,
// for a run that finds it left should this one stop before it takes the
// index's place (finish): made, and the lock's hash.
func (l *indexLock) mark(made string) error {
	return os.WriteFile(l.file(marked), []byte(made+" "+l.sum+"\n"), 0o600)
}

// commit makes the lock, filled, the index, then sweeps kit's own files
// away, its mark among them; a lock no longer kit's it leaves be.
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
	l.letGo()
	return nil
}

// release lets go of the lock, the index as it was, kit's own files swept
// away, unless commit made the lock the index; a lock no longer kit's it
// leaves be, and kit's files with it.
func (l *indexLock) release() {
	if l.done {
		return
	}
	_ = l.f.Close()
	if l.mine() {
		l.sweep()
		_ = os.Remove(l.path())
	}
	l.letGo()
}

func (l *indexLock) letGo() {
	l.done = true
	if l.unlocked != nil {
		l.unlocked()
	}
}
