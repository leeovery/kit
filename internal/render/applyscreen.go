package render

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/steps"
)

// Applying at a terminal big enough takes a screen of its own while it runs
// (kit-look, round 10): the terminal's alternate screen, drawn whole each
// time, at the window's full size. Its header is the wordmark, as the
// arrival leaves it, with what runs, the bar and how the run stands beside
// it; under a rule, two panes. The left has Everything, then the rows, a
// row each of kit's kinds and its own areas, each saying how its steps
// stand; then the keys. The right has what the run
// waits on you for, when it does, a panel ruled off above the log; then the
// log, everything or a row's lines alone, following the newest or scrolled
// back. It reads keys while it's up, the terminal in its raw mode, with the
// mouse's clicks and wheel (applykeys.go). When the run ends the screen
// goes, and its record is printed in its place.

// The alternate screen, which applying takes while it runs, and gives back;
// the mouse's buttons and wheel, reported in SGR's form while it reads
// keys; and the window coming into focus and leaving it, reported with
// them.
const (
	altOn    = "\x1b[?1049h"
	altOff   = "\x1b[?1049l"
	mouseOn  = "\x1b[?1000h\x1b[?1006h"
	mouseOff = "\x1b[?1006l\x1b[?1000l"
	focusOn  = "\x1b[?1004h"
	focusOff = "\x1b[?1004l"
)

// The least window applying takes a screen of its own in: below it, the run
// stays in place under its heading.
const (
	screenWidth  = 100
	screenHeight = 30
)

// The panes: the left at least leftLeast wide, leftAt160 in a window 160
// wide, as round 10 draws it, growing and shrinking with it; the right at
// least rightLeast.
const (
	leftLeast  = 52
	leftAt160  = 60
	rightLeast = 40
)

// How many cells the header's bar has, and a running row's.
const (
	headBar = 48
	rowBar  = 24
)

// splitAt is the column the panes part at, in a window w wide.
func splitAt(w int) int { return max(leftLeast, min(w*leftAt160/160, w-1-rightLeast)) }

// headline is what runs, the Mac and when, on the line beside the
// wordmark's first, as applying's screen has it and the arrival types it
// in: what runs bold, the Mac and when muted, a dim dot between each.
type headline struct{ what, mac, when string }

// typed is the line up to n of its characters.
func (h headline) typed(n int) string {
	var b strings.Builder
	for _, part := range []struct {
		text  string
		style func(string) string
	}{{h.what, look.Strong}, {" · ", look.Dim}, {h.mac, look.Muted}, {" · ", look.Dim}, {h.when, look.Muted}} {
		text := []rune(part.text)
		if n > 0 {
			b.WriteString(part.style(string(text[:min(n, len(text))])))
		}
		n -= len(text)
	}
	return b.String()
}

// length is how many characters the line has.
func (h headline) length() int {
	return utf8.RuneCountInString(h.what + " · " + h.mac + " · " + h.when)
}

// whole is the line, all of it.
func (h headline) whole() string { return h.typed(h.length()) }

// drawApplying draws applying's live part: its screen, when the window's
// big enough for it; otherwise in place, under its heading.
func (p *Pretty) drawApplying() {
	if w, h := p.window(); p.screenFits(w, h) {
		p.drawScreen(w, h)
		return
	}
	p.leaveScreen()
	lines := p.applyLive()
	// In a window too short for the heading too, it goes, as it would have
	// scrolled away.
	if head := p.headingLive(false); len(head)+len(lines) < p.height() {
		lines = append(head, lines...)
	}
	p.drawLive(lines)
}

// window is the terminal's size as it is now, all of it: no size when the
// face wasn't told how to ask.
func (p *Pretty) window() (int, int) {
	if p.size == nil {
		return 0, 0
	}
	return p.size()
}

// screenFits is whether a window w by h is big enough for applying's
// screen.
func (p *Pretty) screenFits(w, h int) bool { return w >= screenWidth && h >= screenHeight }

