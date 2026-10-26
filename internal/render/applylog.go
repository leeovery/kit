package render

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/steps"
)

// Applying's log (kit-look, round 10) is every line of the run, oldest
// first, as it happens: a line for each thing a step tells it it did, each
// step finished, and each card up for you and done with; each line its time,
// a mark and kit's own words, never a tool's. What's at work shows at its
// foot, changing in place. Each line belongs to a row, for the log to show
// a row's lines alone: a step by hand's to the row it holds up, or to none.

// logLine is a line of the log, styled, and what's under it, on its spine;
// and the row it belongs to, "" for none.
type logLine struct {
	row   string
	lines []string
}

// clockFormat is a log line's time.
const clockFormat = "15:04:05"

// logUnder is where what's under a log line starts: its mark's column.
var logUnder = strings.Repeat(" ", 2+len(clockFormat)+2)

// stamp is t, or, when it isn't set, now: when what happened did.
func (p *Pretty) stamp(t time.Time) time.Time {
	if t.IsZero() {
		return p.now()
	}
	return t
}

// note adds a line to the log, at at, belonging to row: mark, then text,
// then under, a line each on the spine.
func (p *Pretty) note(at time.Time, row, mark, text string, under ...string) {
	lines := []string{"  " + look.Dim(at.Format(clockFormat)) + "  " + mark + " " + text}
	for _, u := range under {
		lines = append(lines, logUnder+look.Dim("│")+" "+u)
	}
	p.log = append(p.log, logLine{row: row, lines: lines})
}

// told is what a log line says of a step titled title: its title, then
// what its row says, the parts in style after dim dots.
func told(style func(string) string, title, says string) string {
	parts := strings.Split(ansi.Strip(says), " · ")
	parts[0] = title + ": " + parts[0]
	for i, part := range parts {
		parts[i] = style(part)
	}
	return look.Says(parts...)
}

// noteFinished adds a step finished to the log, in kit's words for it as it
// ended: failed, what went wrong, why and what to run under it; left to do
// at the end, what it says (laterState); unwell, what it says; not checked,
// why; well, what it did or found, and what's still waiting under it. A step the run was stopped before, or
// in the middle of, has no line. A step by hand of its own whose card came
// up has its card's line, and no other.
func (p *Pretty) noteFinished(f event.StepFinished) {
	at, row, title, part := p.stamp(f.Time), p.rowOf(f.Step), p.title(f.Step), p.step(f.Step).Part
	ended := p.ended(f)
	switch {
	case p.toldByItsCard(f.Step) && (ended == look.Done || ended == look.ToDo):
		return
	case ended == look.Done && (p.toldByItsToDos(f.Step) || p.nothingByHand(f.Step)):
		return
	}
	switch ended {
	case look.Failed:
		head, _ := failure(f.Result)
		p.note(at, row, look.Mark(look.Failed), look.Red(title+" failed: "+head), p.failedRow(f).Under...)
	case look.ToDo:
		p.note(at, row, look.Mark(p.laterState()), told(look.White, title, p.toDoRow(f).Says))
	case look.NeedsYou:
		r := p.workRow(f, part)
		p.note(at, row, look.Mark(look.NeedsYou), told(look.White, title, r.Says), r.Under...)
	case look.Skipped:
		if !wasStopped(f) {
			p.note(at, row, look.Mark(look.Skipped), told(look.Muted, title, p.listRow(p.step(f.Step)).Says))
		}
	default:
		r := p.workRow(f, part)
		p.note(at, row, look.Mark(look.Done), told(look.White, title, r.Says), r.Under...)
	}
}

// toldByItsCard is whether step is a step by hand of its own whose card came
// up, so the log has said how it went.
func (p *Pretty) toldByItsCard(step string) bool {
	s := p.step(step)
	if len(s.ByHand) != 1 || s.ByHand[0].Name != s.Name {
		return false
	}
	_, raised := p.cards[cardID(s.Name, s.Name)]
	return raised
}

// toldByItsToDos is whether step is the step whose to-dos the run worked
// through at its end, so the log has said how each went.
func (p *Pretty) toldByItsToDos(step string) bool {
	return p.todos != nil && p.todos.Step == step
}

// nothingByHand is whether step is the steps by hand with no lines of
// their own: there at a terminal for the to-dos the rest of the run leaves,
// done, they've nothing to say.
func (p *Pretty) nothingByHand(step string) bool {
	s := p.step(step)
	return s.Area == steps.AreaManual && len(s.ByHand) == 0
}

