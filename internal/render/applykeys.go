package render

import (
	"io"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/look"
)

// screenKeys are the keys applying's screen reads while it's up, and what
// they've done: listen reads them, in the terminal's raw mode, till the
// stop it returns puts the terminal back; halt stops the run. reading is
// the reading under way; asking is whether the foot asks whether to stop,
// choice the answer its cursor's on; back is how many lines back the log
// is scrolled.
type screenKeys struct {
	listen  func() (<-chan ask.Key, func())
	halt    func()
	reading *reading
	asking  bool
	choice  int
	back    int
}

// reading is keys being read, and what stops it.
type reading struct{ stop func() }

// The keys at the foot of applying's screen: while it reads keys, those
// that scroll and stop; while it asks whether to stop, those that answer;
// without keys, the terminal's interrupt, which stops the run.
var (
	runningKeys  = []look.Key{{Key: "↑↓", Does: "scroll"}, {Key: "q", Does: "stop"}}
	stoppingKeys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "decide"}, {Key: "esc", Does: "keep going"}}
	signalKeys   = []look.Key{{Key: "ctrl+c", Does: "stop"}}
)

// stopAnswers are the answers to whether to stop, and stopIt the one that
// does.
var stopAnswers = []look.Choice{{Label: "Keep going"}, {Label: "Stop"}}

const stopIt = 1

// Keys has applying's screen read keys while it's up, through listen,
// which reads them in the terminal's raw mode till the stop it returns puts
// the terminal back as it was; halt stops the run. q asks at the foot
// whether to stop, and Stop stops the run, as ctrl+c does at once; ↑ and ↓
// scroll the log.
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
// run: ctrl+c at once; q asks first, at the foot; ↑ and ↓ scroll the log a
// line, back as far as its slots have lines, forth to the newest.
func (p *Pretty) press(k ask.Key) (halt bool) {
	switch {
	case k.Is("ctrl+c"):
		return true
	case p.keys.asking:
		return p.answer(k)
	case k.Is("q"):
		p.keys.asking, p.keys.choice = true, 0
	case k.Is("up"):
		p.keys.back = min(p.keys.back+1, p.scrollable())
	case k.Is("down"):
		p.keys.back = max(p.keys.back-1, 0)
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

// scrollable is how far back the log scrolls, at the window as it is: till
// the slot with the most lines shows its first.
func (p *Pretty) scrollable() int {
	w, h := p.window()
	steps, rooms := p.logRooms(h - len(p.screenHead(w)) - len(p.screenFoot(w)))
	most := 0
	for i, step := range steps {
		most = max(most, len(p.output[step])-(rooms[i]-1))
	}
	return most
}

// scrolled are room of lines, ending back lines before the newest, or
// the first room, scrolled back past them.
func scrolled(lines []string, room, back int) []string {
	end := max(len(lines)-back, min(room, len(lines)))
	return lines[max(end-room, 0):end]
}

// footKeys are the keys at the screen's foot, as it reads them.
func (p *Pretty) footKeys() string {
	switch {
	case p.keys.asking:
		return look.Keys(stoppingKeys...)
	case p.keys.listen != nil:
		return look.Keys(runningKeys...)
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
// face had going: its own screen, the cursor shown, the keys no longer
// read and the terminal's mode as it was. It draws nothing after, and
// what it held isn't written.
func (p *Pretty) Unwind() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.aside = true
	p.out.Reset()
	p.unlisten()
	var back strings.Builder
	if p.onScreen {
		back.WriteString(altOff)
		p.onScreen = false
	}
	if p.hidden {
		back.WriteString(showCursor)
		p.hidden = false
	}
	_, _ = io.WriteString(p.w, back.String())
}
