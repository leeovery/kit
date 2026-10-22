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
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hold"
	"github.com/leeovery/kit/internal/runner"
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
	// Row is the row of applying's screen it sits in, when it says, as a
	// kind's step does; Tool is what its commands run, as the person knows
	// it, when it says: Homebrew.
	Row, Tool string
	// Macs are the Macs it runs on: every Mac when empty.
	Macs []string
	// Needs are the steps that must stand ok before it's checked or applied.
	Needs []string
	// After are steps it's applied after, whether or not they stand ok, as
	// git's settings are after the secrets they sign with: a check doesn't
	// wait for them, and a step a run doesn't take is no wait at all. Before are
	// the steps applied after it, each as though it named it in its After.
	After, Before []string
	// Waits are things it's applied after, one at a time: each one of
	// another step's things, waited for until it's in place, or until that
	// step has finished, whichever comes first, as a step waits for the one
	// cask it's about while the rest still install; or, without a Step, a
	// thing no step in particular puts in place, as a command it runs,
	// waited for until it's in place, or until nothing at work in the run
	// could put it there (quiet). One that doesn't come leaves the step
	// unapplied: deferred, unless it stands ok.
	Waits []Wait
	// Later are what it waits for partway through applying, once it has
	// applied what it can without them, as a kind installs a section of its
	// lines needing a secret once the secret's synced: its Apply waits for
	// each with Await. Each is one of another step's things, or, without
	// Ready, another step, done once it's finished. They don't order it:
	// the step it waits for can come after it.
	Later []Wait
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
	// ByHand are the steps by hand applying it raises, in order.
	ByHand []event.Hand
	// Did is what applying says it did to each thing it put in place, as
	// in installed, set or synced: a kind's verb. Optional.
	Did string
	// AlwaysApply has a run that applies apply it however its check finds
	// it, as the steps by hand at the run's end are, which raise what the
	// rest of the run left the person to do as well as their own lines.
	AlwaysApply bool
	// Choice is what the to-dos its check finds do when they're a choice
	// (an item's ToDo with keys), as applying raises them at its end, and
	// once one it watches is done. Optional.
	Choice *Choice
	// Again does the step once more, finished, should a step applied after
	// it find it wasn't done after all (Undone), as a step by hand's card
	// raised again. Optional.
	Again func(ctx context.Context) error
}

// Choice is what a step's to-dos do beyond being looked at: Ready is one as
// it comes up, what it says filled in where that takes looking, as the
// date of the save a key brings back (optional); Answer does what key
// says, and says what it did, in kit's words; Settle finishes what one kit
// watched waited for, once it's found done, as bringing back the settings
// of the apps it had the person open, and says what it did, if anything
// (optional).
type Choice struct {
	Ready  func(ctx context.Context, it check.Item) check.Item
	Answer func(ctx context.Context, it check.Item, key string) (said string, err error)
	Settle func(ctx context.Context, it check.Item) (said string)
}

// Wait is a thing a step is applied after: one of the things another step
// puts in place, by its step's name and its own, as in cask:firefox, and
// whether it's in place yet; among a step's Waits, without a step, a thing
// no step in particular puts in place; or, among a step's Later, without a
// thing or Ready, that step itself.
type Wait struct {
	Step, Thing string
	Ready       func(ctx context.Context) bool
}

// there reports whether w's thing is in place, as Ready says, looked at as
// a check looks, whichever step waits and however far it's got applying:
// what a command run to look prints is never shown as the step's changing
// things.
func (w Wait) there(ctx context.Context) bool {
	return w.Ready(event.WithChecking(ctx))
}

// waitPoll is how often a step waiting for a thing looks again, when the
// step putting it in place hasn't said it may have (Changed): looking can
// cost, as listing what Homebrew has installed does.
var waitPoll = 30 * time.Second

// NotYet is what applying a step returns when the step can't be applied
// yet, as what it needs isn't there: the step doesn't stand ok, isn't
// failed, and says why.
func NotYet(reason string) error { return &notYet{reason} }

type notYet struct{ reason string }

func (n *notYet) Error() string { return n.reason }

// IsNotYet reports whether err says a step can't be applied yet (NotYet),
// rather than that applying it failed.
func IsNotYet(err error) bool {
	_, ok := errors.AsType[*notYet](err)
	return ok
}

// NotThere is why what a step's applying needs can't be done without
// things, by name, that haven't come: needs TOKEN, which isn't there yet.
func NotThere(things []string) string {
	if len(things) == 1 {
		return "needs " + things[0] + ", which isn't there yet"
	}
	return "needs " + strings.Join(things, " and ") + ", which aren't there yet"
}

