package status_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/status"
)

func TestBuilder(t *testing.T) {
	var b status.Builder
	b.Emit(event.RunStarted{Command: "status", Machine: "laptop", Version: "0.1.0", Steps: []event.Step{{Name: "homebrew", Title: "Homebrew"}, {Name: "brew", Title: "Formulae"}}})
	b.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "2 declared, all installed"}})
	b.Emit(event.StepFinished{Step: "other", Result: check.Result{State: check.Attention}})

	doc := b.Document()
	if doc.Schema != status.Schema || doc.Kit != "0.1.0" || doc.Machine != "laptop" || !doc.Attention {
		t.Errorf("Document() = %+v, want schema, kit, machine, and attention for the step that never finished", doc)
	}
	want := []status.Step{
		{ID: "homebrew", Title: "Homebrew", State: check.Failed, Reason: "the run ended before it finished"},
		{ID: "brew", Title: "Formulae", State: check.OK, Summary: "2 declared, all installed"},
	}
	if !slices.EqualFunc(doc.Steps, want, func(a, b status.Step) bool {
		return a.ID == b.ID && a.Title == b.Title && a.State == b.State && a.Summary == b.Summary && a.Reason == b.Reason
	}) {
		t.Errorf("Steps = %+v\nwant %+v", doc.Steps, want)
	}

	b.Emit(event.StepFinished{Step: "homebrew", Result: check.Result{State: check.OK}})
	if doc := b.Document(); doc.Attention {
		t.Errorf("with every step ok, Attention = true, want false")
	}
}
