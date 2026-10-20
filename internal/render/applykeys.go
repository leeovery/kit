package render

import (
	"io"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/look"
)

// screenKeys are the keys applying's screen reads while it's up, and what
// they've done: listen reads them, in the terminal's raw mode, till the
// stop it returns puts the terminal back; halt stops the run. reading is
// the reading under way; asking is whether the left pane asks whether to
// stop, choice the answer its cursor's on.
type screenKeys struct {
	listen  func() (<-chan ask.Key, func())
	halt    func()
	reading *reading
	asking  bool
	choice  int
	// chosen is the row whose lines alone the log shows, "" for everything;
	// raw is whether its raw view shows in the log's place. anchored is
	// whether the log's scrolled back, top the first of its lines shown
	// then; else it follows the newest.
	chosen   string
	raw      bool
	anchored bool
	top      int
	// Where the screen was last drawn: which choice each line of the left
	// pane is, by its line in the pane; the screen's line the panes start
	// at, and the log; the column they part at.
	choices      map[int]string
	paneAt       int
	logAt, split int
	// away is whether the window's out of focus; focused when it last came
	// into it.
	away    bool
	focused time.Time
}

// reading is keys being read, and what stops it.
type reading struct{ stop func() }

// The keys at the foot of applying's left pane: while it reads them; while
// another card waits, those but tab, which the card up says moves on to it;
// while it asks whether to stop, those that answer; without keys, the
// terminal's interrupt, which stops the run.
var (
	runningKeys  = []look.Key{{Key: "tab", Does: "next row"}, {Key: "↑↓", Does: "scroll"}, {Key: "q", Does: "stop"}}
	cardingKeys  = []look.Key{{Key: "↑↓", Does: "scroll"}, {Key: "q", Does: "stop"}}
	stoppingKeys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "decide"}, {Key: "esc", Does: "keep going"}}
	signalKeys   = []look.Key{{Key: "ctrl+c", Does: "stop"}}
)

// doneKey is the key the card shows saying a step by hand's done, when kit
// can't tell by itself; enter says so too, unshown. There's no putting one
// off: it's done, or the run's stopped.
var doneKey = look.Key{Key: "space", Does: "when it's done"}

// nextKey is the key the card up shows moving on to the next card
// waiting, while one is: the card left stays to do, and comes back.
var nextKey = look.Key{Key: "tab", Does: "next card, this one comes back"}

// cardKeys are the keys card c shows answering it: none when kit tells by
// itself that it's done; and the key moving on to the next card, while one
// waits.
func cardKeys(c event.NeedsYou) []look.Key {
	var keys []look.Key
	if !c.Tells {
		keys = append(keys, doneKey)
	}
	if c.More > 0 {
		keys = append(keys, nextKey)
	}
	return keys
}

// wheelLines are how many lines a turn of the mouse's wheel scrolls the
// log.
const wheelLines = 3

// focusSettles is how long after the window comes into focus a click is
// taken as the one that brought it there, and not as a choice.
const focusSettles = 300 * time.Millisecond

// stopAnswers are the answers to whether to stop, and stopIt the one that
// does.
var stopAnswers = []look.Choice{{Label: "Keep going"}, {Label: "Stop"}}

const stopIt = 1

// Keys has applying's screen read keys while it's up, and the mouse's
// clicks and wheel, through listen, which reads them in the terminal's raw
// mode till the stop it returns puts the terminal back as it was; halt
// stops the run. q asks whether to stop, and Stop stops the run, as ctrl+c
// does at once; tab and shift+tab choose the next row and the one before,
// a click on one chooses it, and esc chooses everything; with a row
// chosen, r shows its raw view, and esc or r goes back to the log; the
// arrows, the page keys, home, end and the wheel scroll the log; with a
// card up, space and enter say it's done, or, a choice, its keys answer
// it, and, while another card waits, tab moves on to it instead of
// choosing a row. With the to-dos at the run's end up, there are no rows to
// choose, and Stop stops there, what's left staying to do.
func (p *Pretty) Keys(listen func() (<-chan ask.Key, func()), halt func()) *Pretty {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys.listen, p.keys.halt = listen, halt
	return p
}

