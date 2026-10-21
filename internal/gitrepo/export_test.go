package gitrepo

import (
	"os"

	"golang.org/x/sys/unix"
)

// OnUnlock is r calling f as soon as a commit lets go of git's index, for a
// test to be the next git to take it.
func OnUnlock(r Repo, f func()) Repo {
	r.onUnlock = f
	return r
}

// WithFlock is r taking the kernel's lock on a file with f, in
// syscall.Flock's place, for a test to say what the filesystem does.
func WithFlock(r Repo, f func(fd int) error) Repo {
	r.flock = f
	return r
}

// WithStatfs is r reading what a path's filesystem says of itself with f,
// in unix.Statfs's place, for a test to say what holds it.
func WithStatfs(r Repo, f func(path string, fs *unix.Statfs_t) error) Repo {
	r.statfs = f
	return r
}

// WithFsync is r syncing the git folder to the disk, once kit's mark is
// renamed into it, with f, in (*os.File).Sync's place, for a test to say
// what the filesystem does.
func WithFsync(r Repo, f func(*os.File) error) Repo {
	r.fsync = f
	return r
}

// Since is since, how a lock's age is said.
var Since = since