// raise is a card come up for e's step by hand, in place of the card up
// before it, when that's another (putDown). The first time it comes up,
// the log says it waits for you, unless it's one of the to-dos at the
// run's end, which their list shows; come back from being set aside, it's
// up since it first came up. The same card again, as when more come to
// wait after it, changes only what it says. What came up before never
// says how that card ended: each card's own end does.
func (p *Pretty) raise(e event.NeedsYou) {
	id := cardID(e.Step, e.Item)
	if p.card != nil && cardID(p.card.Step, p.card.Item) != id {
		p.putDown()
	}
	delete(p.asides, id)
	if _, seen := p.since[id]; !seen {
		p.since[id] = p.stamp(e.Time)
		if !p.listing(e.Step) {
			p.note(p.since[id], p.rowOf(e.Step), look.Mark(look.NeedsYou), look.White(e.Title+": waiting for you"))
		}
	}
	p.card = &e
	p.cards[id] = e
}

// putDown takes the card up down for another to take its place, saying
// nothing of how it ended: a to-do's end its list tells (workThrough),
// any other's its step finishing (lowerAside).
func (p *Pretty) putDown() {
	c := *p.card
	p.card = nil
	if !p.listing(c.Step) {
		p.asides[cardID(c.Step, c.Item)] = c
	}
}

// setAside takes the card up down, when it's step's, its step saying it
// waits its turn again, as another card's taken it: it's still to do, and
// comes back as it was. The to-dos at the run's end are never set aside.
func (p *Pretty) setAside(step string) {
	if p.carded(step) && !p.listing(step) {
		p.putDown()
	}
}

// upSince is when the card up first came up.
func (p *Pretty) upSince() time.Time { return p.since[cardID(p.card.Step, p.card.Item)] }

// takeDown takes e's card down the moment it's done, or the person stopped
// there, before its step finishes: up, or set aside and done meanwhile, the
// log says it's done; one of the to-dos at the run's end, its list says how
// it ended, how long it took counted to now.
func (p *Pretty) takeDown(e event.CardDown) {
	id := cardID(e.Step, e.Item)
	switch c, aside := p.asides[id]; {
	case p.card != nil && cardID(p.card.Step, p.card.Item) == id && p.listing(e.Step):
		p.card, p.downAt[id] = nil, p.stamp(e.Time)
	case p.card != nil && cardID(p.card.Step, p.card.Item) == id:
		p.lower(e.Time, nil)
	case aside:
		delete(p.asides, id)
		p.noteDown(c, e.Time, nil)
	}
}

// lowerAside has the log say how the cards down of f's step ended, its
// step finished, as lower does for the card up: done meanwhile, or left to
// do.
func (p *Pretty) lowerAside(f event.StepFinished) {
	for id, c := range p.asides {
		if c.Step == f.Step {
			delete(p.asides, id)
			p.noteDown(c, f.Time, &f)
		}
	}
}

// listing is whether step's to-dos are being worked through, one up.
func (p *Pretty) listing(step string) bool {
	return p.todos != nil && p.todos.Step == step && p.todos.Up >= 0
}

// workThrough is the to-dos at the run's end as they stand now: the log
// says each newly done, what kit saw when it has words, and how long it
// took when it came up, till its card came down, however long kit took to
// settle it after; one the person left for later, that it's skipped, and
// how long it was up, once; one that was up and no longer is, left as the
// person stopped there, that it's not done. Of one settling, it says
// nothing till it's settled: what kit finished saying what it saw, or what
// was under way as they stopped saying whether it's done. The card of the
// one that was up is down once it's no longer up, when it's still the card
// up: another card may have come up in its place first. The first time,
// the log shows every line: the to-dos take the left pane, so there's no
// row to choose.
func (p *Pretty) workThrough(e event.ToDos) {
	before, wasUp := p.todos, -1
	if before == nil {
		p.choose("")
	} else {
		wasUp = before.Up
	}
	at, row := p.stamp(e.Time), p.rowOf(e.Step)
	for i, t := range e.List {
		id := cardID(e.Step, t.ID)
		since, up := p.since[id]
		switch {
		case t.Settling || endSaid(before, t.ID):
			continue
		case t.Done:
			text := t.Title + ": done"
			if up {
				text += ", in " + took(cmp.Or(p.downAt[id], at).Sub(since))
			}
			parts := []string{look.White(text)}
			if t.Saw != "" {
				parts = append(parts, look.Muted(t.Saw))
			}
			p.note(at, row, look.Mark(look.Done), look.Says(parts...))
		case t.Skipped && skipSaid(before, t.ID):
			continue
		case t.Skipped:
			text := t.Title + ": skipped"
			if up {
				text += ", after " + took(cmp.Or(p.downAt[id], at).Sub(since))
			}
			p.note(at, row, look.Mark(look.Skipped), look.Muted(text))
		case up && i != e.Up:
			p.note(at, row, look.Mark(look.ToDo), look.White(t.Title+": not done"))
		default:
			continue
		}
		delete(p.since, id)
		delete(p.downAt, id)
	}
	if wasUp >= 0 && wasUp != e.Up {
		id := cardID(e.Step, e.List[wasUp].ID)
		if p.card != nil && cardID(p.card.Step, p.card.Item) == id {
			p.card = nil
		}
	}
	p.todos = &e
}

