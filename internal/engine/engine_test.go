package engine_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
)

// recorder notes a run's events.
type recorder struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *recorder) Emit(e event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// finished is each step's result, by name, as the run's events said.
func (r *recorder) finished() map[string]check.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	results := map[string]check.Result{}
	for _, e := range r.events {
		if f, ok := e.(event.StepFinished); ok {
			results[f.Step] = f.Result
		}
	}
	return results
}

func ok(summary string) func(context.Context) check.Result {
	return func(context.Context) check.Result { return check.Result{State: check.OK, Summary: summary} }
}

func failing(reason string) func(context.Context) check.Result {
	return func(context.Context) check.Result { return check.Result{State: check.Failed, Reason: reason} }
}

func names(steps []engine.Step) []string {
	var n []string
	for _, s := range steps {
		n = append(n, s.Name)
	}
	return n
}

func TestNewRefuses(t *testing.T) {
	tests := []struct {
		name  string
		steps []engine.Step
		want  string
	}{
		{name: "a step without a name", steps: []engine.Step{{Check: ok("")}}, want: "a step without a name"},
		{name: "a step without a check", steps: []engine.Step{{Name: "brew"}}, want: "step brew has no check: every step needs one"},
		{name: "two steps of one name", steps: []engine.Step{{Name: "brew", Check: ok("")}, {Name: "brew", Check: ok("")}}, want: "two steps named brew"},
		{name: "a need that isn't a step", steps: []engine.Step{{Name: "brew", Needs: []string{"homebrew"}, Check: ok("")}}, want: "step brew needs homebrew, which isn't a step"},
		{name: "after a step that isn't one", steps: []engine.Step{{Name: "app", After: []string{"brew"}, Check: ok("")}}, want: "step app comes after brew, which isn't a step"},
		{
			name: "a cycle through after",
			steps: []engine.Step{
				{Name: "a", After: []string{"b"}, Check: ok("")},
				{Name: "b", Needs: []string{"a"}, Check: ok("")},
			},
			want: "steps a, b need each other in a cycle",
		},
		{
			name: "a cycle",
			steps: []engine.Step{
				{Name: "a", Needs: []string{"c"}, Check: ok("")},
				{Name: "b", Needs: []string{"a"}, Check: ok("")},
				{Name: "c", Needs: []string{"b"}, Check: ok("")},
				{Name: "d", Check: ok("")},
			},
			want: "steps a, b, c need each other in a cycle",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.New(tt.steps...)
			if err == nil || err.Error() != tt.want {
				t.Errorf("New() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestStepsComeAfterWhatTheyNeed(t *testing.T) {
	p, err := engine.New(
		engine.Step{Name: "cask", Needs: []string{"homebrew"}, Check: ok("")},
		engine.Step{Name: "brew", Needs: []string{"homebrew"}, Check: ok("")},
		engine.Step{Name: "homebrew", Check: ok("")},
		engine.Step{Name: "mas", Check: ok("")},
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Check(t.Context(), &recorder{}, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(report.Steps), []string{"homebrew", "mas", "cask", "brew"}; !slices.Equal(got, want) {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// Requirement 1: a failed step never ends the run. It's recorded, what needs
// it is deferred with the reason, the rest carry on, and the run finishes.
func TestAFailedStepNeverEndsTheRun(t *testing.T) {
	p, err := engine.New(
		engine.Step{Name: "homebrew", Title: "Homebrew", Check: failing("brew isn't on kit's PATH")},
		engine.Step{Name: "brew", Title: "Formulae", Needs: []string{"homebrew"}, Check: ok("")},
		engine.Step{Name: "tap", Title: "Taps", Needs: []string{"brew"}, Check: ok("")},
		engine.Step{Name: "panics", Title: "Panics", Check: func(context.Context) check.Result { panic("oops") }},
		engine.Step{Name: "silent", Title: "Silent", Check: func(context.Context) check.Result { return check.Result{} }},
		engine.Step{Name: "secrets", Title: "Secrets", Check: ok("2 synced")},
	)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	report, err := p.Check(t.Context(), rec, engine.Options{Command: "status", Machine: "laptop"})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]check.Result{
		"homebrew": {State: check.Failed, Reason: "brew isn't on kit's PATH"},
		"brew":     {State: check.Deferred, Reason: "needs Homebrew"},
		"tap":      {State: check.Deferred, Reason: "needs Formulae"},
		"panics":   {State: check.Failed, Reason: "the check panicked: oops"},
		"silent":   {State: check.Failed, Reason: "the check said nothing of how the step stands"},
		"secrets":  {State: check.OK, Summary: "2 synced"},
	}
	for name, w := range want {
		if got := report.Results[name]; got.State != w.State || got.Reason != w.Reason || got.Summary != w.Summary {
			t.Errorf("%s = %+v, want %+v", name, got, w)
		}
		if got := rec.finished()[name]; got.State != w.State {
			t.Errorf("%s's StepFinished = %+v, want %s", name, got, w.State)
		}
	}
	if !report.Attention() {
		t.Error("Attention() = false, want true")
	}
	last, isFinish := rec.events[len(rec.events)-1].(event.RunFinished)
	if !isFinish || last.Counts[check.OK] != 1 || last.Counts[check.Failed] != 3 || last.Counts[check.Deferred] != 2 {
		t.Errorf("the last event = %+v, want the run finished with its counts", rec.events[len(rec.events)-1])
	}
}

func TestARunsEvents(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	p, err := engine.New(
		engine.Step{Name: "homebrew", Title: "Homebrew", Check: ok("/opt/homebrew")},
		engine.Step{Name: "brew", Needs: []string{"homebrew"}, Check: ok("1 declared, all installed")},
	)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if _, err := p.Check(t.Context(), rec, engine.Options{Command: "status", Machine: "laptop", Version: "0.1.0", Jobs: 1, Now: func() time.Time { return clock }}); err != nil {
		t.Fatal(err)
	}
	want := []event.Event{
		event.RunStarted{Time: clock, Command: "status", Machine: "laptop", Version: "0.1.0", Steps: []event.Step{{Name: "homebrew", Title: "Homebrew"}, {Name: "brew", Title: "brew", Waits: []string{"homebrew"}}}},
		event.StepStarted{Time: clock, Step: "homebrew", Doing: "checking"},
		event.StepFinished{Time: clock, Step: "homebrew", Result: check.Result{State: check.OK, Summary: "/opt/homebrew"}},
		event.StepStarted{Time: clock, Step: "brew", Doing: "checking"},
		event.StepFinished{Time: clock, Step: "brew", Result: check.Result{State: check.OK, Summary: "1 declared, all installed"}},
		event.RunFinished{Time: clock, Counts: map[check.State]int{check.OK: 2}},
	}
	if len(rec.events) != len(want) {
		t.Fatalf("events = %+v\nwant %+v", rec.events, want)
	}
	for i := range want {
		got, w := rec.events[i], want[i]
		if gs, ok := got.(event.RunStarted); ok {
			ws := w.(event.RunStarted)
			if gs.Command != ws.Command || gs.Machine != ws.Machine || gs.Version != ws.Version || !reflect.DeepEqual(gs.Steps, ws.Steps) || !gs.Time.Equal(ws.Time) {
				t.Errorf("event %d = %+v, want %+v", i, got, w)
			}
			continue
		}
		if gf, ok := got.(event.RunFinished); ok {
			if gf.Counts[check.OK] != 2 {
				t.Errorf("event %d = %+v, want %+v", i, got, w)
			}
			continue
		}
		if gf, ok := got.(event.StepFinished); ok {
			wf := w.(event.StepFinished)
			if gf.Step != wf.Step || gf.Result.State != wf.Result.State || gf.Result.Summary != wf.Result.Summary {
				t.Errorf("event %d = %+v, want %+v", i, got, w)
			}
			continue
		}
		if got != w {
			t.Errorf("event %d = %+v, want %+v", i, got, w)
		}
	}
}

func TestStepsRunOnlyOnTheirMacs(t *testing.T) {
	p, err := engine.New(
		engine.Step{Name: "brew", Check: ok("")},
		engine.Step{Name: "plex", Macs: []string{"studio"}, Check: ok("")},
		engine.Step{Name: "xcode", Macs: []string{"laptop", "studio"}, Check: ok("")},
	)
	if err != nil {
		t.Fatal(err)
	}
	report, _ := p.Check(t.Context(), &recorder{}, engine.Options{Machine: "laptop"})
	if got := names(report.Steps); !slices.Equal(got, []string{"brew", "xcode"}) {
		t.Errorf("steps on laptop = %q, want brew and xcode", got)
	}
}

func TestOnlyRunsTheStepsNamedAndWhatTheyNeed(t *testing.T) {
	p, err := engine.New(
		engine.Step{Name: "homebrew", Check: ok("")},
		engine.Step{Name: "brew", Needs: []string{"homebrew"}, Check: ok("")},
		engine.Step{Name: "cask", Needs: []string{"homebrew"}, Check: ok("")},
		engine.Step{Name: "plex", Macs: []string{"studio"}, Check: ok("")},
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Check(t.Context(), &recorder{}, engine.Options{Machine: "laptop", Only: []string{"cask"}})
	if err != nil || !slices.Equal(names(report.Steps), []string{"homebrew", "cask"}) {
		t.Errorf("only cask = %q, %v; want homebrew and cask", names(report.Steps), err)
	}
	_, err = p.Check(t.Context(), &recorder{}, engine.Options{Machine: "laptop", Only: []string{"plex"}})
	if err == nil || err.Error() != "no step named plex on this Mac: one of homebrew, brew, cask" {
		t.Errorf("only plex on laptop: error = %v, want it refused", err)
	}
}

func TestIndependentChecksRunSideBySide(t *testing.T) {
	var running, most atomic.Int32
	both := make(chan struct{})
	var once sync.Once
	busy := func(context.Context) check.Result {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		if n == 2 {
			once.Do(func() { close(both) })
		}
		select {
		case <-both:
		case <-time.After(200 * time.Millisecond):
		}
		return check.Result{State: check.OK}
	}
	for _, tt := range []struct {
		jobs int
		want int32
	}{{jobs: 2, want: 2}, {jobs: 1, want: 1}} {
		running.Store(0)
		most.Store(0)
		p, err := engine.New(engine.Step{Name: "a", Check: busy}, engine.Step{Name: "b", Check: busy})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Check(t.Context(), &recorder{}, engine.Options{Jobs: tt.jobs}); err != nil {
			t.Fatal(err)
		}
		if got := most.Load(); got != tt.want {
			t.Errorf("with %d jobs, %d checks ran at once, want %d", tt.jobs, got, tt.want)
		}
	}
}

func TestAStoppedRunFailsTheStepsNotStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	p, err := engine.New(
		engine.Step{Name: "first", Check: func(context.Context) check.Result { cancel(); return check.Result{State: check.OK} }},
		engine.Step{Name: "second", Needs: []string{"first"}, Check: ok("")},
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Check(ctx, &recorder{}, engine.Options{Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results["second"]; got.State != check.Failed || got.Reason != "the run was stopped" {
		t.Errorf("second = %+v, want it failed: the run was stopped", got)
	}
}

func TestEachStepsContextNamesIt(t *testing.T) {
	var got []string
	var mu sync.Mutex
	note := func(ctx context.Context) check.Result {
		mu.Lock()
		got = append(got, event.StepOf(ctx))
		mu.Unlock()
		return check.Result{State: check.OK}
	}
	p, _ := engine.New(engine.Step{Name: "brew", Check: note}, engine.Step{Name: "cask", Check: note})
	if _, err := p.Check(t.Context(), &recorder{}, engine.Options{Jobs: 1}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"brew", "cask"}) {
		t.Errorf("checks ran in steps %q, want brew then cask", got)
	}
}

func TestApply(t *testing.T) {
	installed := false
	var applied []string
	brew := engine.Step{
		Name: "brew", Title: "Formulae",
		Check: func(context.Context) check.Result {
			if installed {
				return check.Result{State: check.OK, Summary: "1 declared, all installed"}
			}
			return check.Result{State: check.Attention, Summary: "1 declared, 0 installed"}
		},
		Apply: func(context.Context, check.Result) error {
			applied = append(applied, "brew")
			installed = true
			return nil
		},
	}
	broken := engine.Step{
		Name: "broken", Title: "Broken",
		Check: func(context.Context) check.Result { return check.Result{State: check.Attention} },
		Apply: func(context.Context, check.Result) error {
			applied = append(applied, "broken")
			return errors.New("couldn't install jq")
		},
	}
	fine := engine.Step{Name: "fine", Check: ok("already"), Apply: func(context.Context, check.Result) error { applied = append(applied, "fine"); return nil }}
	after := engine.Step{Name: "after", Needs: []string{"broken"}, Check: ok(""), Apply: func(context.Context, check.Result) error { applied = append(applied, "after"); return nil }}
	manual := engine.Step{Name: "fda", Title: "Full Disk Access", Manual: "add Ghostty to Full Disk Access", Check: func(context.Context) check.Result {
		return check.Result{State: check.Attention, Summary: "not granted"}
	}}

	p, err := engine.New(brew, broken, fine, after, manual)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	report, err := p.Apply(t.Context(), rec, engine.Options{Command: "apply", Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(applied, []string{"brew", "broken"}) {
		t.Errorf("applied %q, want brew and broken alone: fine stood ok, and after needs broken", applied)
	}
	if got := report.Results["brew"]; got.State != check.OK || got.Summary != "1 declared, all installed" {
		t.Errorf("brew = %+v, want it checked again once applied", got)
	}
	if got := report.Results["broken"]; got.State != check.Failed || got.Reason != "couldn't install jq" {
		t.Errorf("broken = %+v, want it failed with apply's error", got)
	}
	if got := report.Results["after"]; got.State != check.Deferred || got.Reason != "needs Broken" {
		t.Errorf("after = %+v, want it deferred", got)
	}
	if got := report.Results["fda"]; got.State != check.Attention || len(got.Items) != 1 || got.Items[0] != (check.Item{ID: "fda:manual", Name: "add Ghostty to Full Disk Access", State: "manual"}) {
		t.Errorf("fda = %+v, want what to do by hand as an item", got)
	}
	var doing []string
	for _, e := range rec.events {
		if s, ok := e.(event.StepStarted); ok {
			doing = append(doing, s.Step+" "+s.Doing)
		}
	}
	if want := "brew checking,brew applying,broken checking,broken applying,fine checking,fda checking"; strings.Join(doing, ",") != want {
		t.Errorf("steps started %q, want %q", strings.Join(doing, ","), want)
	}
}

func TestApplyActsOnQuietItems(t *testing.T) {
	applied := false
	s := engine.Step{
		Name: "brew",
		Check: func(context.Context) check.Result {
			if applied {
				return check.Result{State: check.OK}
			}
			// Missing, but new: nothing needs attention yet, yet there's
			// something to install.
			return check.Result{State: check.OK, Items: []check.Item{{ID: "brew:jq", Name: "jq", State: "missing", Quiet: "new", Action: "install"}}}
		},
		Apply: func(context.Context, check.Result) error { applied = true; return nil },
	}
	p, err := engine.New(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(t.Context(), &recorder{}, engine.Options{}); err != nil || applied {
		t.Fatalf("Check() applied = %v, %v; want nothing applied by a check", applied, err)
	}
	report, err := p.Apply(t.Context(), &recorder{}, engine.Options{})
	if err != nil || !applied || report.Results["brew"].State != check.OK || len(report.Results["brew"].Items) != 0 {
		t.Errorf("Apply() = %+v, %v, applied %v; want the quiet item installed", report.Results["brew"], err, applied)
	}
}

// A kind's program comes from an earlier step: applying waits for that step,
// whatever it found, and never defers for it; checking doesn't wait at all.
func TestApplyComesAfter(t *testing.T) {
	var mu sync.Mutex
	var order []string
	note := func(what string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, what)
	}
	release := make(chan struct{})
	brew := engine.Step{
		Name: "brew",
		Check: func(context.Context) check.Result {
			note("brew checked")
			return check.Result{State: check.Attention, Summary: "ffmpeg is extra"}
		},
		Apply: func(context.Context, check.Result) error {
			<-release
			note("brew applied")
			return nil
		},
	}
	app := engine.Step{
		Name:  "app",
		After: []string{"brew"},
		Check: func(context.Context) check.Result {
			note("app checked")
			return check.Result{State: check.OK}
		},
	}
	p, err := engine.New(app, brew)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(p.Steps()); !slices.Equal(got, []string{"brew", "app"}) {
		t.Errorf("order = %q, want app after brew", got)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(release)
	}()
	report, err := p.Apply(t.Context(), &recorder{}, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results["app"]; got.State != check.OK {
		t.Errorf("app = %+v, want it applied though brew needs attention", got)
	}
	if i, j := slices.Index(order, "brew applied"), slices.Index(order, "app checked"); i < 0 || j < i {
		t.Errorf("order = %q, want app checked once brew's applied", order)
	}

	// Checking waits for nothing: app may run alongside brew.
	order = nil
	if _, err := p.Check(t.Context(), &recorder{}, engine.Options{Only: []string{"app"}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"app checked"}) {
		t.Errorf("checking app ran %q, want app alone: after isn't a need", order)
	}
}

// A check can defer its own step, as a kind does when its program isn't
// installed; applying leaves it.
func TestACheckThatDefersIsntApplied(t *testing.T) {
	applied := false
	s := engine.Step{
		Name: "npm",
		Check: func(context.Context) check.Result {
			return check.Result{State: check.Deferred, Reason: "needs npm, which isn't installed"}
		},
		Apply: func(context.Context, check.Result) error { applied = true; return nil },
	}
	p, err := engine.New(s)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Apply(t.Context(), &recorder{}, engine.Options{})
	if err != nil || applied || report.Results["npm"].State != check.Deferred {
		t.Errorf("Apply() = %+v, %v, applied %v; want it deferred, not applied", report.Results["npm"], err, applied)
	}
}

// A check that doesn't answer in time is tried once more, then fails,
// saying so, while the rest of the run goes on.
func TestACheckThatHangsFails(t *testing.T) {
	var tries atomic.Int32
	p, err := engine.New(
		engine.Step{Name: "stuck", Check: func(ctx context.Context) check.Result {
			tries.Add(1)
			<-ctx.Done()
			return check.Result{State: check.Failed, Reason: ctx.Err().Error()}
		}},
		engine.Step{Name: "quick", Check: func(context.Context) check.Result { return check.Result{State: check.OK} }},
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Check(t.Context(), &recorder{}, engine.Options{Machine: "laptop", CheckWithin: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	stuck, quick := report.Results["stuck"], report.Results["quick"]
	if stuck.State != check.Failed || stuck.Reason != "didn't answer in 50ms, tried twice" || tries.Load() != 2 || quick.State != check.OK {
		t.Errorf("stuck = %+v after %d tries, quick = %+v", stuck, tries.Load(), quick)
	}
}

// A step before another is applied first, as though the other named it in
// its After; one before a step that isn't there is refused.
func TestBefore(t *testing.T) {
	var mu sync.Mutex
	var order []string
	applied := func(name string) func(context.Context, check.Result) error {
		return func(context.Context, check.Result) error {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
			return nil
		}
	}
	notYet := func(context.Context) check.Result { return check.Result{State: check.Attention} }
	p, err := engine.New(
		engine.Step{Name: "secret", Check: notYet, Apply: applied("secret")},
		engine.Step{Name: "sign-in", Before: []string{"secret"}, Check: notYet, Apply: applied("sign-in")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(t.Context(), &recorder{}, engine.Options{Jobs: 4}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"sign-in", "secret"}) {
		t.Errorf("applied in the order %q, want sign-in, then secret", order)
	}
	if _, err := engine.New(engine.Step{Name: "a", Before: []string{"nowhere"}, Check: ok("")}); err == nil {
		t.Error("a step before one that isn't there was taken")
	}
}

// A step waiting for one thing is applied once that thing is in place,
// while the step putting it there still runs; or, when it never is, once
// that step has finished. A check alone waits for nothing.
func TestWaits(t *testing.T) {
	defer engine.SetWaitPoll(time.Millisecond)()
	var ready atomic.Bool
	release := make(chan struct{})
	var sawCaskRunning atomic.Bool
	casksDone := make(chan struct{})
	p, err := engine.New(
		engine.Step{Name: "cask", Check: func(context.Context) check.Result { return check.Result{State: check.Attention} },
			Apply: func(context.Context, check.Result) error {
				ready.Store(true)
				<-release
				close(casksDone)
				return nil
			}},
		engine.Step{Name: "sign-in", Waits: []engine.Wait{{Step: "cask", Thing: "1password", Ready: func(context.Context) bool { return ready.Load() }}},
			Check: func(context.Context) check.Result {
				select {
				case <-casksDone:
				default:
					sawCaskRunning.Store(true)
				}
				close(release)
				return check.Result{State: check.OK}
			}},
	)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if _, err := p.Apply(t.Context(), rec, engine.Options{Jobs: 4}); err != nil {
		t.Fatal(err)
	}
	if !sawCaskRunning.Load() {
		t.Error("the step waiting for 1password waited for every cask: want it applied once 1password was in")
	}
	var said bool
	for _, e := range rec.events {
		if s, ok := e.(event.StepStarted); ok && s.Step == "sign-in" && s.Doing == "waiting for 1password" {
			said = true
		}
	}
	if !said {
		t.Error("it didn't say what it was waiting for")
	}

	never, err := engine.New(
		engine.Step{Name: "cask", Check: ok("")},
		engine.Step{Name: "sign-in", Waits: []engine.Wait{{Step: "cask", Thing: "1password", Ready: func(context.Context) bool { return false }}}, Check: ok("")},
	)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = never.Apply(t.Context(), &recorder{}, engine.Options{Jobs: 4})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a step waiting for a thing never put in place waited on after its step finished")
	}
}

// A step whose apply says it can't be applied yet isn't failed: it says
// why, and doesn't stand ok.
func TestNotYet(t *testing.T) {
	p, err := engine.New(engine.Step{Name: "secret", Check: func(context.Context) check.Result { return check.Result{State: check.Attention} },
		Apply: func(context.Context, check.Result) error { return engine.NotYet("1Password isn't signed in") }})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	report, err := p.Apply(t.Context(), rec, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res := report.Results["secret"]; res.State != check.Deferred || res.Reason != "1Password isn't signed in" {
		t.Errorf("secret = %+v, want deferred, saying why", res)
	}
}