// drawScreen draws applying's screen, w by h, over the last: from the
// window's top, each line cleared past its end, in one write; no more
// often than drawEvery. It takes the alternate screen first, the live part
// drawn in place taken down, and reads keys while it has it, the mouse's
// and the window's focus among them.
func (p *Pretty) drawScreen(w, h int) {
	switch {
	case !p.onScreen:
		p.clearLive()
		p.listen()
		p.write(altOn + hideCursor)
		if p.keys.reading != nil {
			p.write(mouseOn + focusOn)
		}
		p.onScreen, p.hidden, p.drawnSize = true, true, [2]int{}
	case time.Since(p.drawn) < drawEvery:
		return
	}
	var b strings.Builder
	if p.drawnSize != [2]int{w, h} {
		b.WriteString("\x1b[2J")
	}
	b.WriteString("\x1b[H")
	for i, l := range p.screenFrame(w, h) {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(l)
		// A line as wide as the window leaves the cursor on its last cell,
		// which clearing to the end would take.
		if ansi.StringWidth(l) < w {
			b.WriteString("\x1b[K")
		}
	}
	p.write(b.String())
	p.drawnSize, p.drawn = [2]int{w, h}, time.Now()
}

// leaveScreen gives the alternate screen back, when applying has it: the
// terminal's own screen shows again, as it was, and keys aren't read. The
// mouse and the window's focus stop being reported before the terminal
// leaves its raw mode, which would show what they reported as typed.
func (p *Pretty) leaveScreen() {
	if !p.onScreen {
		return
	}
	if p.keys.reading != nil {
		p.write(focusOff + mouseOff)
		p.flush()
	}
	p.unlisten()
	p.write(altOff)
	p.onScreen = false
}

// screenFrame is applying's screen as it stands, w by h, a line a row: the
// header, a rule with a junction where the panes part, then the panes side
// by side, a rule between, with a junction where the right pane has a rule
// of its own. It notes where the panes and the log are, for the mouse.
func (p *Pretty) screenFrame(w, h int) []string {
	head := p.header()
	split := splitAt(w)
	room := max(h-len(head)-1, 0)
	left := p.leftPane(split, room)
	right, rules, logAt := p.rightPane(w-split-1, room)
	p.keys.split, p.keys.paneAt, p.keys.logAt = split, len(head)+1, len(head)+1+logAt
	lines := append(head, look.Junction(w, split, "┬"))
	for i := range room {
		l := padTo(look.Cut(lineAt(left, i), split-1), split)
		if rules[i] {
			lines = append(lines, l+look.Junction(w-split, 0, "├"))
			continue
		}
		lines = append(lines, l+look.Dim("│")+look.Cut(lineAt(right, i), w-split-1))
	}
	frame := make([]string, h)
	for i := range min(h, len(lines)) {
		frame[i] = look.Cut(lines[i], w)
	}
	return frame
}

// lineAt is lines' line i, or a blank one past its last.
func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// padTo is s with spaces after it, n cells wide.
func padTo(s string, n int) string { return s + strings.Repeat(" ", max(n-ansi.StringWidth(s), 0)) }

// header is the screen's header, a blank line under it: a blank line, then
// the wordmark, as the arrival leaves it, with what runs, the Mac and when
// beside its first line, the bar and how far the run's got its second, and
// how it stands its third.
func (p *Pretty) header() []string {
	line := headline{p.start.Command, p.start.Machine, when(p.start.Time)}
	got := p.howFar()
	bar := look.Progress(got, headBar, look.Done) + "  " +
		look.Says(look.White(fmt.Sprintf("%d%%", int(got*100))), look.Muted(took(p.now().Sub(p.began))+" so far"))
	return append(append([]string{""}, look.Head(line.whole(), bar, p.stateLine())...), "")
}

// howFar is how far the run's got, of 1, weighted by work: each step one,
// a finished one whole, a running one that counts as far as it's got.
func (p *Pretty) howFar() float64 {
	total := len(p.start.Steps)
	if total == 0 {
		return 0
	}
	got := float64(len(p.results))
	for _, r := range p.running {
		if r.of > 0 {
			got += r.howFar()
		}
	}
	return min(got/float64(total), 1)
}

