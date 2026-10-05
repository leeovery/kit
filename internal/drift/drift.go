// Package drift is what kit remembers of drift from run to run: when each
// item was first seen, the snoozes, and the temporary installs; and how
// that keeps items quiet for a while. Drift counts only after a day, so a
// throwaway install that's soon removed never needs attention.
package drift

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
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
}

// Load reads the record in the state directory: an empty one when there's
// none.
func Load(stateDir string) (Record, error) {
	return state.Load[Record](stateDir, file)
}

// Update reads the record, changes it, and writes it back whole, holding a
// lock throughout, so two runs at once can't lose each other's changes.
func Update(stateDir string, change func(*Record)) error {
	return state.Update(stateDir, file, change)
}

// Quieten marks each of res's items quiet while its time hasn't come, saying
// when it was first seen: new, first seen under Grace ago, or now; snoozed,
// until a time still ahead; temporary, installed so under TemporaryFor ago.
// A step whose items are all quiet stands ok.
func Quieten(res check.Result, r Record, now time.Time) check.Result {
	if len(res.Items) == 0 {
		return res
	}
	items := slices.Clone(res.Items)
	loud := false
	for i, it := range items {
		since, seen := r.FirstSeen[it.ID]
		if seen {
			items[i].Since = since
		}
		switch {
		case r.Snoozed[it.ID].After(now):
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
	if res.State == check.Attention && !loud {
		res.State = check.OK
	}
	return res
}

// Seen notes, in r, each item of results (by kind) first seen now that
// wasn't seen before. For each kind whose check ran, standing ok or needing
// attention, it forgets what's no longer drifting; a kind not checked, or
// whose check failed or was deferred, keeps what it had. Snoozes that have
// ended go.
func (r *Record) Seen(results map[string]check.Result, now time.Time) {
	present := make(map[string]bool)
	ran := make(map[string]bool)
	for kind, res := range results {
		if res.State != check.OK && res.State != check.Attention {
			continue
		}
		ran[kind] = true
		for _, it := range res.Items {
			present[it.ID] = true
		}
	}
	if r.FirstSeen == nil {
		r.FirstSeen = make(map[string]time.Time)
	}
	for id := range present {
		if _, ok := r.FirstSeen[id]; !ok {
			r.FirstSeen[id] = now
		}
	}
	gone := func(id string) bool {
		kind, _, _ := strings.Cut(id, ":")
		return ran[kind] && !present[id]
	}
	maps.DeleteFunc(r.FirstSeen, func(id string, _ time.Time) bool { return gone(id) })
	maps.DeleteFunc(r.Snoozed, func(id string, until time.Time) bool { return gone(id) || !until.After(now) })
	maps.DeleteFunc(r.Temporary, func(id string, _ time.Time) bool { return gone(id) })
}

// Snooze quiets the item id until SnoozeFor from now.
func (r *Record) Snooze(id string, now time.Time) {
	if r.Snoozed == nil {
		r.Snoozed = make(map[string]time.Time)
	}
	r.Snoozed[id] = now.Add(SnoozeFor)
}

// Temporarily notes the item id as a temporary install, made now.
func (r *Record) Temporarily(id string, now time.Time) {
	if r.Temporary == nil {
		r.Temporary = make(map[string]time.Time)
	}
	r.Temporary[id] = now
}
