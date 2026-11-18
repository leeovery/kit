package snapshot

import (
	"context"
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
// on disk (each file's blocks; links not followed; nothing on another
// volume), till ctx is done: a folder not counted whole by then has as
// much as was counted. A folder found to hold nothing is left out.
func sizeAll(ctx context.Context, home string, folders []Folder) []Folder {
	s := &sizing{sizes: make([]int64, len(folders)), pending: make([]int, len(folders)), devs: make([]int32, len(folders))}
	s.more = sync.NewCond(&s.mu)
	for i, f := range folders {
		dir := filepath.Join(home, strings.TrimPrefix(f.Path, "~/"))
		if dev, ok := device(dir); ok {
			s.devs[i] = dev
			s.todo = append(s.todo, job{folder: i, dir: dir})
			s.pending[i]++
			s.left++
		}
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
	wg.Wait()
	out := make([]Folder, 0, len(folders))
	for i, f := range folders {
		f.Size, f.Whole = s.sizes[i], s.pending[i] == 0
		if f.Size > 0 || !f.Whole {
			out = append(out, f)
		}
	}
	return out
}

// device is the volume path is on, itself, not what a link leads to; not ok
// when it can't be read.
func device(path string) (int32, bool) {
	var st syscall.Stat_t
	if syscall.Lstat(path, &st) != nil {
		return 0, false
	}
	return st.Dev, true
}

// sizing is folders being sized: the folders in them still to read, each a
// job, taken by whichever reader's free.
type sizing struct {
	mu   sync.Mutex
	more *sync.Cond
	todo []job
	// sizes are what each folder holds, as counted so far; pending how many
	// of its folders are still to read, or being read; left, of every
	// folder's.
	sizes   []int64
	pending []int
	left    int
	// devs are the volumes the folders are on.
	devs []int32
}

// job is a folder in the folder sized, dir.
type job struct {
	folder int
	dir    string
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

// read counts what j's folder holds itself, and queues the folders in it on
// its volume.
func (s *sizing) read(j job) {
	entries, _ := os.ReadDir(j.dir)
	var blocks int64
	var dirs []job
	for _, e := range entries {
		var st syscall.Stat_t
		p := filepath.Join(j.dir, e.Name())
		if syscall.Lstat(p, &st) != nil {
			continue
		}
		blocks += st.Blocks
		if e.IsDir() && st.Dev == s.devs[j.folder] {
			dirs = append(dirs, job{folder: j.folder, dir: p})
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizes[j.folder] += blocks * 512
	s.todo = slices.Concat(s.todo, dirs)
	s.pending[j.folder] += len(dirs) - 1
	s.left += len(dirs) - 1
	s.more.Broadcast()
}
