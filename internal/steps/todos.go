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

func (l lineToDo) card(ctx context.Context) event.NeedsYou {
	c := l.byHand.card(ctx)
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

// card is what its card says: as its step readies it, when it's a choice;
// kit telling by itself when it's done, but for a choice, whose keys say.
func (o ownToDo) card(ctx context.Context) event.NeedsYou {
	it := o.item
	if c := engine.ChoiceOf(ctx, o.step); c != nil {
		it = c.Ready(ctx, it)
	}
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
// (followUps). The person can stop where they are (hand.Stop), the list
// emitted stopped at once: what's left stays to do, the one up done if it
// was done as they stopped, or found done by the look under way then, left
// to finish, which the list is emitted again to say. Their
// answers come on one listener for the whole list, so a Stop given between
// two to-dos is never lost, and anything else given then, for a to-do done
// with, goes nowhere. Then the steps the to-dos done held are checked again
// (recheckHeld).
func (r *Raise) workThrough(ctx context.Context, step string, list []toDo) error {
	if len(list) == 0 {
		return nil
	}
	answers, down := r.Desk.Listen(step)
	defer down()
	entries := make([]event.ToDo, len(list))
	for i, t := range list {
		entries[i] = t.entry()
	}
	emit := func(up int) {
		r.Sink.Emit(event.ToDos{Time: r.Now(), Step: step, List: slices.Clone(entries), Up: up})
	}
	resolved := map[string]bool{}
	stoppedAt, late := -1, (*looks)(nil)
	for i := 0; i < len(list); i++ {
		emit(i)
		if stoppedMeanwhile(answers) {
			break
		}
		saw, looking, err := r.up(ctx, step, list[i], seat{held: true, after: len(list) - 1 - i, answers: answers})
		switch {
		case err == nil, errors.Is(err, errDoneThenStop):
			entries[i].Done, entries[i].Saw = true, saw
			resolved[entries[i].ID] = true
			for j, t := range followUps(ctx, list[i], list) {
				list = slices.Insert(list, i+1+j, t)
				entries = slices.Insert(entries, i+1+j, t.entry())
			}
		case errors.Is(err, errStopHere):
			stoppedAt, late = i, looking
		default:
			return err
		}
		if err != nil {
			break
		}
	}
	emit(-1)
	if late != nil {
		if saw, done := late.outcome(); done {
			entries[stoppedAt].Done, entries[stoppedAt].Saw = true, saw
			resolved[entries[stoppedAt].ID] = true
			emit(-1)
		}
	}
	recheckHeld(ctx, entries, resolved)
	return nil
}

// recheckHeld checks again the steps the to-dos done held, those they came
// from, for their rows and the run's record. resolved are the done ones'
// ids.
func recheckHeld(ctx context.Context, entries []event.ToDo, resolved map[string]bool) {
	from := map[string]bool{}
	for _, e := range entries {
		if resolved[e.ID] {
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
