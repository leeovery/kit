package render

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/look"
)

// Applying at a terminal big enough takes a screen of its own while it runs
// (kit-look, round 6): the terminal's alternate screen, drawn whole each
// time, at the window's full size. Its header is the wordmark, as the
// arrival leaves it, with what runs, the bar and the counts beside it; then
// every step in a grid, by area; then the log, a slot a step at work; then
// what needs you, and the keys, which it reads while it's up, the terminal
// in its raw mode (applykeys.go). When the run ends the screen goes, and
// its record is printed under the run's heading.

// The alternate screen, which applying takes while it runs, and gives back.
const (
	altOn  = "\x1b[?1049h"
	altOff = "\x1b[?1049l"
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
// screen: its header, its grid, the keys, and logLeast lines of log.
func (p *Pretty) screenFits(w, h int) bool {
	if w < screenWidth || h < screenHeight {
		return false
	}
	return h-len(p.screenHead(w))-2 >= logLeast
}

// drawScreen draws applying's screen, w by h, over the last: from the
// window's top, each line cleared past its end, in one write; no more
// often than drawEvery. It takes the alternate screen first, the live part
// drawn in place taken down, and reads keys while it has it.
func (p *Pretty) drawScreen(w, h int) {
	switch {
	case !p.onScreen:
		p.clearLive()
		p.listen()
		p.write(altOn + hideCursor)
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
// terminal's own screen shows again, as it was, and keys aren't read.
func (p *Pretty) leaveScreen() {
	if p.onScreen {
		p.unlisten()
		p.write(altOff)
		p.onScreen = false
	}
}

// screenFrame is applying's screen as it stands, w by h, a line a row: the
// header and the grid, the log in the room they and the foot leave, then
// the foot.
func (p *Pretty) screenFrame(w, h int) []string {
	head, foot := p.screenHead(w), p.screenFoot(w)
	lines := append(append(head, p.logSlots(w, h-len(head)-len(foot))...), foot...)
	frame := make([]string, h)
	for i := range min(h, len(lines)) {
		frame[i] = look.Cut(lines[i], w)
	}
	return frame
}

// screenHead is the screen's header, then its grid, a blank line under
// each: a blank line, then the wordmark, as the arrival leaves it, with
// what runs, the Mac and when beside its first line, the bar its second,
// and the counts its third.
func (p *Pretty) screenHead(w int) []string {
	bar, counts := p.progress()
	line := headline{p.start.Command, p.start.Machine, when(p.start.Time)}
	head := append([]string{""}, look.Head(line.typed(line.length()), bar, counts)...)
	head = append(append(head, ""), look.Grid(w, p.areas()...)...)
	return append(head, "")
}

// areas are the run's steps as the grid has them: by area, in the views'
// order, the boot's first, each as it stands.
func (p *Pretty) areas() []look.Area {
	var areas []look.Area
	for _, area := range viewOrder {
		a := look.Area{Name: area}
		for _, s := range p.start.Steps {
			if viewArea(s.Area) == area {
				a.Cells = append(a.Cells, look.Cell{State: p.standing(s.Name), Name: p.title(s.Name)})
			}
		}
		if len(a.Cells) > 0 {
			areas = append(areas, a)
		}
	}
	return areas
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

// logSlots are the log, n lines, blank where nothing's taken them: each
// slot taken, in order, its step's row, then on a line from its mark the
// lines it printed, the last of them or as far back as the log's scrolled,
// and, once it's finished, what's left of it, which they give way to; a
// blank line between slots.
func (p *Pretty) logSlots(w, n int) []string {
	steps, rooms := p.logRooms(n)
	var out []string
	for i, step := range steps {
		if i > 0 {
			out = append(out, "")
		}
		row := p.listRow(p.step(step))
		left := p.slotLeft(step, rooms[i]-1)
		row.Under = append(look.Output(scrolled(p.output[step], rooms[i]-1-len(left), p.keys.back)...), left...)
		block := look.Timeline("  ", w, row)
		out = append(append(out, block...), make([]string, rooms[i]-len(block))...)
	}
	return append(out, make([]string, max(n-len(out), 0))...)[:max(n, 0)]
}

// slotLeft is what's left of step once it's finished, as applying's list
// has it, in at most room lines: none while it runs, or when the run
// stopping cut it off.
func (p *Pretty) slotLeft(step string, room int) []string {
	f, ok := p.results[step]
	if !ok || wasStopped(f) {
		return nil
	}
	left := p.left(f, p.step(step).Part)
	return left[:min(len(left), room)]
}

// logRooms are the steps in the log's slots, in order, and how many lines
// each has of n: shared evenly, a blank line between, the last taking
// what's left; those there's no room for left out.
func (p *Pretty) logRooms(n int) (steps []string, rooms []int) {
	var taken []string
	for _, s := range p.slots {
		if s.step != "" {
			taken = append(taken, s.step)
		}
	}
	k := len(taken)
	if k == 0 || n <= 0 {
		return nil, nil
	}
	per, used := max((n-(k-1))/k, 1), 0
	for i := range taken {
		if i > 0 {
			used++
		}
		room := per
		if i == k-1 {
			room = n - used
		}
		if room <= 0 {
			break
		}
		rooms = append(rooms, room)
		used += room
	}
	return taken[:len(rooms)], rooms
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
