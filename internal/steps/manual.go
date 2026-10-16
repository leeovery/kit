package steps

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// ManualName names the step of what a person does by hand.
const ManualName = "manual"

// manualRecord names the record of the manual steps marked done, on this
// Mac.
const manualRecord = "manual.json"

type manualDone struct {
	Done map[string]time.Time `json:"done,omitempty"`
}

// ManualStep is a [manual] line: what to do; what it comes after (a step,
// or one of a step's things, as in cask:1password) and the steps it comes
// before; the app it opens; the command saying whether it's done, if it
// has one; and the steps to do it, as the lines indented under it say.
type ManualStep struct {
	Name, Do      string
	After, Before []string
	// Open is the app it opens, by its name, as in System Settings.
	Open    string
	Command *runner.Command
	Steps   []string
	// Pos is where its line is, as in laptop/declarations:12.
	Pos string
}

// Ordered reports whether the line says a step comes after it (--before),
// as one waits on it: a step of its own, then, its card up mid-run, rather
// than one of the rest, at the end. --after alone only orders it, and the
// end is after everything.
func (m ManualStep) Ordered() bool { return len(m.Before) > 0 }

// ParseManual reads a [manual] line's value: what to do, in quotes, then
// --after and --before, each as often as wanted, --open and the app it
// opens, and --test, then the rest of the line, the command saying whether
// it's done (exit 0: done), a word starting ~/ starting at home. Its steps
// are the entry's.
func ParseManual(home string, e config.Entry) (ManualStep, error) {
	m := ManualStep{Name: e.Name, Steps: e.Steps, Pos: e.Pos()}
	words, err := config.Words(e.Value)
	if err != nil {
		return m, err
	}
	if len(words) == 0 || strings.HasPrefix(words[0], "--") {
		return m, errors.New("what to do comes after the name, in quotes")
	}
	m.Do = words[0]
	var cmd []string
	for i := 1; i < len(words) && cmd == nil; i++ {
		switch w := words[i]; w {
		case "--after", "--before":
			name, err := orderedBy(w, words, i)
			if err != nil {
				return m, err
			}
			i++
			if w == "--after" {
				m.After = append(m.After, name)
			} else {
				m.Before = append(m.Before, name)
			}
		case "--open":
			if m.Open, err = appToOpen(m.Open, words, i); err != nil {
				return m, err
			}
			i++
		case "--test":
			if cmd = words[i+1:]; len(cmd) == 0 {
				return m, errors.New("--test needs the command saying whether it's done after it")
			}
		case "--":
			return m, errors.New("the command saying whether it's done comes after --test now")
		default:
			return m, fmt.Errorf("%q isn't one of a step by hand's options: --after, --before, --open and --test", w)
		}
	}
	if cmd == nil {
		return m, nil
	}
	for i, w := range cmd {
		if rest, ok := strings.CutPrefix(w, "~/"); ok {
			cmd[i] = filepath.Join(home, rest)
		}
	}
	m.Command = &runner.Command{Name: cmd[0], Args: cmd[1:], Timeout: ownTimeout}
	return m, nil
}

// appToOpen is the app the --open at words[i] names, one word, quoted when
// it has spaces; had is what an --open before it named, as a step by hand
// opens one app.
func appToOpen(had string, words []string, i int) (string, error) {
	switch {
	case had != "":
		return "", errors.New("--open is there twice: a step by hand opens one app")
	case i+1 == len(words) || strings.HasPrefix(words[i+1], "--"):
		return "", errors.New(`--open needs the app it opens after it, as in --open "System Settings"`)
	}
	return words[i+1], nil
}

// hand is the step by hand m is, as the step raising it lists it.
func (m ManualStep) hand() event.Hand {
	return event.Hand{Name: m.Name, Title: m.Do}
}

// hands are the steps by hand list's lines are, in order: but those that
// don't read, which its step's check fails on.
func hands(home string, list config.List) []event.Hand {
	var out []event.Hand
	for _, e := range list.Entries {
		if m, err := ParseManual(home, e); err == nil {
			out = append(out, m.hand())
		}
	}
	return out
}

// SplitManual splits [manual]'s lines: those a step comes after
// (Ordered), each a step of its own, and the rest, checked together. One
// that doesn't read stays with the rest, whose check says so.
func SplitManual(home string, list config.List) (rest config.List, own []ManualStep) {
	rest = config.List{Kind: list.Kind}
	for _, e := range list.Entries {
		if m, err := ParseManual(home, e); err == nil && m.Ordered() {
			own = append(own, m)
			continue
		}
		rest.Entries = append(rest.Entries, e)
	}
	return rest, own
}

// byHand is a step by hand as its step checks and raises it: its line, and
// where to look to tell it's done.
type byHand struct {
	ManualStep
	run      runner.Runner
	stateDir string
}