// Reason is err as a step's reason says it: each command that failed, as
// wrapped or among several joined, by what it said, the last line of its
// standard error, or, when it said nothing, by its program and how it
// exited; never the command itself, which can be long, or many lines, and
// is in the run's log.
func Reason(err error) string {
	reason := err.Error()
	for _, exit := range exits(err) {
		said := exit.Stderr
		if said == "" {
			said = fmt.Sprintf("%s exited %d", program(exit.Command), exit.Code)
		}
		reason = strings.Replace(reason, exit.Error(), said, 1)
	}
	return reason
}

// exits are the commands that failed that err holds, wrapped or joined, in
// order.
func exits(err error) []*runner.ExitError {
	switch e := err.(type) {
	case *runner.ExitError:
		return []*runner.ExitError{e}
	case interface{ Unwrap() error }:
		return exits(e.Unwrap())
	case interface{ Unwrap() []error }:
		var out []*runner.ExitError
		for _, inner := range e.Unwrap() {
			out = append(out, exits(inner)...)
		}
		return out
	}
	return nil
}

// program is the name of the program command runs, command as a shell
// would take it, its first word quoted when it needs to be.
func program(command string) string {
	first, _, _ := strings.Cut(command, " ")
	if quoted, ok := strings.CutPrefix(command, "'"); ok {
		first, _, _ = strings.Cut(quoted, "'")
	}
	return filepath.Base(first)
}

// title is the step's title, or its name.
func (s Step) title() string {
	return cmp.Or(s.Title, s.Name)
}

