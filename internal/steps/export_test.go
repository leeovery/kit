package steps

import "time"

// SetManualPoll has a raised step by hand's command run every d, for a
// test, and returns what puts it back.
func SetManualPoll(d time.Duration) (restore func()) {
	was := manualPoll
	manualPoll = d
	return func() { manualPoll = was }
}
