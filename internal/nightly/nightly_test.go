package nightly_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/nightly"
)

func at(day, hour, minute int) time.Time {
	return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC)
}

// The nightly run is due once it's fallen due since the last one started:
// at 03:00, or later that day when the Mac was asleep at 03:00.
func TestFullDue(t *testing.T) {
	three := 3 * time.Hour
	for _, tt := range []struct {
		name    string
		started time.Time
		now     time.Time
		due     bool
	}{
		{"never run", time.Time{}, at(5, 14, 0), true},
		{"at 03:00", at(4, 3, 0), at(5, 3, 0), true},
		{"the hour before", at(4, 3, 0), at(5, 2, 0), false},
		{"ran at 03:00 today", at(5, 3, 0), at(5, 4, 0), false},
		{"asleep at 03:00, awake at 09:00", at(4, 3, 0), at(5, 9, 0), true},
		{"a day missed", at(3, 3, 0), at(5, 1, 0), true},
	} {
		got := nightly.FullDue(nightly.Record{Full: nightly.Run{Started: tt.started}}, tt.now, three)
		if got != tt.due {
			t.Errorf("%s: FullDue() = %v, want %v", tt.name, got, tt.due)
		}
	}
	if got := nightly.LastDue(at(5, 1, 0), three); !got.Equal(at(4, 3, 0)) {
		t.Errorf("LastDue(01:00) = %v, want yesterday's 03:00", got)
	}
}

type recorder struct{ events []event.Event }

func (r *recorder) Emit(e event.Event) { r.events = append(r.events, e) }

// Jobs run in order, those due alone; one that fails never stops the
// others, and each outcome is recorded.
func TestSteps(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time { return at(5, 3, 0) }
	var ran []string
	job := func(name string, fails bool) nightly.Job {
		return nightly.Job{Name: name, Title: name, Run: func(context.Context) error {
			ran = append(ran, name)
			if fails {
				return errors.New(name + " broke")
			}
			return nil
		}}
	}
	jobs := []nightly.Job{job("first", false), job("second", true), job("third", false), job("skipped", false)}
	p, err := engine.New(nightly.Steps(jobs, map[string]bool{"first": true, "second": true, "third": true}, dir, now(), now)...)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Apply(t.Context(), &recorder{}, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"first", "second", "third"}) {
		t.Errorf("ran %q, want the three due, in order", ran)
	}
	for name, want := range map[string]check.State{"first": check.OK, "second": check.Failed, "third": check.OK, "skipped": check.OK} {
		if got := report.Results[name]; got.State != want {
			t.Errorf("%s = %+v, want %s", name, got, want)
		}
	}
	if got := report.Results["second"].Reason; got != "second broke" {
		t.Errorf("second's reason = %q", got)
	}
	if got := report.Results["skipped"].Summary; got != "not due" {
		t.Errorf("skipped = %q, want not due", got)
	}
	rec, err := nightly.Load(dir)
	if err != nil || !rec.Jobs["first"].OK || rec.Jobs["second"].OK || rec.Jobs["second"].Said != "second broke" {
		t.Errorf("record = %+v, %v", rec.Jobs, err)
	}
	if _, ok := rec.Jobs["skipped"]; ok {
		t.Error("skipped recorded, though it didn't run")
	}
}

// Checking says what's due, and runs nothing.
func TestStepsChecked(t *testing.T) {
	ran := false
	jobs := []nightly.Job{{Name: "hourly:x", Title: "x", Run: func(context.Context) error { ran = true; return nil }}}
	now := func() time.Time { return at(5, 3, 0) }
	p, err := engine.New(nightly.Steps(jobs, map[string]bool{"hourly:x": true}, t.TempDir(), now(), now)...)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Check(t.Context(), &recorder{}, engine.Options{})
	if err != nil || ran {
		t.Fatalf("Check() ran it: %v, %v", ran, err)
	}
	if got := report.Results["hourly:x"]; got.Summary != "due" || !got.Actions() {
		t.Errorf("hourly:x = %+v, want due", got)
	}
}