// asks is what the step asks of the person, as its card's title says, when
// it's a step by hand of its own, as the faces show it; else its title.
func (s Step) asks() string {
	if len(s.ByHand) == 1 && s.ByHand[0].Name == s.Name {
		return s.ByHand[0].Title
	}
	return s.title()
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
			switch _, ok := byName[w.Step]; {
			case w.Step == "" && w.Ready == nil:
				return nil, fmt.Errorf("step %s comes after %s, and nothing says when it's there", s.Name, w.Thing)
			case w.Step != "" && (!ok || w.Ready == nil):
				return nil, fmt.Errorf("step %s comes after %s:%s, and %s isn't a step", s.Name, w.Step, w.Thing, w.Step)
			}
		}
		for _, w := range s.Later {
			if _, ok := byName[w.Step]; !ok {
				return nil, fmt.Errorf("step %s waits for %s, which isn't a step", s.Name, w.Step)
			}
		}
	}
	if err := waitsOnEachOther(steps, byName); err != nil {
		return nil, err
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

// waitsOnEachOther finds a step waiting for another's thing, or for
// another, that can't finish till it has: one in that step's lead-up needs
// it to stand ok. Waiting frees the rest of the lead-up from it (dispatch's
// free), so only a need would hold the run up for good.
func waitsOnEachOther(steps []Step, byName map[string]Step) error {
	for _, s := range steps {
		for _, w := range slices.Concat(s.Waits, s.Later) {
			if w.Step == s.Name || w.Step == "" {
				continue
			}
			lead := leadUp(w.Step, byName)
			for _, x := range steps {
				if lead[x.Name] && slices.Contains(x.Needs, s.Name) {
					return fmt.Errorf("step %s waits for %s, which waits on %s: %s needs it", s.Name, w.Step, s.Name, x.Name)
				}
			}
		}
	}
	return nil
}

// leadUp is the step named name and what must be done, or waiting, before
// it can finish: the steps it needs, comes after and waits for, and theirs
// in turn, by name.
func leadUp(name string, byName map[string]Step) map[string]bool {
	lead := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		if lead[name] {
			return
		}
		lead[name] = true
		s := byName[name]
		for _, next := range slices.Concat(s.Needs, s.After) {
			visit(next)
		}
		for _, w := range slices.Concat(s.Waits, s.Later) {
			if w.Step != "" {
				visit(w.Step)
			}
		}
	}
	visit(name)
	return lead
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
	// Held is whether the run's time limits wait, a check's and each
	// command's: while macOS is asking the person at the Mac something,
	// which holds up the command that brought it. Never, when nil.
	Held func() bool
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
		if w.Step != "" {
			waits = append(waits, w.Step)
		}
	}
	for _, w := range s.Later {
		if w.Thing == "" && !slices.Contains(waits, w.Step) {
			waits = append(waits, w.Step)
		}
	}
	return event.Step{Name: s.Name, Title: s.title(), Area: s.Area, Part: s.Part, Row: s.Row, Tool: s.Tool, Waiting: s.Waiting, Waits: waits, ByHand: s.ByHand, Did: s.Did}
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
	ctx = hold.With(ctx, opts.Held)
	started := now()
	infos := make([]event.Step, len(steps))
	titles := make(map[string]string, len(steps))
	byName := make(map[string]Step, len(steps))
	for i, s := range steps {
		infos[i] = s.info()
		titles[s.Name] = s.title()
		byName[s.Name] = s
	}
	sink.Emit(event.RunStarted{Time: started, Command: opts.Command, Machine: opts.Machine, Version: opts.Version, Steps: infos, Only: opts.Only})

	d := dispatch{
		sink:     sink,
		now:      now,
		within:   cmp.Or(opts.CheckWithin, DefaultCheckWithin),
		apply:    apply,
		titles:   titles,
		byName:   byName,
		steps:    steps,
		calls:    make(chan func()),
		results:  make(map[string]check.Result, len(steps)),
		started:  make(map[string]bool, len(steps)),
		finished: make(chan outcome),
		parked:   make(chan parking),
		resumed:  make(chan chan struct{}),
		looked:   make(chan struct{}),
		over:     make(map[string]chan struct{}, len(steps)),
		putting:  make(map[string]map[string]bool),
		freed:    make(map[string]map[string]bool),
		looks:    make(map[*look]bool),
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
// stand ok. Only its own goroutine touches its maps, but away, putting,
// freed and looks, which mu guards, and titles, byName, steps and over,
// which steps' goroutines read, as they never change once it's started; a
// step's goroutine has it do anything else through calls (inLoop).
type dispatch struct {
	sink     event.Sink
	now      func() time.Time
	within   time.Duration
	apply    bool
	titles   map[string]string
	byName   map[string]Step
	steps    []Step
	calls    chan func()
	results  map[string]check.Result
	started  map[string]bool
	finished chan outcome
	// parked are steps giving their jobs up while they wait for a thing;
	// resumed, steps done waiting, each asking for a job back, given it as
	// its channel's closed; queued, those still asking, first come first;
	// looked, steps waiting on, having looked again.
	parked  chan parking
	resumed chan chan struct{}
	queued  []chan struct{}
	looked  chan struct{}
	// over are closed as each step finishes, for a step waiting on one of
	// its things.
	over map[string]chan struct{}
	// away are how many steps have given their jobs up while they wait for
	// a person (Release), who may put in place what others wait for;
	// putting, for each step, the things it says it's putting in place, not
	// there yet (Putting); freed, for each step, the steps it takes as done
	// before they've finished, as each waits for what that step leads up to
	// (free); looks, the waits of the steps waiting.
	mu      sync.Mutex
	away    int
	putting map[string]map[string]bool
	freed   map[string]map[string]bool
	looks   map[*look]bool
}

// look is a step's wait for a thing of the step on, or, on none, for one no
// step in particular puts in place: wake is closed to have it look again,
// as on's things may have come; woken is whether it's been woken, or is
// looking, and hasn't gone back to waiting; quiet, whether the run went
// quiet while it waited.
type look struct {
	on            string
	wake          chan struct{}
	closed, woken bool
	quiet         bool
}

// parking is a step giving its job up as it waits for a thing of the step
// on, or for on itself, or for a person (on ""): away, when it gave it up
// already, waiting for a person (Release), so has none to give up.
type parking struct {
	step, on string
	away     bool
}

// outcome is a step done, as its goroutine tells the dispatcher: away,
// when it finished with its job given up, waiting for a person (Release).
type outcome struct {
	step   Step
	result check.Result
	took   time.Duration
	away   bool
}

// run runs steps, jobs at a time: a step waiting for a thing gives its job
// up meanwhile, and gets one back, before any step not yet started, once
// it's done waiting.
func (d *dispatch) run(ctx context.Context, steps []Step, jobs int) {
	running := 0
	for len(d.results) < len(steps) {
		for ; len(d.queued) > 0 && running < jobs; running++ {
			close(d.queued[0])
			d.queued = d.queued[1:]
		}
		for d.startReady(ctx, steps, &running, jobs) {
		}
		if len(d.results) == len(steps) {
			return
		}
		if running == 0 && len(d.queued) == 0 {
			d.hush()
		}
		select {
		case o := <-d.finished:
			if o.away {
				d.mu.Lock()
				d.away--
				d.mu.Unlock()
			} else {
				running--
			}
			d.finish(o.step, o.result, o.took)
		case p := <-d.parked:
			if !p.away {
				running--
			}
			d.free(p)
		case granted := <-d.resumed:
			d.queued = append(d.queued, granted)
		case <-d.looked:
		case call := <-d.calls:
			call()
		}
	}
}

// inLoop has the dispatcher's goroutine do f, for a step's, and waits till
// it's done: the dispatcher's still running while the step that asks is,
// as it runs till every step has finished.
func (d *dispatch) inLoop(f func()) {
	done := make(chan struct{})
	d.calls <- func() {
		f()
		close(done)
	}
	<-done
}

// hush has the waits for things no step in particular puts in place end,
// as the run's gone quiet: no step's at work, or can start, or is to look
// again at what it waits for, or waits for a person, so none could put them
// there. Each looks once more, then ends without what it waited for.
func (d *dispatch) hush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.away > 0 {
		return
	}
	for lk := range d.looks {
		if lk.woken {
			return
		}
	}
	for lk := range d.looks {
		if lk.on == "" && !lk.quiet {
			lk.quiet = true
			lk.rouse()
		}
	}
}

