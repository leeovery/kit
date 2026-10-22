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

// ManualStep is a [manual] line: what to do; why, as the notes above it
// say; what it comes after (a step, or one of a step's things, as in
// cask:firefox) and the steps it comes before; the group it's in, if any;
// the command saying whether it's done, if it has one; and the steps to do
// it, as the lines indented under it say. Or it's one of kit's own, as a
// kind's card is (kind.Card): told done as Done says, and opening its app
// as its card first comes up, which a [manual] line never does.
type ManualStep struct {
	Name, Do, Why string
	After, Before []string
	Group         string
	Command       *runner.Command
	Steps         []string
	// Pos is where its line is, as in laptop/declarations:12.
	Pos string
	// Done reports whether it's done, asking no one, for one of kit's own;
	// nil for a [manual] line.
	Done func(ctx context.Context) bool
	// Open is the app, by bundle id, kit opens as its card first comes up:
	// "" for none, as for every [manual] line.
	Open string
	// Await are what one of kit's own waits for once it's found not done,
	// before its card goes up, as a store's sign-in waits for the store's
	// program and app: nothing to wait for while it's done.
	Await []engine.Wait
}

// Ordered reports whether the line says a step comes after it (--before),
// as one waits on it: a step of its own, then, its card up mid-run, rather
// than one of the rest, at the end. --after alone only orders it, and the
// end is after everything.
func (m ManualStep) Ordered() bool { return len(m.Before) > 0 }

