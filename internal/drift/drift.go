// Package drift is what kit remembers of drift from run to run: when each
// item was first seen, the snoozes, the temporary installs, and the apps
// left installed by hand; and how that keeps items quiet, for a while or
// for good. Drift counts only after a day, so a throwaway install that's soon
// removed never needs attention.
package drift

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/state"
)

const (
	// Grace is how long drift stays new before it needs attention.
	Grace = 24 * time.Hour
	// SnoozeFor is how long a snooze lasts.
	SnoozeFor = 7 * 24 * time.Hour
	// TemporaryFor is how long a temporary install stays quiet.
	TemporaryFor = 7 * 24 * time.Hour
)

// The reasons an item is quiet.
const (
	New       = "new"
	Snoozed   = "snoozed"
	Temporary = "temporary"
	Left      = "left"
)

// file is the drift record, in kit's state directory.
const file = "drift.json"

// Record is what kit remembers of drift, by item id.
type Record struct {
	FirstSeen map[string]time.Time `json:"first_seen,omitempty"`
	// Snoozed is when each snooze ends.
	Snoozed map[string]time.Time `json:"snoozed_until,omitempty"`
	// Temporary is when each temporary install was made.
	Temporary map[string]time.Time `json:"temporary,omitempty"`
	// Hand is what's remembered of the things installed by hand, kept in
	// a file of its own, as Load reads it: Update leaves it be.
	Hand Hand `json:"-"`
}

// Load reads the record in the state directory, with what's remembered of
// the things installed by hand: an empty one when there's none.
func Load(stateDir string) (Record, error) {
	r, err := state.Load[Record](stateDir, file)
	if err != nil {
		return Record{}, err
	}
	r.Hand, err = state.Load[Hand](stateDir, handFile)
	return r, err
}

// Update reads the record, changes it, and writes it back whole, holding a
// lock throughout, so two runs at once can't lose each other's changes.
func Update(stateDir string, change func(*Record)) error {
	return state.Update(stateDir, file, change)
}

// Quieten marks each of res's items quiet while its time hasn't come, saying
// when it was first seen: installed by hand, its app left so, for good;
// new, first seen under Grace ago, or now; snoozed, until a time still
// ahead; temporary, installed so under TemporaryFor ago. What's installed
// by hand goes by r's Hand, by its app.
// A step whose items are all quiet stands ok. One whose check couldn't look,
// as macOS hasn't let it (check.Result's Refused), isn't drift, so is never
// quiet. Nor is an item applying the step was given to act on, by id in
// tried, that's still to act on, whatever its age: applying left it, so the
// step needs attention.
func Quieten(res check.Result, r Record, now time.Time, tried map[string]bool) check.Result {
	if _, refused := res.Refused(); refused || len(res.Items) == 0 {
		return res
	}
	items := slices.Clone(res.Items)
	loud, left := false, false
	for i, it := range items {
		firstSeen, snoozed, key := r.FirstSeen, r.Snoozed, it.ID
		if it.State == kind.ByHand {
			firstSeen, snoozed, key = r.Hand.FirstSeen, r.Hand.Snoozed, handKey(it)
		}
		since, seen := firstSeen[key]
		if seen {
			items[i].Since = since
		}
		switch {
		case tried[it.ID] && it.Action != "":
			loud, left = true, true
		case it.State == kind.ByHand && r.Hand.left(it.App):
			items[i].Quiet = Left
		case snoozed[key].After(now):
			items[i].Quiet = Snoozed
		case !r.Temporary[it.ID].IsZero() && now.Sub(r.Temporary[it.ID]) < TemporaryFor:
			items[i].Quiet = Temporary
		case !seen || now.Sub(since) < Grace:
			items[i].Quiet = New
		default:
			loud = true
		}
	}
	res.Items = items
	switch {
	case res.State == check.OK && left:
		res.State = check.Attention
	case res.State == check.Attention && !loud:
		res.State = check.OK
	}
	return res
}