// free has the steps p.on leads up to take p.step as done, as p.step waits
// on p.on: none of them, then, waits for p.step, which waits for them. Those
// waiting for p.step's things look again, to see it.
func (d *dispatch) free(p parking) {
	if p.on == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for name := range leadUp(p.on, d.byName) {
		if d.freed[name] == nil {
			d.freed[name] = map[string]bool{}
		}
		d.freed[name][p.step] = true
	}
	d.wake(p.step)
}

// isFreed reports whether step takes the step from as done, as from waits
// for what step leads up to.
func (d *dispatch) isFreed(step, from string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.freed[step][from]
}

// watch notes a wait for a thing of the step on, or of none, as on "".
func (d *dispatch) watch(on string) *look {
	d.mu.Lock()
	defer d.mu.Unlock()
	lk := &look{on: on, wake: make(chan struct{})}
	d.looks[lk] = true
	return lk
}

// unwatch forgets lk, done waiting.
func (d *dispatch) unwatch(lk *look) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.looks, lk)
}

// poke wakes the steps waiting for step's things, and for things no step in
// particular puts in place, to look again.
func (d *dispatch) poke(step string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.wake(step)
}

// wake is poke, mu held.
func (d *dispatch) wake(step string) {
	for lk := range d.looks {
		if lk.on == step || lk.on == "" {
			lk.rouse()
		}
	}
}

// rouse has the step waiting look again: mu is held.
func (lk *look) rouse() {
	lk.woken = true
	if !lk.closed {
		lk.closed = true
		close(lk.wake)
	}
}

// looking notes lk's step looking again, woken or of its own accord, and
// has what wakes it next start afresh.
func (d *dispatch) looking(lk *look) {
	d.mu.Lock()
	defer d.mu.Unlock()
	lk.woken, lk.wake, lk.closed = true, make(chan struct{}), false
}

// idle notes lk's step waiting on, having looked: still woken, should it
// have been woken again meanwhile, to look once more. The dispatcher hears,
// as the run may have gone quiet.
func (d *dispatch) idle(lk *look) {
	d.mu.Lock()
	lk.woken = lk.closed
	d.mu.Unlock()
	d.looked <- struct{}{}
}

// isQuiet reports whether the run went quiet while lk's step waited.
func (d *dispatch) isQuiet(lk *look) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return lk.quiet
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
			d.finish(s, stopped(), 0)
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
			j := &job{d: d, step: s.Name}
			res := d.step(d.stepContext(ctx, s, j), s)
			// A step the run was stopped in the middle of failed for that,
			// whatever stopping it broke on the way.
			if ctx.Err() != nil && res.State == check.Failed {
				res = stopped()
			}
			d.finished <- outcome{step: s, result: res, took: d.now().Sub(began), away: j.isAway()}
		}()
	}
	return progressed
}

// stepContext is ctx for running s: what runs in it is s's, what s says
// it's doing as it goes, and tells the run's log, is emitted, what it waits
// for partway through applying is waited for as its Later are, what it says
// may be in place has the steps waiting for its things look again, what it
// says it's putting in place isn't there for them till it says it is, and
// what it comes after that it finds wasn't done after all is done again. j
// is its job.
func (d *dispatch) stepContext(ctx context.Context, s Step, j *job) context.Context {
	ctx = context.WithValue(d.attributed(ctx, s), dispatchKey{}, d)
	ctx = context.WithValue(ctx, changedKey{}, func() { d.poke(s.Name) })
	ctx = context.WithValue(ctx, jobKey{}, j)
	ctx = context.WithValue(ctx, puttingKey{}, func(things []string) func() { return d.putIn(s.Name, things) })
	ctx = context.WithValue(ctx, putKey{}, func(thing string) { d.put(s.Name, thing) })
	ctx = context.WithValue(ctx, undoneKey{}, func(ctx context.Context) bool { return d.undone(ctx, s) })
	return context.WithValue(ctx, awaitKey{}, func(ctx context.Context, w Wait, says string) bool {
		return d.await(ctx, s, w, func() { event.Say(ctx, event.Doing{Says: says}) })
	})
}

// attributed is ctx for running something of s's: what runs in it is s's,
// and what it says it's doing, and tells the run's log, is emitted as s's.
func (d *dispatch) attributed(ctx context.Context, s Step) context.Context {
	ctx = event.WithSaying(event.WithStep(ctx, s.Name), func(e event.Doing) {
		e.Time, e.Step = d.now(), s.Name
		d.sink.Emit(e)
	})
	return event.WithLogging(ctx, func(e event.Logged) {
		e.Time, e.Step = d.now(), s.Name
		d.sink.Emit(e)
	})
}

