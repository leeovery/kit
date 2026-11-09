package restore

import (
	"time"

	"github.com/leeovery/kit/internal/state"
)

// recordName names the restore's record, in kit's state.
const recordName = "restore.json"

// Record is the restore's record on this Mac: the source the run chose, so
// a run again resumes from it; each listed path's restore, by its line's
// path; and when the restore first settled, every listed path done or here,
// which closes set-up's window: nothing is set aside after it.
type Record struct {
	From    Origin          `json:"from,omitzero"`
	Paths   map[string]Path `json:"paths,omitempty"`
	Settled time.Time       `json:"settled,omitzero"`
}

// Origin is where a restore copies from: Kind, the old Mac over SSH, its
// Time Machine disk, or Arq; and Detail, the old Mac's host, the snapshot's
// name, or the folder Arq restored into.
type Origin struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// The kinds of origin.
const (
	FromMac         = "mac"
	FromTimeMachine = "time-machine"
	FromArq         = "arq"
)

// State is how a listed path's restore stands.
type State string

const (
	// Copying is its copy under way, and Checking its second pass,
	// comparing the copy with its source.
	Copying  State = "copying"
	Checking State = "checking"
	// Done is restored: never restored again.
	Done State = "done"
	// Here is a path found holding files of its own, older than this Mac's
	// set-up, before anything was restored: never touched.
	Here State = "here"
	// Held is its restore held back, waiting for its source.
	Held State = "held"
)

// Path is a listed path's restore: how it stands; where it came from; the
// files and bytes copied; when it started and finished; its second pass;
// and what was set aside to make way for it.
type Path struct {
	State    State     `json:"state,omitempty"`
	From     Origin    `json:"from,omitzero"`
	Files    int64     `json:"files,omitempty"`
	Bytes    int64     `json:"bytes,omitempty"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
	Second   *Pass     `json:"second,omitempty"`
	SetAside []Aside   `json:"set_aside,omitempty"`
}

// Pass is a second pass's result: the files it copied again, changed at a
// live source since the first; and the differences it found between the
// copy and its source.
type Pass struct {
	Copied      int64 `json:"copied"`
	Differences int64 `json:"differences"`
}

// Aside is what was set aside from a listed path, and when: its files, each
// from ~, moved into Dir, each keeping its path from the home folder.
type Aside struct {
	Time  time.Time `json:"time"`
	Dir   string    `json:"dir"`
	Paths []string  `json:"paths"`
}

// LoadRecord reads the restore's record in stateDir, kit's state: an empty
// one when there's none.
func LoadRecord(stateDir string) (Record, error) {
	return state.Load[Record](stateDir, recordName)
}

// UpdateRecord changes the restore's record in stateDir, under its lock.
func UpdateRecord(stateDir string, change func(*Record)) error {
	return state.Update(stateDir, recordName, change)
}

// Settle records that the restore has settled on this Mac, at now, every
// listed path done or here: once, the first time, so set-up's window stays
// closed whatever's listed later.
func Settle(stateDir string, now time.Time) error {
	return UpdateRecord(stateDir, func(r *Record) {
		if r.Settled.IsZero() {
			r.Settled = now
		}
	})
}

// Change changes the record of the listed path path, an empty one if
// there's none.
func (r *Record) Change(path string, change func(*Path)) {
	if r.Paths == nil {
		r.Paths = map[string]Path{}
	}
	p := r.Paths[path]
	change(&p)
	r.Paths[path] = p
}
