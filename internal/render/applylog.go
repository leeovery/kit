package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
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
// ended: failed, what went wrong, why and what to run under it; left to do,
// or unwell, what it says; not checked, why; well, what it did or found,
// and what's still waiting under it. A step the run was stopped before, or
// in the middle of, has no line. A step by hand of its own whose card came
// up has its card's line, and no other.
func (p *Pretty) noteFinished(f event.StepFinished) {
	at, row, title, part := p.stamp(f.Time), p.rowOf(f.Step), p.title(f.Step), p.step(f.Step).Part
	ended := p.ended(f)
	if p.toldByItsCard(f.Step) && (ended == look.Done || ended == look.ToDo) {
		return
	}
	switch ended {
	case look.Failed:
		head, _ := failure(f.Result)
		p.note(at, row, look.Mark(look.Failed), look.Red(title+" failed: "+head), p.failedRow(f).Under...)
	case look.ToDo:
		p.note(at, row, look.Mark(look.ToDo), told(look.White, title, p.toDoRow(f).Says))
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

// raise is a card come up for e's step by hand: the card up before it done
// with, when it's another; the log saying it waits for you. The same card
// again, as when more come to wait after it, changes only what it says.
func (p *Pretty) raise(e event.NeedsYou) {
	if p.card != nil && cardID(p.card.Step, p.card.Item) != cardID(e.Step, e.Item) {
		p.lower(e.Time, nil)
	}
	if p.card == nil {
		p.cardSince = p.stamp(e.Time)
		p.note(p.cardSince, p.rowOf(e.Step), look.Mark(look.NeedsYou), look.White(e.Title+": waiting for you"))
	}
	p.card = &e
	p.cards[cardID(e.Step, e.Item)] = e
}

// lower takes the card up down, at at, its step finished as finished, if
// that's why: left to do as its step ended, it's not done; else it's done,
// and how long it was up; the run stopped, it says nothing.
func (p *Pretty) lower(at time.Time, finished *event.StepFinished) {
	c := *p.card
	p.card = nil
	at, row := p.stamp(at), p.rowOf(c.Step)
	switch {
	case finished != nil && wasStopped(*finished):
	case finished != nil && stillToDo(finished.Result, c.Item):
		p.note(at, row, look.Mark(look.ToDo), look.White(c.Title+": not done"))
	default:
		p.note(at, row, look.Mark(look.Done), look.White(c.Title+": done, in "+took(at.Sub(p.cardSince))))
	}
}

// logBody is the log's lines, row's alone, or every one for "": the
// run's, oldest first, then what's under way.
func (p *Pretty) logBody(row string) []string {
	var out []string
	for _, l := range p.log {
		if row == "" || l.row == row {
			out = append(out, l.lines...)
		}
	}
	return append(out, p.underWay(row)...)
}

// underWay is what's under way, at the log's foot, row's alone, or every
// step's for "": a line each step applying and at work, in the order they
// started, what it's doing and how far it's got; the first with the time
// now.
func (p *Pretty) underWay(row string) []string {
	var out []string
	for _, r := range p.running {
		if !r.applying || !p.atWork(r) || row != "" && p.rowOf(r.step) != row {
			continue
		}
		at := strings.Repeat(" ", len(clockFormat))
		if len(out) == 0 {
			at = look.Dim(p.now().Format(clockFormat))
		}
		out = append(out, "  "+at+"  "+look.Mark(look.Running)+" "+r.doingSays(true, true))
	}
	return out
}

// doingSays is what r says it's doing, cyan, its title after generic words
// when named is true, and capitalised as a sentence when sentence is; then
// how far it's got, white, when it counts.
func (r runningStep) doingSays(named, sentence bool) string {
	doing := r.doing
	if named && (doing == "applying" || doing == "checking") {
		doing += " " + r.title
	}
	if sentence && doing != "" {
		doing = strings.ToUpper(doing[:1]) + doing[1:]
	}
	if r.of == 0 {
		return look.Cyan(doing)
	}
	return look.Says(look.Cyan(doing), look.White(fmt.Sprintf("%d of %d", r.done, r.of)))
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