// ParseManual reads a [manual] line's value: what to do, in quotes, then
// --after and --before, each as often as wanted, --group and the group
// it's in, and --test, then the rest of the line, the command saying
// whether it's done (exit 0: done), a word starting ~/ starting at home.
// Its why and its steps are the entry's notes and steps.
func ParseManual(home string, e config.Entry) (ManualStep, error) {
	m := ManualStep{Name: e.Name, Why: e.Notes, Steps: e.Steps, Pos: e.Pos()}
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
		case "--group":
			if m.Group, err = groupOf(m.Group, words, i); err != nil {
				return m, err
			}
			i++
		case "--open":
			return m, errors.New(`--open is gone: a step by hand of yours opens nothing, so say what to open in a step, a line indented under it, as in "Open Firefox"`)
		case "--test":
			if cmd = words[i+1:]; len(cmd) == 0 {
				return m, errors.New("--test needs the command saying whether it's done after it")
			}
		case "--":
			return m, errors.New("the command saying whether it's done comes after --test now")
		default:
			return m, fmt.Errorf("%q isn't one of a step by hand's options: --after, --before, --group and --test", w)
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

// groupOf is the group the --group at words[i] names, one word, quoted
// when it has spaces; had is what a --group before it named, as a step by
// hand is in one group.
func groupOf(had string, words []string, i int) (string, error) {
	switch {
	case had != "":
		return "", errors.New("--group is there twice: a step by hand is in one group")
	case i+1 == len(words) || strings.TrimSpace(words[i+1]) == "" || strings.HasPrefix(words[i+1], "--"):
		return "", errors.New(`--group needs the group's name after it, as in --group "First things"`)
	}
	return words[i+1], nil
}

// hand is the step by hand m is, as the step raising it lists it.
func (m ManualStep) hand() event.Hand {
	return event.Hand{Name: m.Name, Title: m.Do, Group: m.Group, Why: m.Why}
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
// where to look to tell it's done; and, for one opening its app, whether
// it's opened it yet.
type byHand struct {
	ManualStep
	run      runner.Runner
	stateDir string
	opened   *sync.Once
}

// watched reports whether kit tells by itself when it's done.
func (b byHand) watched() bool { return b.Command != nil || b.Done != nil }

// isDone reports whether it's done: as Done says, or its command exits 0,
// or, without either, it's been marked done on this Mac.
func (b byHand) isDone(ctx context.Context) bool {
	if b.Done != nil {
		return b.Done(ctx)
	}
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
	if !b.watched() {
		it.Detail = "then: kit manual done " + b.Name
	}
	return it
}

// card is what its card says: its line's name, what to do, its group, why,
// its steps, and whether kit tells by itself when it's done.
func (b byHand) card() event.NeedsYou {
	return event.NeedsYou{Item: b.Name, Title: b.Do, Group: b.Group, Why: b.Why, Steps: b.Steps, Tells: b.watched()}
}

// open opens its app, once, as its card comes up: nothing for one with
// none. What opening says, the run's log has.
func (b byHand) open(ctx context.Context) {
	if b.Open == "" || b.opened == nil {
		return
	}
	b.opened.Do(func() {
		_, _ = b.run.Run(ctx, runner.Command{Name: "open", Args: []string{"-b", b.Open}})
	})
}

// done reports whether it's done, as isDone does; kit has no words for it.
func (b byHand) done(ctx context.Context) (bool, string) { return b.isDone(ctx), "" }

// answer is what the person's answer does: done, with no test, marks it
// done on this Mac, as kit manual done does; with one, has kit look at once,
// as it does every few seconds anyway.
func (b byHand) answer(ctx context.Context, a hand.Answer, now time.Time) (bool, string, error) {
	switch {
	case a != hand.Done:
		return false, "", nil
	case !b.watched():
		return true, "", MarkDone(b.stateDir, []string{b.Name}, now, false)
	}
	return b.isDone(ctx), "", nil
}

// entry is it as the list of to-dos at the run's end has it.
func (b byHand) entry() event.ToDo {
	it := b.item()
	return event.ToDo{Step: ManualName, ID: it.ID, Title: b.Do, Group: b.Group, How: howOf(b.Steps, it.Detail)}
}

// howOf is how to do a to-do, as the record says: its steps, else what its
// item says, if anything.
func howOf(steps []string, detail string) []string {
	switch {
	case len(steps) > 0:
		return steps
	case detail != "":
		return []string{detail}
	}
	return nil
}

// OwnManual is a step by hand a step comes after (Ordered), as a step of
// its own: done as its Done says, when its command exits 0, or once marked
// done. Applying it with raise raises it, opening its app the first time,
// and waits till it's done; and so does doing it again, should a step it
// comes before find it wasn't done after all (again). Without raise, with
// no one to act, applying leaves it as it stands, and it can't be done
// again.
func OwnManual(run runner.Runner, stateDir string, m ManualStep, raise *Raise) engine.Step {
	b := byHand{ManualStep: m, run: run, stateDir: stateDir, opened: &sync.Once{}}
	step := engine.Step{
		Name: m.Name, Title: m.Name, Area: AreaManual, ByHand: []event.Hand{m.hand()},
		Check: func(ctx context.Context) check.Result {
			if b.isDone(ctx) {
				return check.Result{State: check.OK, Summary: "done"}
			}
			return check.Result{State: check.Attention, Summary: "not done", Items: []check.Item{b.item()}}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if raise == nil {
				return nil
			}
			if err := awaitAll(ctx, m.Await); err != nil {
				return err
			}
			if _, _, err := raise.up(ctx, m.Name, b, midRun(ctx, m.Name)); !errors.Is(err, errStopHere) {
				return err
			}
			return nil
		},
	}
	if raise != nil {
		step.Again = func(ctx context.Context) error { return b.again(ctx, raise) }
	}
	return step
}

// awaitAll waits, partway through applying, for each of waits in turn: not
// yet, saying what didn't come, when one doesn't.
func awaitAll(ctx context.Context, waits []engine.Wait) error {
	var missing []string
	for _, w := range waits {
		if engine.Await(ctx, w, "waiting for "+w.Thing) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		missing = append(missing, w.Thing)
	}
	if len(missing) > 0 {
		return engine.NotYet(engine.NotThere(missing))
	}
	return nil
}

// again raises it once more, as a step it comes before found it wasn't done
// after all (engine.Undone), and waits till it's done: marked done no more,
// as kit manual done --undo unmarks it, else it'd be found done before its
// card went up. One whose test still passes has nothing to raise.
func (b byHand) again(ctx context.Context, raise *Raise) error {
	switch {
	case !b.watched():
		if err := MarkDone(b.stateDir, []string{b.Name}, time.Time{}, true); err != nil {
			return err
		}
	case b.isDone(ctx):
		return fmt.Errorf("%s's test says it's done", b.Name)
	}
	_, _, err := raise.up(ctx, b.Name, b, midRun(ctx, b.Name))
	return err
}

// Manual checks what a person must do by hand, as [manual] declares it: a
// step with a command is done when it exits 0; one without, when it's been
// marked done on this Mac (kit manual done). Each step not done is an item
// saying what to do. Applied with raise, at the run's end, it works through
// the to-dos: each line not done, with what the run's other steps left for
// the person to do (an item's ToDo), one at a time in kit's order, as
// OwnManual raises its own; so it's applied however its own lines stand.
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
				if !b.isDone(ctx) {
					res.Items = append(res.Items, b.item())
				}
			}
			done := len(all) - len(res.Items)
			res.Summary = fmt.Sprintf("%d of %d done", done, len(all))
			res.Glance = res.Summary
			switch {
			case len(all) == 0:
				res.Summary, res.Glance = "none declared", ""
			case len(res.Items) == 0:
				res.Summary = fmt.Sprintf("all %d done", len(all))
				res.Glance = "all done"
			default:
				res.State = check.Attention
			}
			return res
		},
	}
	if raise == nil {
		return step
	}
	step.AlwaysApply = true
	step.Apply = func(ctx context.Context, found check.Result) error {
		// A line that doesn't read fails the step, but the rest of the run's
		// to-dos still come up.
		all, err := lines()
		due := slices.DeleteFunc(all, func(b byHand) bool {
			return !slices.ContainsFunc(found.Items, func(it check.Item) bool { return it.ID == b.item().ID })
		})
		return errors.Join(raise.workThrough(ctx, ManualName, toDoList(ctx, due)), err)
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