// Remember notes the drift of results, by kind, in the records in the
// state directory (Seen), as of now, and forgets what it had of the kinds
// unmanaged, those no longer looked after (Forget).
func Remember(stateDir string, results map[string]check.Result, now time.Time, unmanaged []string) error {
	if err := Update(stateDir, func(r *Record) {
		r.Seen(results, now)
		r.Forget(unmanaged)
	}); err != nil {
		return err
	}
	return UpdateHand(stateDir, func(h *Hand) {
		h.Seen(results, now)
		h.Forget(unmanaged)
	})
}

// Seen notes, in r, each item of results (by kind) first seen now that
// wasn't seen before, but what's installed by hand, which is the Hand's.
// For each kind whose check ran, standing ok or needing attention, it
// forgets what's no longer drifting; a kind not checked, or whose check
// failed, was deferred or couldn't look, keeps what it had. Snoozes that
// have ended go.
func (r *Record) Seen(results map[string]check.Result, now time.Time) {
	present := make(map[string]bool)
	ran := make(map[string]bool)
	for kindName, res := range results {
		if !looked(res) {
			continue
		}
		ran[kindName] = true
		for _, it := range res.Items {
			if it.State != kind.ByHand {
				present[it.ID] = true
			}
		}
	}
	r.FirstSeen = seen(r.FirstSeen, present, now)
	gone := func(id string) bool { return ran[kindOf(id)] && !present[id] }
	maps.DeleteFunc(r.FirstSeen, func(id string, _ time.Time) bool { return gone(id) })
	maps.DeleteFunc(r.Snoozed, func(id string, until time.Time) bool { return gone(id) || !until.After(now) })
	maps.DeleteFunc(r.Temporary, func(id string, _ time.Time) bool { return gone(id) })
}

// looked reports whether res's check looked at how its step stands: it
// stands ok or needs attention, and macOS let it look.
func looked(res check.Result) bool {
	_, refused := res.Refused()
	return !refused && (res.State == check.OK || res.State == check.Attention)
}

// seen is firstSeen with each of present not in it first seen now.
func seen(firstSeen map[string]time.Time, present map[string]bool, now time.Time) map[string]time.Time {
	if firstSeen == nil {
		firstSeen = make(map[string]time.Time)
	}
	for id := range present {
		if _, ok := firstSeen[id]; !ok {
			firstSeen[id] = now
		}
	}
	return firstSeen
}

// kindOf is the kind an item's id is of: the part before its colon.
func kindOf(id string) string {
	kindName, _, _ := strings.Cut(id, ":")
	return kindName
}

// Forget forgets everything of the kinds named: kinds no longer looked
// after, whose items would otherwise come back with old dates when they're
// looked after again.
func (r *Record) Forget(kinds []string) {
	of := func(id string, _ time.Time) bool { return slices.Contains(kinds, kindOf(id)) }
	maps.DeleteFunc(r.FirstSeen, of)
	maps.DeleteFunc(r.Snoozed, of)
	maps.DeleteFunc(r.Temporary, of)
}

// Snooze quiets the item id until SnoozeFor from now.
func (r *Record) Snooze(id string, now time.Time) {
	r.Snoozed = snoozed(r.Snoozed, id, now)
}

// snoozed is until with the item id snoozed till SnoozeFor from now.
func snoozed(until map[string]time.Time, id string, now time.Time) map[string]time.Time {
	if until == nil {
		until = make(map[string]time.Time)
	}
	until[id] = now.Add(SnoozeFor)
	return until
}

// Temporarily notes the item id as a temporary install, made now.
func (r *Record) Temporarily(id string, now time.Time) {
	if r.Temporary == nil {
		r.Temporary = make(map[string]time.Time)
	}
	r.Temporary[id] = now
}

// Snooze quiets it until SnoozeFor from now, in the record in the state
// directory its sort is kept in: the Hand's, by its app, for a thing
// installed by hand.
func Snooze(stateDir string, it check.Item, now time.Time) error {
	if it.State == kind.ByHand {
		return UpdateHand(stateDir, func(h *Hand) { h.Snoozed = snoozed(h.Snoozed, handKey(it), now) })
	}
	return Update(stateDir, func(r *Record) { r.Snooze(it.ID, now) })
}

// handFile is the record of what's installed by hand, in kit's state
// directory.
const handFile = "by-hand.json"

