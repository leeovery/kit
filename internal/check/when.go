package check

import (
	"fmt"
	"time"
)

// When says when t was, from now, as a check's words have it: the time
// today, else the day too.
func When(t, now time.Time) string {
	t = t.In(now.Location())
	if t.Format("2006-01-02") == now.Format("2006-01-02") {
		return t.Format("15:04")
	}
	return t.Format("2 Jan 15:04")
}

// Ago says how long ago t was, from now, roughly.
func Ago(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