// done reports whether it's done: its command exits 0, or, without one,
// it's been marked done on this Mac.
func (b byHand) done(ctx context.Context) bool {
	if b.Command != nil {
		r, err := b.run.Run(ctx, *b.Command)
		return Outcome(*b.Command, r, err) == ""
	}
	rec, err := state.Load[manualDone](b.stateDir, manualRecord)
	return err == nil && !rec.Done[b.Name].IsZero()
}

// item is the item of a check finding it not done: what to do, and, when
// kit can't tell it's done, how to say so.
func (b byHand) item() check.Item {
	it := check.Item{ID: ManualName + ":" + b.Name, Name: b.Do, State: check.ByHand}
	if b.Command == nil {
		it.Detail = "then: kit manual done " + b.Name
	}
	return it
}

// Raise is how a step by hand that's come due is raised while kit applies
// at a terminal, where someone can act on it: a card, front and centre, one
// up at a time, with the steps to do it; its app opened, and a
// notification, in case they're away from it. What they answer the card
// with comes back on Desk.
type Raise struct {
	Sink   event.Sink
	Now    func() time.Time
	Notify func(ctx context.Context, title, text string)
	// Open opens an app by its name, as open -a does.
	Open func(ctx context.Context, app string) error
	Desk *hand.Desk

	// mu guards the turn: whether a card has it (busy); the card up, once
	// it's shown, and how many of its step's cards follow it (after); and
	// the cards waiting for the turn, first come first, each given it as
	// its channel's closed.
	mu     sync.Mutex
	busy   bool
	shown  bool
	card   event.NeedsYou
	after  int
	queued []chan struct{}
}

// manualPoll is how often a raised step by hand's command is run, to see
// whether it's done.
var manualPoll = 3 * time.Second

// up puts b's card up for the step called step, once it's the card's turn,
// unless b's been done meanwhile, after more of the step's own cards to
// follow it; then waits till b's done, the step's job given up meanwhile,
// for other steps to go on (engine.Release). Done on
// the card marks done on this Mac one kit can't tell about, as kit manual
// done does, and has kit look at once at one it can, as it does every few
// seconds anyway; Open opens its app again.
func (r *Raise) up(ctx context.Context, step string, b byHand, more int) error {
	defer engine.Release(ctx)()
	r.Sink.Emit(event.Doing{Time: r.Now(), Step: step, Says: event.Queued})
	if err := r.turn(ctx); err != nil {
		return err
	}
	defer r.pass()
	if b.done(ctx) {
		return nil
	}
	answers, down := r.Desk.Listen(step)
	defer down()
	r.show(ctx, step, b, more)
	// Looking every few seconds finds it done by its command, or marked done
	// elsewhere, as by kit manual done in another window.
	tick := time.NewTicker(manualPoll)
	defer tick.Stop()
	poll := tick.C
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll:
			if b.done(ctx) {
				return nil
			}
		case a := <-answers:
			switch {
			case a == hand.Open:
				r.open(ctx, b.Open)
			case a == hand.Done && b.Command == nil:
				return MarkDone(b.stateDir, []string{b.Name}, r.Now(), false)
			case a == hand.Done && b.done(ctx):
				return nil
			}
		}
	}
}

// show puts b's card up for step: its app opened, when it has one, and a
// notification posted; its More counting more of the step's own cards to
// follow it, and those waiting for their turn.
func (r *Raise) show(ctx context.Context, step string, b byHand, more int) {
	card := event.NeedsYou{Step: step, Item: b.Name, Title: b.Do, Steps: b.Steps, Tells: b.Command != nil}
	if r.open(ctx, b.Open) {
		card.Opened = b.Open
	}
	if r.Notify != nil {
		r.Notify(ctx, b.Do, notice(b))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	card.Time, card.More = r.Now(), more+len(r.queued)
	r.card, r.after, r.shown = card, more, true
	r.Sink.Emit(card)
}

// notice is what b's notification says under its title: its first step,
// else that kit waits for it.
func notice(b byHand) string {
	if len(b.Steps) > 0 {
		return b.Steps[0]
	}
	return "kit waits for you to do this"
}

// open opens app, reporting whether it did: none, when there's no app.
func (r *Raise) open(ctx context.Context, app string) bool {
	return app != "" && r.Open != nil && r.Open(ctx, app) == nil
}

// turn waits for a card's turn, one up at a time, first come first: while
// it waits, it counts in the More of the card that's up.
func (r *Raise) turn(ctx context.Context) error {
	r.mu.Lock()
	if !r.busy {
		r.busy = true
		r.mu.Unlock()
		return nil
	}
	granted := make(chan struct{})
	r.queued = append(r.queued, granted)
	r.recount()
	r.mu.Unlock()
	select {
	case <-granted:
		return nil
	case <-ctx.Done():
	}
	r.mu.Lock()
	i := slices.Index(r.queued, granted)
	if i >= 0 {
		r.queued = slices.Delete(r.queued, i, i+1)
		r.recount()
	}
	r.mu.Unlock()
	if i < 0 {
		// Given the turn as the run was stopped: it's the next card's.
		r.pass()
	}
	return ctx.Err()
}

// pass ends the turn of the card that has it, giving it to the next
// waiting.
func (r *Raise) pass() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shown = false
	if len(r.queued) == 0 {
		r.busy = false
		return
	}
	close(r.queued[0])
	r.queued = r.queued[1:]
}

