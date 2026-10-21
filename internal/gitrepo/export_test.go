package gitrepo

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

// Since is since, how a lock's age is said.
var Since = since
