package drift_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/drift"
)

var now = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

func extra(name string) check.Item {
	return check.Item{ID: "brew:" + name, Name: name, State: "extra"}
}

func TestQuieten(t *testing.T) {
	r := drift.Record{
		FirstSeen: map[string]time.Time{
			"brew:recent": ago(2 * time.Hour), "brew:old": ago(25 * time.Hour), "brew:snoozed": ago(30 * 24 * time.Hour),
			"brew:woke": ago(30 * 24 * time.Hour), "brew:temp": ago(26 * time.Hour), "brew:stale-temp": ago(9 * 24 * time.Hour),
		},
		Snoozed:   map[string]time.Time{"brew:snoozed": now.Add(time.Hour), "brew:woke": ago(time.Minute)},
		Temporary: map[string]time.Time{"brew:temp": ago(26 * time.Hour), "brew:stale-temp": ago(8 * 24 * time.Hour)},
	}
	tests := []struct {
		name      string
		quiet     string
		since     time.Time
		stillLoud bool
	}{
		{name: "unseen", quiet: drift.New},
		{name: "recent", quiet: drift.New, since: ago(2 * time.Hour)},
		{name: "old", stillLoud: true, since: ago(25 * time.Hour)},
		{name: "snoozed", quiet: drift.Snoozed, since: ago(30 * 24 * time.Hour)},
		{name: "woke", stillLoud: true, since: ago(30 * 24 * time.Hour)},
		{name: "temp", quiet: drift.Temporary, since: ago(26 * time.Hour)},
		{name: "stale-temp", stillLoud: true, since: ago(9 * 24 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := drift.Quieten(check.Result{State: check.Attention, Items: []check.Item{extra(tt.name)}}, r, now)
			it := res.Items[0]
			if it.Quiet != tt.quiet || !it.Since.Equal(tt.since) {
				t.Errorf("item = %+v, want quiet %q since %v", it, tt.quiet, tt.since)
			}
			want := check.OK
			if tt.stillLoud {
				want = check.Attention
			}
			if res.State != want {
				t.Errorf("state = %s, want %s", res.State, want)
			}
		})
	}
}

func TestQuietenLeavesOtherResultsAlone(t *testing.T) {
	for _, res := range []check.Result{
		{State: check.OK, Summary: "2 declared, all installed"},
		{State: check.Failed, Reason: "brew leaves exited 1", Items: []check.Item{extra("x")}},
	} {
		got := drift.Quieten(res, drift.Record{}, now)
		if got.State != res.State || got.Summary != res.Summary || got.Reason != res.Reason {
			t.Errorf("Quieten(%+v) = %+v, want its state kept", res, got)
		}
	}
	in := check.Result{State: check.Attention, Items: []check.Item{extra("x")}}
	_ = drift.Quieten(in, drift.Record{}, now)
	if in.Items[0].Quiet != "" {
		t.Error("Quieten changed the result it was given, want a copy changed")
	}
}

func TestSeen(t *testing.T) {
	r := drift.Record{
		FirstSeen: map[string]time.Time{"brew:kept": ago(48 * time.Hour), "brew:gone": ago(48 * time.Hour), "cask:unchecked": ago(48 * time.Hour), "mas:failed": ago(48 * time.Hour), "login-item:unseen": ago(48 * time.Hour)},
		Snoozed:   map[string]time.Time{"brew:kept": now.Add(time.Hour), "brew:ended": ago(time.Second), "brew:gone": now.Add(time.Hour)},
		Temporary: map[string]time.Time{"brew:gone": ago(time.Hour), "brew:kept": ago(time.Hour)},
	}
	r.Seen(map[string]check.Result{
		"brew":       {State: check.Attention, Items: []check.Item{extra("kept"), extra("fresh"), extra("ended")}},
		"mas":        {State: check.Failed},
		"login-item": refused,
	}, now)

	wantSeen := map[string]time.Time{"brew:kept": ago(48 * time.Hour), "brew:fresh": now, "brew:ended": now, "cask:unchecked": ago(48 * time.Hour), "mas:failed": ago(48 * time.Hour), "login-item:unseen": ago(48 * time.Hour)}
	if fmt.Sprint(r.FirstSeen) != fmt.Sprint(wantSeen) {
		t.Errorf("FirstSeen = %v\nwant %v", r.FirstSeen, wantSeen)
	}
	if wantSnoozed := map[string]time.Time{"brew:kept": now.Add(time.Hour)}; fmt.Sprint(r.Snoozed) != fmt.Sprint(wantSnoozed) {
		t.Errorf("Snoozed = %v, want %v", r.Snoozed, wantSnoozed)
	}
	if wantTemp := map[string]time.Time{"brew:kept": ago(time.Hour)}; fmt.Sprint(r.Temporary) != fmt.Sprint(wantTemp) {
		t.Errorf("Temporary = %v, want %v", r.Temporary, wantTemp)
	}
}

// refused is a kind's check that couldn't look at what's installed, as
// macOS hasn't let it.
var refused = check.Result{State: check.Attention, Items: []check.Item{{ID: "login-item:allowed", Name: "macOS hasn't let the terminal control System Events", State: check.NotAllowed}}}

// A check that couldn't look isn't drift: what it says needs the person at
// the Mac now, never quiet as new.
func TestQuietenLeavesWhatsNotAllowedLoud(t *testing.T) {
	got := drift.Quieten(refused, drift.Record{}, now)
	if got.State != check.Attention || got.Items[0].Quiet != "" {
		t.Errorf("Quieten() = %+v, want it needing attention, loud", got)
	}
}

func TestSnoozeAndTemporarily(t *testing.T) {
	var r drift.Record
	r.Snooze("brew:x", now)
	r.Temporarily("brew:y", now)
	if !r.Snoozed["brew:x"].Equal(now.Add(drift.SnoozeFor)) || !r.Temporary["brew:y"].Equal(now) {
		t.Errorf("record = %+v", r)
	}
}

// Forgetting kinds forgets everything of theirs, and nothing of others'.
func TestForget(t *testing.T) {
	rec := drift.Record{
		FirstSeen: map[string]time.Time{"npm:docx": ago(time.Hour), "brew:jq": ago(time.Hour), "npmish:x": ago(time.Hour)},
		Snoozed:   map[string]time.Time{"npm:docx": now.Add(time.Hour), "brew:jq": now.Add(time.Hour)},
		Temporary: map[string]time.Time{"npm:left-pad": ago(time.Hour), "cask:zoom": ago(time.Hour)},
	}
	rec.Forget([]string{"npm", "go"})
	if len(rec.FirstSeen) != 2 || len(rec.Snoozed) != 1 || len(rec.Temporary) != 1 || rec.Snoozed["brew:jq"].IsZero() || rec.Temporary["cask:zoom"].IsZero() {
		t.Errorf("Forget(npm, go) left %+v, want npm's gone and the rest kept", rec)
	}
}

func TestUpdateAndLoad(t *testing.T) {
	state := t.TempDir() + "/state/kit"
	if r, err := drift.Load(state); err != nil || r.FirstSeen != nil {
		t.Fatalf("Load() before any = %+v, %v; want an empty record", r, err)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if err := drift.Update(state, func(r *drift.Record) { r.Snooze(fmt.Sprintf("brew:%d", i), now) }); err != nil {
				t.Errorf("Update() error = %v", err)
			}
		})
	}
	wg.Wait()
	r, err := drift.Load(state)
	if err != nil || len(r.Snoozed) != 20 {
		t.Errorf("after 20 updates at once, Load() = %d snoozes, %v; want every one", len(r.Snoozed), err)
	}
}