// Hand is what kit remembers of the things installed by hand that a kind
// could take over, in a file of its own, as a kit from before it, still
// running hourly on a Mac mid-upgrade, writes the drift record back whole
// without what it doesn't know: when each item was first seen and when its
// snooze ends, by its kind and app (handKey); and when each app was left
// installed by hand, for good, by its bundle id, lower case. Going by the
// app, each outlasts the cask it's matched with being renamed, or matching
// it no more for a while, as the app updates itself past the cask; a leave
// outlasts its kind not being looked after too.
type Hand struct {
	FirstSeen map[string]time.Time `json:"first_seen,omitempty"`
	Snoozed   map[string]time.Time `json:"snoozed_until,omitempty"`
	Left      map[string]time.Time `json:"left,omitempty"`
}

// UpdateHand reads the record of what's installed by hand, changes it, and
// writes it back whole, holding a lock throughout.
func UpdateHand(stateDir string, change func(*Hand)) error {
	return state.Update(stateDir, handFile, change)
}

// Seen notes, in h, each item installed by hand of results (by kind)
// first seen now that wasn't seen before. For each kind whose look at
// what's installed by hand went well (check.Result's HandLook), it forgets
// the items whose app is no longer installed by hand at all, as it's gone,
// or a cask's now; an app still installed by hand that its kind can't take
// over for now keeps what it had, as does a kind that made no look, or
// whose look failed. Once a look was made and none failed, the leaves of
// apps no longer installed by hand go, as an app may be any kind's.
// Snoozes that have ended go.
func (h *Hand) Seen(results map[string]check.Result, now time.Time) {
	present, there, apps := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	ran := make(map[string]bool)
	failed := false
	for kindName, res := range results {
		if !looked(res) || res.HandLook == nil {
			continue
		}
		if res.HandLook.Unseen != "" {
			failed = true
			continue
		}
		ran[kindName] = true
		for _, app := range res.HandLook.Apps {
			there[kindName+":"+appKey(app)], apps[appKey(app)] = true, true
		}
		for _, it := range res.Items {
			if it.State == kind.ByHand {
				key := handKey(it)
				present[key], there[key], apps[appKey(it.App)] = true, true, true
			}
		}
	}
	h.FirstSeen = seen(h.FirstSeen, present, now)
	gone := func(key string) bool { return ran[kindOf(key)] && !there[key] }
	maps.DeleteFunc(h.FirstSeen, func(key string, _ time.Time) bool { return gone(key) })
	maps.DeleteFunc(h.Snoozed, func(key string, until time.Time) bool { return gone(key) || !until.After(now) })
	if len(ran) > 0 && !failed {
		maps.DeleteFunc(h.Left, func(app string, _ time.Time) bool { return !apps[app] })
	}
}

// handKey is what the Hand keeps it by, a thing installed by hand: its
// kind and its app's bundle id, lower case, as in cask:com.example.editor.
func handKey(it check.Item) string {
	return kindOf(it.ID) + ":" + appKey(it.App)
}

// Forget forgets the items of the kinds named, but the apps left by hand,
// which stay on the Mac.
func (h *Hand) Forget(kinds []string) {
	of := func(id string, _ time.Time) bool { return slices.Contains(kinds, kindOf(id)) }
	maps.DeleteFunc(h.FirstSeen, of)
	maps.DeleteFunc(h.Snoozed, of)
}

// left reports whether the app, by its bundle id, is left by hand.
func (h Hand) left(app string) bool {
	return app != "" && !h.Left[appKey(app)].IsZero()
}

// appKey is an app's bundle id as leaves are kept by: lower case, as
// macOS matches them.
func appKey(app string) string { return strings.ToLower(app) }

// Leave leaves each of apps, by bundle id, installed by hand for good,
// from now, in the record in the state directory.
func Leave(stateDir string, now time.Time, apps ...string) error {
	return UpdateHand(stateDir, func(h *Hand) {
		if h.Left == nil {
			h.Left = make(map[string]time.Time)
		}
		for _, app := range apps {
			h.Left[appKey(app)] = now
		}
	})
}

// Unleave forgets that the app, by its bundle id, was left by hand, in the
// record in the state directory, as another answer about it replaces the
// leave.
func Unleave(stateDir, app string) error {
	return UpdateHand(stateDir, func(h *Hand) { delete(h.Left, appKey(app)) })
}
