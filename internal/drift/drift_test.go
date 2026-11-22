package drift_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/state"
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
			res := drift.Quieten(check.Result{State: check.Attention, Items: []check.Item{extra(tt.name)}}, r, now, nil)
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

// A thing installed by hand whose app is left so is quiet for good,
// however old, its snooze ended or never seen, its id in any case; it's
// first seen, and snoozed, as the Hand remembers it. A leave holds for
// nothing but a thing installed by hand, as the cask installed since.
func TestQuietenWhatsLeftByHand(t *testing.T) {
	r := drift.Record{
		FirstSeen: map[string]time.Time{"cask:since": ago(30 * 24 * time.Hour), "cask:snoozed": ago(time.Hour)},
		Hand: drift.Hand{
			FirstSeen: map[string]time.Time{"cask:left": ago(30 * 24 * time.Hour), "cask:woke": ago(30 * 24 * time.Hour), "cask:snoozed": ago(30 * 24 * time.Hour), "cask:old": ago(30 * 24 * time.Hour)},
			Snoozed:   map[string]time.Time{"cask:woke": ago(time.Minute), "cask:snoozed": now.Add(time.Hour)},
			Left:      map[string]time.Time{"com.example.left": ago(20 * 24 * time.Hour), "com.example.woke": ago(20 * 24 * time.Hour), "com.example.unseen": ago(time.Hour), "com.example.since": ago(20 * 24 * time.Hour)},
		},
	}
	for _, tt := range []struct {
		name, state, app, quiet string
		since                   time.Time
	}{
		{"left", kind.ByHand, "com.example.left", drift.Left, ago(30 * 24 * time.Hour)},
		{"woke", kind.ByHand, "com.example.woke", drift.Left, ago(30 * 24 * time.Hour)},
		{"unseen", kind.ByHand, "COM.Example.Unseen", drift.Left, time.Time{}},
		{"snoozed", kind.ByHand, "com.example.snoozed", drift.Snoozed, ago(30 * 24 * time.Hour)},
		{"old", kind.ByHand, "com.example.old", "", ago(30 * 24 * time.Hour)},
		{"since", kind.Extra, "com.example.since", "", ago(30 * 24 * time.Hour)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := drift.Quieten(check.Result{State: check.Attention, Items: []check.Item{{ID: "cask:" + tt.name, Name: tt.name, State: tt.state, App: tt.app}}}, r, now, nil)
			if it := res.Items[0]; it.Quiet != tt.quiet || !it.Since.Equal(tt.since) {
				t.Errorf("item = %+v, want quiet %q since %v", it, tt.quiet, tt.since)
			}
		})
	}
}

func TestQuietenLeavesOtherResultsAlone(t *testing.T) {
	for _, res := range []check.Result{
		{State: check.OK, Summary: "2 declared, all installed"},
		{State: check.Failed, Reason: "brew leaves exited 1", Items: []check.Item{extra("x")}},
	} {
		got := drift.Quieten(res, drift.Record{}, now, nil)
		if got.State != res.State || got.Summary != res.Summary || got.Reason != res.Reason {
			t.Errorf("Quieten(%+v) = %+v, want its state kept", res, got)
		}
	}
	in := check.Result{State: check.Attention, Items: []check.Item{extra("x")}}
	_ = drift.Quieten(in, drift.Record{}, now, nil)
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
		"brew":       {State: check.Attention, Items: []check.Item{extra("kept"), extra("fresh"), extra("ended"), {ID: "brew:by-hand", Name: "by-hand", State: kind.ByHand}}},
		"mas":        {State: check.Failed},
		"login-item": refused,
	}, now)

	// What's installed by hand is the Hand's, never the record's.
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

