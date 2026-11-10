package cli

import (
	"testing"
	"time"

	"github.com/leeovery/kit/internal/steps"
)

// --at reads a date or a time in the Mac's own time zone, meaning the end
// of it, so a backup taken in it counts: a date, the whole of that day, its
// clocks going back or forward or not; a time to the minute, that minute;
// to the second, that second. With --restore-from time-machine, it's the
// restore's source's.
func TestRestoreAtReadsADateOrATime(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	for _, tt := range []struct {
		given string
		want  time.Time
	}{
		{"2026-11-08", time.Date(2026, 11, 8, 23, 59, 59, 999_999_999, zone)},
		{"2026-11-01", time.Date(2026, 11, 1, 23, 59, 59, 999_999_999, zone)},
		{"2026-11-08 14:30", time.Date(2026, 11, 8, 14, 30, 59, 999_999_999, zone)},
		{"2026-11-08 14:30:15", time.Date(2026, 11, 8, 14, 30, 15, 999_999_999, zone)},
	} {
		at := restoreAt{now: func() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, zone) }}
		if err := at.Set(tt.given); err != nil || !at.at.Equal(tt.want) || at.String() != tt.given {
			t.Errorf("--at %s = %s, %v; want %s", tt.given, at.at, err, tt.want)
		}
		chosen, err := restoreFlags{from: "time-machine", at: at}.chosen()
		if want := (steps.RestoreFrom{Kind: "time-machine", At: tt.want}); err != nil || chosen != want {
			t.Errorf("--restore-from time-machine --at %s = %+v, %v; want %+v", tt.given, chosen, err, want)
		}
	}
}
