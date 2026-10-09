// Package engine runs the pipeline: every step kit checks and applies,
// ordered by what each needs, independent ones side by side. A step that
// fails never ends the run: it's recorded, the steps that need it are
// deferred with the reason, and the rest carry on.
package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
)

// DefaultJobs is how many steps run side by side, unless Options say.
const DefaultJobs = 4

// DefaultCheckWithin is how long a check may take, by default, before it's
// taken as not answering: a check is quick, and one that hangs mustn't hold
// up the rest of the run.
const DefaultCheckWithin = 30 * time.Second

// Step is one thing kit checks, and applies.
type Step struct {
	Name  string
	Title string
	// Waiting is what the step says while it waits its turn, when that's
	// more than that it's waiting: what it'll need of you, or do.
	Waiting string
	// Area is what the step is about, as the at-a-glance view groups steps:
	// Backups, Mac, Drift, Config, Checks.
	Area string
	// Part is what the step counts towards in a view rolling its area up:
	// Packages, Settings, Files, Secrets, or the config repository.
	Part string
	// Macs are the Macs it runs on: every Mac when empty.
	Macs []string
	// Needs are the steps that must stand ok before it's checked or applied.
	Needs []string
	// After are steps it's applied after, whether or not they stand ok, as
	// a kind's program is installed by an earlier step: a check doesn't wait
	// for them, and a step a run doesn't take is no wait at all. Before are
	// the steps applied after it, each as though it named it in its After.
	After, Before []string
	// Waits are things it's applied after, one at a time: each one of
	// another step's things, waited for until it's in place, or until that
	// step has finished, whichever comes first, as a step waits for the one
	// cask it's about while the rest still install.
	Waits []Wait
	// Check finds out how the step stands: cheap, and without side effects.
	Check func(ctx context.Context) check.Result
	// Apply does what the step is for, given what its check found: safe to
	// repeat. Optional.
	Apply func(ctx context.Context, found check.Result) error
	// Manual is what a person must do when the step can't be automated,
	// shown while its check fails. Optional.
	Manual string
	// Admin is whether applying it needs an administrator's password, which
	// the command settles before anything is applied.
	Admin bool
}

// Wait is a thing a step is applied after: one of the things another step
// puts in place, by its step's name and its own, as in cask:1password, and
// whether it's in place yet.
type Wait struct {
	Step, Thing string
	Ready       func(ctx context.Context) bool
}

// waitPoll is how often a step waiting for a thing looks again.
var waitPoll = 3 * time.Second

// NotYet is what applying a step returns when the step can't be applied
// yet, as what it needs isn't there: the step doesn't stand ok, isn't
// failed, and says why.
func NotYet(reason string) error { return &notYet{reason} }

type notYet struct{ reason string }

func (n *notYet) Error() string { return n.reason }

// title is the step's title, or its name.
func (s Step) title() string {
	return cmp.Or(s.Title, s.Name)
}

// runsOn reports whether the step runs on the Mac named mac.
func (s Step) runsOn(mac string) bool {
	return len(s.Macs) == 0 || slices.Contains(s.Macs, mac)
}

// Pipeline is the steps, in an order that puts each after what it needs.
type Pipeline struct {
	steps []Step
}

