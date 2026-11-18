package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
)

// sizers is how many folders are read at once. Measured on 2.9 million
// files, reading one at a time, as du does, took 155 seconds, and sixteen
// at once 80.
const sizers = 16

// sizeAll sizes folders, in home, side by side, as du counts what they hold
// on disk (each file's blocks, a file of several hard links once; links not
// followed; nothing on another volume), till ctx is done, and no longer: a
// folder not counted whole by then has as much as was counted, and the
// readers still at work, as one waiting on a file that's in iCloud alone,
// finish unheeded. A folder part of which couldn't be read, as macOS keeps
// Documents from a terminal not allowed it, isn't whole either, saying
// why. A folder counted whole and found to hold nothing is left out. Each
// folder is read by readDir, as os.ReadDir reads one.
func sizeAll(ctx context.Context, home string, folders []Folder, readDir func(string) ([]os.DirEntry, error)) []Folder {
	s := &sizing{readDir: readDir, folders: slices.Clone(folders), pending: make([]int, len(folders)), devs: make([]int32, len(folders)), seen: map[file]bool{}}
	s.more = sync.NewCond(&s.mu)
	for i, f := range folders {
		dir := filepath.Join(home, strings.TrimPrefix(f.Path, "~/"))
		st, err := lstat(dir)
		if err != nil {
			s.unread(i, err)
			continue
		}
		s.devs[i], s.folders[i].Size = st.Dev, st.Blocks*512
		s.todo = append(s.todo, job{folder: i, dir: dir})
		s.pending[i]++
		s.left++
	}
	stop := context.AfterFunc(ctx, s.wake)
	defer stop()
	var wg sync.WaitGroup
	for range sizers {
		wg.Go(func() {
			for j, ok := s.take(ctx); ok; j, ok = s.take(ctx) {
				s.read(j)
			}
		})
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Folder, 0, len(folders))
	for i, f := range s.folders {
		f.Whole = s.pending[i] == 0 && f.Err == nil
		if f.Size > 0 || !f.Whole {
			out = append(out, f)
		}
	}
	return out
}

// lstat is what's at path, itself, not what a link leads to: its volume,
// and the blocks it takes.
func lstat(path string) (syscall.Stat_t, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return st, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	return st, nil
}

// sizing is folders being sized: the folders in them still to read, each a
// job, taken by whichever reader's free.
type sizing struct {
	mu   sync.Mutex
	more *sync.Cond
	todo []job
	// folders are those sized, with what they hold as counted so far;
	// pending how many of each one's folders are still to read, or being
	// read; left, of every one's.
	folders []Folder
	pending []int
	left    int
	// devs are the volumes the folders are on.
	devs []int32
	// seen are the files of several hard links counted already.
	seen    map[file]bool
	readDir func(string) ([]os.DirEntry, error)
}

// job is a folder in the folder sized, dir.
type job struct {
	folder int
	dir    string
}

// file is a file on a volume, whatever its links are called.
type file struct {
	dev int32
	ino uint64
}

// take is the next folder to read, once there's one: not ok once every
// one's read, or ctx is done.
func (s *sizing) take(ctx context.Context) (job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.todo) == 0 && s.left > 0 && ctx.Err() == nil {
		s.more.Wait()
	}
	if len(s.todo) == 0 || ctx.Err() != nil {
		return job{}, false
	}
	j := s.todo[len(s.todo)-1]
	s.todo = s.todo[:len(s.todo)-1]
	return j, true
}

// wake has every reader waiting for a folder look again.
func (s *sizing) wake() {
	s.mu.Lock()
	s.more.Broadcast()
	s.mu.Unlock()
}

// unread notes that some of the folder i couldn't be read, and why, unless
// it's gone, as a file can go while it's read: mu is held, or no reader's
// begun.
func (s *sizing) unread(i int, err error) {
	if !errors.Is(err, fs.ErrNotExist) && s.folders[i].Err == nil {
		s.folders[i].Err = fmt.Errorf("couldn't read all of it: %w", err)
	}
}

// read counts what j's folder holds itself, and queues the folders in it on
// its volume.
func (s *sizing) read(j job) {
	entries, readErr := s.readDir(j.dir)
	var blocks int64
	var dirs []job
	linked := map[file]int64{}
	var errs []error
	for _, e := range entries {
		var st syscall.Stat_t
		p := filepath.Join(j.dir, e.Name())
		if err := syscall.Lstat(p, &st); err != nil {
			errs = append(errs, &fs.PathError{Op: "lstat", Path: p, Err: err})
			continue
		}
		switch {
		case e.IsDir() && st.Dev == s.devs[j.folder]:
			dirs = append(dirs, job{folder: j.folder, dir: p})
		case !e.IsDir() && st.Nlink > 1:
			linked[file{dev: st.Dev, ino: st.Ino}] = st.Blocks
			continue
		}
		blocks += st.Blocks
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for f, b := range linked {
		if !s.seen[f] {
			s.seen[f] = true
			blocks += b
		}
	}
	for _, err := range append(errs, readErr) {
		if err != nil {
			s.unread(j.folder, err)
		}
	}
	s.folders[j.folder].Size += blocks * 512
	s.todo = slices.Concat(s.todo, dirs)
	s.pending[j.folder] += len(dirs) - 1
	s.left += len(dirs) - 1
	s.more.Broadcast()
}
