package steps

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
)

// The groups kit's own to-dos are in.
const (
	groupApps    = "Apps"
	groupBackups = "Backups"
)

// toDo is one of the to-dos at the run's end: what the list, and the
// record, say of it, and how it's raised.
type toDo interface {
	raised
	entry() event.ToDo
}

// toDoList is what's left for the person at the Mac to do as the run ends,
// in kit's order: kit's own that come first (as apps' settings do, so the
// apps opened next start with them), then due, the config's lines not
// done, then the rest of kit's own, each from a step that's finished, in
// the run's order. Then grouped: each group where its first comes, those
// in it in that order, one with no group as a group of its own.
func toDoList(ctx context.Context, due []byHand) []toDo {
	var first, last []toDo
	for _, f := range engine.Finished(ctx) {
		for _, it := range f.Result.Items {
			switch {
			case it.ToDo == nil:
			case it.ToDo.First:
				first = append(first, ownOf(ctx, f.Step, it))
			default:
				last = append(last, ownOf(ctx, f.Step, it))
			}
		}
	}
	lines := make([]toDo, len(due))
	for i, b := range due {
		lines[i] = lineToDo{b}
	}
	return grouped(slices.Concat(first, lines, last))
}

// lineToDo is a [manual] line as one of the to-dos at the run's end: its
// card known by the id the list knows it by, as kit's own to-dos' are, so
// the list's changes and its card name the same to-do.
type lineToDo struct{ byHand }

func (l lineToDo) card() event.NeedsYou {
	c := l.byHand.card()
	c.Item = l.item().ID
	return c
}

// grouped is list by group, each group where its first comes, those in it
// in list's order.
func grouped(list []toDo) []toDo {
	var groups []string
	in := map[string][]toDo{}
	for _, t := range list {
		g := t.entry().Group
		if _, ok := in[g]; !ok {
			groups = append(groups, g)
		}
		in[g] = append(in[g], t)
	}
	out := make([]toDo, 0, len(list))
	for _, g := range groups {
		out = append(out, in[g]...)
	}
	return out
}

// ownToDo is one of kit's own to-dos: the item a step's check found for
// the person to do (its ToDo), and the step, which tells when it's done.
type ownToDo struct {
	step string
	item check.Item
}

// ownOf is the to-do of it, found by step's check: a choice whose step
// readies it when it has keys and its step readies what they say, else
// one kit watches, or a choice as it stands.
func ownOf(ctx context.Context, step string, it check.Item) toDo {
	o := ownToDo{step: step, item: it}
	if c := engine.ChoiceOf(ctx, step); len(it.ToDo.Keys) > 0 && c != nil && c.Ready != nil {
		return choiceToDo{o}
	}
	return o
}

// owned is one of kit's own to-dos, a choice or not.
type owned interface{ own() ownToDo }

func (o ownToDo) own() ownToDo { return o }

// choiceToDo is one of kit's own to-dos that's a choice its step readies,
// its card readied as its turn comes, as the date of the save a key brings
// back.
type choiceToDo struct{ ownToDo }

func (o ownToDo) entry() event.ToDo {
	t := o.item.ToDo
	return event.ToDo{Step: o.step, ID: o.item.ID, Title: t.Title, Group: t.Group, How: howOf(t.Steps, o.item.Detail)}
}

// card is what its card says, as its item has it: kit telling by itself
// when it's done, but for a choice, whose keys say.
func (o ownToDo) card() event.NeedsYou { return cardOf(o.item) }

// ready is what its card says once its step's readied it.
func (c choiceToDo) ready(ctx context.Context) event.NeedsYou {
	return cardOf(engine.ChoiceOf(ctx, c.step).Ready(ctx, c.item))
}

// cardOf is what the card of it, a to-do of kit's own, says.
func cardOf(it check.Item) event.NeedsYou {
	t := it.ToDo
	return event.NeedsYou{Item: it.ID, Title: t.Title, Group: t.Group, Why: t.Why, Steps: t.Steps, Watch: t.Watch, Keys: t.Keys, Tells: len(t.Keys) == 0}
}

