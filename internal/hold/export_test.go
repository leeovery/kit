package hold

import "time"

// SetEvery sets how often a time limit looks whether it's held, for a test,
// putting it back after.
func SetEvery(d time.Duration) (restore func()) {
	was := every
	every = d
	return func() { every = was }
}
