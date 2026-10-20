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
func (b byHand) card(context.Context) event.NeedsYou {
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

// raised is what a card is raised for, and how to tell it's done: a step by
// hand's line, or one of kit's own to-dos. card is what its card says, but
// its step and how many follow it; done whether it's done, and what kit saw,
// in words, if it has some; answer what the person's answer does, at now,
// and likewise.
type raised interface {
	card(ctx context.Context) event.NeedsYou
	done(ctx context.Context) (bool, string)
	answer(ctx context.Context, a hand.Answer, now time.Time) (bool, string, error)
}

// opener is a raised card that opens something as it comes up: a step by
// hand of kit's own, its app.
type opener interface {
	open(ctx context.Context)
}

// errStopHere is why a card came down without being done: the person
// stopped where they were, at the run's to-dos, what's left staying to do.
var errStopHere = errors.New("stopped here")

// Raise is how a step by hand that's come due is raised while kit applies
// at a terminal, where someone can act on it: a card, front and centre, one
// up at a time, with the steps to do it; and a notification, in case
// they're away from it. What they answer the card with comes back on Desk.
type Raise struct {
	Sink   event.Sink
	Now    func() time.Time
	Notify Notifier
	Desk   *hand.Desk

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

// Notifier posts notifications, each under an id, replacing any posted
// under it before, and takes them away.
type Notifier interface {
	Notify(ctx context.Context, id, title, body string)
	Clear(ctx context.Context, id string)
}

// manualPoll is how often a raised step by hand's command is run, to see
// whether it's done.
var manualPoll = 3 * time.Second

// up puts c's card up for the step called step, once it's the card's turn,
// unless it's been done meanwhile, after more of the step's own cards to
// follow it; then waits till it's done, the step's job given up meanwhile,
// for other steps to go on (engine.Release), looking every few seconds as
// well as at each answer. While kit does what a choice's key says, the card
// offers none of its keys, and offers them again should it still be to do,
// any answer that came meanwhile dropped: a key the screen passed on before
// it showed the card being answered.
// It returns what kit saw as it was done, in words, if it has some;
// errStopHere when the person stopped where they were.
func (r *Raise) up(ctx context.Context, step string, c raised, more int) (string, error) {
	defer engine.Release(ctx)()
	r.Sink.Emit(event.Doing{Time: r.Now(), Step: step, Says: event.Queued})
	if err := r.turn(ctx); err != nil {
		return "", err
	}
	defer r.pass()
	if ok, saw := c.done(ctx); ok {
		return saw, nil
	}
	answers, down := r.Desk.Listen(step)
	defer down()
	card := c.card(ctx)
	r.show(ctx, step, card, more)
	defer r.unnotify(ctx, step, card.Item)
	if o, ok := c.(opener); ok {
		o.open(ctx)
	}
	// Looking every few seconds finds it done by its command or its check,
	// or marked done elsewhere, as by kit manual done in another window.
	tick := time.NewTicker(manualPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
			if ok, saw := c.done(ctx); ok {
				return saw, nil
			}
		case a := <-answers:
			if a == hand.Stop {
				return "", errStopHere
			}
			chose := slices.ContainsFunc(card.Keys, func(k check.Key) bool { return hand.Answer(k.Key) == a })
			if chose {
				r.answering(string(a))
			}
			if ok, saw, err := c.answer(ctx, a, r.Now()); ok || err != nil {
				return saw, err
			}
			if chose {
				if stoppedMeanwhile(answers) {
					return "", errStopHere
				}
				r.answering("")
			}
		}
	}
}

// stoppedMeanwhile empties answers of what came while a key was being done,
// without waiting, and reports whether the person stopped where they were
// meanwhile, which isn't dropped.
func stoppedMeanwhile(answers <-chan hand.Answer) bool {
	stop := false
	for {
		select {
		case a := <-answers:
			stop = stop || a == hand.Stop
		default:
			return stop
		}
	}
}

// answering puts the card up again, answered by key while kit does what it
// says, or, "", waiting for an answer again.
func (r *Raise) answering(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.card.Time, r.card.Answering = r.Now(), key
	r.Sink.Emit(r.card)
}

// show puts card up for step, a notification posted once it's up; its
// More counting more of the step's own cards to follow it, and those
// waiting for their turn.
func (r *Raise) show(ctx context.Context, step string, card event.NeedsYou, more int) {
	card.Step = step
	r.mu.Lock()
	card.Time, card.More = r.Now(), more+len(r.queued)
	r.card, r.after, r.shown = card, more, true
	r.Sink.Emit(card)
	r.mu.Unlock()
	if r.Notify != nil {
		r.Notify.Notify(ctx, noticeID(step, card.Item), card.Title, notice(card))
	}
}

// unnotify takes the notification of item's card away, once it's down:
// done, or the run stopped.
func (r *Raise) unnotify(ctx context.Context, step, item string) {
	if r.Notify != nil {
		r.Notify.Clear(context.WithoutCancel(ctx), noticeID(step, item))
	}
}

// noticeID is the id of the notification for the card of item, of step.
func noticeID(step, item string) string {
	return "card:" + step + ":" + item
}

// notice is what a card's notification says under its title: its first
// step, else that kit waits for it.
func notice(card event.NeedsYou) string {
	if len(card.Steps) > 0 {
		return card.Steps[0]
	}
	return "kit waits for you to do this"
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
			if _, err := raise.up(ctx, m.Name, b, 0); !errors.Is(err, errStopHere) {
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
	_, err := raise.up(ctx, b.Name, b, 0)
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