type dispatchKey struct{}

// running is the run ctx is a step of, if it is.
func running(ctx context.Context) (*dispatch, bool) {
	d, ok := ctx.Value(dispatchKey{}).(*dispatch)
	return d, ok
}

// Title is the title of the step called name, as the run ctx is a step of
// knows it: its name outside a run, or for a step the run hasn't.
func Title(ctx context.Context, name string) string {
	if d, ok := running(ctx); ok {
		if title, ok := d.titles[name]; ok {
			return title
		}
	}
	return name
}

// Takes reports whether the run ctx is a step of takes the step called
// name: false outside a run.
func Takes(ctx context.Context, name string) bool {
	d, ok := running(ctx)
	if !ok {
		return false
	}
	_, ok = d.titles[name]
	return ok
}

// Holding is how many of the steps of the run ctx is a step of wait on the
// step called name: those not finished yet that need it, are applied after
// it, or wait for it or one of its things, and those that wait on them in
// turn. None outside a run.
func Holding(ctx context.Context, name string) int {
	d, ok := running(ctx)
	if !ok {
		return 0
	}
	finished := map[string]bool{}
	d.inLoop(func() {
		for step := range d.results {
			finished[step] = true
		}
	})
	return len(waitingOn(name, d.steps, finished))
}

// waitingOn are the steps of steps that wait on the one called name, by
// name, as Holding counts them: a finished step waits on nothing.
func waitingOn(name string, steps []Step, finished map[string]bool) map[string]bool {
	next := map[string][]string{}
	for _, s := range steps {
		if finished[s.Name] {
			continue
		}
		on := slices.Concat(s.Needs, s.After)
		for _, w := range slices.Concat(s.Waits, s.Later) {
			if w.Step != "" {
				on = append(on, w.Step)
			}
		}
		for _, x := range on {
			next[x] = append(next[x], s.Name)
		}
	}
	held := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		for _, s := range next[name] {
			if !held[s] {
				held[s] = true
				visit(s)
			}
		}
	}
	visit(name)
	delete(held, name)
	return held
}

// StepResult is a step of the run, by name, and how it stands.
type StepResult struct {
	Step   string
	Result check.Result
}

// Finished are the steps of the run that have finished, in the pipeline's
// order, each with how it stands now, as the step ctx is running sees them.
// Outside a run, none.
func Finished(ctx context.Context) []StepResult {
	d, ok := running(ctx)
	if !ok {
		return nil
	}
	var out []StepResult
	d.inLoop(func() {
		for _, s := range d.steps {
			if res, done := d.results[s.Name]; done {
				out = append(out, StepResult{Step: s.Name, Result: res})
			}
		}
	})
	return out
}

// Look runs the check of the run's step called name, for the step ctx is
// running, as that step's own, given the time a check is given: how it
// stands now, the run's record of it left as it is. False outside a run,
// or for a step the run doesn't take.
func Look(ctx context.Context, name string) (check.Result, bool) {
	d, ok := running(ctx)
	if !ok {
		return check.Result{}, false
	}
	s, ok := d.byName[name]
	if !ok {
		return check.Result{}, false
	}
	return d.check(d.attributed(event.WithChecking(ctx), s), s), true
}

// Recheck checks again the run's step called name, finished, for the step
// ctx is running, as Look does, and takes what it finds as how the step
// stands, as when what held it has been done: in the run's report, and
// emitted as the step finishing again, for the faces and the run's log,
// with what applying it did earlier in the run. False outside a run, for a
// step not finished, and once the run's stopped.
func Recheck(ctx context.Context, name string) (check.Result, bool) {
	d, ok := running(ctx)
	if !ok || ctx.Err() != nil {
		return check.Result{}, false
	}
	s, ok := d.byName[name]
	if !ok {
		return check.Result{}, false
	}
	var before check.Result
	d.inLoop(func() { before, ok = d.results[name] })
	if !ok {
		return check.Result{}, false
	}
	d.sink.Emit(event.StepStarted{Time: d.now(), Step: name, Doing: "checking"})
	began := d.now()
	res := orFailed(withManual(s, d.check(d.attributed(event.WithChecking(ctx), s), s)))
	if ctx.Err() != nil {
		return check.Result{}, false
	}
	res.Done = before.Done
	d.inLoop(func() {
		d.results[name] = res
		d.sink.Emit(event.StepFinished{Time: d.now(), Step: name, Result: res, Duration: d.now().Sub(began)})
	})
	return res, true
}

