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
// restore first settled, every listed path finished with (Finished), which
// closes set-up's window: nothing is set aside after it.
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
// Time Machine disk, or Arq; and Detail, the old Mac's kit name, the
// snapshot's name, or the folder holding Arq's restores, from ~ when it's
// in the home folder.
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
	// NotThere is a path its source hadn't, its From, nothing copied: done
	// with, as the old Mac never made it, set-up's window shut to it; judged
	// again each run, as a line added after the restore settled is, holding
	// anything here, else waiting for kit apply to name a source.
	NotThere State = "not-there"
)

// Path is a listed path's restore: how it stands; where it came from; the
// files it holds, restored, and their bytes, however many runs copied them;
// when its copy first started and when it finished; its second pass; what
// was set aside to make way for it; and, while it's restored through Arq,
// the number of its holding folder (Holding). A path whose copy has
// started resumes till done, never judged again.
type Path struct {
	State    State     `json:"state,omitempty"`
	From     Origin    `json:"from,omitzero"`
	Files    int       `json:"files,omitempty"`
	Bytes    int64     `json:"bytes,omitempty"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
	Second   *Pass     `json:"second,omitempty"`
	SetAside []Aside   `json:"set_aside,omitempty"`
	Holding  int       `json:"holding,omitempty"`
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

// started is whether p's copy has ever started: under way now, or begun
// by a run before.
func (p Path) started() bool {
	return p.State == Copying || p.State == Checking || !p.Started.IsZero()
}

// PartWay reports whether the restore of any of lines' paths has started
// and not finished: a run carries it on, whatever kit apply's flags say.
func (r Record) PartWay(lines []Line) bool {
	_, ok := r.Resuming(lines, "")
	return ok
}

// Resuming is where the first of lines' paths whose restore has started
// and not finished came from, a source of kind, any for "": ok false when
// there's none.
func (r Record) Resuming(lines []Line, kind string) (from Origin, ok bool) {
	for _, l := range lines {
		if p := r.Paths[l.Path]; !r.Finished(l.Path) && p.started() && (kind == "" || p.From.Kind == kind) {
			return p.From, true
		}
	}
	return Origin{}, false
}

// mayAside is whether the listed path path may be set aside: one the
// restore's first run listed, before the restore settled, its source yet to
// be found without it.
func (r Record) mayAside(path string) bool {
	return r.Settled.IsZero() && slices.Contains(r.First.Paths, path) && r.Paths[path].State != NotThere
}

// MaySetAside reports whether a file at the listed path path, newer than
// its copy, so kept by it, may be set aside for that copy, as written here
// by set-up since it began, began: in set-up's window (zero on a Mac in
// use, which has none), as the judge has it (mayAside), and only before
// the path's copy first began, from any source, as a copy begun may have
// put it there. r is the record from before this run's copy began.
func (r Record) MaySetAside(path string, began time.Time) bool {
	return !began.IsZero() && r.mayAside(path) && !r.Paths[path].started()
}

// Finished reports whether the restore is done with the listed path path:
// restored, here, or not in its source.
func (r Record) Finished(path string) bool {
	s := r.Paths[path].State
	return s == Done || s == Here || s == NotThere
}

// Begin records the copy of the listed path path from from under way, as
// it begins, at now: from its source, and the restore's, so a run again
// resumes from it. Only then, and only path's: a source opened that copied
// nothing is never kept, and a path part-way from a source another stood
// in for, but hasn't, keeps the one it began from, for a run to finish it
// from there. It returns where path's copy was part-way from before, if
// it was.
func (r *Record) Begin(path string, from Origin, now time.Time) (was Origin) {
	r.From = from
	r.Change(path, func(p *Path) {
		if p.started() {
			was = p.From
		}
		p.State, p.From = Copying, from
		if p.Started.IsZero() {
			p.Started = now
		}
	})
	return was
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
// listed path finished with: once, the first time, so set-up's window stays
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
