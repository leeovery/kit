package dialog

import "time"

// Owners is the script that lists the windows' owners, for tests to script.
const Owners = owners

// SetEvery sets how often a watch looks, for a test, putting it back after.
func SetEvery(d time.Duration) (restore func()) {
	was := every
	every = d
	return func() { every = was }
}

// SetStays sets how long a window must stay before a watch tells of it, for
// a test, putting it back after.
func SetStays(d time.Duration) (restore func()) {
	was := stays
	stays = d
	return func() { stays = was }
}
