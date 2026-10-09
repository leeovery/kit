package logs_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/logs"
)

var at = time.Date(2026, 1, 2, 3, 4, 5, 678_000_000, time.FixedZone("BST", 3600))

func TestALogRecordsEveryEvent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Logs", "kit")
	l, err := logs.Open(dir, at, "status", false)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if want := filepath.Join(dir, "2026-01-02T030405.678-status.jsonl"); l.Path() != want {
		t.Errorf("Path() = %s, want %s", l.Path(), want)
	}
	steps := []event.Step{{Name: "brew", Title: "Formulae"}}
	result := check.Result{State: check.Attention, Summary: "2 declared, 1 installed", Items: []check.Item{{ID: "brew:jq", Name: "jq", State: "missing"}}}
	for _, e := range []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Version: "0.1.0", Steps: steps},
		event.StepStarted{Time: at, Step: "brew"},
		event.CommandRan{Time: at, Step: "brew", Command: "brew leaves", Exit: 0, Duration: 1500 * time.Millisecond, Stdout: "ripgrep\n"},
		event.CommandRan{Time: at, Step: "brew", Command: "brew info jq", Exit: 1, Stderr: "Error: no\n", Error: "brew info jq exited 1: Error: no"},
		event.StepFinished{Time: at, Step: "brew", Result: result, Duration: 2 * time.Second},
		event.RunFinished{Time: at, Duration: 3 * time.Second, Counts: map[check.State]int{check.Attention: 1}},
	} {
		l.Emit(e)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	records, err := logs.Read(l.Path())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	var kinds []string
	for _, r := range records {
		kinds = append(kinds, r.Event)
		if !r.Time.Equal(at) {
			t.Errorf("%s at %v, want %v", r.Event, r.Time, at)
		}
	}
	if want := []string{"run_started", "step_started", "command", "command", "step_finished", "run_finished"}; !slices.Equal(kinds, want) {
		t.Fatalf("records = %q, want %q", kinds, want)
	}
	if r := records[0]; r.Command != "status" || r.Machine != "laptop" || r.Version != "0.1.0" || !reflect.DeepEqual(r.Steps, steps) {
		t.Errorf("run_started = %+v", r)
	}
	if r := records[2]; r.Step != "brew" || r.Command != "brew leaves" || r.Exit == nil || *r.Exit != 0 || r.DurationMS != 1500 || r.Stdout != "ripgrep\n" {
		t.Errorf("first command = %+v", r)
	}
	if r := records[3]; r.Exit == nil || *r.Exit != 1 || r.Stderr != "Error: no\n" || r.Error != "brew info jq exited 1: Error: no" {
		t.Errorf("second command = %+v", r)
	}
	if r := records[4]; r.Result == nil || r.Result.State != check.Attention || r.Result.Items[0].ID != "brew:jq" || r.DurationMS != 2000 {
		t.Errorf("step_finished = %+v", r)
	}
	if r := records[5]; r.Counts[check.Attention] != 1 || r.DurationMS != 3000 {
		t.Errorf("run_finished = %+v", r)
	}
	info, err := os.Stat(l.Path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the log: %v, %v, want it private", info, err)
	}
}

func TestALogCapsCommandsOutput(t *testing.T) {
	long := strings.Repeat("x", logs.OutputCap+10)
	for _, verbose := range []bool{false, true} {
		l, err := logs.Open(t.TempDir(), at, "status", verbose)
		if err != nil {
			t.Fatal(err)
		}
		l.Emit(event.CommandRan{Time: at, Command: "brew info --json=v2 --installed", Stdout: long, Stderr: "short"})
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		records, err := logs.Read(l.Path())
		if err != nil {
			t.Fatal(err)
		}
		r := records[0]
		if verbose && (r.Stdout != long || r.StdoutSize != 0) {
			t.Errorf("verbose: stdout of %d bytes, size %d; want it whole", len(r.Stdout), r.StdoutSize)
		}
		if !verbose && (len(r.Stdout) != logs.OutputCap || r.StdoutSize != len(long) || r.Stderr != "short" || r.StderrSize != 0) {
			t.Errorf("stdout of %d bytes, size %d; want it cut at %d, noting its size", len(r.Stdout), r.StdoutSize, logs.OutputCap)
		}
	}
}