// stateLine says how the run stands, always whether it needs you: working,
// or waiting for you, and what for; then what failed. What's left to do
// waits for the run's end: there's no later during it (the maintainer, 16 Oct). Its
// to-dos up, how many are left, and done; once they're over, that kit's
// carrying on.
func (p *Pretty) stateLine() string {
	if p.todos != nil {
		return p.toDoState()
	}
	parts := []string{look.Cyan("◐ working"), look.White("nothing needs you")}
	if waits := p.waitsOnYou(); len(waits) > 0 {
		parts = append([]string{look.Orange("▲ waiting for you")}, waits...)
	}
	if n := p.failedSteps(); n > 0 {
		parts = append(parts, look.Red(fmt.Sprintf("%d failed", n)))
	}
	return look.Says(parts...)
}

// waitsOnYou is what the run waits on you for, if anything: the card up;
// macOS asking, and that it's in a window of its own; a step saying what
// you're to do.
func (p *Pretty) waitsOnYou() []string {
	var waits []string
	if p.card != nil {
		waits = append(waits, look.White(p.card.Title))
	}
	if p.asking != "" {
		waits = append(waits, look.White("macOS "+p.asking), look.Muted("in its window"))
	}
	for _, r := range p.running {
		if r.todo != "" {
			waits = append(waits, look.White(r.title+" "+r.doing))
		}
	}
	return waits
}

// failedSteps is how many of the run's steps failed.
func (p *Pretty) failedSteps() int {
	n := 0
	for _, f := range p.results {
		if p.ended(f) == look.Failed {
			n++
		}
	}
	return n
}

// leftPane is the screen's left pane, w wide and room lines: Everything,
// then the rows, each with a bar under it while a step of its that counts
// is at work; the keys at its foot. It notes which choice each line is, for
// the mouse. Once the to-dos at the run's end have started, it's theirs.
func (p *Pretty) leftPane(w, room int) []string {
	if p.todos != nil {
		return p.toDoPane(w, room)
	}
	every := "  " + look.Dim("≡") + " " + look.Strong("Everything")
	if p.keys.chosen == "" {
		every = "  " + look.Dim("≡") + look.Pill("Everything", look.Done)
	}
	lines := []string{"", every, ""}
	choices := map[int]string{1: ""}
	for _, r := range p.shownRows() {
		row := p.rowLook(r)
		choices[len(lines)] = r.name
		lines = append(lines, p.rowLine(row, w))
		if run, ok := p.rowAtWork(r); ok && row.State == look.Running && run.of > 0 {
			lines = append(lines, "    "+look.Progress(run.howFar(), rowBar, look.Done)+"  "+look.Muted(took(p.now().Sub(run.since))))
		}
	}
	foot := p.leftFoot(w)
	room -= len(foot)
	lines = lines[:min(len(lines), max(room, 0))]
	for at := range choices {
		if at >= len(lines) {
			delete(choices, at)
		}
	}
	p.keys.choices = choices
	return append(append(lines, make([]string, max(room-len(lines), 0))...), foot...)
}

// rowLine is a row's line, w wide: the chosen one's name a pink pill after
// its mark.
func (p *Pretty) rowLine(row look.Row, w int) string {
	if row.Name != p.keys.chosen {
		return look.Rows("  ", w, row)[0]
	}
	l := "  " + look.Mark(row.State) + look.Pill(row.Name, look.Done)
	if row.Says != "" {
		l += " " + row.Says
	}
	return look.Cut(l, w)
}

// leftFoot is the left pane's foot, w wide: a blank line and the keys, the
// question whether to stop over them while it's asked.
func (p *Pretty) leftFoot(w int) []string {
	if p.keys.asking {
		return append(append([]string{""}, p.stopQuestion(w)...), "", p.footKeys())
	}
	return []string{"", p.footKeys()}
}

