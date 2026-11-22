package render_test

import (
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/render"
)

// kit reconcile's question says the thing's kind, what's wrong, more on it
// and for how long; its answers are labelled, one done for every Mac said
// apart, and Skip comes last.
func TestReconcileQuestion(t *testing.T) {
	now := time.Date(2026, 11, 20, 16, 20, 0, 0, time.UTC)
	q := render.ReconcileQuestion(render.Drifted{Name: "ffmpeg", Kind: "brew", What: "installed, not declared", Detail: "a dependency", Since: now.Add(-50 * time.Hour)},
		[]render.Answer{
			{Words: kind.Words{Label: "Declare", Does: "declare it, for this Mac"}},
			{Words: kind.Words{Label: "Declare", Does: "declare it, for every Mac"}, Shared: true},
			{Words: kind.UninstallWords},
		}, now)
	if got, want := ansi.Strip(q.About.Says), "brew · installed, not declared · a dependency · 2 days"; q.About.Name != "ffmpeg" || got != want {
		t.Errorf("asked about %s, saying %q; want ffmpeg, saying %q", q.About.Name, got, want)
	}
	var labels []string
	for _, a := range q.Answers {
		labels = append(labels, a.Label+": "+a.Does)
	}
	want := []string{"Declare: declare it, for this Mac", "Declare everywhere: declare it, for every Mac", "Uninstall: uninstall it", "Skip: not now"}
	if !slices.Equal(labels, want) {
		t.Errorf("answers %q, want %q", labels, want)
	}
	if q := render.ReconcileQuestion(render.Drifted{Name: "jq", Kind: "brew", What: "declared, not installed"}, nil, now); ansi.Strip(q.About.Says) != "brew · declared, not installed" {
		t.Errorf("with nothing more known, says %q", ansi.Strip(q.About.Says))
	}
	byHand := "installed by hand as Editor.app"
	if q := render.ReconcileQuestion(render.Drifted{Name: "editor", Kind: "cask", What: byHand + ", not declared", Detail: byHand}, nil, now); ansi.Strip(q.About.Says) != "cask · "+byHand+", not declared" {
		t.Errorf("with what's wrong saying its detail, says %q; want it said once", ansi.Strip(q.About.Says))
	}
}
