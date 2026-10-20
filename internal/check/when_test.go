package check

import (
	"testing"
	"time"
)

// A check says a time today by the clock, another day with the day; and
// how long ago in minutes within the hour, hours within two days, else
// days.
func TestWhenAndAgo(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at        time.Time
		when, ago string
	}{
		{now.Add(-40 * time.Minute), "11:20", "40 min ago"},
		{now.Add(-5 * time.Hour), "07:00", "5 hours ago"},
		{now.Add(-30 * time.Hour), "4 Oct 06:00", "30 hours ago"},
		{now.Add(-72 * time.Hour), "2 Oct 12:00", "3 days ago"},
	} {
		if got := When(c.at, now); got != c.when {
			t.Errorf("When(%s) = %q, want %q", c.at, got, c.when)
		}
		if got := Ago(c.at, now); got != c.ago {
			t.Errorf("Ago(%s) = %q, want %q", c.at, got, c.ago)
		}
	}
}
