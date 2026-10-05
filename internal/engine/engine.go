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

// Step is one thing kit checks, and applies.
type Step struct {
	Name  string
	Title string
	// Area is what the step is about, as the at-a-glance view groups steps:
	// Backups, Mac, Drift, Config, Checks.
	Area string
	// Macs are the Macs it runs on: every Mac when empty.
	Macs []string
	// Needs are the steps that must stand ok before it's checked or applied.
	Needs []string
	// After are steps it's applied after, whether or not they stand ok, as
	// a kind's program is installed by an earlier step: a check doesn't wait
	// for them, and a step a run doesn't take is no wait at all.
	After []string
	// Check finds out how the step stands: cheap, and without side effects.
	Check func(ctx context.Context) check.Result
	// Apply does what the step is for, given what its check found: safe to
	// repeat. Optional.
	Apply func(ctx context.Context, found check.Result) error
	// Manual is what a person must do when the step can't be automated,
	// shown while its check fails. Optional.
	Manual string
}

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
	Now  func() time.Time
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
		infos[i] = event.Step{Name: s.Name, Title: s.title(), Area: s.Area}
		titles[s.Name] = s.title()
	}
	sink.Emit(event.RunStarted{Time: started, Command: opts.Command, Machine: opts.Machine, Version: opts.Version, Steps: infos})

	d := dispatch{
		sink:     sink,
		now:      now,
		apply:    apply,
		titles:   titles,
		results:  make(map[string]check.Result, len(steps)),
		started:  make(map[string]bool, len(steps)),
		finished: make(chan outcome),
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
	apply    bool
	titles   map[string]string
	results  map[string]check.Result
	started  map[string]bool
	finished chan outcome
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
	d.sink.Emit(event.StepStarted{Time: d.now(), Step: s.Name, Doing: "checking"})
	res := checkSafely(ctx, s)
	if d.apply && res.State != check.Deferred && (res.State != check.OK || res.Actions()) && s.Apply != nil && ctx.Err() == nil {
		d.sink.Emit(event.StepStarted{Time: d.now(), Step: s.Name, Doing: "applying"})
		before := res
		if err := applySafely(ctx, s, res); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		res = checkSafely(ctx, s)
		res.Done = done(before, res)
	}
	if res.State == check.Attention && s.Manual != "" {
		res.Items = append(res.Items, check.Item{ID: s.Name + ":manual", Name: s.Manual, State: "manual"})
	}
	return res
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