// done reports whether it's done, as its step's check finds it: the check
// looked, and found it gone; and, as what kit saw, what the check says.
// What it waited for is finished after, its card down (finishing).
func (o ownToDo) done(ctx context.Context) (bool, string) {
	res, ok := engine.Look(ctx, o.step)
	switch {
	case !ok, res.State == check.Failed, res.State == check.Deferred:
		return false, ""
	case slices.ContainsFunc(res.Items, func(it check.Item) bool { return it.ID == o.item.ID }):
		return false, ""
	}
	return true, res.Summary
}

// finishing is what finishes what t waited for, once it's found done, as
// bringing back the settings of the apps it had the person open, saying
// what it did, if anything (engine.Choice's Settle): nil when there's
// nothing to finish, as for a to-do that isn't kit's own. It's never cut
// short, as by the run stopping: what t waited for would be half done.
func finishing(ctx context.Context, t toDo) func() string {
	o, ok := t.(owned)
	if !ok {
		return nil
	}
	own := o.own()
	c := engine.ChoiceOf(ctx, own.step)
	if c == nil || c.Settle == nil {
		return nil
	}
	return func() string { return c.Settle(context.WithoutCancel(ctx), own.item) }
}

// followUps are the to-dos t's step's check finds now, as bringing back
// apps' settings can leave apps to open: none for one that isn't kit's
// own. Those listed already, the list leaves (take).
func followUps(ctx context.Context, t toDo) []toDo {
	o, ok := t.(owned)
	if !ok {
		return nil
	}
	step := o.own().step
	res, ok := engine.Look(ctx, step)
	if !ok {
		return nil
	}
	var more []toDo
	for _, it := range res.Items {
		if it.ToDo != nil {
			more = append(more, ownOf(ctx, step, it))
		}
	}
	return more
}

// answer is what the person's answer does: a choice's key does what its
// step says, then kit looks, taking what the step said it did as what it
// saw, should that have done it; what goes wrong, the log says, and it
// stays up. Done has kit look at once. What a key does is never cut short,
// as by the run stopping: bringing apps' settings back half way would be
// worse than not at all.
func (o ownToDo) answer(ctx context.Context, a hand.Answer, _ time.Time) (bool, string, error) {
	if !slices.ContainsFunc(o.item.ToDo.Keys, func(k check.Key) bool { return hand.Answer(k.Key) == a }) {
		ok, saw := o.done(ctx)
		return ok, saw, nil
	}
	c := engine.ChoiceOf(ctx, o.step)
	if c == nil {
		return false, "", nil
	}
	said, err := c.Answer(context.WithoutCancel(ctx), o.item, string(a))
	if err != nil {
		event.Log(ctx, event.Logged{Line: o.item.ToDo.Title + ": " + err.Error(), Alert: true})
		return false, "", nil
	}
	ok, _ := o.done(ctx)
	return ok, said, nil
}

// workThrough raises list's to-dos for step one at a time, in order, each
// till it's done or the person leaves it for later (hand.Skip), the next
// coming up at once: the list emitted as each comes up, as each is done,
// and once they're over. One left for later stays to do, settling while
// what was under way as they left it finishes, which may find it done
// after all (leave). Kit finishes what one done waited for (settling), one
// finish at a time, and, one of kit's own, looks for those its step's
// check finds now, to come next (followUps), aside, the list emitted again
// as each comes in. The person can stop where they are (hand.Stop), the
// list emitted stopped at once, whatever's under way: what's left stays to
// do, the one up done if it was done as they stopped, or settling, its
// work left under way to finish and say whether it found it done, which
// the list is emitted again to say, with the to-dos it leaves. Their
// answers come on one listener for the whole list, so a Stop given
// between two to-dos is never lost, and anything else given then, for a
// to-do done with, goes nowhere. Then the steps the to-dos done held are
// checked again (recheckHeld). The run stopped, the list's emitted
// stopped, and nothing more's done.
func (r *Raise) workThrough(ctx context.Context, step string, list []toDo) error {
	if len(list) == 0 {
		return nil
	}
	answers, down := r.Desk.Listen(step)
	defer down()
	l := &toDos{r: r, step: step, list: list, entries: make([]event.ToDo, len(list))}
	for i, t := range list {
		l.entries[i] = t.entry()
	}
	settling, err := l.through(ctx, answers)
	l.emit(-1)
	l.awaitLater(ctx)
	if err != nil {
		return err
	}
	if settling != nil {
		if _, err := l.between(ctx, -1, nil, settling); err != nil {
			return err
		}
	}
	l.mu.Lock()
	entries := slices.Clone(l.entries)
	l.mu.Unlock()
	recheckHeld(ctx, entries)
	return nil
}

