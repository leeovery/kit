package render

import (
	"io"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/look"
)

// screenKeys are the keys applying's screen reads while it's up, and what
// they've done: listen reads them, in the terminal's raw mode, till the
// stop it returns puts the terminal back; halt stops the run. reading is
// the reading under way; asking is whether the foot asks whether to stop,
// choice the answer its cursor's on. focus is the log's panel with the
// focus, by its place; back how many lines back each step's panel is
// scrolled; pinned the steps pinned, in the order pinned.
type screenKeys struct {
	listen  func() (<-chan ask.Key, func())
	halt    func()
	reading *reading
	asking  bool
	choice  int
	focus   int
	back    map[string]int
	pinned  []string
}

// reading is keys being read, and what stops it.
type reading struct{ stop func() }

// The keys at the foot of applying's screen: while it reads keys, those
// that move between the log's panels, scroll them, pin steps and stop,
// and, as the mouse is read, how text is selected; while it asks whether
// to stop, those that answer; without keys, the terminal's interrupt,
// which stops the run.
var (
	selecting    = look.Muted("shift+drag selects")
	stoppingKeys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "decide"}, {Key: "esc", Does: "keep going"}}
	signalKeys   = []look.Key{{Key: "ctrl+c", Does: "stop"}}
)

// runningKeys are the keys while the screen reads them: clicking a step
// pins or unpins it once one's pinned.
func runningKeys(pinned bool) []look.Key {
	click := "a step to pin"
	if pinned {
		click = "a step to pin or unpin"
	}
	return []look.Key{{Key: "tab", Does: "panel"}, {Key: "↑↓", Does: "scroll"}, {Key: "click", Does: click}, {Key: "q", Does: "stop"}}
}

// wheelLines are how many lines a turn of the mouse's wheel scrolls a
// panel.
const wheelLines = 3

// stopAnswers are the answers to whether to stop, and stopIt the one that
// does.
var stopAnswers = []look.Choice{{Label: "Keep going"}, {Label: "Stop"}}

const stopIt = 1

// Keys has applying's screen read keys while it's up, and the mouse's
// clicks and wheel, through listen, which reads them in the terminal's raw
// mode till the stop it returns puts the terminal back as it was; halt
// stops the run. q asks at the foot whether to stop, and Stop stops the
// run, as ctrl+c does at once; tab moves the focus between the log's
// panels, and ↑ and ↓ scroll the one with it, as the wheel scrolls the one
// under the mouse; a click on a step in the grid pins it, or unpins it.
func (p *Pretty) Keys(listen func() (<-chan ask.Key, func()), halt func()) *Pretty {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys.listen, p.keys.halt = listen, halt
	return p
}

// listen starts reading keys, as applying's screen goes up, when the face
// reads them. What was written before goes out first, as the terminal had
// it: its raw mode doesn't take a new line back to the line's start.
func (p *Pretty) listen() {
	if p.keys.listen == nil || p.keys.reading != nil {
		return
	}
	p.flush()
	keys, stop := p.keys.listen()
	r := &reading{stop: stop}
	p.keys.reading = r
	go p.read(r, keys)
}

// unlisten stops reading keys, as applying's screen goes, the terminal put
// back as it was; the question whether to stop goes with them.
func (p *Pretty) unlisten() {
	if r := p.keys.reading; r != nil {
		p.keys.reading = nil
		r.stop()
	}
	p.keys.asking = false
}

// read takes the keys r reads, one at a time, while it's the reading under
// way: each drawn at once, and the run stopped by a key that stops it.
func (p *Pretty) read(r *reading, keys <-chan ask.Key) {
	for k := range keys {
		p.mu.Lock()
		var halt func()
		if p.keys.reading == r {
			if p.press(k) {
				halt = p.keys.halt
			}
			p.drawn = time.Time{}
			p.drawSpinner()
			p.flush()
		}
		p.mu.Unlock()
		if halt != nil {
			halt()
		}
	}
}

// press is what a key does on applying's screen, and whether it stops the
// run: ctrl+c at once; q asks first, at the foot; tab and shift+tab move
// the focus between the log's panels; ↑ and ↓ scroll the one with it a
// line, and the wheel the one under the mouse; a click on a step in the
// grid pins it, or unpins it.
func (p *Pretty) press(k ask.Key) (halt bool) {
	switch {
	case k.Is("ctrl+c"):
		return true
	case p.keys.asking:
		return p.answer(k)
	case k.Is("q"):
		p.keys.asking, p.keys.choice = true, 0
	case k.Is("tab"):
		p.moveFocus(1)
	case k.Is("shift+tab"):
		p.moveFocus(-1)
	case k.Is("up"):
		p.scrollFocused(1)
	case k.Is("down"):
		p.scrollFocused(-1)
	case k.Is("wheel up"):
		p.scrollAt(k.Y, wheelLines)
	case k.Is("wheel down"):
		p.scrollAt(k.Y, -wheelLines)
	case k.Is("click"):
		p.click(k.X, k.Y)
	}
	return false
}