// recount puts the card that's up up again when how many follow it has
// changed: mu is held.
func (r *Raise) recount() {
	if more := r.after + len(r.queued); r.shown && more != r.card.More {
		r.card.Time, r.card.More = r.Now(), more
		r.Sink.Emit(r.card)
	}
}

// OwnManual is a step by hand a step comes after (Ordered), as a step of
// its own: done when its command exits 0, or once marked done. Applying it
// with raise raises it, and waits till it's done. Without raise, with no
// one to act, applying leaves it as it stands.
func OwnManual(run runner.Runner, stateDir string, m ManualStep, raise *Raise) engine.Step {
	b := byHand{ManualStep: m, run: run, stateDir: stateDir}
	return engine.Step{
		Name: m.Name, Title: m.Name, Area: AreaManual, ByHand: []event.Hand{m.hand()},
		Check: func(ctx context.Context) check.Result {
			if b.done(ctx) {
				return check.Result{State: check.OK, Summary: "done"}
			}
			return check.Result{State: check.Attention, Summary: "not done", Items: []check.Item{b.item()}}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if raise == nil {
				return nil
			}
			return raise.up(ctx, m.Name, b, 0)
		},
	}
}

// Notify posts a macOS notification from kit: about a step by hand that
// needs someone, what to do, and its first step.
func Notify(run runner.Runner) func(ctx context.Context, title, text string) {
	quote := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
	return func(ctx context.Context, title, text string) {
		script := "display notification " + quote(text) + " with title " + quote("kit") + " subtitle " + quote(title)
		_, _ = run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-e", script}})
	}
}

// OpenApp opens an app by its name, as open -a does, through run.
func OpenApp(run runner.Runner) func(ctx context.Context, app string) error {
	return func(ctx context.Context, app string) error {
		_, err := run.Run(ctx, runner.Command{Name: "open", Args: []string{"-a", app}})
		return err
	}
}

// Manual checks what a person must do by hand, as [manual] declares it: a
// step with a command is done when it exits 0; one without, when it's been
// marked done on this Mac (kit manual done). Each step not done is an item
// saying what to do. Applied with raise, each not done is raised in turn,
// in order, as OwnManual raises its own.
func Manual(run runner.Runner, home, stateDir string, list config.List, raise *Raise) engine.Step {
	lines := func() ([]byHand, error) {
		out := make([]byHand, 0, len(list.Entries))
		for _, e := range list.Entries {
			m, err := ParseManual(home, e)
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %w", e.Pos(), e.Name, err)
			}
			out = append(out, byHand{ManualStep: m, run: run, stateDir: stateDir})
		}
		return out, nil
	}
	step := engine.Step{
		Name: ManualName, Title: "By hand", Area: AreaManual, ByHand: hands(home, list),
		Check: func(ctx context.Context) check.Result {
			if _, err := state.Load[manualDone](stateDir, manualRecord); err != nil {
				return failed(err)
			}
			all, err := lines()
			if err != nil {
				return failed(err)
			}
			res := check.Result{State: check.OK}
			for _, b := range all {
				if !b.done(ctx) {
					res.Items = append(res.Items, b.item())
				}
			}
			done := len(all) - len(res.Items)
			res.Summary = fmt.Sprintf("%d of %d done", done, len(all))
			res.Glance = res.Summary
			if len(res.Items) == 0 {
				res.Summary = fmt.Sprintf("all %d done", len(all))
				res.Glance = "all done"
			} else {
				res.State = check.Attention
			}
			return res
		},
	}
	if raise == nil {
		return step
	}
	step.Apply = func(ctx context.Context, found check.Result) error {
		all, err := lines()
		if err != nil {
			return err
		}
		due := slices.DeleteFunc(all, func(b byHand) bool {
			return !slices.ContainsFunc(found.Items, func(it check.Item) bool { return it.ID == b.item().ID })
		})
		for i, b := range due {
			if err := raise.up(ctx, ManualName, b, len(due)-1-i); err != nil {
				return err
			}
		}
		return nil
	}
	return step
}

// MarkDone marks the manual steps named done on this Mac, at now, or not
// done, with undo.
func MarkDone(stateDir string, names []string, now time.Time, undo bool) error {
	return state.Update(stateDir, manualRecord, func(rec *manualDone) {
		if rec.Done == nil {
			rec.Done = map[string]time.Time{}
		}
		for _, name := range names {
			if undo {
				delete(rec.Done, name)
			} else {
				rec.Done[name] = now
			}
		}
	})
}
