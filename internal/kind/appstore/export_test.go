package appstore

import "time"

// SetFirstWord sets how long, till an app's in, mas may take to say
// anything as it installs, for a test, putting it back after.
func SetFirstWord(d time.Duration) (restore func()) {
	was := firstWord
	firstWord = d
	return func() { firstWord = was }
}
