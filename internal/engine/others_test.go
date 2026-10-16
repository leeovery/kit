package engine_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
)

// A step that's always applied is applied while it stands ok, as the steps
// by hand at the run's end are, which raise what the rest left: never by a
// check.
func TestAlwaysApply(t *testing.T) {
	for _, tt := range []struct {
		always, apply, want bool
	}{{true, true, true}, {false, true, false}, {true, false, false}} {
		applied := false
		p, err := engine.New(engine.Step{Name: "manual", AlwaysApply: tt.always, Check: ok("fine"), Apply: func(context.Context, check.Result) error {
			applied = true
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		run := p.Check
		if tt.apply {
			run = p.Apply
		}
		if _, err := run(t.Context(), &recorder{}, engine.Options{}); err != nil || applied != tt.want {
			t.Errorf("always %v, applying %v: applied %v, %v; want %v", tt.always, tt.apply, applied, err, tt.want)
		}
	}
}

// changingCheck is a check saying how many times it's been run, the step
// it's run as, and whether as changing things.
type changingCheck struct {
	mu    sync.Mutex
	runs  int
	as    []string
	found func(run int) check.Result
}

func (c *changingCheck) check(ctx context.Context) check.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runs++
	as := event.StepOf(ctx)
	if event.Changing(ctx) {
		as += " changing"
	}
	c.as = append(c.as, as)
	return c.found(c.runs)
}

// lastStep is a step applied after those named, its apply given what's in
// its context: for a step that works on the run's others.
func lastStep(after []string, apply func(ctx context.Context)) engine.Step {
	return engine.Step{Name: "last", After: after, AlwaysApply: true, Check: ok("fine"), Apply: func(ctx context.Context, _ check.Result) error {
		apply(ctx)
		return nil
	}}
}

// A step sees the run's steps that have finished, in the pipeline's order,
// as they stand; looks at one, its check run as that step's, the run's
// record of it left; and checks one again, taking what it finds as how it
// stands: the run's report, and the step finishing again for the faces and
// the log, with what applying it did earlier kept. Outside a run, there's
// nothing to see.
func TestAStepWorksOnTheRunsOthers(t *testing.T) {
	disk := &changingCheck{found: func(run int) check.Result {
		if run == 1 {
			return check.Result{State: check.Attention, Summary: "no disk", Items: []check.Item{{ID: "disk:none", Name: "Add a disk", State: check.Problem}}}
		}
		return check.Result{State: check.OK, Summary: "a disk"}
	}}
	fixed := false
	steps := []engine.Step{
		{Name: "disk", Check: disk.check},
		{Name: "brew", Check: func(context.Context) check.Result {
			if fixed {
				return check.Result{State: check.OK, Summary: "all installed"}
			}
			return check.Result{State: check.Attention, Items: []check.Item{{ID: "brew:jq", Name: "jq", State: "missing", Action: "install"}}}
		}, Apply: func(context.Context, check.Result) error { fixed = true; return nil }},
	}
	var seen []engine.StepResult
	var looked, rechecked check.Result
	var lookedOK, recheckedOK, unknown, notFinished bool
	steps = append(steps, lastStep([]string{"disk", "brew"}, func(ctx context.Context) {
		seen = engine.Finished(ctx)
		looked, lookedOK = engine.Look(ctx, "disk")
		_, unknown = engine.Look(ctx, "nothing")
		_, notFinished = engine.Recheck(ctx, "last")
		rechecked, recheckedOK = engine.Recheck(ctx, "brew")
	}))
	p, err := engine.New(steps...)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	report, err := p.Apply(t.Context(), rec, engine.Options{Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range seen {
		order = append(order, s.Step+" "+string(s.Result.State))
	}
	if !slices.Equal(order, []string{"disk attention", "brew ok"}) {
		t.Errorf("the last step saw %q; want the two finished, in order, as they stood", order)
	}
	if !lookedOK || looked.State != check.OK || unknown || report.Results["disk"].State != check.Attention {
		t.Errorf("looked at disk: %+v %v, at a step not in the run %v; disk stands %s: want it ok, nothing for the other, disk's record left", looked, lookedOK, unknown, report.Results["disk"].State)
	}
	if !slices.Equal(disk.as, []string{"disk", "disk"}) {
		t.Errorf("disk's check ran as %q; want it disk's both times, never changing things", disk.as)
	}
	if notFinished || !recheckedOK || rechecked.State != check.OK || len(rechecked.Done) != 1 || !sameResult(report.Results["brew"], rechecked) {
		t.Errorf("rechecked brew: %+v %v, the running step %v; the report %+v: want brew's new result in the report, what it installed kept", rechecked, recheckedOK, notFinished, report.Results["brew"])
	}
	var brew []string
	for _, e := range rec.events {
		switch e := e.(type) {
		case event.StepStarted:
			if e.Step == "brew" {
				brew = append(brew, "started "+e.Doing)
			}
		case event.StepFinished:
			if e.Step == "brew" {
				brew = append(brew, "finished "+string(e.Result.State))
			}
		}
	}
	if want := []string{"started checking", "started applying", "finished ok", "started checking", "finished ok"}; !slices.Equal(brew, want) {
		t.Errorf("brew's events %q, want %q", brew, want)
	}
	if engine.Finished(t.Context()) != nil {
		t.Error("outside a run, steps were seen")
	}
	if _, ok := engine.Recheck(t.Context(), "brew"); ok {
		t.Error("outside a run, a step was checked again")
	}
}

// sameResult is whether two results say the same: how they stand, and what applying did.
func sameResult(a, b check.Result) bool {
	return a.State == b.State && a.Summary == b.Summary && slices.EqualFunc(a.Done, b.Done, func(x, y check.Item) bool { return x.ID == y.ID })
}

// Once the run's stopped, nothing's checked again.
func TestNothingsRecheckedOnceTheRunsStopped(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	var rechecked bool
	p, err := engine.New(engine.Step{Name: "disk", Check: ok("fine")}, lastStep([]string{"disk"}, func(ctx context.Context) {
		stop()
		_, rechecked = engine.Recheck(ctx, "disk")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(ctx, &recorder{}, engine.Options{}); err != nil || rechecked {
		t.Errorf("stopped, rechecked %v, %v; want nothing checked again", rechecked, err)
	}
}

// A step's choice is another step's to use, each part run as its step's
// own: ready as a check, looking; answering as changing things. A step
// without one has none; one without Ready leaves a to-do as it is.
func TestAStepsChoiceRunsAsItsOwn(t *testing.T) {
	var as []string
	note := func(ctx context.Context, what string) {
		s := event.StepOf(ctx) + " " + what
		if event.Changing(ctx) {
			s += " changing"
		}
		as = append(as, s)
	}
	prefs := engine.Step{Name: "prefs", Check: ok("paused"), Choice: &engine.Choice{
		Ready: func(ctx context.Context, it check.Item) check.Item {
			note(ctx, "ready")
			it.Name += ", 15 Oct"
			return it
		},
		Answer: func(ctx context.Context, _ check.Item, key string) (string, error) {
			note(ctx, "answer "+key)
			return "brought back", nil
		},
	}}
	plain := engine.Step{Name: "arq", Check: ok("fine"), Choice: &engine.Choice{
		Answer: func(context.Context, check.Item, string) (string, error) { return "", nil },
	}}
	var readied check.Item
	var said string
	var none, ready bool
	p, err := engine.New(prefs, plain, engine.Step{Name: "disk", Check: ok("fine")}, lastStep([]string{"prefs", "arq", "disk"}, func(ctx context.Context) {
		c := engine.ChoiceOf(ctx, "prefs")
		readied = c.Ready(ctx, check.Item{Name: "brings them back"})
		said, _ = c.Answer(ctx, readied, "r")
		none = engine.ChoiceOf(ctx, "disk") == nil
		ready = engine.ChoiceOf(ctx, "arq").Ready(ctx, check.Item{Name: "as it is"}).Name == "as it is"
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(t.Context(), &recorder{}, engine.Options{Jobs: 1}); err != nil {
		t.Fatal(err)
	}
	if readied.Name != "brings them back, 15 Oct" || said != "brought back" || !none || !ready {
		t.Errorf("readied %q, said %q, disk's none %v, arq's left as it is %v", readied.Name, said, none, ready)
	}
	if !slices.Equal(as, []string{"prefs ready", "prefs answer r changing"}) {
		t.Errorf("the choice ran as %q; want prefs' own, ready looking, answering changing things", as)
	}
	if engine.ChoiceOf(t.Context(), "prefs") != nil {
		t.Error("outside a run, a step's choice was there")
	}
}
