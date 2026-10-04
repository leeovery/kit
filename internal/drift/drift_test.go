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
		FirstSeen: map[string]time.Time{"brew:kept": ago(48 * time.Hour), "brew:gone": ago(48 * time.Hour), "cask:unchecked": ago(48 * time.Hour), "mas:failed": ago(48 * time.Hour)},
		Snoozed:   map[string]time.Time{"brew:kept": now.Add(time.Hour), "brew:ended": ago(time.Second), "brew:gone": now.Add(time.Hour)},
		Temporary: map[string]time.Time{"brew:gone": ago(time.Hour), "brew:kept": ago(time.Hour)},
	}
	r.Seen(map[string]check.Result{
		"brew": {State: check.Attention, Items: []check.Item{extra("kept"), extra("fresh"), extra("ended")}},
		"mas":  {State: check.Failed},
	}, now)

	wantSeen := map[string]time.Time{"brew:kept": ago(48 * time.Hour), "brew:fresh": now, "brew:ended": now, "cask:unchecked": ago(48 * time.Hour), "mas:failed": ago(48 * time.Hour)}
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

func TestSnoozeAndTemporarily(t *testing.T) {
	var r drift.Record
	r.Snooze("brew:x", now)
	r.Temporarily("brew:y", now)
	if !r.Snoozed["brew:x"].Equal(now.Add(drift.SnoozeFor)) || !r.Temporary["brew:y"].Equal(now) {
		t.Errorf("record = %+v", r)
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