// rightPane is the screen's right pane, w wide and room lines, the lines
// that are rules, and the line the log starts at: the panel, while a
// card's up, ruled off; the log's heading; its lines, as far as it's
// scrolled; and, scrolled back, a rule, and how many lines are below.
func (p *Pretty) rightPane(w, room int) (lines []string, rules map[int]bool, logAt int) {
	top, rule := p.paneTop(w)
	rules = map[int]bool{}
	if rule >= 0 {
		rules[rule] = true
	}
	body := p.paneBody()
	start, end, back := p.keys.logWindow(len(body), room-len(top))
	lines = append(top, body[start:end]...)
	if back {
		lines = append(lines, make([]string, max(room-backFoot-len(lines), 0))...)
		rules[len(lines)] = true
		lines = append(lines, "", backLine(len(body)-end))
	}
	return lines, rules, len(top)
}

// paneTop is what's over the log in the right pane, w wide, and which of
// its lines is a rule, -1 for none: the panel, while a card's up, a rule
// under it; then the log's heading, Log, or Log and the row chosen, a blank
// line over and under it; or, in the log's place, the raw view's heading.
func (p *Pretty) paneTop(w int) (top []string, rule int) {
	rule = -1
	switch {
	case p.card != nil && p.listing(p.card.Step):
		top = p.toDoPanel(w)
	case p.card != nil:
		top = p.panel(w)
	}
	if top != nil {
		rule = len(top)
		top = append(top, "")
	}
	return append(top, "", p.paneHeading(w), ""), rule
}

// panelAt is where a panel's words start, under its title's.
const panelAt = "      "

// panel is the card up, as the panel over the log has it, w wide: its
// title, orange, how many more wait after it and how long it's waited at
// the right, as far as there's room; why it's there (cardWhy); its steps
// (panelSteps); that kit carries on by itself, when it tells; and its
// keys, a line each.
func (p *Pretty) panel(w int) []string {
	c := p.card
	var right []string
	if c.More > 0 {
		right = append(right, look.Muted(fmt.Sprintf("%d more after this", c.More)))
	}
	right = append(right, look.Muted("waiting "+took(p.now().Sub(p.upSince()))))
	title := "  " + look.Mark(look.NeedsYou) + " " + look.Urgent(c.Title)
	for len(right) > 1 && ansi.StringWidth(title)+ansi.StringWidth(look.Says(right...))+3 > w {
		right = right[:len(right)-1]
	}
	out := []string{"", spread(title, look.Says(right...)+"  ", w), ""}
	room := w - len(panelAt)
	for _, l := range look.Wrap(p.cardWhy(), room) {
		out = append(out, panelAt+look.Muted(l))
	}
	out = append(append(out, ""), panelSteps(c.Steps, room)...)
	if len(c.Steps) > 0 {
		out = append(out, "")
	}
	if c.Tells {
		out = append(out, panelAt+look.White("kit carries on by itself once it's done"))
	}
	for _, k := range cardKeys(*c) {
		out = append(out, panelAt+look.Urgent(k.Key)+" "+look.White(k.Does))
	}
	return append(out, "")
}

// panelSteps are a card's steps as its panel has them, room wide after
// the panel's margin: numbered, a subheading muted and unnumbered, after a
// blank line, the numbering carrying on across it.
func panelSteps(steps []string, room int) []string {
	var out []string
	n := 0
	for i, s := range steps {
		if look.Subheading(s) {
			if i > 0 {
				out = append(out, "")
			}
			for _, l := range look.Wrap(s, room) {
				out = append(out, panelAt+look.Muted(l))
			}
			continue
		}
		n++
		num := fmt.Sprint(n)
		lead := look.Urgent(num) + "  "
		for _, l := range look.Wrap(s, room-len(num)-2) {
			out = append(out, panelAt+lead+look.White(l))
			lead = strings.Repeat(" ", len(num)+2)
		}
	}
	return out
}

