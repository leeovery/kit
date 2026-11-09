package restore

import (
	"slices"
	"time"

	"github.com/leeovery/kit/internal/state"
)

// recordName names the restore's record, in kit's state.
const recordName = "restore.json"

// Record is the restore's record on this Mac: the source the run chose, so
// a run again resumes from it; its first run, whose paths alone are ever set
// aside; each listed path's restore, by its line's path; and when the
// restore first settled, every listed path done or here, which closes
// set-up's window: nothing is set aside after it.
type Record struct {
	From    Origin          `json:"from,omitzero"`
	First   Run             `json:"first,omitzero"`
	Paths   map[string]Path `json:"paths,omitempty"`
	Settled time.Time       `json:"settled,omitzero"`
}

// Run is a run of the restore: when, and the listed paths it judged, by
// their lines' paths.
type Run struct {
	Time  time.Time `json:"time"`
	Paths []string  `json:"paths,omitempty"`
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
	// Here is a path found holding files of its own, before anything was
	// restored to it: never touched.
	Here State = "here"
	// Held is its restore held back, waiting for its source, nothing yet
	// copied: judged again each run, as a path with no record.
	Held State = "held"
)

// Path is a listed path's restore: how it stands; where it came from; the
// files it holds, restored, and their bytes, however many runs copied them;
// when its copy first started and when it finished; its second pass; and
// what was set aside to make way for it. A path whose copy has started
// resumes till done, never judged again.
type Path struct {
	State    State     `json:"state,omitempty"`
	From     Origin    `json:"from,omitzero"`
	Files    int       `json:"files,omitempty"`
	Bytes    int64     `json:"bytes,omitempty"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
	Second   *Pass     `json:"second,omitempty"`
	SetAside []Aside   `json:"set_aside,omitempty"`
}

// Pass is a second pass's result: how many files it copied again, found
// different from their source, changed there since the copy or copied
// wrong.
type Pass struct {
	Copied int `json:"copied"`
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

// started is whether p's copy has ever started: under way now, or held
// since.
func (p Path) started() bool {
	return p.State == Copying || p.State == Checking || !p.Started.IsZero()
}

// PartWay reports whether the restore of any of lines' paths has started
// and not finished: a run carries it on, whatever kit apply's flags say.
func (r Record) PartWay(lines []Line) bool {
	for _, l := range lines {
		if p := r.Paths[l.Path]; p.State != Done && p.State != Here && p.started() {
			return true
		}
	}
	return false
}

// mayAside is whether the listed path path may be set aside: one the
// restore's first run listed, before the restore settled.
func (r Record) mayAside(path string) bool {
	return r.Settled.IsZero() && slices.Contains(r.First.Paths, path)
}

// RecordFirstRun records the restore's first run on this Mac, at now,
// judging paths, its lines' paths: once, the first time, so a line added
// after it is never set aside. The step calls it before it first judges.
func RecordFirstRun(stateDir string, now time.Time, paths []string) error {
	return UpdateRecord(stateDir, func(r *Record) {
		if r.First.Time.IsZero() {
			r.First = Run{Time: now, Paths: slices.Clone(paths)}
		}
	})
}

// Settle records that the restore has settled on this Mac, at now, every
// listed path done or here: once, the first time, so set-up's window stays
// closed.
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
