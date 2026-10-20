package render_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/steps"
)

// Each kind's step sits in the row the kind says, one of applying's rows,
// and each step a kind needs in the row of the first kind needing it.
func TestEveryKindSitsInItsRow(t *testing.T) {
	for _, d := range kinds.All {
		for _, name := range append([]string{d.Name}, d.Needs...) {
			want, _ := kinds.Of(name)
			row, known := render.RowOf(event.Step{Name: name, Area: steps.AreaDrift})
			if !known || row != want.Row || !slices.Contains(render.Rows, row) {
				t.Errorf("%s is in %q (known %v), want %q, one of %v", name, row, known, want.Row, render.Rows)
			}
		}
	}
}
