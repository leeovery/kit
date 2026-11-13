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
