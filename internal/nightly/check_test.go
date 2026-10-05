package nightly_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/nightly"
)

func TestCheck(t *testing.T) {
	jobs := []nightly.Job{{Name: "hourly:marks", Title: "marks"}, {Name: "nightly:tidy", Title: "tidy"}}
	now := at(5, 12, 0)
	for _, tt := range []struct {
		name  string
		rec   nightly.Record
		state check.State
		ids   []string
	}{
		{"never scheduled", nightly.Record{}, check.OK, nil},
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
			res := nightly.Check(jobs, true, true, dir, func() time.Time { return now }).Check(context.Background())
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
