package steps_test

import (
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// kitAppPath is where the tests' kit.app is.
const kitAppPath = "/Users/someone/.local/share/kit/kit.app"

// accessAt is when the tests' hourly run found kit.app's access.
var accessAt = time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)

// appAccess is the step giving kit.app Full Disk Access, the kit.app there
// built from the files whose fingerprint is built, with the record of the
// scheduled runs saying last found, raised by raise.
func appAccess(t *testing.T, fake *runnertest.Fake, state, built string, last nightly.Access, raise *steps.Raise) engine.Step {
	t.Helper()
	if err := nightly.Update(state, func(r *nightly.Record) { r.Access = last }); err != nil {
		t.Fatal(err)
	}
	return steps.AppAccess(steps.AppAccessing{
		Run: fake, App: kitAppPath, Built: func() string { return built }, StateDir: state, Raise: raise,
		Now: func() time.Time { return accessAt.Add(time.Hour) },
	})
}

// kit.app's Full Disk Access is done as last found for the kit.app there:
// granted by an hourly run, or given, as the person said; one built from
// other files has none till it's given again.
func TestAppAccessStands(t *testing.T) {
	for _, tt := range []struct {
		name    string
		built   string
		last    nightly.Access
		ok      bool
		summary string
	}{
		{"never found", "build-1", nightly.Access{}, false, "not given"},
		{"kit.app not built", "", nightly.Access{At: accessAt, App: "build-1", Granted: true}, false, "not given"},
		{"granted, found by an hourly run", "build-1", nightly.Access{At: accessAt, App: "build-1", Granted: true}, true, "granted"},
		{"given, as the person said", "build-1", nightly.Access{At: accessAt, App: "build-1", Granted: true, Given: true}, true, "given: the next hourly run checks it"},
		{"not granted, found by an hourly run", "build-1", nightly.Access{At: accessAt, App: "build-1"}, false, "not granted: the hourly run at 14:00 couldn't read what it protects"},
		{"granted to the build before", "build-2", nightly.Access{At: accessAt, App: "build-1", Granted: true}, false, "not given to this build of kit.app"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := appAccess(t, runnertest.New(t), t.TempDir(), tt.built, tt.last, nil).Check(t.Context())
			if (res.State == check.OK) != tt.ok || res.Summary != tt.summary {
				t.Errorf("check = %+v, want ok %v, %q", res, tt.ok, tt.summary)
			}
			if !tt.ok && (len(res.Items) != 1 || res.Items[0].ID != "kit-app-access:kit-app-access" || res.Items[0].State != check.ByHand || res.Items[0].Name != "Give kit.app Full Disk Access") {
				t.Errorf("items = %+v, want one by hand", res.Items)
			}
		})
	}
}

// At a terminal, its card goes up, opening System Settings at Full Disk
// Access's list and Finder at kit.app, for the drag; done, as the person
// says, it's recorded given to the kit.app there.
func TestAppAccessCard(t *testing.T) {
	c := newCard(t)
	c.fake.On("open", steps.AccessSettings)
	c.fake.On("open", "-R", kitAppPath)
	step := appAccess(t, c.fake, c.state, "build-2", nightly.Access{At: accessAt, App: "build-1", Granted: true}, c.raise)
	applied := applying(t, step)

	up := c.cards(t, 1)[0]
	if up.Step != "kit-app-access" || up.Title != "Give kit.app Full Disk Access" || up.Tells || len(up.Steps) == 0 {
		t.Errorf("card = %+v, want kit.app's Full Disk Access, the person saying when it's done", up)
	}
	c.answer(t, "kit-app-access", hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	c.raise.Wait()
	opened := []string{runner.Command{Name: "open", Args: []string{steps.AccessSettings}}.String(), "open -R " + kitAppPath}
	if calls := c.fake.Calls(); !slices.Equal(calls, opened) {
		t.Errorf("ran %q, want System Settings and Finder opened", calls)
	}
	rec, err := nightly.Load(c.state)
	if want := (nightly.Access{At: accessAt.Add(time.Hour), App: "build-2", Granted: true, Given: true}); err != nil || rec.Access != want {
		t.Errorf("recorded %+v (%v), want %+v", rec.Access, err, want)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("once given, check = %+v", res)
	}
}

// Stopped at its card, nothing's recorded: it's still to do.
func TestAppAccessCardStopped(t *testing.T) {
	c := newCard(t)
	c.fake.On("open", steps.AccessSettings)
	c.fake.On("open", "-R", kitAppPath)
	step := appAccess(t, c.fake, c.state, "build-1", nightly.Access{}, c.raise)
	applied := applying(t, step)
	c.cards(t, 1)
	c.answer(t, "kit-app-access", hand.Stop)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State == check.OK {
		t.Errorf("stopped, check = %+v, want it still to do", res)
	}
}

// Without kit.app built, there's nothing to give it to yet.
func TestAppAccessWaitsForKitApp(t *testing.T) {
	c := newCard(t)
	step := appAccess(t, c.fake, c.state, "", nightly.Access{}, c.raise)
	if err := step.Apply(t.Context(), step.Check(t.Context())); !engine.IsNotYet(err) {
		t.Errorf("Apply() = %v, want not yet", err)
	}
}