// New returns the pipeline of steps, ordered so each comes after what it
// needs and what it's applied after, and otherwise as given. Every step must
// have a name of its own and a check, and need or come after only steps in
// the pipeline, without a cycle.
func New(steps ...Step) (*Pipeline, error) {
	byName := make(map[string]Step, len(steps))
	for _, s := range steps {
		switch {
		case s.Name == "":
			return nil, errors.New("a step without a name")
		case s.Check == nil:
			return nil, fmt.Errorf("step %s has no check: every step needs one", s.Name)
		}
		if _, ok := byName[s.Name]; ok {
			return nil, fmt.Errorf("two steps named %s", s.Name)
		}
		byName[s.Name] = s
	}
	steps = slices.Clone(steps)
	at := make(map[string]int, len(steps))
	for i, s := range steps {
		at[s.Name] = i
	}
	for _, s := range steps {
		for _, before := range s.Before {
			i, ok := at[before]
			if !ok {
				return nil, fmt.Errorf("step %s comes before %s, which isn't a step", s.Name, before)
			}
			steps[i].After = append(slices.Clone(steps[i].After), s.Name)
		}
	}
	for _, s := range steps {
		byName[s.Name] = s
	}
	for _, s := range steps {
		for _, need := range s.Needs {
			if _, ok := byName[need]; !ok {
				return nil, fmt.Errorf("step %s needs %s, which isn't a step", s.Name, need)
			}
		}
		for _, after := range s.After {
			if _, ok := byName[after]; !ok {
				return nil, fmt.Errorf("step %s comes after %s, which isn't a step", s.Name, after)
			}
		}
		for _, w := range s.Waits {
			if _, ok := byName[w.Step]; !ok || w.Ready == nil {
				return nil, fmt.Errorf("step %s comes after %s:%s, and %s isn't a step", s.Name, w.Step, w.Thing, w.Step)
			}
		}
	}
	ordered := make([]Step, 0, len(steps))
	placed := make(map[string]bool, len(steps))
	for len(ordered) < len(steps) {
		progress := false
		for _, s := range steps {
			if placed[s.Name] || !allOf(s.Needs, placed) || !allOf(s.After, placed) {
				continue
			}
			ordered = append(ordered, s)
			placed[s.Name] = true
			progress = true
		}
		if !progress {
			var stuck []string
			for _, s := range steps {
				if !placed[s.Name] {
					stuck = append(stuck, s.Name)
				}
			}
			return nil, fmt.Errorf("steps %s need each other in a cycle", strings.Join(stuck, ", "))
		}
	}
	return &Pipeline{steps: ordered}, nil
}

// Steps are the pipeline's steps, in order.
func (p *Pipeline) Steps() []Step {
	return slices.Clone(p.steps)
}

func allOf(names []string, in map[string]bool) bool {
	for _, n := range names {
		if !in[n] {
			return false
		}
	}
	return true
}

// Options are how a run goes.
type Options struct {
	// Command is kit's command the run is for, as in status.
	Command string
	Machine string
	Version string
	// Only are the steps to run, with what they need: every step that runs
	// on the Mac when empty.
	Only []string
	// Jobs is how many steps run side by side: DefaultJobs when 0.
	Jobs int
	// CheckWithin is how long a check may take before it's taken as not
	// answering: DefaultCheckWithin when 0.
	CheckWithin time.Duration
	Now         func() time.Time
}

// Report is how a run's steps stood, in pipeline order.
type Report struct {
	Steps   []Step
	Results map[string]check.Result
}

// Attention reports whether any step needs attention: found wanting,
// failed, or deferred.
func (r Report) Attention() bool {
	for _, res := range r.Results {
		if res.State != check.OK {
			return true
		}
	}
	return false
}

// Check runs the checks of the steps opts select, each once what it needs
// has stood ok, emitting the run's events to sink.
func (p *Pipeline) Check(ctx context.Context, sink event.Sink, opts Options) (Report, error) {
	return p.run(ctx, sink, opts, false)
}

// Apply checks the steps opts select, as Check does, applying each that
// doesn't stand ok and can be, then checking it again.
func (p *Pipeline) Apply(ctx context.Context, sink event.Sink, opts Options) (Report, error) {
	return p.run(ctx, sink, opts, true)
}

// selected are the steps a run takes: those that run on the Mac and opts
// select, with what they need, in pipeline order.
func (p *Pipeline) selected(opts Options) ([]Step, error) {
	var on []Step
	for _, s := range p.steps {
		if s.runsOn(opts.Machine) {
			on = append(on, s)
		}
	}
	if len(opts.Only) == 0 {
		return on, nil
	}
	byName := make(map[string]Step, len(on))
	for _, s := range on {
		byName[s.Name] = s
	}
	want := make(map[string]bool)
	var add func(name string)
	add = func(name string) {
		if want[name] {
			return
		}
		want[name] = true
		for _, need := range byName[name].Needs {
			add(need)
		}
	}
	for _, name := range opts.Only {
		if _, ok := byName[name]; !ok {
			names := make([]string, len(on))
			for i, s := range on {
				names[i] = s.Name
			}
			return nil, fmt.Errorf("no step named %s on this Mac: one of %s", name, strings.Join(names, ", "))
		}
		add(name)
	}
	var steps []Step
	for _, s := range on {
		if want[s.Name] {
			steps = append(steps, s)
		}
	}
	return steps, nil
}

// Planned are the steps a run with opts would run, as its start lists
// them: for a face to show before it starts.
func (p *Pipeline) Planned(opts Options) ([]event.Step, error) {
	steps, err := p.selected(opts)
	if err != nil {
		return nil, err
	}
	infos := make([]event.Step, len(steps))
	for i, s := range steps {
		infos[i] = s.info()
	}
	return infos, nil
}