// ChoiceOf is the choice of the run's step called name, for the step ctx is
// running, each of its parts run as that step's own: nil when it has none,
// or outside a run.
func ChoiceOf(ctx context.Context, name string) *Choice {
	d, ok := running(ctx)
	if !ok {
		return nil
	}
	s, ok := d.byName[name]
	if !ok || s.Choice == nil {
		return nil
	}
	c := *s.Choice
	ready := func(_ context.Context, it check.Item) check.Item { return it }
	if c.Ready != nil {
		ready = func(ctx context.Context, it check.Item) check.Item {
			return c.Ready(d.attributed(event.WithChecking(ctx), s), it)
		}
	}
	settle := func(context.Context, check.Item) string { return "" }
	if c.Settle != nil {
		settle = func(ctx context.Context, it check.Item) string {
			return c.Settle(d.attributed(event.WithChanging(ctx), s), it)
		}
	}
	return &Choice{
		Ready: ready,
		Answer: func(ctx context.Context, it check.Item, key string) (string, error) {
			return c.Answer(d.attributed(event.WithChanging(ctx), s), it, key)
		},
		Settle: settle,
	}
}

type undoneKey struct{}

// Undone tells the run that what the step ctx is running comes after wasn't
// done after all, as an App Store install finds no one signed in, which a
// step by hand before it asks for: each of the steps it's applied after that
// can be done again (Again), finished, is, in the pipeline's order, as its
// own, then checked again, as Recheck does; the step saying meanwhile that it
// waits for it, then that it's applying again. It reports whether each was
// done again and stands ok: false outside a run, with none to do again, or
// once the run's stopped.
func Undone(ctx context.Context) bool {
	if undone, ok := ctx.Value(undoneKey{}).(func(context.Context) bool); ok {
		return undone(ctx)
	}
	return false
}

// undone does again, for s, what it comes after that can be (Undone).
func (d *dispatch) undone(ctx context.Context, s Step) bool {
	if ctx.Err() != nil {
		return false
	}
	var again []Step
	d.inLoop(func() {
		for _, a := range d.steps {
			if _, done := d.results[a.Name]; done && a.Again != nil && slices.Contains(s.After, a.Name) {
				again = append(again, a)
			}
		}
	})
	if len(again) == 0 {
		return false
	}
	defer event.Say(ctx, event.Doing{Says: "applying"})
	// What's done again waits for a person, s's job given up meanwhile
	// (Release): s takes it back to go on once each is checked again, so
	// that its card's down first, whatever s waits for a job.
	defer jobOf(ctx).back()
	for _, a := range again {
		event.Say(ctx, event.Doing{Says: "waiting for " + a.asks()})
		if err := a.Again(d.attributed(event.WithChanging(ctx), a)); err != nil {
			return false
		}
		if res, ok := Recheck(ctx, a.Name); !ok || res.State != check.OK {
			return false
		}
	}
	return true
}

type awaitKey struct{}

// Await waits, partway through applying the step ctx is running, for w, the
// step saying says meanwhile: till w's thing is in place, w's step has
// finished, or takes this one as done, or the run's stopped. It reports
// whether w came: a step waited for itself comes once it's finished. w is
// one of its Later, or a thing of a step whose lead-up has no step needing
// this one: New refuses Later that wait on what can't finish till the step
// has, but sees no other wait. Outside a run, it waits for nothing.
func Await(ctx context.Context, w Wait, says string) bool {
	if await, ok := ctx.Value(awaitKey{}).(func(context.Context, Wait, string) bool); ok {
		return await(ctx, w, says)
	}
	return w.Ready == nil || w.there(ctx)
}

type jobKey struct{}

// Release gives up the job of the step ctx is running while it waits for a
// person, as a step by hand waits for its card's turn and for what it asks
// to be done: the run starts others meanwhile. It returns what takes a job
// back, waiting for one before any step not yet started, to be called
// before the step goes on with more work; a step that finishes without
// taking it back finishes without waiting for a job, as one whose card is
// done has nothing more to do. Given up already, it does nothing, and
// neither does what it returns. Outside a run, it does nothing.
func Release(ctx context.Context) (back func()) {
	return jobOf(ctx).release()
}

// jobOf is the job of the step ctx is running: nil outside a run, which
// has none to give up.
func jobOf(ctx context.Context) *job {
	j, _ := ctx.Value(jobKey{}).(*job)
	return j
}

// job is a step's job as it runs: away while it's given up for the step to
// wait for a person (Release).
type job struct {
	d    *dispatch
	step string
	mu   sync.Mutex
	away bool
}

