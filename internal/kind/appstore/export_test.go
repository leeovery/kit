package appstore

import "time"

// SetFirstQuiet sets how long, till an app's in, mas may say nothing as it
// installs, for a test, putting it back after.
func SetFirstQuiet(d time.Duration) (restore func()) {
	was := firstQuiet
	firstQuiet = d
	return func() { firstQuiet = was }
}