// toDos are the to-dos at the run's end as they're worked through: each as
// raised, and as the list says it, and which is up, which what's left for
// later changes as it comes (mu); one finish at a time (finishes), as two
// restores of apps' settings would clash; and the work of those left for
// later still under way (later).
type toDos struct {
	r    *Raise
	step string

	mu      sync.Mutex
	list    []toDo
	entries []event.ToDo
	up      int

	finishes sync.Mutex
	later    sync.WaitGroup
}

// through raises the to-dos in turn, till they're over or the person stops
// there, returning what the one up then is still settling to, if anything.
// One done, the list says so at once, its card down, settling or not.
func (l *toDos) through(ctx context.Context, answers <-chan hand.Answer) (<-chan settled, error) {
	for i := 0; ; i++ {
		t, after, ok := l.next(i)
		if !ok || stoppedMeanwhile(answers) {
			return nil, nil
		}
		saw, late, err := l.r.up(ctx, l.step, t, seat{held: true, after: after, answers: answers})
		skipped := errors.Is(err, errSkipped)
		if err != nil && !skipped && !errors.Is(err, errStopHere) {
			return nil, err
		}
		finish := l.finishing(ctx, t)
		if skipped {
			l.leave(ctx, i, t, late, finish)
			continue
		}
		l.mu.Lock()
		if late == nil {
			l.entries[i].Done, l.entries[i].Saw = true, saw
		}
		l.entries[i].Settling = late != nil || finish != nil
		l.mu.Unlock()
		settling := l.settle(ctx, i, t, late, finish)
		if err != nil {
			return settling, nil
		}
		l.emit(i)
		if stopped, err := l.between(ctx, i, answers, settling); stopped || err != nil {
			return settling, err
		}
	}
}

// next is the to-do at i, up now, the list emitted so, and how many come
// after it; none once they're over.
func (l *toDos) next(i int) (t toDo, after int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i >= len(l.list) {
		return nil, 0, false
	}
	l.up = i
	l.emitNow()
	return l.list[i], len(l.list) - 1 - i, true
}

// emit says the list as it stands, the one at up up: -1 for none, once
// they're over or the person stopped.
func (l *toDos) emit(up int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.up = up
	l.emitNow()
}

// emitNow says the list as it stands; l.mu held.
func (l *toDos) emitNow() {
	l.r.Sink.Emit(event.ToDos{Time: l.r.Now(), Step: l.step, List: slices.Clone(l.entries), Up: l.up})
}

// finishing is what finishes what t waited for (finishing), once the
// finish under way, if any, is over.
func (l *toDos) finishing(ctx context.Context, t toDo) func() string {
	finish := finishing(ctx, t)
	if finish == nil {
		return nil
	}
	return func() string {
		l.finishes.Lock()
		defer l.finishes.Unlock()
		return finish()
	}
}

// settled is what the to-do at i came to, its card down: whether it's
// done, and what kit saw, if it has words; or the to-dos its step's check
// found once it was done, to follow it (more).
type settled struct {
	i    int
	done bool
	saw  string
	more []toDo
}

