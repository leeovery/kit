package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/steps"
)

// Each step committing to kit-config, the new Mac's and its host key's,
// comes after git's settings and the steps of the user's own, but those
// that come after it, and says what it waits for: only what the run has.
// The config's edits come after it, unless it comes after them.
func TestPlaceCommits(t *testing.T) {
	for _, committer := range commitSteps {
		t.Run(committer, func(t *testing.T) { placesCommit(t, committer) })
	}
}

// placesCommit is TestPlaceCommits for the step named committer.
func placesCommit(t *testing.T, committer string) {
	ok := func(context.Context) check.Result { return check.Result{State: check.OK} }
	step := func(name string, after ...string) engine.Step {
		return engine.Step{Name: name, Check: ok, After: after}
	}
	for _, tt := range []struct {
		name        string
		all, own    []engine.Step
		wantAfter   []string
		wantWaiting string
		editsAfter  []string
	}{
		{
			name:        "git's settings and steps of your own",
			all:         []engine.Step{step("git-config"), step(committer), step("key"), step("late", committer)},
			own:         []engine.Step{step("key"), step("late", committer)},
			wantAfter:   []string{"git-config", "key"},
			wantWaiting: "waiting for git's settings and your steps",
		},
		{
			name:        "git's settings alone",
			all:         []engine.Step{step("git-config"), step(committer)},
			wantAfter:   []string{"git-config"},
			wantWaiting: "waiting for git's settings",
		},
		{
			name:        "steps of your own alone",
			all:         []engine.Step{step(committer), step("key")},
			own:         []engine.Step{step("key")},
			wantAfter:   []string{"key"},
			wantWaiting: "waiting for your steps",
		},
		{
			name: "neither",
			all:  []engine.Step{step(committer)},
		},
		{
			name:       "the config's edits",
			all:        []engine.Step{step(steps.ConfigEditsName), step(committer)},
			editsAfter: []string{committer},
		},
		{
			name:        "the config's edits, a step new-mac comes after",
			all:         []engine.Step{step(steps.ConfigEditsName), step("key", steps.ConfigEditsName), step(committer)},
			own:         []engine.Step{step("key", steps.ConfigEditsName)},
			wantAfter:   []string{"key"},
			wantWaiting: "waiting for your steps",
		},
	} {
		placeCommits(tt.all, tt.own)
		i := slices.IndexFunc(tt.all, func(s engine.Step) bool { return s.Name == committer })
		if got := tt.all[i]; !slices.Equal(got.After, tt.wantAfter) || got.Waiting != tt.wantWaiting {
			t.Errorf("%s: after %q, waiting %q; want after %q, waiting %q", tt.name, got.After, got.Waiting, tt.wantAfter, tt.wantWaiting)
		}
		if e := slices.IndexFunc(tt.all, func(s engine.Step) bool { return s.Name == steps.ConfigEditsName }); e >= 0 && !slices.Equal(tt.all[e].After, tt.editsAfter) {
			t.Errorf("%s: the config's edits after %q; want %q", tt.name, tt.all[e].After, tt.editsAfter)
		}
		if _, err := engine.New(tt.all...); err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

// The host key's step comes after the new Mac's, as both commit kit.toml,
// the new Mac's table first.
func TestPlaceCommitsTheHostKeyAfterTheNewMac(t *testing.T) {
	ok := func(context.Context) check.Result { return check.Result{State: check.OK} }
	all := []engine.Step{{Name: steps.HostKeyName, Check: ok}, {Name: newMacName, Check: ok}}
	placeCommits(all, nil)
	if !slices.Equal(all[0].After, []string{newMacName}) || len(all[1].After) > 0 {
		t.Errorf("host-key after %q, new-mac after %q; want host-key after new-mac", all[0].After, all[1].After)
	}
	if _, err := engine.New(all...); err != nil {
		t.Error(err)
	}
}