// info is the step as a run's start lists it.
func (s Step) info() event.Step {
	waits := slices.Concat(s.Needs, s.After)
	for _, w := range s.Waits {
		waits = append(waits, w.Step)
	}
	return event.Step{Name: s.Name, Title: s.title(), Area: s.Area, Part: s.Part, Waiting: s.Waiting, Waits: waits}
}

func (p *Pipeline) run(ctx context.Context, sink event.Sink, opts Options, apply bool) (Report, error) {
	steps, err := p.selected(opts)
	if err != nil {
		return Report{}, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	infos := make([]event.Step, len(steps))
	titles := make(map[string]string, len(steps))
	for i, s := range steps {
		infos[i] = s.info()
		titles[s.Name] = s.title()
	}
	sink.Emit(event.RunStarted{Time: started, Command: opts.Command, Machine: opts.Machine, Version: opts.Version, Steps: infos, Only: opts.Only})

	d := dispatch{
		sink:     sink,
		now:      now,
		within:   cmp.Or(opts.CheckWithin, DefaultCheckWithin),
		apply:    apply,
		titles:   titles,
		results:  make(map[string]check.Result, len(steps)),
		started:  make(map[string]bool, len(steps)),
		finished: make(chan outcome),
		over:     make(map[string]chan struct{}, len(steps)),
	}
	for _, s := range steps {
		d.over[s.Name] = make(chan struct{})
	}
	d.run(ctx, steps, cmp.Or(opts.Jobs, DefaultJobs))

	results := make([]check.Result, 0, len(steps))
	for _, s := range steps {
		results = append(results, d.results[s.Name])
	}
	sink.Emit(event.RunFinished{Time: now(), Duration: now().Sub(started), Counts: event.Tally(results...)})
	return Report{Steps: steps, Results: d.results}, nil
}

// dispatch runs a run's steps: it starts each, in pipeline order, once what
// it needs is done and a job is free, and defers each whose needs didn't
// stand ok. Only its own goroutine touches its maps.
type dispatch struct {
	sink     event.Sink
	now      func() time.Time
	within   time.Duration
	apply    bool
	titles   map[string]string
	results  map[string]check.Result
	started  map[string]bool
	finished chan outcome
	// over are closed as each step finishes, for a step waiting on one of
	// its things.
	over map[string]chan struct{}
}

// outcome is a step done, as its goroutine tells the dispatcher.
type outcome struct {
	step   Step
	result check.Result
	took   time.Duration
}

func (d *dispatch) run(ctx context.Context, steps []Step, jobs int) {
	running := 0
	for len(d.results) < len(steps) {
		for d.startReady(ctx, steps, &running, jobs) {
		}
		if len(d.results) == len(steps) {
			return
		}
		o := <-d.finished
		running--
		d.finish(o.step, o.result, o.took)
	}
}

// startReady goes through the steps not yet started, in order, and starts
// or settles each that can be: deferred when a need didn't stand ok, failed
// when the run was stopped, started when a job is free. It reports whether
// it did anything, as settling one step can free others.
func (d *dispatch) startReady(ctx context.Context, steps []Step, running *int, jobs int) bool {
	progressed := false
	for _, s := range steps {
		if d.started[s.Name] || !d.needsDone(s) || !d.afterDone(s) {
			continue
		}
		if unmet := d.unmet(s); len(unmet) > 0 {
			d.started[s.Name] = true
			d.finish(s, check.Result{State: check.Deferred, Reason: "needs " + strings.Join(unmet, ", ")}, 0)
			progressed = true
			continue
		}
		if ctx.Err() != nil {
			d.started[s.Name] = true
			d.finish(s, check.Result{State: check.Failed, Reason: event.ErrStopped.Error()}, 0)
			progressed = true
			continue
		}
		if *running >= jobs {
			continue
		}
		d.started[s.Name] = true
		*running++
		progressed = true
		go func() {
			began := d.now()
			res := d.step(event.WithStep(ctx, s.Name), s)
			d.finished <- outcome{step: s, result: res, took: d.now().Sub(began)}
		}()
	}
	return progressed
}

// needsDone reports whether every step s needs is done.
func (d *dispatch) needsDone(s Step) bool {
	for _, need := range s.Needs {
		if _, ok := d.results[need]; !ok {
			return false
		}
	}
	return true
}

// afterDone reports whether, in a run that applies, every step s comes
// after that the run takes is done.
func (d *dispatch) afterDone(s Step) bool {
	if !d.apply {
		return true
	}
	for _, after := range s.After {
		_, taken := d.titles[after]
		if _, done := d.results[after]; taken && !done {
			return false
		}
	}
	return true
}

// unmet are the titles of the steps s needs that didn't stand ok.
func (d *dispatch) unmet(s Step) []string {
	var unmet []string
	for _, need := range s.Needs {
		if d.results[need].State != check.OK {
			unmet = append(unmet, d.titles[need])
		}
	}
	return unmet
}

// step checks s, and applies it, when the run applies and s doesn't stand
// ok, then checks it again. A step its own check defers, as what it needs
// isn't there, isn't applied.
func (d *dispatch) step(ctx context.Context, s Step) check.Result {
	if d.apply {
		d.waitFor(ctx, s)
	}
	d.sink.Emit(event.StepStarted{Time: d.now(), Step: s.Name, Doing: "checking"})
	res := d.check(ctx, s)
	if d.apply && res.State != check.Deferred && (res.State != check.OK || res.Actions()) && s.Apply != nil && ctx.Err() == nil {
		d.sink.Emit(event.StepStarted{Time: d.now(), Step: s.Name, Doing: "applying"})
		before := res
		if err := applySafely(event.WithChanging(ctx), s, res); err != nil {
			if _, ok := errors.AsType[*notYet](err); ok {
				return check.Result{State: check.Deferred, Reason: err.Error()}
			}
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		res = d.check(ctx, s)
		res.Done = done(before, res)
	}
	if res.State == check.Attention && s.Manual != "" {
		res.Items = append(res.Items, check.Item{ID: s.Name + ":manual", Name: s.Manual, State: "manual"})
	}
	return res
}

// waitFor waits for each of the things s is applied after: till it's in
// place, its step has finished, or the run's stopped; saying, meanwhile,
// what it's waiting for.
func (d *dispatch) waitFor(ctx context.Context, s Step) {
	for _, w := range s.Waits {
		if w.Ready(ctx) {
			continue
		}
		d.sink.Emit(event.StepStarted{Time: d.now(), Step: s.Name, Doing: "waiting for " + w.Thing})
		tick := time.NewTicker(waitPoll)
		for waiting := true; waiting; {
			select {
			case <-ctx.Done():
				waiting = false
			case <-d.over[w.Step]:
				waiting = false
			case <-tick.C:
				waiting = !w.Ready(ctx)
			}
		}
		tick.Stop()
	}
}

// done are the items before had an action for that after no longer has:
// what applying the step dealt with.
func done(before, after check.Result) []check.Item {
	still := make(map[string]bool, len(after.Items))
	for _, it := range after.Items {
		still[it.ID] = true
	}
	var dealt []check.Item
	for _, it := range before.Items {
		if it.Action != "" && !still[it.ID] {
			dealt = append(dealt, it)
		}
	}
	return dealt
}

// finish records s's result, and emits it.
func (d *dispatch) finish(s Step, res check.Result, took time.Duration) {
	if res.State == "" {
		res = check.Result{State: check.Failed, Reason: "the check said nothing of how the step stands"}
	}
	d.results[s.Name] = res
	d.sink.Emit(event.StepFinished{Time: d.now(), Step: s.Name, Result: res, Duration: took})
	close(d.over[s.Name])
}

// checkSafely runs s's check, a panic in it failing the step rather than
// the run.
func checkSafely(ctx context.Context, s Step) (res check.Result) {
	defer func() {
		if p := recover(); p != nil {
			res = check.Result{State: check.Failed, Reason: fmt.Sprintf("the check panicked: %v", p)}
		}
	}()
	return s.Check(ctx)
}

// check runs s's check, giving it d.within to answer: one that doesn't is
// tried once more, then fails, saying so, while the rest of the run goes
// on.
func (d *dispatch) check(ctx context.Context, s Step) check.Result {
	for try := 0; ; try++ {
		within, cancel := context.WithTimeout(ctx, d.within)
		res := checkSafely(within, s)
		late := errors.Is(within.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		switch {
		case !late:
			return res
		case try == 1:
			return check.Result{State: check.Failed, Reason: fmt.Sprintf("didn't answer in %s, tried twice", d.within)}
		}
	}
}

// applySafely runs s's apply on what its check found, a panic in it
// failing the step rather than the run.
func applySafely(ctx context.Context, s Step, found check.Result) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("applying panicked: %v", p)
		}
	}()
	return s.Apply(ctx, found)
}
