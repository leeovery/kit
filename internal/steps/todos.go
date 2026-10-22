package steps

import (
	"context"
	"errors"
	"slices"
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
				first = append(first, ownToDo{step: f.Step, item: it})
			default:
				last = append(last, ownToDo{step: f.Step, item: it})
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

func (o ownToDo) entry() event.ToDo {
	t := o.item.ToDo
	return event.ToDo{Step: o.step, ID: o.item.ID, Title: t.Title, Group: t.Group, How: howOf(t.Steps, o.item.Detail)}
}

// card is what its card says, as its item has it: kit telling by itself
// when it's done, but for a choice, whose keys say.
func (o ownToDo) card() event.NeedsYou { return cardOf(o.item) }

// ready is what its card says once its step's readied it, when it's a
// choice.
func (o ownToDo) ready(ctx context.Context) event.NeedsYou {
	it := o.item
	if c := engine.ChoiceOf(ctx, o.step); c != nil {
		it = c.Ready(ctx, it)
	}
	return cardOf(it)
}

// cardOf is what the card of it, a to-do of kit's own, says.
func cardOf(it check.Item) event.NeedsYou {
	t := it.ToDo
	return event.NeedsYou{Item: it.ID, Title: t.Title, Group: t.Group, Why: t.Why, Steps: t.Steps, Watch: t.Watch, Keys: t.Keys, Tells: len(t.Keys) == 0}
}

// done reports whether it's done, as its step's check finds it: the check
// looked, and found it gone; and, as what kit saw, what settling what it
// waited for did, if anything (engine.Choice's Settle), else what the
// check says.
func (o ownToDo) done(ctx context.Context) (bool, string) {
	res, ok := engine.Look(ctx, o.step)
	switch {
	case !ok, res.State == check.Failed, res.State == check.Deferred:
		return false, ""
	case slices.ContainsFunc(res.Items, func(it check.Item) bool { return it.ID == o.item.ID }):
		return false, ""
	}
	// Settling is never cut short, as by the card coming down meanwhile:
	// what it waited for would be half done.
	if c := engine.ChoiceOf(ctx, o.step); c != nil {
		if said := c.Settle(context.WithoutCancel(ctx), o.item); said != "" {
			return true, said
		}
	}
	return true, res.Summary
}

// followUps are the to-dos t's step's check finds now that list hasn't,
// as bringing back apps' settings can leave apps to open: none for one
// that isn't kit's own.
func followUps(ctx context.Context, t toDo, list []toDo) []toDo {
	o, ok := t.(ownToDo)
	if !ok {
		return nil
	}
	res, ok := engine.Look(ctx, o.step)
	if !ok {
		return nil
	}
	var more []toDo
	for _, it := range res.Items {
		listed := slices.ContainsFunc(list, func(t toDo) bool { return t.entry().ID == it.ID })
		if it.ToDo != nil && !listed {
			more = append(more, ownToDo{step: o.step, item: it})
		}
	}
	return more
}

// answer is what the person's answer does: a choice's key does what its
// step says, then kit looks, taking what the step said it did as what it
// saw, should that have done it; what goes wrong, the log says, and it
// stays up. Done has kit look at once.
func (o ownToDo) answer(ctx context.Context, a hand.Answer, _ time.Time) (bool, string, error) {
	if !slices.ContainsFunc(o.item.ToDo.Keys, func(k check.Key) bool { return hand.Answer(k.Key) == a }) {
		ok, saw := o.done(ctx)
		return ok, saw, nil
	}
	c := engine.ChoiceOf(ctx, o.step)
	if c == nil {
		return false, "", nil
	}
	said, err := c.Answer(ctx, o.item, string(a))
	if err != nil {
		event.Log(ctx, event.Logged{Line: o.item.ToDo.Title + ": " + err.Error(), Alert: true})
		return false, "", nil
	}
	ok, _ := o.done(ctx)
	return ok, said, nil
}

// workThrough raises list's to-dos for step one at a time, in order, each
// till it's done: the list emitted as each comes up, and once they're over;
// one of kit's own done, those its step's check finds now come next
// (followUps), looked for aside. The person can stop where they are
// (hand.Stop), the list emitted stopped at once, whatever's under way:
// what's left stays to do, the one up done if it was done as they stopped,
// or found done by the work its card left under way, left to finish, which
// the list is emitted again to say, with the to-dos it leaves. Their
// answers come on one listener for the whole list, so a Stop given between
// two to-dos is never lost, and anything else given then, for a to-do done
// with, goes nowhere. Then the steps the to-dos done held are checked again
// (recheckHeld). The run stopped, the list's emitted stopped, and nothing
// more's done.
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
	if err != nil {
		return err
	}
	if settling != nil {
		select {
		case s := <-settling:
			if l.take(s) {
				l.emit(-1)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	recheckHeld(ctx, l.entries)
	return nil
}

// toDos are the to-dos at the run's end as they're worked through: each as
// raised, and as the list says it.
type toDos struct {
	r       *Raise
	step    string
	list    []toDo
	entries []event.ToDo
}

// through raises the to-dos in turn, till they're over or the person stops
// there, returning what the one up then is still settling to, if anything.
func (l *toDos) through(ctx context.Context, answers <-chan hand.Answer) (<-chan settled, error) {
	for i := 0; i < len(l.list); i++ {
		l.emit(i)
		if stoppedMeanwhile(answers) {
			return nil, nil
		}
		saw, late, err := l.r.up(ctx, l.step, l.list[i], seat{held: true, after: len(l.list) - 1 - i, answers: answers})
		if err != nil && !errors.Is(err, errStopHere) {
			return nil, err
		}
		if late == nil {
			l.done(i, saw)
		}
		settling := l.settle(ctx, i, late)
		if err != nil {
			return settling, nil
		}
		if stopped, err := l.between(ctx, answers, settling); stopped || err != nil {
			return settling, err
		}
	}
	return nil, nil
}

// emit says the list as it stands, the one at up up: -1 for none, once
// they're over or the person stopped.
func (l *toDos) emit(up int) {
	l.r.Sink.Emit(event.ToDos{Time: l.r.Now(), Step: l.step, List: slices.Clone(l.entries), Up: up})
}

// done marks the to-do at i done, kit having seen saw.
func (l *toDos) done(i int, saw string) {
	l.entries[i].Done, l.entries[i].Saw = true, saw
}

// settled is what the to-do at i came to, its card down: whether it's
// done, what kit saw, and the to-dos its step's check found then, to follow
// it.
type settled struct {
	i    int
	done bool
	saw  string
	more []toDo
}

// settle is what the to-do at i comes to, aside, its card down: done, or,
// left with work under way (late), done should that find it so; and, done,
// the to-dos its step's check finds then that the list hasn't.
func (l *toDos) settle(ctx context.Context, i int, late *work) <-chan settled {
	list := slices.Clone(l.list)
	return aside(l.r, func() settled {
		s := settled{i: i, done: late == nil}
		if late != nil {
			s.saw, s.done = late.outcome(ctx)
		}
		if s.done {
			s.more = followUps(ctx, list[i], list)
		}
		return s
	})
}

// between waits, a to-do done, for what settling it finds, taking the
// person's answers meanwhile: a Stop stops the list there, what settling
// finds taken once it's stopped; anything else, for a to-do done with,
// goes nowhere. It reports whether they stopped.
func (l *toDos) between(ctx context.Context, answers <-chan hand.Answer, settling <-chan settled) (stopped bool, err error) {
	for {
		select {
		case s := <-settling:
			l.take(s)
			return false, nil
		case a := <-answers:
			if a == hand.Stop {
				return true, nil
			}
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// take takes what settling a to-do found: done, the list says so, with
// what kit saw, unless it did already; the to-dos found to follow it come
// next. It reports whether the list changed.
func (l *toDos) take(s settled) bool {
	changed := false
	if s.done && !l.entries[s.i].Done {
		l.done(s.i, s.saw)
		changed = true
	}
	for j, t := range s.more {
		l.list = slices.Insert(l.list, s.i+1+j, t)
		l.entries = slices.Insert(l.entries, s.i+1+j, t.entry())
		changed = true
	}
	return changed
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
