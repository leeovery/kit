package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if out != "" || errOut != "kit: no runs logged yet\n" || status != 1 {
		t.Errorf("kit log printed %q, %q, exit %d; want it to say there's none, exit 1", out, errOut, status)
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
		"    brew leaves  exit 0  0.9s\n\n" +
		"Nothing needs attention\n"
	if out != want || errOut != "" || status != 0 {
		t.Errorf("kit log printed\n%s%q, exit %d\nwant\n%s", out, errOut, status, want)
	}

	out, _, status = w.run(t, "log", "--json")
	var doc struct {
		Schema  int           `json:"schema"`
		Log     string        `json:"log"`
		Records []logs.Record `json:"records"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || status != 0 {
		t.Fatalf("kit log --json printed %q, exit %d: %v", out, status, err)
	}
	if doc.Schema != 1 || doc.Log != path || len(doc.Records) != 4 || doc.Records[1].Command != "brew leaves" {
		t.Errorf("kit log --json = %+v, want the last run's records", doc)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("kit log --json printed escapes: %q", out)
	}
}