// skipSaid is whether the log said the to-do with id was left for later as
// the to-dos stood before.
func skipSaid(before *event.ToDos, id string) bool {
	if before == nil {
		return false
	}
	i := slices.IndexFunc(before.List, func(t event.ToDo) bool { return t.ID == id })
	return i >= 0 && before.List[i].Skipped && !before.List[i].Done
}

// endSaid is whether the log said how the to-do with id ended as the to-dos
// stood before: done, and settled.
func endSaid(before *event.ToDos, id string) bool {
	if before == nil {
		return false
	}
	i := slices.IndexFunc(before.List, func(t event.ToDo) bool { return t.ID == id })
	return i >= 0 && before.List[i].Done && !before.List[i].Settling
}

// lower takes the card up down, at at, its step finished as finished, if
// that's why: left to do as its step ended, it's not done; else it's done,
// and how long it was up; the run stopped, it says nothing.
func (p *Pretty) lower(at time.Time, finished *event.StepFinished) {
	c := *p.card
	p.card = nil
	p.noteDown(c, at, finished)
}

// noteDown has the log say card c came down at at, as lower says, how long
// since it first came up; it's done with.
func (p *Pretty) noteDown(c event.NeedsYou, at time.Time, finished *event.StepFinished) {
	id := cardID(c.Step, c.Item)
	since := p.since[id]
	delete(p.since, id)
	at, row := p.stamp(at), p.rowOf(c.Step)
	switch {
	case finished != nil && wasStopped(*finished):
	case finished != nil && stillToDo(finished.Result, c.Item):
		p.note(at, row, look.Mark(look.ToDo), look.White(c.Title+": not done"))
	default:
		p.note(at, row, look.Mark(look.Done), look.White(c.Title+": done, in "+took(at.Sub(since))))
	}
}

// logBody is the log's lines, w wide, row's alone, or every one for "":
// the run's, oldest first, then what's under way.
func (p *Pretty) logBody(row string, w int) []string {
	var out []string
	for _, l := range p.log {
		if row == "" || l.row == row {
			out = append(out, l.lines...)
		}
	}
	return append(out, p.underWay(row, w)...)
}

// underWay is what's under way, at the log's foot, w wide, row's alone, or
// every step's for "": a line each step applying and at work, in the order
// they started, what it's doing and how far it's got, that kept whole when
// the line's cut; the first with the time now.
func (p *Pretty) underWay(row string, w int) []string {
	var out []string
	for _, r := range p.running {
		if !r.applying || !p.atWork(r) || row != "" && p.rowOf(r.step) != row {
			continue
		}
		at := strings.Repeat(" ", len(clockFormat))
		if len(out) == 0 {
			at = look.Dim(p.now().Format(clockFormat))
		}
		says, far := r.doingSays(true, true)
		out = append(out, look.CutKeeping("  "+at+"  "+look.Mark(look.Running)+" "+says, far, w))
	}
	return out
}

// doingSays is what r says it's doing, cyan, its title after generic words
// when named is true, and capitalised as a sentence when sentence is; and
// how far it's got, white, when it counts, else "".
func (r runningStep) doingSays(named, sentence bool) (says, far string) {
	doing := r.doing
	if named && (doing == "applying" || doing == "checking") {
		doing += " " + r.title
	}
	if sentence && doing != "" {
		doing = strings.ToUpper(doing[:1]) + doing[1:]
	}
	if r.of > 0 {
		far = look.White(fmt.Sprintf("%d of %d", r.done, r.of))
	}
	return look.Cyan(doing), far
}

// backFoot is how many of the log's lines its foot takes while it's
// scrolled back: a rule, then what's below.
const backFoot = 2

// logWindow is which of n lines of the log room lines show, from start to
// end: the newest, following; else from where it's anchored, whatever comes
// after, the foot taking backFoot of the room; and whether it's scrolled
// back.
func (k screenKeys) logWindow(n, room int) (start, end int, back bool) {
	if !k.anchored || k.top >= n-room {
		return max(n-room, 0), n, false
	}
	return k.top, min(k.top+max(room-backFoot, 1), n), true
}

// backLine is the log's foot while it's scrolled back, below lines after
// what it shows.
func backLine(below int) string {
	return "  " + look.Says(look.Cyan("↓")+" "+look.White(fmt.Sprintf("%d new %s", below, plural(below, "line", "lines"))),
		look.Strong("end")+" "+look.Muted("or scroll down to follow again"))
}
