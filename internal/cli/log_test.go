package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/logs"
)

// logRun writes a run's log into the world's logs directory, and returns
// its path.
func (w *world) logRun(t *testing.T, when time.Time, events ...event.Event) string {
	t.Helper()
	l, err := logs.Open(filepath.Join(w.home, "Library", "Logs", "kit"), when, "status", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		l.Emit(e)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return l.Path()
}

func TestLogWithoutARun(t *testing.T) {
	out, errOut, status := newWorld(t, nil).run(t, "log")
	if out != "" || errOut != "kit: no runs logged yet\n" || status != 0 {
		t.Errorf("kit log printed %q, %q, exit %d; want it to say there's none, exit 0", out, errOut, status)
	}
}

func TestLogShowsTheLastRun(t *testing.T) {
	w := newWorld(t, nil)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	w.logRun(t, when.Add(-time.Hour), event.RunStarted{Time: when.Add(-time.Hour), Command: "status", Machine: "studio"})
	path := w.logRun(t, when,
		event.RunStarted{Time: when, Command: "status", Machine: "laptop", Version: "0.1.0", Steps: []event.Step{{Name: "brew", Title: "Formulae"}}},
		event.CommandRan{Time: when, Step: "brew", Command: "brew leaves", Duration: 900 * time.Millisecond},
		event.StepFinished{Time: when, Step: "brew", Result: check.Result{State: check.OK, Summary: "2 declared, all installed"}, Duration: time.Second},
		event.RunFinished{Time: when, Duration: time.Second, Counts: map[check.State]int{check.OK: 1}},
	)

	out, errOut, status := w.run(t, "log")
	want := "kit status · laptop · 2 Jan 2026 03:04:05 · 1.0s\n" + path + "\n\n" +
		"✓ Formulae   1.0s  2 declared, all installed\n" +
		"    exit 0        0.9s  brew leaves\n\n" +
		"Nothing needs attention\n"
	if out != want || errOut != "" || status != 0 {
		t.Errorf("kit log printed\n%s%q, exit %d\nwant\n%s", out, errOut, status, want)
	}

	out, _, status = w.run(t, "log", "status", "--json")
	var doc struct {
		Schema  int           `json:"schema"`
		Log     string        `json:"log"`
		Records []logs.Record `json:"records"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || status != 0 {
		t.Fatalf("kit log --json printed %q, exit %d: %v", out, status, err)
	}
	if doc.Schema != 2 || doc.Log != path || len(doc.Records) != 4 || doc.Records[1].Command != "brew leaves" {
		t.Errorf("kit log --json = %+v, want the last run's records", doc)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("kit log --json printed escapes: %q", out)
	}
}

// kit log shows the last run, never a newer log with no run in it, as kit
// list's, kit why's and a change's are, which kit agent order doesn't even
// keep.
func TestLogShowsTheLastRunNotALogWithNoRun(t *testing.T) {
	w := placesWorld(t)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	path := w.logRun(t, when, event.RunStarted{Time: when, Command: "status", Machine: "laptop"})
	w.now = when.Add(time.Hour)
	if _, _, status := w.run(t, "secret", "places"); status != 0 {
		t.Fatalf("kit secret places exit %d", status)
	}
	w.writeSection(t, "laptop", "steps", `alone "stands alone"`+"\n")
	w.now = when.Add(2 * time.Hour)
	if out, errOut, status := w.run(t, "agent", "order", "alone"); status != 0 {
		t.Fatalf("kit agent order printed\n%s%s\nexit %d", out, errOut, status)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 2 || slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.Contains(e.Name(), "agent") }) {
		t.Errorf("logs = %v, %v: want the run's and secret places', none of kit agent order's", entries, err)
	}
	if out, _, status := w.run(t, "log"); status != 0 || !strings.Contains(out, "\n"+path+"\n") {
		t.Errorf("kit log printed\n%s\nexit %d, want the run's log, %s", out, status, path)
	}
}

// kit log --json, no command named, lists the runs, newest first: each
// one's log, what it was of, when it started and finished, how it ended in
// a word, the worst of its steps' states or unfinished, and its counts.
func TestLogListsTheRunsWithJSON(t *testing.T) {
	w := newWorld(t, nil)
	if out, errOut, status := w.run(t, "log", "--json"); out != "{\n  \"schema\": 2,\n  \"runs\": []\n}\n" || errOut != "" || status != 0 {
		t.Errorf("kit log --json, no runs, printed %q, %q, exit %d; want none listed", out, errOut, status)
	}
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ends := []map[check.State]int{{check.OK: 8, check.Failed: 1}, {check.OK: 2, check.Attention: 1}, {check.OK: 2, check.Deferred: 1}, {check.OK: 23}, nil}
	var paths []string
	for i, counts := range ends {
		at := when.Add(time.Duration(i) * time.Hour)
		events := []event.Event{event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Only: []string{"brew"}}}
		if counts != nil {
			events = append(events, event.RunFinished{Time: at.Add(time.Minute), Counts: counts})
		}
		paths = append(paths, w.logRun(t, at, events...))
	}
	out, errOut, status := w.run(t, "log", "--json")
	var doc struct {
		Schema int `json:"schema"`
		Runs   []struct {
			Log, Command, Result string
			Started, Finished    time.Time
			Counts               map[check.State]int
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || errOut != "" || status != 0 || doc.Schema != 2 || len(doc.Runs) != len(ends) {
		t.Fatalf("kit log --json printed\n%s%q, exit %d: %v", out, errOut, status, err)
	}
	for i, want := range []string{"unfinished", "ok", "deferred", "attention", "failed"} {
		r, at := doc.Runs[i], when.Add(time.Duration(len(ends)-1-i)*time.Hour)
		if r.Log != paths[len(ends)-1-i] || r.Command != "apply brew" || r.Result != want || !r.Started.Equal(at) {
			t.Errorf("runs[%d] = %+v, want %s's, %s", i, r, at, want)
		}
		if finished := !r.Finished.IsZero(); finished != (want != "unfinished") || finished && (!r.Finished.Equal(at.Add(time.Minute)) || len(r.Counts) == 0) {
			t.Errorf("runs[%d] finished %s with %v", i, r.Finished, r.Counts)
		}
	}
}

// At a terminal, kit log lists the runs, newest first, to pick one, which it
// shows; picking none shows nothing.
func TestLogListsTheRuns(t *testing.T) {
	w := newWorld(t, nil)
	w.terminal = true
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	w.logRun(t, when, event.RunStarted{Time: when, Command: "apply", Machine: "laptop"},
		event.RunFinished{Time: when, Duration: 4 * time.Second, Counts: map[check.State]int{check.OK: 8, check.Failed: 1}})
	w.logRun(t, when.Add(time.Hour), event.RunStarted{Time: when.Add(time.Hour), Command: "status", Machine: "laptop"},
		event.RunFinished{Time: when.Add(time.Hour), Duration: time.Second, Counts: map[check.State]int{check.OK: 23}})
	var shown []string
	w.pick = func(lines []ask.Line) (string, error) {
		for _, l := range lines {
			shown = append(shown, ansi.Strip(l.Text))
		}
		return lines[2].Value, nil
	}
	out, errOut, status := w.run(t, "log")
	want := []string{"  FRI 2 JAN", "  ● 04:04 status  23 fine · 1.0s", "  ✗ 03:04 apply  1 failed · 8 fine · 4.0s"}
	if !slices.Equal(shown, want) {
		t.Errorf("kit log listed\n%s\nwant\n%s", strings.Join(shown, "\n"), strings.Join(want, "\n"))
	}
	if out = ansi.Strip(out); !strings.Contains(out, "✗ kit apply") || errOut != "" || status != 0 {
		t.Errorf("kit log, apply picked, printed\n%s%q, exit %d; want apply's log", out, errOut, status)
	}

	w.pick = func([]ask.Line) (string, error) { return "", ask.ErrCancelled }
	if out, errOut, status := w.run(t, "log"); out != "" || errOut != "" || status != 0 {
		t.Errorf("kit log, nothing picked, printed %q, %q, exit %d; want nothing", out, errOut, status)
	}
}

// kit log <command> shows the last run of that command, with what it was
// given, or says there's none.
func TestLogOfACommand(t *testing.T) {
	w := newWorld(t, nil)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	w.logRun(t, when, event.RunStarted{Time: when, Command: "brew add jq", Machine: "laptop"}, event.RunFinished{Time: when})
	w.logRun(t, when.Add(time.Minute), event.RunStarted{Time: when.Add(time.Minute), Command: "status", Machine: "laptop"}, event.RunFinished{Time: when})
	if out, _, status := w.run(t, "log", "brew", "add"); !strings.HasPrefix(out, "kit brew add jq · laptop") || status != 0 {
		t.Errorf("kit log brew add printed\n%s exit %d; want brew add jq's run", out, status)
	}
	if out, errOut, status := w.run(t, "log", "apply"); out != "" || errOut != "kit: no run of apply logged\n" || status != 0 {
		t.Errorf("kit log apply printed %q, %q, exit %d; want it to say there's none", out, errOut, status)
	}
}