// The Hand keeps the items installed by hand, and the apps left so, while
// a kind's look at them goes well: those it no longer reports go, and the
// leaves of apps no longer among them, as one reported otherwise, as the
// cask installed since, whatever its cask's name now. A look that failed,
// or none, as of a kind that can take over nothing, keeps all it had of
// them, but the snoozes ended; and the record goes on pruning the rest of
// the kind, as an extra cask uninstalled.
func TestSeenByHand(t *testing.T) {
	editor := check.Item{ID: "cask:editor-renamed", Name: "editor-renamed", State: kind.ByHand, App: "com.example.Editor"}
	hand := func() drift.Hand {
		return drift.Hand{
			FirstSeen: map[string]time.Time{"cask:editor": ago(48 * time.Hour), "cask:gone": ago(48 * time.Hour)},
			Snoozed:   map[string]time.Time{"cask:gone": now.Add(time.Hour), "cask:ended": ago(time.Second)},
			Left:      map[string]time.Time{"com.example.editor": ago(time.Hour), "com.example.gone": ago(time.Hour), "com.example.since": ago(time.Hour)},
		}
	}
	h := hand()
	h.Seen(map[string]check.Result{
		"cask": {State: check.Attention, HandLook: &check.Look{}, Items: []check.Item{editor, {ID: "cask:since", Name: "since", State: kind.Extra}}},
		"brew": {State: check.Attention, Items: []check.Item{extra("ffmpeg")}},
	}, now)
	if want := map[string]time.Time{"cask:editor-renamed": now}; fmt.Sprint(h.FirstSeen) != fmt.Sprint(want) {
		t.Errorf("FirstSeen = %v, want %v of it, the rest gone", h.FirstSeen, want)
	}
	if want := map[string]time.Time{"com.example.editor": ago(time.Hour)}; fmt.Sprint(h.Left) != fmt.Sprint(want) || len(h.Snoozed) != 0 {
		t.Errorf("Hand = %+v, want the editor's leave alone, the snoozes gone", h)
	}

	for name, res := range map[string]check.Result{
		"a look that failed":  {State: check.OK, HandLook: &check.Look{Unseen: "kit can't tell which apps are installed by hand: no catalogue"}},
		"no look":             {State: check.OK},
		"a check that failed": {State: check.Failed, HandLook: &check.Look{}},
	} {
		h := hand()
		h.Seen(map[string]check.Result{"cask": res}, now)
		if len(h.FirstSeen) != 2 || len(h.Left) != 3 || len(h.Snoozed) != 1 {
			t.Errorf("after %s, Hand = %+v; want all it had kept, but the ended snooze", name, h)
		}
	}
	h = hand()
	h.Seen(map[string]check.Result{
		"cask": {State: check.OK, HandLook: &check.Look{Unseen: "kit can't tell which apps are installed by hand: no catalogue"}},
		"mas":  {State: check.OK, HandLook: &check.Look{}},
	}, now)
	if len(h.Left) != 3 || len(h.FirstSeen) != 2 {
		t.Errorf("one kind's look failing, another's well, Hand = %+v; want every leave kept, the casks' items", h)
	}
	r := drift.Record{FirstSeen: map[string]time.Time{"cask:firefox": ago(48 * time.Hour), "cask:zoom": ago(48 * time.Hour)}}
	r.Seen(map[string]check.Result{"cask": {State: check.OK, HandLook: &check.Look{Unseen: "kit can't tell which apps are installed by hand: no catalogue"}, Items: []check.Item{{ID: "cask:zoom", Name: "zoom", State: kind.Extra}}}}, now)
	if want := map[string]time.Time{"cask:zoom": ago(48 * time.Hour)}; fmt.Sprint(r.FirstSeen) != fmt.Sprint(want) {
		t.Errorf("FirstSeen = %v, want %v: firefox uninstalled, forgotten though the look failed", r.FirstSeen, want)
	}
}

// refused is a kind's check that couldn't look at what's installed, as
// macOS hasn't let it.
var refused = check.Result{State: check.Attention, Items: []check.Item{{ID: "login-item:allowed", Name: "macOS hasn't let the terminal control System Events", State: check.NotAllowed}}}

// A check that couldn't look isn't drift: what it says needs the person at
// the Mac now, never quiet as new.
func TestQuietenLeavesWhatsNotAllowedLoud(t *testing.T) {
	got := drift.Quieten(refused, drift.Record{}, now, nil)
	if got.State != check.Attention || got.Items[0].Quiet != "" {
		t.Errorf("Quieten() = %+v, want it needing attention, loud", got)
	}
}

