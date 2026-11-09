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
		From: snapshot,
		Paths: map[string]Path{
			"~/Code": {
				State: Done, From: snapshot, Files: 80301, Bytes: 36 << 30,
				Started: start, Finished: start.Add(2 * time.Hour),
				Second:   &Pass{Copied: 3, Differences: 0},
				SetAside: []Aside{{Time: start, Dir: "/Users/someone/.local/state/kit/restore/set-aside/20261120-100000", Paths: []string{"~/Code/notes.md"}}},
			},
			"~/.zsh_history":  {State: Here},
			"~/Documents":     {State: Copying, From: Origin{Kind: FromMac, Detail: "192.0.2.10"}, Started: start},
			"~/.config/valet": {State: Held},
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
	for _, key := range []string{`"from"`, `"kind": "time-machine"`, `"state": "done"`, `"second"`, `"set_aside"`, `"started"`, `"finished"`, `"settled"`} {
		if !strings.Contains(text, key) {
			t.Errorf("restore.json has no %s:\n%s", key, text)
		}
	}
	if strings.Contains(text, `"bytes": 0`) || strings.Count(text, `"finished"`) != 1 {
		t.Errorf("restore.json holds empty parts:\n%s", text)
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