// release gives the job up, as Release does, and returns what takes it
// back: the step is away meanwhile, so the run doesn't go quiet while it
// waits, as a person may put in place what others wait for.
func (j *job) release() (back func()) {
	if j == nil {
		return func() {}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.away {
		return func() {}
	}
	j.away = true
	j.d.mu.Lock()
	j.d.away++
	j.d.mu.Unlock()
	j.d.parked <- parking{step: j.step}
	return j.back
}

// back takes the job back, when it's away: asking for it, the step's
// queued, and no longer away.
func (j *job) back() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.away {
		return
	}
	granted := make(chan struct{})
	j.d.resumed <- granted
	j.d.mu.Lock()
	j.d.away--
	j.d.mu.Unlock()
	<-granted
	j.away = false
}

// isAway reports whether the job's given up.
func (j *job) isAway() bool {
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.away
}

type puttingKey struct{}

// Putting tells the run the step ctx is running is putting things in place,
// not there yet whatever a look finds, as a cask with an installer is
// listed installed once it's staged, before its installer's done: a wait
// for one of them isn't over till the step says it's in place (Put), stops
// putting them (the func it returns), or finishes. Things are named as its
// kind names them: a wait's thing is one by its name in full, or its last
// part, as cask:tool is owner/tap/tool. Outside a run, it does nothing.
func Putting(ctx context.Context, things []string) (stop func()) {
	if putting, ok := ctx.Value(puttingKey{}).(func([]string) func()); ok {
		return putting(things)
	}
	return func() {}
}

type putKey struct{}

// Put tells the run the step ctx is running has put thing in place: no
// longer one it's putting (Putting), and the steps waiting for its things
// look again at once, as Changed has them. Outside a run, it does nothing.
func Put(ctx context.Context, thing string) {
	if put, ok := ctx.Value(putKey{}).(func(string)); ok {
		put(thing)
	}
}

// putIn notes things step is putting in place, and returns what notes it
// putting them no more.
func (d *dispatch) putIn(step string, things []string) (stop func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.putting[step] == nil {
		d.putting[step] = map[string]bool{}
	}
	for _, t := range things {
		d.putting[step][t] = true
	}
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, t := range things {
			delete(d.putting[step], t)
		}
		d.wake(step)
	}
}

// put notes thing put in place by step, waking the steps waiting for its
// things.
func (d *dispatch) put(step, thing string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.putting[step], thing)
	d.wake(step)
}

// isPutting reports whether step is putting thing in place, by its name in
// full or its last part.
func (d *dispatch) isPutting(step, thing string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	last := func(name string) string { return name[strings.LastIndex(name, "/")+1:] }
	for t := range d.putting[step] {
		if t == thing || last(t) == last(thing) {
			return true
		}
	}
	return false
}

type changedKey struct{}

// Changed tells the run the step ctx is running may have put one of its
// things in place, as an install does as it counts one done, and as it
// ends: the steps waiting for its things, and for things no step in
// particular puts in place, look again at once, rather than at their next
// look. Outside a run, it does nothing.
func Changed(ctx context.Context) {
	if changed, ok := ctx.Value(changedKey{}).(func()); ok {
		changed()
	}
}

