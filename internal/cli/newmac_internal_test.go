package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
)

// The step committing a new Mac comes after git's settings and the steps
// of the user's own, but those that come after it, and says what it waits
// for: only what the run has.
func TestPlaceNewMac(t *testing.T) {
	ok := func(context.Context) check.Result { return check.Result{State: check.OK} }
	step := func(name string, after ...string) engine.Step {
		return engine.Step{Name: name, Check: ok, After: after}
	}
	for _, tt := range []struct {
		name        string
		all, own    []engine.Step
		wantAfter   []string
		wantWaiting string
	}{
		{
			name:        "git's settings and steps of your own",
			all:         []engine.Step{step("git-config"), step(newMacName), step("key"), step("late", newMacName)},
			own:         []engine.Step{step("key"), step("late", newMacName)},
			wantAfter:   []string{"git-config", "key"},
			wantWaiting: "waiting for git's settings and your steps",
		},
		{
			name:        "git's settings alone",
			all:         []engine.Step{step("git-config"), step(newMacName)},
			wantAfter:   []string{"git-config"},
			wantWaiting: "waiting for git's settings",
		},
		{
			name:        "steps of your own alone",
			all:         []engine.Step{step(newMacName), step("key")},
			own:         []engine.Step{step("key")},
			wantAfter:   []string{"key"},
			wantWaiting: "waiting for your steps",
		},
		{
			name: "neither",
			all:  []engine.Step{step(newMacName)},
		},
	} {
		placeNewMac(tt.all, tt.own)
		i := slices.IndexFunc(tt.all, func(s engine.Step) bool { return s.Name == newMacName })
		if got := tt.all[i]; !slices.Equal(got.After, tt.wantAfter) || got.Waiting != tt.wantWaiting {
			t.Errorf("%s: after %q, waiting %q; want after %q, waiting %q", tt.name, got.After, got.Waiting, tt.wantAfter, tt.wantWaiting)
		}
		if _, err := engine.New(tt.all...); err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}
