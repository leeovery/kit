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

// A copy starting records its source as the run's; standing in for
// another, gone or passed over, it becomes the source of every path
// part-way from that one, reported, but not of one finished, nor one that
// never started.
func TestRecordBegin(t *testing.T) {
	started := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	laptop, tm := Origin{Kind: FromMac, Detail: "laptop"}, Origin{Kind: FromTimeMachine, Detail: "snapshot"}
	fresh := func() Record {
		return Record{Paths: map[string]Path{
			"~/Code":      {State: Copying, From: laptop, Started: started},
			"~/Documents": {State: Checking, From: laptop, Started: started},
			"~/Music":     {State: Done, From: laptop, Started: started},
			"~/Gone":      {State: NotThere, From: laptop},
		}}
	}
	rec := fresh()
	if moved := rec.Begin(tm, Origin{}); moved || rec.From != tm || rec.Paths["~/Code"].From != laptop {
		t.Errorf("standing in for none, Begin() = %v, the record %+v; want its source recorded, nothing moved", moved, rec)
	}
	rec = fresh()
	if moved := rec.Begin(tm, laptop); !moved || rec.From != tm {
		t.Errorf("standing in for laptop, Begin() = %v, from %+v; want it moved, from Time Machine", moved, rec.From)
	}
	for path, want := range map[string]Origin{"~/Code": tm, "~/Documents": tm, "~/Music": laptop, "~/Gone": laptop} {
		if got := rec.Paths[path].From; got != want {
			t.Errorf("standing in for laptop, %s is from %+v, want %+v", path, got, want)
		}
	}
	if moved := rec.Begin(tm, laptop); moved {
		t.Error("begun again, Begin() reported paths moved")
	}
}

// Resuming is where the first listed path part-way came from, in the
// lines' order: none when none is.
func TestRecordResuming(t *testing.T) {
	started := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tm, arq := Origin{Kind: FromTimeMachine, Detail: "snapshot"}, Origin{Kind: FromArq, Detail: "/hold"}
	rec := Record{Paths: map[string]Path{
		"~/Code":      {State: Done, From: tm, Started: started},
		"~/Documents": {State: Copying, From: arq, Started: started},
		"~/Music":     {State: Checking, From: tm, Started: started},
	}}
	if from, ok := rec.Resuming([]Line{{Path: "~/Code"}, {Path: "~/Documents"}, {Path: "~/Music"}}); !ok || from != arq {
		t.Errorf("Resuming() = %+v, %v; want Arq's", from, ok)
	}
	if from, ok := rec.Resuming([]Line{{Path: "~/Code"}}); ok {
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
