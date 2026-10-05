package render_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/render"
)

// glanceRun is a run's events: a step a line, with its area and result.
func glanceRun(steps ...glanceStep) []event.Event {
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	var infos []event.Step
	for _, s := range steps {
		infos = append(infos, event.Step{Name: s.name, Title: s.title, Area: s.area})
	}
	events := []event.Event{event.RunStarted{Time: at, Machine: "laptop", Steps: infos}}
	for _, s := range steps {
		events = append(events, event.StepFinished{Time: at, Step: s.name, Result: s.res})
	}
	return append(events, event.RunFinished{Time: at})
}

type glanceStep = struct {
	name, title, area string
	res               check.Result
}

func glance(t *testing.T, pretty bool, events []event.Event) string {
	t.Helper()
	var out bytes.Buffer
	g := render.NewGlance(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, pretty, func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) })
	for _, e := range events {
		g.Emit(e)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestGlanceAllWell(t *testing.T) {
	ok := func(glance string) check.Result { return check.Result{State: check.OK, Glance: glance} }
	got := glance(t, false, glanceRun(
		glanceStep{"homebrew", "Homebrew", "Drift", ok("")},
		glanceStep{"brew", "Formulae", "Drift", check.Result{State: check.OK, Items: []check.Item{{ID: "brew:jq", Name: "jq", State: "extra", Quiet: "new"}}}},
		glanceStep{"time-machine", "Time Machine", "Backups", ok("Time Machine 11:20")},
		glanceStep{"arq", "Arq", "Backups", ok("Arq 01:05")},
		glanceStep{"disk", "Disk space", "Mac", ok("48% free")},
		glanceStep{"file-events", "File events", "Mac", ok("")},
		glanceStep{"config-private", "Config repository", "Config", ok("private")},
	))
	want := `kit · laptop
ok Backups  Time Machine 11:20 · Arq 01:05
ok Mac      48% free
ok Drift    nothing to reconcile · 1 new, under a day
ok Config   private
`
	if got != want {
		t.Errorf("glance =\n%s\nwant\n%s", got, want)
	}
}

// What needs attention says what it is and what to run; the worst state in
// an area marks it.
func TestGlanceAttention(t *testing.T) {
	since := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	got := glance(t, false, glanceRun(
		glanceStep{"brew", "Formulae", "Drift", check.Result{State: check.Attention, Items: []check.Item{
			{ID: "brew:graphviz", Name: "graphviz", State: "extra", Since: since},
			{ID: "brew:jq", Name: "jq", State: "missing", Quiet: "snoozed"},
		}}},
		glanceStep{"claude-mcp", "Claude MCP servers", "Drift", check.Result{State: check.Attention, Items: []check.Item{{ID: "claude-mcp:docs", Name: "docs", State: "changed"}}}},
		glanceStep{"arq", "Arq", "Backups", check.Result{State: check.Failed, Reason: "arqc stats exited 1"}},
		glanceStep{"time-machine", "Time Machine", "Backups", check.Result{State: check.Attention, Items: []check.Item{{ID: "time-machine:stale", Name: "last backup 5 hours ago", State: "problem", Detail: "connect the disk"}}}},
		glanceStep{"disk", "Disk space", "Mac", check.Result{State: check.Attention, Items: []check.Item{{ID: "disk:low", Name: "only 8% free on the startup disk", State: "problem"}}}},
	))
	want := `kit · laptop
failed    Backups  Arq: arqc stats exited 1, last backup 5 hours ago  → kit status
attention Mac      only 8% free on the startup disk  → kit status
attention Drift    brew graphviz (not declared, 3 days), claude-mcp docs (changed)  → kit reconcile
`
	if got != want {
		t.Errorf("glance =\n%s\nwant\n%s", got, want)
	}
}

func TestGlancePrettyMarks(t *testing.T) {
	got := glance(t, true, glanceRun(glanceStep{"disk", "Disk space", "Mac", check.Result{State: check.OK, Glance: "48% free"}}))
	if want := "kit · laptop\n✓ Mac  48% free\n"; got != want {
		t.Errorf("glance = %q, want %q (no colour in a test's buffer)", got, want)
	}
}

func TestGlanceSilentWithoutARun(t *testing.T) {
	if got := glance(t, false, nil); got != "" {
		t.Errorf("glance without a run = %q", got)
	}
}
