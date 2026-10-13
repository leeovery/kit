package render

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
)

// Applying at a terminal big enough takes a screen of its own while it runs
// (kit-look, rounds 6 and 7): the terminal's alternate screen, drawn whole
// each time, at the window's full size. Its header is the wordmark, as the
// arrival leaves it, with what runs, the bar and the counts beside it; then
// every step in a grid, by area; then, between two rules, the log, a panel
// a step at work, or a step pinned; then what needs you, and the keys,
// which it reads while it's up, the terminal in its raw mode, with the
// mouse's clicks and wheel (applykeys.go). When the run ends the screen
// goes, and its record is printed under the run's heading.

// The alternate screen, which applying takes while it runs, and gives back;
// and the mouse's buttons and wheel, reported in SGR's form while it reads
// keys.
const (
	altOn    = "\x1b[?1049h"
	altOff   = "\x1b[?1049l"
	mouseOn  = "\x1b[?1000h\x1b[?1006h"
	mouseOff = "\x1b[?1006l\x1b[?1000l"
)

// The least window applying takes a screen of its own in: below it, the run
// stays in place under its heading. logLeast is the fewest lines the log
// may have under the grid.
const (
	screenWidth  = 100
	screenHeight = 30
	logLeast     = 8
)

// slot is a place in applying's log: the step in it, and the step it took
// the place from, whose lines come back if it gives the place up.
type slot struct{ step, was string }

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

