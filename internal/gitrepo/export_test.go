package gitrepo

// OnUnlock is r calling f as soon as a commit lets go of git's index, for a
// test to be the next git to take it.
func OnUnlock(r Repo, f func()) Repo {
	r.onUnlock = f
	return r
}