// What applying was given to act on and left still to act on is never
// quiet, new or snoozed: its step needs attention, though its check stood
// ok, unless it failed. What applying wasn't given, or no longer acts on,
// as a line held back saying why, is quiet as ever.
func TestQuietenLeavesWhatApplyingLeftLoud(t *testing.T) {
	missing := func(name, action string) check.Item {
		return check.Item{ID: "brew:" + name, Name: name, State: "missing", Action: action}
	}
	r := drift.Record{Snoozed: map[string]time.Time{"brew:snoozed": now.Add(time.Hour)}}
	tried := map[string]bool{"brew:left": true, "brew:snoozed": true, "brew:held": true}
	for _, tt := range []struct {
		name  string
		res   check.Result
		quiet string
		want  check.State
	}{
		{"left, new", check.Result{State: check.Attention, Items: []check.Item{missing("left", "install")}}, "", check.Attention},
		{"left, snoozed", check.Result{State: check.Attention, Items: []check.Item{missing("snoozed", "install")}}, "", check.Attention},
		{"left, its check ok", check.Result{State: check.OK, Items: []check.Item{missing("left", "install")}}, "", check.Attention},
		{"left, its check failed", check.Result{State: check.Failed, Items: []check.Item{missing("left", "install")}}, "", check.Failed},
		{"held back, saying why", check.Result{State: check.Attention, Items: []check.Item{missing("held", "")}}, drift.New, check.OK},
		{"not given to act on", check.Result{State: check.Attention, Items: []check.Item{missing("other", "install")}}, drift.New, check.OK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := drift.Quieten(tt.res, r, now, tried)
			if got.Items[0].Quiet != tt.quiet || got.State != tt.want {
				t.Errorf("Quieten() = %+v, want it %s, its item quiet %q", got, tt.want, tt.quiet)
			}
		})
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

// Forgetting kinds forgets everything of theirs, and nothing of others',
// but the apps left by hand, which stay on the Mac.
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
	h := drift.Hand{
		FirstSeen: map[string]time.Time{"cask:editor": ago(time.Hour), "brew:x": ago(time.Hour)},
		Snoozed:   map[string]time.Time{"cask:editor": now.Add(time.Hour)},
		Left:      map[string]time.Time{"com.example.editor": ago(time.Hour)},
	}
	h.Forget([]string{"cask"})
	if len(h.FirstSeen) != 1 || len(h.Snoozed) != 0 || len(h.Left) != 1 {
		t.Errorf("Hand.Forget(cask) left %+v, want the casks' gone but the leave, the rest kept", h)
	}
}

// Leaving apps by hand notes when, by bundle id, lower case, in a file of
// its own, which a kit from before it, writing the drift record back whole
// from what it knows, leaves alone; and another answer ends it.
func TestLeave(t *testing.T) {
	dir := t.TempDir()
	if err := drift.Update(dir, func(r *drift.Record) { r.Snooze("cask:zoom", now) }); err != nil {
		t.Fatal(err)
	}
	if err := drift.Leave(dir, now, "com.example.Editor", "dev.example.spaceman"); err != nil {
		t.Fatal(err)
	}
	type older struct {
		FirstSeen map[string]time.Time `json:"first_seen,omitempty"`
		Snoozed   map[string]time.Time `json:"snoozed_until,omitempty"`
		Temporary map[string]time.Time `json:"temporary,omitempty"`
	}
	if err := state.Update(dir, "drift.json", func(o *older) { o.FirstSeen = map[string]time.Time{"brew:jq": now} }); err != nil {
		t.Fatal(err)
	}
	r, err := drift.Load(dir)
	if err != nil || len(r.Hand.Left) != 2 || !r.Hand.Left["com.example.editor"].Equal(now) || !r.Hand.Left["dev.example.spaceman"].Equal(now) || r.Snoozed["cask:zoom"].IsZero() {
		t.Errorf("Load() after Leave() and an older kit's write = %+v, %v; want both left now, the snooze kept", r, err)
	}
	if err := drift.Unleave(dir, "COM.example.editor"); err != nil {
		t.Fatal(err)
	}
	if r, err := drift.Load(dir); err != nil || len(r.Hand.Left) != 1 {
		t.Errorf("Load() after Unleave() = %+v, %v; want the editor left no more", r.Hand, err)
	}
}

// Snoozing a thing installed by hand snoozes it in the Hand, and anything
// else in the record.
func TestSnooze(t *testing.T) {
	state := t.TempDir()
	if err := drift.Snooze(state, check.Item{ID: "cask:editor", State: kind.ByHand}, now); err != nil {
		t.Fatal(err)
	}
	if err := drift.Snooze(state, extra("ffmpeg"), now); err != nil {
		t.Fatal(err)
	}
	r, err := drift.Load(state)
	if err != nil || !r.Hand.Snoozed["cask:editor"].Equal(now.Add(drift.SnoozeFor)) || !r.Snoozed["brew:ffmpeg"].Equal(now.Add(drift.SnoozeFor)) || len(r.Snoozed) != 1 {
		t.Errorf("Load() = %+v, %v; want the editor snoozed in the Hand, ffmpeg in the record", r, err)
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
