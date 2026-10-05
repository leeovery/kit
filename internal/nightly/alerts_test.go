package nightly_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/status"
)

func doc(steps ...status.Step) status.Document { return status.Document{Steps: steps} }

func driftStep(names ...string) status.Step {
	s := status.Step{ID: "brew", Title: "Formulae", Area: "Drift", State: check.Attention}
	for _, n := range names {
		s.Items = append(s.Items, check.Item{ID: "brew:" + n, Name: n, State: "extra"})
	}
	s.Items = append(s.Items, check.Item{ID: "brew:quiet", Name: "quiet", State: "extra", Quiet: "new"})
	return s
}

func TestAlertsForProblems(t *testing.T) {
	got, err := nightly.Alerts(doc(
		status.Step{ID: "disk", Title: "Disk space", Area: "Mac", State: check.Attention, Items: []check.Item{{ID: "disk:low", Name: "only 8% free", State: "problem"}}},
		status.Step{ID: "arq", Title: "Arq", Area: "Backups", State: check.Failed, Reason: "arqc stats exited 1"},
		status.Step{ID: "claude-mcp", Title: "Claude MCP servers", Area: "Drift", State: check.Deferred, Reason: "needs claude"},
		status.Step{ID: "hourly:marks", Title: "marks", Area: "Jobs", State: check.Failed, Reason: "broke"},
		status.Step{ID: "nightly", Title: "Scheduled runs", Area: "Jobs", State: check.Attention, Items: []check.Item{{ID: "nightly:hourly:marks", Name: "marks failed: broke", State: "problem"}}},
	), t.TempDir(), at(5, 3, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := []nightly.Alert{
		{ID: "disk:low", Title: "Disk space", Body: "only 8% free"},
		{ID: "arq", Title: "Arq", Body: "arqc stats exited 1"},
		{ID: "claude-mcp", Title: "Claude MCP servers", Body: "needs claude"},
		{ID: "nightly:hourly:marks", Title: "Scheduled runs", Body: "marks failed: broke"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Alerts() = %+v\nwant %+v (the job's step left to the check on the runs)", got, want)
	}
}

// Drift is one digest, quiet items never in it: the same list keeps its id;
// a changed one waits a day; none clears it.
func TestDriftDigest(t *testing.T) {
	dir := t.TempDir()
	digestOf := func(now time.Time, names ...string) nightly.Alert {
		t.Helper()
		got, err := nightly.Alerts(doc(driftStep(names...)), dir, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(names) == 0 {
			if len(got) != 0 {
				t.Errorf("no drift: alerts %+v", got)
			}
			return nightly.Alert{}
		}
		if len(got) != 1 {
			t.Fatalf("alerts = %+v, want the digest alone", got)
		}
		return got[0]
	}
	first := digestOf(at(5, 3, 0), "graphviz", "ffmpeg", "jq", "wget")
	if !strings.HasPrefix(first.ID, "drift-") || first.Title != "Drift" || first.Body != "4 things differ from the config: brew ffmpeg, brew graphviz, brew jq and 1 more → kit reconcile" {
		t.Errorf("digest = %+v", first)
	}
	if again := digestOf(at(5, 4, 0), "wget", "jq", "ffmpeg", "graphviz"); again != first {
		t.Errorf("the same list = %+v, want it unchanged", again)
	}
	if changed := digestOf(at(5, 10, 0), "graphviz"); changed != first {
		t.Errorf("changed within a day = %+v, want the last digest kept", changed)
	}
	next := digestOf(at(6, 3, 0), "graphviz")
	if next.ID == first.ID || next.Body != "1 thing differs from the config: brew graphviz → kit reconcile" {
		t.Errorf("changed a day on = %+v", next)
	}
	digestOf(at(6, 4, 0))
	if fresh := digestOf(at(6, 5, 0), "graphviz"); fresh.ID != next.ID {
		t.Errorf("after clearing, the same list = %+v, want its id", fresh)
	}
}