// stopped is the result of a step the run was stopped before, or during.
func stopped() check.Result {
	return check.Result{State: check.Failed, Reason: event.ErrStopped.Error()}
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
		if _, done := d.results[after]; taken && !done && !d.isFreed(s.Name, after) {
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
	var missing string
	if d.apply {
		missing = d.waitFor(ctx, s)
	}
	d.sink.Emit(event.StepStarted{Time: d.now(), Step: s.Name, Doing: "checking"})
	res := d.check(ctx, s)
	if missing != "" && res.State != check.OK {
		return check.Result{State: check.Deferred, Reason: missing}
	}
	if d.apply && missing == "" && res.State != check.Deferred && (res.State != check.OK || res.Actions() || s.AlwaysApply) && s.Apply != nil && ctx.Err() == nil {
		d.sink.Emit(event.StepStarted{Time: d.now(), Step: s.Name, Doing: "applying"})
		before := res
		if err := applySafely(event.WithChanging(ctx), s, res); err != nil {
			if IsNotYet(err) {
				return check.Result{State: check.Deferred, Reason: err.Error()}
			}
			return check.Result{State: check.Failed, Reason: Reason(err), Phase: check.Applying}
		}
		res = d.check(ctx, s)
		res.Done = done(before, res)
	}
	return withManual(s, res)
}

// withManual is res, s's check's finding, with what a person must do by
// hand an item while s needs attention.
func withManual(s Step, res check.Result) check.Result {
	if res.State == check.Attention && s.Manual != "" {
		res.Items = append(res.Items, check.Item{ID: s.Name + ":manual", Name: s.Manual, State: "manual"})
	}
	return res
}

// waitFor waits for each of the things s is applied after, as await does,
// saying, meanwhile, what it's waiting for. It returns what s needs of
// those that didn't come, unless the run was stopped: "" when all came.
func (d *dispatch) waitFor(ctx context.Context, s Step) string {
	var installed, there []string
	for _, w := range s.Waits {
		came := d.await(ctx, s, w, func() {
			d.sink.Emit(event.StepStarted{Time: d.now(), Step: s.Name, Doing: "waiting for " + w.Thing})
		})
		switch {
		case came || ctx.Err() != nil:
		case w.Step == "":
			there = append(there, w.Thing)
		default:
			installed = append(installed, w.Thing)
		}
	}
	var needs []string
	if len(installed) > 0 {
		which := ", which isn't installed"
		if len(installed) > 1 {
			which = ", which aren't installed"
		}
		needs = append(needs, "needs "+strings.Join(installed, " and ")+which)
	}
	if len(there) > 0 {
		needs = append(needs, NotThere(there))
	}
	return strings.Join(needs, "; ")
}

// await waits, for s, for w: till its thing's in place, its step has
// finished, or has taken s as done (free), or, a thing of no step, till the
// run's gone quiet (hush); or till the run's stopped; saying so, as waiting
// does, once it starts. It looks again each time a step that could put the
// thing in place says it may have (w's step, or any, for a thing of none),
// or finishes, and every waitPoll. Meanwhile s's job is another's, and the
// steps w's step leads up to take s as done. It reports whether w came: a
// step waited for itself, once it's finished, or has taken s as done. A
// thing of s's own, or of a step the run doesn't take, isn't waited for.
func (d *dispatch) await(ctx context.Context, s Step, w Wait, waiting func()) bool {
	if _, taken := d.titles[w.Step]; w.Step == s.Name || w.Step != "" && !taken {
		return w.Ready == nil || w.there(ctx)
	}
	// Watched before the first look, so nothing that comes between a look
	// and the wait after it is missed.
	lk := d.watch(w.Step)
	defer d.unwatch(lk)
	if came, settled := d.settled(ctx, s, w, lk); settled {
		return came
	}
	waiting()
	// A step away already, waiting for a person, has no job to give up.
	away := jobOf(ctx).isAway()
	d.parked <- parking{step: s.Name, on: w.Step, away: away}
	if !away {
		defer d.resume()
	}
	tick := time.NewTicker(waitPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-lk.wake:
		case <-tick.C:
		}
		d.looking(lk)
		if came, settled := d.settled(ctx, s, w, lk); settled {
			return came
		}
		d.idle(lk)
	}
}

// settled reports whether s's wait for w, watched by lk, is over, and
// whether w came: once w's step has finished, w came if its thing's in
// place, or it's the step itself; before, once its thing's in place and its
// step isn't saying it's still putting it there (Putting), or
// once its step takes s as done, as it waits for what s leads up to, when
// only the step itself has come. A wait for a thing of no step is over once
// it's in place, or the run's gone quiet without it.
func (d *dispatch) settled(ctx context.Context, s Step, w Wait, lk *look) (came, over bool) {
	if w.Step == "" {
		ready := w.there(ctx)
		return ready, ready || d.isQuiet(lk)
	}
	select {
	case <-d.over[w.Step]:
		return w.Ready == nil || w.there(ctx), true
	default:
	}
	switch {
	// Asked again after the look: a step starts putting a thing in place
	// (Putting) before a look can find it, so a look finding it between a
	// first ask and the step starting isn't taken as it being in place.
	// The first spares the look while it's being put.
	case w.Ready != nil && !d.isPutting(w.Step, w.Thing) && w.there(ctx) && !d.isPutting(w.Step, w.Thing):
		return true, true
	case d.isFreed(s.Name, w.Step):
		return w.Ready == nil, true
	}
	return false, false
}

// resume waits for a job to go on with, once a step's done waiting.
func (d *dispatch) resume() {
	granted := make(chan struct{})
	d.resumed <- granted
	<-granted
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
	res = orFailed(res)
	d.results[s.Name] = res
	d.sink.Emit(event.StepFinished{Time: d.now(), Step: s.Name, Result: res, Duration: took})
	close(d.over[s.Name])
	d.poke(s.Name)
}

// orFailed is res, or, when it says nothing of how its step stands, that
// its check failed for that.
func orFailed(res check.Result) check.Result {
	if res.State == "" {
		return check.Result{State: check.Failed, Reason: "the check said nothing of how the step stands", Phase: check.Checking}
	}
	return res
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
// on. A check that fails, failed checking.
func (d *dispatch) check(ctx context.Context, s Step) check.Result {
	res := d.checkWithin(ctx, s)
	if res.State == check.Failed {
		res.Phase = check.Checking
	}
	return res
}

// checkWithin is check, before the phase it failed in is said.
func (d *dispatch) checkWithin(ctx context.Context, s Step) check.Result {
	for try := 0; ; try++ {
		within, cancel := hold.WithTimeout(ctx, d.within)
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
