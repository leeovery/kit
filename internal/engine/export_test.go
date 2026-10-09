package engine

import "time"

// SetWaitPoll has a step waiting for a thing look again every d, for a
// test, and returns what puts it back.
func SetWaitPoll(d time.Duration) (restore func()) {
	was := waitPoll
	waitPoll = d
	return func() { waitPoll = was }
}