// answer is what a key does to the question whether to stop, and whether
// it's stopping: ↑ and ↓ choose, enter decides, esc keeps going.
func (p *Pretty) answer(k ask.Key) (halt bool) {
	switch {
	case k.Is("up", "down"):
		p.keys.choice = (p.keys.choice + 1) % len(stopAnswers)
	case k.Is("enter"):
		p.keys.asking = false
		return p.keys.choice == stopIt
	case k.Is("esc"):
		p.keys.asking = false
	}
	return false
}

// focused is the log's panel with the focus, of n: the one it was moved
// to, or the last, when there are fewer now.
func (p *Pretty) focused(n int) int { return min(p.keys.focus, n-1) }

// moveFocus moves the focus by places among the log's panels, round from
// the last to the first and back.
func (p *Pretty) moveFocus(by int) {
	w, h := p.window()
	if n := len(p.layout(w, h).steps); n > 0 {
		p.keys.focus = ((p.focused(n)+by)%n + n) % n
	}
}

// scrollFocused scrolls the log's panel with the focus by lines.
func (p *Pretty) scrollFocused(by int) {
	w, h := p.window()
	l := p.layout(w, h)
	if len(l.steps) > 0 {
		p.scroll(l, p.focused(len(l.steps)), by)
	}
}

// scrollAt scrolls the log's panel at the screen's line y by lines: off
// the panels, nothing.
func (p *Pretty) scrollAt(y, by int) {
	w, h := p.window()
	l := p.layout(w, h)
	if i, ok := l.panelAt(y); ok {
		p.scroll(l, i, by)
	}
}

// scroll scrolls the panel at i of the log, as l lays it out, by lines:
// back for more than none, forth for fewer; back as far as its first line,
// forth to its newest.
func (p *Pretty) scroll(l screenLayout, i, by int) {
	step, room := l.steps[i], l.rooms[i]-1
	most := max(len(p.output[step])-(room-len(p.slotLeft(step, room))), 0)
	if p.keys.back == nil {
		p.keys.back = map[string]int{}
	}
	p.keys.back[step] = max(min(min(p.keys.back[step], most)+by, most), 0)
}

// scrolled is the window of n lines room shows, ending back lines before
// the newest, or the first room, scrolled back past them: where it starts,
// and where it ends.
func scrolled(n, room, back int) (start, end int) {
	end = max(n-back, min(room, n))
	return max(end-room, 0), end
}

// click is the mouse's left button pressed at column x of the screen's
// line y: on a step in the grid, it pins the step, or unpins it; anywhere
// else, nothing.
func (p *Pretty) click(x, y int) {
	w, _ := p.window()
	if step, ok := p.stepAt(w, x, y); ok {
		p.pin(step)
	}
}

// pin pins step, after those pinned, its panel taking the focus; or unpins
// it, when it's pinned. With as many pinned as the log has panels, the one
// pinned first gives way.
func (p *Pretty) pin(step string) {
	if i := slices.Index(p.keys.pinned, step); i >= 0 {
		p.keys.pinned = slices.Delete(p.keys.pinned, i, i+1)
		return
	}
	if len(p.keys.pinned) == len(p.slots) {
		p.keys.pinned = slices.Delete(p.keys.pinned, 0, 1)
	}
	p.keys.pinned = append(p.keys.pinned, step)
	p.keys.focus = len(p.keys.pinned) - 1
}

// footKeys are the keys at the screen's foot, as it reads them.
func (p *Pretty) footKeys() string {
	switch {
	case p.keys.asking:
		return look.Keys(stoppingKeys...)
	case p.keys.listen != nil:
		return look.Says(look.Keys(runningKeys(len(p.keys.pinned) > 0)...), selecting)
	}
	return look.Keys(signalKeys...)
}

// stopQuestion is the question whether to stop, w wide, at the screen's
// foot: what stops where it is, the steps at work, and the answers.
func (p *Pretty) stopQuestion(w int) []string {
	var says string
	switch names := p.atWork(); len(names) {
	case 0:
	case 1:
		says = look.Muted(names[0] + " stops where it is")
	default:
		says = look.Muted(strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " stop where they are")
	}
	return look.Timeline("  ", w, look.Row{State: look.NeedsYou, Name: "Stop applying?", Says: says, Under: look.Answers(stopAnswers, p.keys.choice)})
}

// atWork are the titles of the steps at work, in the order they started.
func (p *Pretty) atWork() []string {
	var titles []string
	for _, r := range p.running {
		if r.atWork() {
			titles = append(titles, r.title)
		}
	}
	return titles
}

// Unwind puts the terminal back at once, for kit to panic on, whatever the
// face had going: the mouse no longer reported, its own screen, the cursor
// shown, the keys no longer read and the terminal's mode as it was. It
// draws nothing after, and what it held isn't written.
func (p *Pretty) Unwind() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.aside = true
	p.out.Reset()
	var back strings.Builder
	if p.keys.reading != nil {
		back.WriteString(mouseOff)
	}
	if p.onScreen {
		back.WriteString(altOff)
		p.onScreen = false
	}
	if p.hidden {
		back.WriteString(showCursor)
		p.hidden = false
	}
	_, _ = io.WriteString(p.w, back.String())
	p.unlisten()
}
