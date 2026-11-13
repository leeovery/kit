package nightly_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/nightly"
)

// The scheduled runs need attention once they've never happened, or none
// has in two hours, the hourly launch loaded no later: its first run comes
// on the hour after it's loaded.
func TestCheck(t *testing.T) {
	jobs := []nightly.Job{{Name: "hourly:marks", Title: "marks"}, {Name: "nightly:tidy", Title: "tidy"}}
	now := at(5, 12, 0)
	for _, tt := range []struct {
		name  string
		rec   nightly.Record
		state check.State
		ids   []string
	}{
		{"never scheduled", nightly.Record{}, check.Attention, []string{"nightly:never"}},
		{"loaded within the hour, its first run to come", nightly.Record{Scheduled: at(5, 11, 30)}, check.OK, nil},
		{"loaded two hours ago and never run", nightly.Record{Scheduled: at(5, 9, 59)}, check.Attention, []string{"nightly:hourly"}},
		{"loaded again after it stopped, its next run to come", nightly.Record{
			Hourly:    nightly.Run{Started: at(4, 8, 0), Finished: at(4, 8, 2)},
			Full:      nightly.Run{Started: at(5, 3, 0), Finished: at(5, 3, 20)},
			Scheduled: at(5, 11, 30),
		}, check.OK, nil},
		{"all well", nightly.Record{
			Hourly: nightly.Run{Started: at(5, 11, 0), Finished: at(5, 11, 2)},
			Full:   nightly.Run{Started: at(5, 3, 0), Finished: at(5, 3, 20)},
		}, check.OK, nil},
		{"the hourly run stopped", nightly.Record{
			Hourly: nightly.Run{Started: at(5, 8, 0), Finished: at(5, 8, 2)},
			Full:   nightly.Run{Started: at(5, 3, 0), Finished: at(5, 3, 20)},
		}, check.Attention, []string{"nightly:hourly"}},
		{"stuck", nightly.Record{
			Hourly: nightly.Run{Started: at(5, 11, 0), Finished: at(5, 11, 2)},
			Full:   nightly.Run{Started: at(5, 3, 0)},
		}, check.Attention, []string{"nightly:stuck"}},
		{"stale", nightly.Record{
			Hourly: nightly.Run{Started: at(5, 11, 0), Finished: at(5, 11, 2)},
			Full:   nightly.Run{Started: at(3, 3, 0), Finished: at(3, 3, 20)},
		}, check.Attention, []string{"nightly:stale"}},
		{"a job failed", nightly.Record{
			Hourly: nightly.Run{Started: at(5, 11, 0), Finished: at(5, 11, 2)},
			Full:   nightly.Run{Started: at(5, 3, 0), Finished: at(5, 3, 20)},
			Jobs:   map[string]nightly.Outcome{"nightly:tidy": {At: at(5, 3, 10), Said: "the disk isn't connected"}, "hourly:marks": {At: at(5, 11, 1), OK: true}},
		}, check.Attention, []string{"nightly:nightly:tidy"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := nightly.Update(dir, func(r *nightly.Record) { *r = tt.rec }); err != nil {
				t.Fatal(err)
			}
			res := nightly.Check(jobs, true, dir, func() time.Time { return now }).Check(context.Background())
			var ids []string
			for _, it := range res.Items {
				ids = append(ids, it.ID)
			}
			if res.State != tt.state || !slices.Equal(ids, tt.ids) {
				t.Errorf("Check() = %s %q (%s), want %s %q", res.State, ids, res.Summary, tt.state, tt.ids)
			}
		})
	}
}

// What the check says of the runs tells the person what to do.
func TestCheckSays(t *testing.T) {
	now := at(5, 12, 0)
	for _, tt := range []struct {
		name    string
		rec     nightly.Record
		summary string
		item    string
	}{
		{"never scheduled", nightly.Record{}, "never run on a schedule", "kit has never run on a schedule (kit apply installs the hourly launch)"},
		{"its first run to come", nightly.Record{Scheduled: at(5, 11, 30)}, "its first run comes on the hour", ""},
		{"loaded, never run", nightly.Record{Scheduled: at(5, 9, 0)}, "never run on a schedule", "no hourly run since its launch was loaded 3 hours ago (kit apply puts the hourly launch back in place; kit log shows the last run)"},
		{"stopped", nightly.Record{Hourly: nightly.Run{Started: at(5, 8, 0), Finished: at(5, 8, 2)}}, "last ran: hourly 08:02", "the hourly run last ran 3 hours ago (kit apply puts the hourly launch back in place; kit log shows the last run)"},
		{"a job failed, run again by its name", nightly.Record{
			Hourly: nightly.Run{Started: at(5, 11, 0), Finished: at(5, 11, 2)},
			Jobs:   map[string]nightly.Outcome{"tidy-up": {At: at(5, 11, 1), Said: "the disk isn't connected"}},
		}, "last ran: hourly 11:02", "Tidy up failed: the disk isn't connected (kit nightly tidy-up runs it again; kit log shows its last run)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := nightly.Update(dir, func(r *nightly.Record) { *r = tt.rec }); err != nil {
				t.Fatal(err)
			}
			res := nightly.Check([]nightly.Job{{Name: "tidy-up", Title: "Tidy up"}}, false, dir, func() time.Time { return now }).Check(context.Background())
			var item string
			for _, it := range res.Items {
				item = it.Name + " (" + it.Detail + ")"
			}
			if res.Summary != tt.summary || item != tt.item {
				t.Errorf("Check() says %q, %q; want %q, %q", res.Summary, item, tt.summary, tt.item)
			}
		})
	}
}
