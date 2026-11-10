package restore

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The record reads back as it was written, every part of it, from kit's
// state's restore.json; with none written, it's empty.
func TestRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if rec, err := LoadRecord(dir); err != nil || !reflect.DeepEqual(rec, Record{}) {
		t.Errorf("LoadRecord() with none = %+v, %v; want it empty", rec, err)
	}
	snapshot := Origin{Kind: FromTimeMachine, Detail: "com.apple.TimeMachine.2026-11-19-030000.backup"}
	start := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	want := Record{
		From:  snapshot,
		First: Run{Time: start, Paths: []string{"~/Code", "~/.zsh_history", "~/Documents", "~/.config/valet"}},
		Paths: map[string]Path{
			"~/Code": {
				State: Done, From: snapshot, Files: 80301, Bytes: 36 << 30,
				Started: start, Finished: start.Add(2 * time.Hour),
				Second:   &Pass{Copied: 3},
				SetAside: []Aside{{Time: start, Dir: "/Users/someone/.local/state/kit/restore/set-aside/20261120-100000", Paths: []string{"~/Code/notes.md"}}},
			},
			"~/.zsh_history":  {State: Here},
			"~/Documents":     {State: Copying, From: Origin{Kind: FromMac, Detail: "laptop"}, Started: start},
			"~/.config/valet": {Holding: 2},
		},
		Settled: start.Add(3 * time.Hour),
	}
	if err := UpdateRecord(dir, func(r *Record) { *r = want }); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRecord(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("LoadRecord() = %+v, %v\nwant %+v", got, err, want)
	}
	text := read(t, filepath.Join(dir, "restore.json"))
	for _, key := range []string{`"from"`, `"kind": "time-machine"`, `"state": "done"`, `"second"`, `"set_aside"`, `"first"`, `"started"`, `"finished"`, `"settled"`, `"holding": 2`} {
		if !strings.Contains(text, key) {
			t.Errorf("restore.json has no %s:\n%s", key, text)
		}
	}
	if strings.Contains(text, `"bytes": 0`) || strings.Count(text, `"finished"`) != 1 {
		t.Errorf("restore.json holds empty parts:\n%s", text)
	}
}

// The restore's first run is recorded once: a run after it, listing more,
// leaves it as it was.
func TestRecordFirstRun(t *testing.T) {
	dir := t.TempDir()
	first := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	paths := []string{"~/Code", "~/.zsh_history"}
	if err := RecordFirstRun(dir, first, paths); err != nil {
		t.Fatal(err)
	}
	paths[0] = "~/Documents"
	if err := RecordFirstRun(dir, first.Add(time.Hour), []string{"~/Code", "~/.zsh_history", "~/.local/state/newtool"}); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadRecord(dir)
	want := Run{Time: first, Paths: []string{"~/Code", "~/.zsh_history"}}
	if err != nil || !reflect.DeepEqual(rec.First, want) {
		t.Errorf("first run = %+v, %v; want %+v", rec.First, err, want)
	}
}

// Changing a path's record starts from an empty one, in a record with no
// paths yet, and keeps the rest.
func TestRecordChange(t *testing.T) {
	var rec Record
	rec.Change("~/Code", func(p *Path) { p.State = Copying })
	rec.Change("~/Code", func(p *Path) { p.Files = 12 })
	rec.Change("~/Documents", func(p *Path) { p.State = Here })
	want := map[string]Path{"~/Code": {State: Copying, Files: 12}, "~/Documents": {State: Here}}
	if !reflect.DeepEqual(rec.Paths, want) {
		t.Errorf("Paths = %+v; want %+v", rec.Paths, want)
	}
}

// A restore is part-way while one of the listed paths has started and not
// finished: copying, checking, or begun by a run before; never one done or
// here, never one not yet started, never a path no longer listed.
func TestRecordPartWay(t *testing.T) {
	started := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	listed := []Line{{Path: "~/Code"}, {Path: "~/Documents"}}
	for _, tt := range []struct {
		name string
		path string
		p    Path
		want bool
	}{
		{"copying", "~/Code", Path{State: Copying, Started: started}, true},
		{"checking", "~/Documents", Path{State: Checking, Started: started}, true},
		{"begun by a run before", "~/Code", Path{Started: started}, true},
		{"its holding folder made, not started", "~/Code", Path{Holding: 1}, false},
		{"done", "~/Code", Path{State: Done, Started: started}, false},
		{"here", "~/Code", Path{State: Here}, false},
		{"not in its source", "~/Code", Path{State: NotThere}, false},
		{"no longer listed", "~/Music", Path{State: Copying, Started: started}, false},
	} {
		rec := Record{Paths: map[string]Path{tt.path: tt.p}}
		if got := rec.PartWay(listed); got != tt.want {
			t.Errorf("%s: PartWay() = %v, want %v", tt.name, got, tt.want)
		}
	}
	if (Record{}).PartWay(listed) {
		t.Error("with no record, PartWay() = true")
	}
}