// settle is what the to-do t, at i, comes to, aside, its card down, closed
// once all's come: first, whether it's done, as it was, or, left with work
// under way (late), as that finds it, and what finishing what it waited for
// said (finish, if any); then, done, the to-dos its step's check finds, if
// any.
func (l *toDos) settle(ctx context.Context, i int, t toDo, late *work, finish func() string) <-chan settled {
	out := make(chan settled, 2)
	l.r.aside.Go(func() {
		defer close(out)
		s := settled{i: i, done: late == nil}
		if late != nil {
			s.saw, s.done = late.outcome(ctx)
		}
		if s.done && finish != nil {
			s.saw = cmp.Or(finish(), s.saw)
		}
		out <- s
		if !s.done {
			return
		}
		if more := followUps(ctx, t); len(more) > 0 {
			out <- settled{i: i, done: true, more: more}
		}
	})
	return out
}

// leave leaves the to-do t, at i, for later, its work under way (late): it
// stays to do, skipped, settling till that's over; found done after all,
// it's done, finished as one done is (finish), with the to-dos to follow
// it at the list's end, the list having moved on; each change emitted as
// it comes.
func (l *toDos) leave(ctx context.Context, i int, t toDo, late *work, finish func() string) {
	l.mu.Lock()
	l.entries[i].Skipped, l.entries[i].Settling = true, true
	l.mu.Unlock()
	settling := l.settle(ctx, i, t, late, finish)
	l.later.Add(1)
	l.r.aside.Go(func() {
		defer l.later.Done()
		for s := range settling {
			l.mu.Lock()
			if l.take(s, true) {
				l.emitNow()
			}
			l.mu.Unlock()
		}
	})
}

// between waits for what settling a to-do finds, the list, the one at up
// up, emitted again as it changes, taking the person's answers meanwhile,
// if any: a Stop stops the list there, what settling finds taken once it's
// stopped; anything else, for a to-do done with, goes nowhere. It reports
// whether they stopped.
func (l *toDos) between(ctx context.Context, up int, answers <-chan hand.Answer, settling <-chan settled) (stopped bool, err error) {
	for {
		select {
		case s, ok := <-settling:
			if !ok {
				return false, nil
			}
			l.mu.Lock()
			if l.take(s, false) {
				l.up = up
				l.emitNow()
			}
			l.mu.Unlock()
		case a := <-answers:
			if a == hand.Stop {
				return true, nil
			}
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// take takes what settling a to-do found, l.mu held: settled, the list
// says how it ended, done, with what kit saw when it has words, or not;
// the to-dos found to follow it that the list hasn't come next, or at its
// end (atEnd). It reports whether the list changed.
func (l *toDos) take(s settled, atEnd bool) bool {
	e := &l.entries[s.i]
	changed := e.Settling
	e.Settling = false
	if s.done && (!e.Done || s.saw != "" && s.saw != e.Saw) {
		e.Done, e.Saw, e.Skipped, changed = true, cmp.Or(s.saw, e.Saw), false, true
	}
	at := s.i + 1
	for _, t := range s.more {
		if slices.ContainsFunc(l.entries, func(e event.ToDo) bool { return e.ID == t.entry().ID }) {
			continue
		}
		if atEnd {
			at = len(l.list)
		}
		l.list = slices.Insert(l.list, at, t)
		l.entries = slices.Insert(l.entries, at, t.entry())
		at, changed = at+1, true
	}
	return changed
}

// awaitLater waits for the work of each to-do left for later to come to
// what it does, which takes it as it comes (leave). The run stopped, it
// waits no more.
func (l *toDos) awaitLater(ctx context.Context) {
	over := make(chan struct{})
	go func() {
		l.later.Wait()
		close(over)
	}()
	select {
	case <-over:
	case <-ctx.Done():
	}
}

// recheckHeld checks again the steps the to-dos done held, those they came
// from, for their rows and the run's record.
func recheckHeld(ctx context.Context, entries []event.ToDo) {
	from := map[string]bool{}
	for _, e := range entries {
		if e.Done {
			from[e.Step] = true
		}
	}
	var held []string
	for _, f := range engine.Finished(ctx) {
		if from[f.Step] {
			held = append(held, f.Step)
		}
	}
	if len(held) == 0 {
		return
	}
	event.Say(ctx, event.Doing{Says: "checking once more"})
	for _, name := range held {
		engine.Recheck(ctx, name)
	}
}