// drawApplying draws applying's live part: its screen, when the window's
// big enough for it; otherwise in place, under its heading.
func (p *Pretty) drawApplying() {
	if w, h := p.window(); p.screenFits(w, h) {
		p.drawScreen(w, h)
		return
	}
	p.leaveScreen()
	p.drawLive(p.applyLive())
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
// screen: its header, its grid, the rules, the keys, and logLeast lines of
// log.
func (p *Pretty) screenFits(w, h int) bool {
	if w < screenWidth || h < screenHeight {
		return false
	}
	return h-len(p.screenHead(w))-4 >= logLeast
}

// drawScreen draws applying's screen, w by h, over the last: from the
// window's top, each line cleared past its end, in one write; no more
// often than drawEvery. It takes the alternate screen first, the live part
// drawn in place taken down, and reads keys while it has it, the mouse's
// among them.
func (p *Pretty) drawScreen(w, h int) {
	switch {
	case !p.onScreen:
		p.clearLive()
		p.listen()
		p.write(altOn + hideCursor)
		if p.keys.reading != nil {
			p.write(mouseOn)
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
// mouse stops being reported before the terminal leaves its raw mode,
// which would show what it reported as typed.
func (p *Pretty) leaveScreen() {
	if !p.onScreen {
		return
	}
	if p.keys.reading != nil {
		p.write(mouseOff)
		p.flush()
	}
	p.unlisten()
	p.write(altOff)
	p.onScreen = false
}

// screenLayout is applying's screen, w by h, as it's laid out: the header
// and the grid, a rule under them; the foot, a rule over it; and the log
// between, its lines, its panels' steps, in order, and how many lines each
// has.
type screenLayout struct {
	head, foot []string
	log        int
	steps      []string
	rooms      []int
}

// layout lays applying's screen out, w by h.
func (p *Pretty) layout(w, h int) screenLayout {
	head := append(p.screenHead(w), look.Edge(w))
	foot := append([]string{look.Edge(w)}, p.screenFoot(w)...)
	log := max(h-len(head)-len(foot), 0)
	steps, rooms := p.panelRooms(log)
	return screenLayout{head: head, foot: foot, log: log, steps: steps, rooms: rooms}
}

// panelAt is the panel at line y of the screen, by its place in the log:
// ok is false off the log's panels.
func (l screenLayout) panelAt(y int) (int, bool) {
	top := len(l.head)
	for i, room := range l.rooms {
		if y >= top && y < top+room {
			return i, true
		}
		top += room
	}
	return 0, false
}

// screenFrame is applying's screen as it stands, w by h, a line a row: the
// header and the grid, the log in the room they and the foot leave, then
// the foot.
func (p *Pretty) screenFrame(w, h int) []string {
	l := p.layout(w, h)
	lines := append(append(l.head, p.logPanels(w, l)...), l.foot...)
	frame := make([]string, h)
	for i := range min(h, len(lines)) {
		frame[i] = look.Cut(lines[i], w)
	}
	return frame
}

// screenHead is the screen's header, then its grid, a blank line under
// it.
func (p *Pretty) screenHead(w int) []string {
	return append(append(p.header(), look.Grid(w, p.areas()...)...), "")
}

// header is the screen's header, a blank line under it: a blank line,
// then the wordmark, as the arrival leaves it, with what runs, the Mac and
// when beside its first line, the bar its second, and the counts its
// third.
func (p *Pretty) header() []string {
	bar, counts := p.progress()
	line := headline{p.start.Command, p.start.Machine, when(p.start.Time)}
	head := append([]string{""}, look.Head(line.typed(line.length()), bar, counts)...)
	return append(head, "")
}

// gridSteps are the run's steps as the grid has them: by area, in the
// views' order, the boot's first.
func (p *Pretty) gridSteps() [][]event.Step {
	var areas [][]event.Step
	for _, area := range viewOrder {
		var steps []event.Step
		for _, s := range p.start.Steps {
			if viewArea(s.Area) == area {
				steps = append(steps, s)
			}
		}
		if len(steps) > 0 {
			areas = append(areas, steps)
		}
	}
	return areas
}

// areas are the grid's areas, each step as it stands, and whether it's
// pinned.
func (p *Pretty) areas() []look.Area {
	var areas []look.Area
	for _, steps := range p.gridSteps() {
		a := look.Area{Name: viewArea(steps[0].Area)}
		for _, s := range steps {
			a.Cells = append(a.Cells, look.Cell{State: p.standing(s.Name), Name: p.title(s.Name), Pinned: slices.Contains(p.keys.pinned, s.Name)})
		}
		areas = append(areas, a)
	}
	return areas
}

// stepAt is the step whose cell in the grid is at column x of the screen's
// line y, w wide: ok is false off every cell.
func (p *Pretty) stepAt(w, x, y int) (string, bool) {
	area, cell, ok := look.GridHit(w, p.areas(), x, y-len(p.header()))
	if !ok {
		return "", false
	}
	return p.gridSteps()[area][cell].Name, true
}

// standing is how the step named step stands as the run goes: finished,
// and how; cut off, or never run, by the run stopping; waiting on you; at
// work; or waiting its turn.
func (p *Pretty) standing(step string) look.State {
	if f, ok := p.results[step]; ok {
		if wasStopped(f) {
			return look.Skipped
		}
		return state(f.Result.State)
	}
	r, ok := p.runningOf(step)
	switch {
	case ok && r.todo != "":
		return look.NeedsYou
	case ok && r.atWork():
		return look.Running
	}
	return look.Queued
}

// logPanels are the log, as l lays it out: a panel each of its steps,
// its rule carrying the step's row, pink when it has the focus, then the
// lines it printed, the last of them or as far back as it's scrolled, and,
// once it's finished, what's left of it, which they give way to; blank
// where nothing's taken them.
func (p *Pretty) logPanels(w int, l screenLayout) []string {
	var out []string
	focus := p.focused(len(l.steps))
	for i, step := range l.steps {
		row := p.listRow(p.step(step))
		var below int
		row.Under, below = p.panelLines(step, l.rooms[i]-1)
		panel := look.Panel(w, i == focus, below, row)
		out = append(append(out, panel...), make([]string, l.rooms[i]-len(panel))...)
	}
	return append(out, make([]string, l.log-len(out))...)
}

// panelLines are what step's panel shows under its row, in room lines: the
// lines it printed, ending as many before its newest as its panel's
// scrolled back, or its first lines, when it has no more; then, once it's
// finished, what's left of it. below is how many of its lines are after
// those shown.
func (p *Pretty) panelLines(step string, room int) (lines []string, below int) {
	left := p.slotLeft(step, room)
	out := p.output[step]
	start, end := scrolled(len(out), room-len(left), p.keys.back[step])
	return append(look.Output(out[start:end]...), left...), len(out) - end
}

// slotLeft is what's left of step once it's finished, as applying's list
// has it, in at most room lines: none while it runs, or when the run
// stopping cut it off.
func (p *Pretty) slotLeft(step string, room int) []string {
	f, ok := p.results[step]
	if !ok || wasStopped(f) {
		return nil
	}
	left := whatsLeft(f.Result, p.step(step).Part, p.start.Time)
	return left[:min(len(left), room)]
}

// panelSteps are the steps the log's panels show: those pinned, in the
// order pinned; else those in its slots, which follow what's running.
func (p *Pretty) panelSteps() []string {
	if len(p.keys.pinned) > 0 {
		return p.keys.pinned
	}
	var taken []string
	for _, s := range p.slots {
		if s.step != "" {
			taken = append(taken, s.step)
		}
	}
	return taken
}

// panelRooms are the steps in the log's panels, in order, and how many
// lines each has of n: shared evenly, the last taking what's left; those
// there's no room for left out.
func (p *Pretty) panelRooms(n int) (steps []string, rooms []int) {
	steps = p.panelSteps()
	k := min(len(steps), n)
	if k == 0 {
		return nil, nil
	}
	rooms = make([]int, k)
	for i := range rooms {
		rooms[i] = n / k
	}
	rooms[k-1] = n - (k-1)*(n/k)
	return steps[:k], rooms
}

// screenFoot is what's at the screen's foot: what needs you, when anything
// does, a blank line above it (macOS asking, then each step waiting on
// you, with what to do), or, while it asks, whether to stop in its place;
// then a blank line, and the keys.
func (p *Pretty) screenFoot(w int) []string {
	if p.keys.asking {
		return append(append([]string{""}, p.stopQuestion(w)...), "", p.footKeys())
	}
	needs := p.askingLines(w)
	for _, r := range p.running {
		if r.todo != "" {
			needs = append(needs, look.Timeline("  ", w, p.runningRow(r, w))...)
		}
	}
	var foot []string
	if len(needs) > 0 {
		foot = append(append(foot, ""), needs...)
	}
	return append(foot, "", p.footKeys())
}

// takeSlot gives step a place in the log, as it starts applying, when it
// hasn't one: it keeps it while it runs, and its lines stay there once it's
// done, till another step needs the place.
func (p *Pretty) takeSlot(step string) {
	if slices.ContainsFunc(p.slots[:], func(s slot) bool { return s.step == step }) {
		return
	}
	if i := p.freeSlot(); i >= 0 {
		p.slots[i] = slot{step: step, was: p.slots[i].step}
	}
}

// freeSlot is the slot a step starting takes: an empty one; else the one
// whose step finished longest ago and didn't fail; else the one whose step
// failed longest ago. -1 while every slot's step runs.
func (p *Pretty) freeSlot() int {
	free := -1
	for i, s := range p.slots {
		if s.step == "" {
			return i
		}
		if _, done := p.results[s.step]; done && (free < 0 || p.givesWay(s.step, p.slots[free].step)) {
			free = i
		}
	}
	return free
}

// givesWay is whether finished step a gives its slot up before finished
// step b: one that didn't fail before one that failed, then the one that
// finished first.
func (p *Pretty) givesWay(a, b string) bool {
	aFailed, bFailed := p.results[a].Result.State == check.Failed, p.results[b].Result.State == check.Failed
	if aFailed != bFailed {
		return bFailed
	}
	return p.finished[a] < p.finished[b]
}

// giveSlotBack gives the place step took in the log back to the step it
// took it from, as it waits on you: it shows at the foot, with what to do.
func (p *Pretty) giveSlotBack(step string) {
	for i, s := range p.slots {
		if s.step == step {
			p.slots[i] = slot{step: s.was}
		}
	}
}
