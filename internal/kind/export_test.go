package kind

import "time"

// Labels are the kinds' rows' names, for tests to check against the
// config's sections.
var Labels = labels

// SetQuiet sets how long an install may print nothing before its step
// says so, and how often it looks, till the func it returns puts them back.
func SetQuiet(after, look time.Duration) func() {
	oldAfter, oldLook := quietAfter, quietLook
	quietAfter, quietLook = after, look
	return func() { quietAfter, quietLook = oldAfter, oldLook }
}