// cardWhy is why the card up is there, said once: as its line's notes say,
// when it has some; else, in kit's words, why the run waits for it: the
// steps it holds up, but those by hand, which wait for everything; or, the
// steps by hand that come once the rest is done, that it's what's left; or
// that nothing waits for it.
func (p *Pretty) cardWhy() string {
	if p.card.Why != "" {
		return p.card.Why
	}
	var held []string
	for _, s := range p.start.Steps {
		if s.Area != steps.AreaManual && slices.Contains(s.Waits, p.card.Step) {
			held = append(held, p.title(s.Name))
		}
	}
	switch {
	case len(held) > 0:
		return "The run waits for this, for " + inWords(held) + "."
	case p.card.Step == steps.ManualName:
		return "The rest is done: this is what's left."
	}
	return "Nothing waits for this: the rest of the run carries on meanwhile."
}

// inWords are names in a sentence: a, b and c.
func inWords(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// spread is left, then right at the end of a line w wide: left cut to keep
// right, when there's no room for both.
func spread(left, right string, w int) string {
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		left, gap = look.Cut(left, max(w-ansi.StringWidth(right)-1, 0)), 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// ended is how a finished step stands: cut off, or never run, by the run
// stopping; left for the person at the Mac to do; or as its result says.
func (p *Pretty) ended(f event.StepFinished) look.State {
	switch {
	case wasStopped(f):
		return look.Skipped
	case p.toDo(f.Step):
		return look.ToDo
	}
	return state(f.Result.State)
}

// toDo is whether the step named step finished leaving only the person at
// the Mac something to do: its own result says so, or it was deferred
// behind steps that did, and nothing else.
func (p *Pretty) toDo(step string) bool {
	return p.standing(step, map[string]bool{}) == toDoLeft
}

// standing is how a finished step stands for those waiting on it.
type standing int

const (
	// inTheWay: not finished, failed, or deferred for its own reasons.
	inTheWay standing = iota
	// clearOfIt: done; or deferred only behind steps on the way to it, which
	// don't count against themselves.
	clearOfIt
	// toDoLeft: left only the person something to do (toDo).
	toDoLeft
)

// standing is how the step named step stands, seen the steps on the way to
// it: steps can wait for each other partway through applying
// (engine.Step.Later), which the run allows, so a step met again on the way
// neither holds it up nor leaves it to do.
func (p *Pretty) standing(step string, seen map[string]bool) standing {
	f, ok := p.results[step]
	switch {
	case !ok:
		return inTheWay
	case f.Result.ToDo():
		return toDoLeft
	case f.Result.State == check.OK:
		return clearOfIt
	case f.Result.State != check.Deferred:
		return inTheWay
	}
	seen[step] = true
	defer delete(seen, step)
	toDo, onTheWay := false, false
	for _, w := range p.step(step).Waits {
		if seen[w] {
			onTheWay = true
			continue
		}
		switch p.standing(w, seen) {
		case inTheWay:
			return inTheWay
		case toDoLeft:
			toDo = true
		}
	}
	switch {
	case toDo:
		return toDoLeft
	case onTheWay:
		return clearOfIt
	}
	return inTheWay
}

// cardLines are the step by hand up for you as a card w wide, in place
// under the run's heading in a window too small for applying's screen:
// what to do, how many more wait after it, why it's there (cardWhy), its
// steps, and that kit carries on by itself, when it tells, what its key
// does while it's being answered, else that a bigger window shows the keys
// to answer it; none when no card's up.
func (p *Pretty) cardLines(w int) []string {
	c := p.card
	if c == nil {
		return nil
	}
	says := []string{look.Orange("needs you")}
	if c.More > 0 {
		says = append(says, look.Muted(fmt.Sprintf("%d more after this", c.More)))
	}
	after := "a bigger window shows the keys to answer it"
	switch {
	case c.Tells:
		after = "kit carries on by itself once it's done"
	case c.Answering != "":
		after = answeringWith(*c)
	}
	return look.Card(w, look.CardText{Title: c.Title, Says: look.Says(says...), Why: p.cardWhy(), Steps: c.Steps, After: after})
}

// cardID is how a step by hand's card is known among those that came up:
// its step, and its line.
func cardID(step, item string) string { return step + "\x00" + item }
