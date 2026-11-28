package cli

import (
	"testing"

	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/steps"
)

// Each feature that changes the system counts towards the settings, as
// the Mac's switches do.
func TestSystemFeaturesCountAsSettings(t *testing.T) {
	for _, name := range steps.Features {
		if name == steps.FeaturePrefs {
			continue
		}
		if got := partOf(name); got != render.PartSettings {
			t.Errorf("partOf(%q) = %q, want %q", name, got, render.PartSettings)
		}
	}
}