func TestOpenNeverOverwritesALog(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for range 3 {
		l, err := logs.Open(dir, at, "status", false)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.Base(l.Path()))
		_ = l.Close()
	}
	want := []string{"2026-01-02T030405.678-status.jsonl", "2026-01-02T030405.678-status-2.jsonl", "2026-01-02T030405.678-status-3.jsonl"}
	if !slices.Equal(paths, want) {
		t.Errorf("logs = %q, want %q", paths, want)
	}
}

func TestOpenRemovesOldLogs(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		when := at.Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	write("old-status.jsonl", logs.Retention+time.Hour)
	write("recent-status.jsonl", logs.Retention-time.Hour)
	write("notes.txt", logs.Retention+time.Hour)

	l, err := logs.Open(dir, at, "status", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"2026-01-02T030405.678-status.jsonl", "notes.txt", "recent-status.jsonl"}; !slices.Equal(names, want) {
		t.Errorf("left = %q, want %q", names, want)
	}
}

func TestLatest(t *testing.T) {
	dir := t.TempDir()
	if _, err := logs.Latest(filepath.Join(dir, "missing")); !errors.Is(err, logs.ErrNoLogs) {
		t.Errorf("Latest() of no directory: error = %v, want ErrNoLogs", err)
	}
	if _, err := logs.Latest(dir); !errors.Is(err, logs.ErrNoLogs) {
		t.Errorf("Latest() of an empty directory: error = %v, want ErrNoLogs", err)
	}
	for _, when := range []time.Time{at, at.Add(time.Hour), at.Add(-time.Hour)} {
		l, err := logs.Open(dir, when, "status", false)
		if err != nil {
			t.Fatal(err)
		}
		_ = l.Close()
	}
	got, err := logs.Latest(dir)
	if err != nil || filepath.Base(got) != "2026-01-02T040405.678-status.jsonl" {
		t.Errorf("Latest() = %s, %v, want the newest", got, err)
	}
}

func TestReadRefusesALineThatIsntARecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(path, []byte("{\"event\":\"run_started\"}\nnot json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Read(path); err == nil || !strings.Contains(err.Error(), "x.jsonl:2:") {
		t.Errorf("Read() error = %v, want one naming line 2", err)
	}
}

// A command's lines as they come aren't logged: its command's line has
// its output whole.
func TestFileLeavesOutLinesAsTheyCome(t *testing.T) {
	dir := t.TempDir()
	f, err := logs.Open(dir, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "status", false)
	if err != nil {
		t.Fatal(err)
	}
	f.Emit(event.Output{Step: "brew", Line: "==> Pouring jq"})
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	path, err := logs.Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	records, err := logs.Read(path)
	if err != nil || len(records) != 0 {
		t.Errorf("logged %+v, %v; want nothing", records, err)
	}
}

// Runs lists the runs logged, newest first, each as it started and ended,
// a run that didn't finish said so.
// Runs lists the runs newest first, each from its start, after commands run
// before it, long ones among them, and its end, when it finished; logs that
// aren't a run's, as kit list's or an empty one, aren't listed.
func TestRuns(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("a line of output\n", 10000)
	for i, command := range []string{"status", "list", "apply", "empty"} {
		f, err := logs.Open(dir, time.Date(2026, 1, 2, 3, 4, 5+i, 0, time.UTC), command, false)
		if err != nil {
			t.Fatal(err)
		}
		switch command {
		case "status":
			f.Emit(event.CommandRan{Command: "git ls-files", Stdout: long})
			f.Emit(event.RunStarted{Command: command, Machine: "laptop", Only: []string{"brew"}})
			f.Emit(event.RunFinished{Duration: 2 * time.Second, Counts: map[check.State]int{check.OK: 3}})
		case "list":
			f.Emit(event.CommandRan{Command: "brew list", Stdout: "jq\n"})
		case "apply":
			f.Emit(event.RunStarted{Command: command, Machine: "laptop"})
			f.Emit(event.CommandRan{Command: "brew install jq", Stdout: long})
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := logs.Runs(dir)
	if err != nil || len(runs) != 2 {
		t.Fatalf("Runs() = %+v, %v; want apply's and status's", runs, err)
	}
	apply, status := runs[0], runs[1]
	if apply.Started.Command != "apply" || apply.Done() || !strings.HasSuffix(apply.Path, "-apply.jsonl") {
		t.Errorf("apply = %+v; want it unfinished", apply)
	}
	if status.Started.Command != "status" || !slices.Equal(status.Started.Only, []string{"brew"}) || !status.Done() ||
		status.Finished.Counts[check.OK] != 3 || status.Finished.DurationMS != 2000 {
		t.Errorf("status = %+v", status)
	}
}