// Hands has applying's screen pass what the person answers the card of a
// step by hand with (space, enter, a choice's keys, stopping at the to-dos)
// to answer, which passes it to the step.
func (p *Pretty) Hands(answer func(step string, a hand.Answer) bool) *Pretty {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hands = answer
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
// run: ctrl+c at once; q asks first; a choice's keys, space and enter
// answer the card up, and tab moves on from it while another card waits;
// else tab, and shift+tab, esc and a click, choose what the log shows, r
// and esc whether a row's raw view shows in its place; the rest scroll it.
// The window's focus coming and going is noted, for a click.
func (p *Pretty) press(k ask.Key) (halt bool) {
	switch {
	case k.Is("ctrl+c"):
		return true
	case k.Is("focus in"):
		p.keys.away, p.keys.focused = false, p.now()
	case k.Is("focus out"):
		p.keys.away = true
	case p.keys.asking:
		return p.answer(k)
	case p.answerChoice(k):
	case k.Is(" ", "enter"):
		p.answerCard()
	case k.Is("q"):
		p.keys.asking, p.keys.choice = true, 0
	case k.Is("tab") && p.cardsWaiting() && p.hands != nil:
		p.hands(p.card.Step, hand.Next)
	case k.Is("tab"):
		p.chooseNext(1)
	case k.Is("shift+tab"):
		p.chooseNext(-1)
	case k.Is("r"):
		p.toggleRaw()
	case k.Is("esc"):
		p.back()
	case k.Is("click"):
		p.click(k.X, k.Y)
	default:
		p.scrollKey(k)
	}
	return false
}

// answerCard passes the card up's step that it's done, when kit can't tell
// by itself and it isn't a choice: nothing with no card up.
func (p *Pretty) answerCard() {
	if p.card == nil || p.hands == nil || p.card.Tells || len(p.card.Keys) > 0 || p.card.Answering != "" {
		return
	}
	p.hands(p.card.Step, hand.Done)
}

// cardsWaiting is whether another card waits after the card up, mid-run,
// for tab to move on to: never one of the to-dos at the run's end, worked
// through in kit's order.
func (p *Pretty) cardsWaiting() bool {
	return p.card != nil && !p.listing(p.card.Step) && p.card.More > 0
}

// answerChoice passes k to the card up's step, when it's one of its
// choice's keys, and reports whether it was: while it's being answered,
// they do nothing.
func (p *Pretty) answerChoice(k ask.Key) bool {
	if p.card == nil || p.hands == nil {
		return false
	}
	for _, c := range p.card.Keys {
		if k.Is(c.Key) {
			if p.card.Answering == "" {
				p.hands(p.card.Step, hand.Answer(c.Key))
			}
			return true
		}
	}
	return false
}

// answer is what a key does to the question whether to stop, and whether
// it's stopping the run: ↑ and ↓ choose, enter decides, esc keeps going.
// Stop with one of the to-dos at the run's end up stops there, the run
// carrying on to its end, what's left staying to do.
func (p *Pretty) answer(k ask.Key) (halt bool) {
	switch {
	case k.Is("up", "down"):
		p.keys.choice = (p.keys.choice + 1) % len(stopAnswers)
	case k.Is("enter"):
		p.keys.asking = false
		return p.keys.choice == stopIt && !p.stopHere()
	case k.Is("esc"):
		p.keys.asking = false
	}
	return false
}

// stopHere has the to-dos stop where they are, while they're being worked
// through, a to-do up or the next coming, and reports whether they took
// it.
func (p *Pretty) stopHere() bool {
	return p.todos != nil && p.todos.Up >= 0 && p.hands != nil && p.hands(p.todos.Step, hand.Stop)
}

// choose has the log show row's lines alone, or every line for "", from
// the newest: a row's raw view, while it's open, shows row's instead, and
// goes with everything.
func (p *Pretty) choose(row string) {
	p.keys.chosen, p.keys.anchored = row, false
	p.keys.raw = p.keys.raw && row != ""
}

// chooseNext chooses the row by places after the one chosen, round from
// the last to everything and back: none while the to-dos have the left
// pane.
func (p *Pretty) chooseNext(by int) {
	if p.todos != nil {
		return
	}
	choices := []string{""}
	for _, r := range p.shownRows() {
		choices = append(choices, r.name)
	}
	at := 0
	for i, c := range choices {
		if c == p.keys.chosen {
			at = i
		}
	}
	p.choose(choices[((at+by)%len(choices)+len(choices))%len(choices)])
}

// click is the mouse's left button pressed at column x of the screen's
// line y: on Everything or a row, in the left pane, it chooses it, or, on
// the row chosen, everything; anywhere else, nothing. A click while the
// window's out of focus, or just after it's come into it, brings it
// forward, and does nothing more.
func (p *Pretty) click(x, y int) {
	if p.keys.away || p.now().Sub(p.keys.focused) < focusSettles || x >= p.keys.split {
		return
	}
	row, ok := p.keys.choices[y-p.keys.paneAt]
	switch {
	case !ok:
	case row == p.keys.chosen:
		p.choose("")
	default:
		p.choose(row)
	}
}

// scrollKey scrolls the log as k does: ↑ and ↓ a line, the page keys a
// page, home to its first line, end to follow its newest; the wheel, over
// the log, a few lines a turn.
func (p *Pretty) scrollKey(k ask.Key) {
	n, room := p.logSize()
	page := max(room-backFoot, 1)
	over := k.X > p.keys.split && k.Y >= p.keys.logAt
	switch {
	case k.Is("up"):
		p.scroll(n, room, 1)
	case k.Is("down"):
		p.scroll(n, room, -1)
	case k.Is("pgup"):
		p.scroll(n, room, page)
	case k.Is("pgdown"):
		p.scroll(n, room, -page)
	case k.Is("home"):
		p.scroll(n, room, n)
	case k.Is("end"):
		p.keys.anchored = false
	case k.Is("wheel up") && over:
		p.scroll(n, room, wheelLines)
	case k.Is("wheel down") && over:
		p.scroll(n, room, -wheelLines)
	}
}

// scroll scrolls the log, n lines with room for room, by lines: back for
// more than none, forth for fewer; back as far as its first line, forth to
// where it follows the newest again. Where all of it shows, nothing.
func (p *Pretty) scroll(n, room, by int) {
	if n <= room {
		return
	}
	top := n - room
	if p.keys.anchored {
		top = p.keys.top
	}
	top = max(top-by, 0)
	p.keys.anchored, p.keys.top = top < n-room, top
}

// logSize is how many lines the log has, as it's chosen, or the raw view
// in its place, and how many it
// has room for, in the window as it is.
func (p *Pretty) logSize() (n, room int) {
	w, h := p.window()
	top, _ := p.paneTop(w - splitAt(w) - 1)
	return len(p.paneBody()), h - len(p.header()) - 1 - len(top)
}

// footKeys are the keys at the left pane's foot, as it reads them.
func (p *Pretty) footKeys() string {
	switch {
	case p.keys.asking:
		return look.Hints(stoppingKeys...)
	case p.keys.listen != nil && p.todos != nil:
		return look.Hints(toDoKeys...)
	case p.keys.listen != nil && p.cardsWaiting():
		return look.Hints(cardingKeys...)
	case p.keys.listen != nil:
		return look.Hints(runningKeys...)
	}
	return look.Hints(signalKeys...)
}

// stopQuestion is the question whether to stop, w wide: what stops where it
// is, the steps at work, and the answers.
func (p *Pretty) stopQuestion(w int) []string {
	var says string
	switch names := p.titlesAtWork(); len(names) {
	case 0:
	case 1:
		says = look.Muted(names[0] + " stops where it is")
	default:
		says = look.Muted(inWords(names) + " stop where they are")
	}
	return look.Timeline("  ", w, look.Row{State: look.NeedsYou, Name: "Stop applying?", Says: says, Under: look.Answers(stopAnswers, p.keys.choice)})
}

// titlesAtWork are the titles of the steps at work, in the order they
// started.
func (p *Pretty) titlesAtWork() []string {
	var titles []string
	for _, r := range p.running {
		if p.atWork(r) {
			titles = append(titles, r.title)
		}
	}
	return titles
}

// Unwind puts the terminal back at once, for kit to panic on, whatever the
// face had going: the mouse and the window's focus no longer reported, its
// own screen, the cursor shown, the keys no longer read and the terminal's
// mode as it was. It draws nothing after, and what it held isn't written.
func (p *Pretty) Unwind() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.aside = true
	p.out.Reset()
	var back strings.Builder
	if p.keys.reading != nil {
		back.WriteString(focusOff + mouseOff)
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