// A copy beginning records its source as the path's and the run's, and
// when it first started, saying where the path was part-way from before:
// only that path, so another part-way from the source it stands in for,
// not begun, keeps its own.
func TestRecordBegin(t *testing.T) {
	started, now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	laptop, tm := Origin{Kind: FromMac, Detail: "laptop"}, Origin{Kind: FromTimeMachine, Detail: "snapshot"}
	rec := Record{Paths: map[string]Path{
		"~/Code":      {State: Copying, From: laptop, Started: started},
		"~/Documents": {State: Checking, From: laptop, Started: started},
		"~/Gone":      {State: NotThere, From: laptop},
	}}
	if was := rec.Begin("~/Code", tm, now); was != laptop {
		t.Errorf("Begin(~/Code) = %+v, want it part-way from laptop before", was)
	}
	if was := rec.Begin("~/Gone", tm, now); was != (Origin{}) {
		t.Errorf("Begin(~/Gone), never begun, = %+v, want none", was)
	}
	want := map[string]Path{
		"~/Code":      {State: Copying, From: tm, Started: started},
		"~/Documents": {State: Checking, From: laptop, Started: started},
		"~/Gone":      {State: Copying, From: tm, Started: now},
	}
	if rec.From != tm || !reflect.DeepEqual(rec.Paths, want) {
		t.Errorf("begun, the record = %+v\nwant from %+v, paths %+v", rec, tm, want)
	}
}

// A file kept by a copy, newer here, may be set aside only in set-up's
// window, on a Mac whose set-up began, and only before its path's copy
// first began, from whatever source: never resumed, nor recorded not
// there.
func TestRecordMaySetAside(t *testing.T) {
	began := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	laptop, tm := Origin{Kind: FromMac, Detail: "laptop"}, Origin{Kind: FromTimeMachine, Detail: "snapshot"}
	open := Record{First: Run{Time: began, Paths: []string{"~/.zsh_history"}}}
	for _, tt := range []struct {
		name  string
		rec   Record
		began time.Time
		path  string
		p     Path
		want  bool
	}{
		{"its first copy", open, began, "~/.zsh_history", Path{}, true},
		{"resumed", open, began, "~/.zsh_history", Path{State: Copying, From: tm, Started: began}, false},
		{"resumed, checking", open, began, "~/.zsh_history", Path{State: Checking, From: laptop, Started: began}, false},
		{"not there", open, began, "~/.zsh_history", Path{State: NotThere, From: laptop}, false},
		{"a Mac in use", open, time.Time{}, "~/.zsh_history", Path{}, false},
		{"a line added after the first run", open, began, "~/.newtool", Path{}, false},
		{"the restore settled", Record{First: open.First, Settled: began.Add(time.Hour)}, began, "~/.zsh_history", Path{}, false},
	} {
		rec := tt.rec
		rec.Paths = map[string]Path{tt.path: tt.p}
		if got := rec.MaySetAside(tt.path, tt.began); got != tt.want {
			t.Errorf("%s: MaySetAside() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Resuming is where the first listed path part-way came from, of a kind if
// asked, in the lines' order: none when none is.
func TestRecordResuming(t *testing.T) {
	started := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tm, arq := Origin{Kind: FromTimeMachine, Detail: "snapshot"}, Origin{Kind: FromArq, Detail: "/hold"}
	rec := Record{Paths: map[string]Path{
		"~/Code":      {State: Done, From: tm, Started: started},
		"~/Documents": {State: Copying, From: arq, Started: started},
		"~/Music":     {State: Checking, From: tm, Started: started},
	}}
	if from, ok := rec.Resuming([]Line{{Path: "~/Code"}, {Path: "~/Documents"}, {Path: "~/Music"}}, ""); !ok || from != arq {
		t.Errorf("Resuming() = %+v, %v; want Arq's", from, ok)
	}
	if from, ok := rec.Resuming([]Line{{Path: "~/Code"}, {Path: "~/Documents"}, {Path: "~/Music"}}, FromTimeMachine); !ok || from != tm {
		t.Errorf("of Time Machine's, Resuming() = %+v, %v; want Time Machine's", from, ok)
	}
	if from, ok := rec.Resuming([]Line{{Path: "~/Code"}}, ""); ok {
		t.Errorf("with ~/Code alone, done, Resuming() = %+v; want none", from)
	}
}

// The restore is done with a path restored, here, or not in its source;
// not with one under way, its holding folder made, or with no record.
func TestRecordFinished(t *testing.T) {
	rec := Record{Paths: map[string]Path{
		"~/a": {State: Done}, "~/b": {State: Here}, "~/c": {State: NotThere},
		"~/d": {State: Copying}, "~/e": {State: Checking}, "~/f": {Holding: 1},
	}}
	for path, want := range map[string]bool{"~/a": true, "~/b": true, "~/c": true, "~/d": false, "~/e": false, "~/f": false, "~/g": false} {
		if got := rec.Finished(path); got != want {
			t.Errorf("Finished(%s) = %v, want %v", path, got, want)
		}
	}
}
