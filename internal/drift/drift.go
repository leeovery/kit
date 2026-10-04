// Package drift is what kit remembers of drift from run to run: when each
// item was first seen, the snoozes, and the temporary installs; and how
// that keeps items quiet for a while. Drift counts only after a day, so a
// throwaway install that's soon removed never needs attention.
package drift

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/leeovery/kit/internal/check"
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

const (
	file     = "drift.json"
	lockFile = "drift.lock"
)

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
	data, err := os.ReadFile(filepath.Join(stateDir, file))
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, nil
	}
	if err != nil {
		return Record{}, fmt.Errorf("read the drift record: %w", err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, fmt.Errorf("read the drift record: %w", err)
	}
	return r, nil
}

// Update reads the record, changes it, and writes it back whole, holding a
// lock throughout, so two runs at once can't lose each other's changes.
func Update(stateDir string, change func(*Record)) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("make the state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("lock the drift record: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock the drift record: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	r, err := Load(stateDir)
	if err != nil {
		return err
	}
	change(&r)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("write the drift record: %w", err)
	}
	tmp, err := os.CreateTemp(stateDir, file+".*")
	if err != nil {
		return fmt.Errorf("write the drift record: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the drift record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the drift record: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(stateDir, file)); err != nil {
		return fmt.Errorf("write the drift record: %w", err)
	}
	return nil
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
