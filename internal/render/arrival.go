package render

import (
	"context"
	"image/color"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/look"
)

// Arrival is the boot arriving in the terminal it handed over to (kit-look,
// rule 22): a quiet screen, "hello again" in amber, the new Mac greeting you
// as if it knew you, as by then it does, till a key. Then the decode again,
// its noise turning from amber into kit's colours, the wordmark locking in
// kit's gradient with a white flash; then the wordmark shrinking into the
// header's place, the header's lines typing in beside it; then the window
// gives way to the real screen, its header in just that place. A key while
// it plays skips to the header; ctrl+c stops.
type Arrival struct {
	boot            look.Boot
	what, mac, when string
	every           time.Duration
	keep            bool

	mu                        sync.Mutex
	phase                     arrivalPhase
	blink                     bool
	frame, step, typed        int
	started, skipped, stopped bool
	landed                    bool
	keyed, skip               chan struct{}
	changed                   chan struct{}
}

// arrivalPhase is where the arrival's got to.
type arrivalPhase int

const (
	greeting arrivalPhase = iota
	decoding
	shrinking
	landing
)

// The arrival's timing: the greeting's cursor blinking; the decode, as many
// frames as the splash's, then held a moment; the wordmark shrinking in
// steps; the header's lines typing in, a character a beat; then held before
// the real screen.
const (
	blinkEvery  = 450 * time.Millisecond
	decodeHold  = 500 * time.Millisecond
	shrinkSteps = 14
	shrinkEvery = 45 * time.Millisecond
	typeEvery   = 25 * time.Millisecond
	landedHold  = 300 * time.Millisecond
)

// flash is the colour a cell of the wordmark flashes as it locks in.
var flash = lipgloss.Color("#FFFFFF")

// NewArrival is the arrival for a terminal whose background is dark or not,
// landing on the header of what runs, on the Mac named mac, at at.
func NewArrival(dark bool, what, mac string, at time.Time) *Arrival {
	return &Arrival{
		boot: look.NewBoot(dark), what: what, mac: mac, when: when(at), every: 38 * time.Millisecond,
		keyed: make(chan struct{}), skip: make(chan struct{}), changed: make(chan struct{}, 1),
	}
}

// KeepHeader has the arrival leave the header it lands on in the window,
// for what runs to carry on under it: the real screen, with nothing
// between.
func (a *Arrival) KeepHeader() *Arrival {
	a.keep = true
	return a
}

// Play plays the arrival at t, the whole window, till it lands; then the
// window's own screen comes back. A terminal too small for the wordmark
// skips it.
func (a *Arrival) Play(ctx context.Context, t ask.Terminal) error {
	if t.Size != nil {
		if w, h := t.Size(); w < bigWidth+4 || h < bigHeight+8 {
			return nil
		}
	}
	quit := make(chan struct{})
	defer close(quit)
	go a.animate(quit)
	return ask.Live(ctx, t, a)
}

// Landed is whether it played to the header, kept in the window when it
// keeps it.
func (a *Arrival) Landed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.landed
}

// Stopped is whether ctrl+c stopped it.
func (a *Arrival) Stopped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopped
}

// animate plays the arrival's timeline: the greeting till a key, the
// decode, the shrink, the header typed; a key skipping to its end.
func (a *Arrival) animate(quit <-chan struct{}) {
	set := func(f func()) {
		a.mu.Lock()
		f()
		a.mu.Unlock()
		select {
		case a.changed <- struct{}{}:
		default:
		}
	}
	// beat waits d, and reports whether to go on: not once it's stopped or
	// skipped.
	beat := func(d time.Duration) bool {
		select {
		case <-quit:
			return false
		case <-a.skip:
			return false
		case <-time.After(d):
			return true
		}
	}
	for waiting := true; waiting; {
		select {
		case <-quit:
			return
		case <-a.keyed:
			waiting = false
		case <-time.After(blinkEvery):
			set(func() { a.blink = !a.blink })
		}
	}
	on := true
	for f := 0; on && f < splashFrames; f++ {
		set(func() { a.phase, a.frame = decoding, f })
		on = beat(a.every)
	}
	on = on && beat(decodeHold)
	for s := 0; on && s < shrinkSteps; s++ {
		set(func() { a.phase, a.step = shrinking, s })
		on = beat(shrinkEvery)
	}
	typing := a.typing()
	for n := 0; on && n <= typing; n++ {
		set(func() { a.phase, a.typed = landing, n })
		on = beat(typeEvery)
	}
	if on {
		beat(landedHold)
	}
	select {
	case <-quit:
		return
	default:
	}
	set(func() { a.phase, a.typed, a.landed = landing, typing, true })
	close(a.changed)
}

// meta are the header's lines beside the wordmark: what runs, the Mac and
// when.
func (a *Arrival) meta() []string { return []string{a.what, a.mac, a.when} }

// typing is how many beats the header's lines take to type in, each a few
// beats behind the line above.
func (a *Arrival) typing() int {
	n := 0
	for r, m := range a.meta() {
		n = max(n, r*3+len([]rune(m)))
	}
	return n
}

// Full is the arrival as it stands: the greeting, the decode, the shrink or
// the header landing.
func (a *Arrival) Full(width, height int) []string {
	a.mu.Lock()
	phase, blink, frame, step, typed := a.phase, a.blink, a.frame, a.step, a.typed
	a.mu.Unlock()
	switch phase {
	case greeting:
		return a.greeting(width, height, blink)
	case decoding:
		return a.decode(width, height, frame)
	case shrinking:
		return a.shrink(width, height, step)
	}
	return a.header(typed)
}

// greeting is the quiet screen: "hello again" in the middle, and under it
// "press any key", its cursor blinking.
func (a *Arrival) greeting(width, height int, blink bool) []string {
	g := newHues(width, height)
	g.text((width-len("hello again"))/2, height/2-1, "hello again", hue{s: look.Bright})
	prompt := "press any key  "
	x := (width - len(prompt)) / 2
	g.text(x, height/2+1, prompt, hue{s: look.Dimmed})
	if blink {
		g.set(x+len("press any key "), height/2+1, hue{r: '▌', s: look.Bright})
	}
	return g.lines(a.boot)
}

// decode is the decode at its frame: the noise thinning and turning from
// amber into kit's colours, the wordmark's cells locking in kit's gradient,
// a white flash as each does, a band of rows glitching sideways now and
// then.
func (a *Arrival) decode(width, height, frame int) []string {
	p := math.Min(1, float64(frame)/float64(splashFrames-1))
	ox, oy := (width-bigWidth)/2, (height-bigHeight)/2-1
	locks := rand.New(rand.NewPCG(7, 7))
	flicker := rand.New(rand.NewPCG(uint64(frame), 11))
	colourful := smoothstep(0.08, 0.45, p)
	density := 0.5 * (1 - smoothstep(0.2, 0.85, p))
	noise := func(glyph rune, strengths ...look.Strength) hue {
		if flicker.Float64() < colourful {
			return hue{r: glyph, c: look.Colours[flicker.IntN(len(look.Colours))]}
		}
		return hue{r: glyph, s: strengths[flicker.IntN(len(strengths))]}
	}
	g := newHues(width, height)
	for y := range height {
		for x := range width {
			lock := 0.22 + 0.55*locks.Float64()
			mx, my := x-ox, y-oy
			in := my >= 0 && my < bigHeight && mx >= 0 && mx < bigWidth && look.WordmarkPixel(mx/4, my/2)
			glyph := noiseGlyphs[flicker.IntN(len(noiseGlyphs))]
			switch {
			case in && p >= lock+0.05:
				g.set(x, y, hue{r: '█', c: look.Gradient(float64(mx) / float64(bigWidth-1))})
			case in && p >= lock:
				g.set(x, y, hue{r: '█', c: flash})
			case in:
				if flicker.Float64() < 0.75 {
					g.set(x, y, noise(glyph, look.Bright, look.Glowing))
				}
			case flicker.Float64() < density:
				g.set(x, y, noise(glyph, look.Dimmed, look.Dimmed, look.Dimmed, look.Middle))
			}
		}
	}
	if p > 0.3 && p < 0.8 && flicker.Float64() < 0.35 {
		g.glitch(flicker.IntN(height), 1+flicker.IntN(3), flicker.IntN(17)-8)
	}
	return g.lines(a.boot)
}

// shrink is the wordmark on its way from the middle of the window to the
// header's place, at step: four sizes, each a little nearer.
func (a *Arrival) shrink(width, height, step int) []string {
	t := smoothstep(0, 1, float64(step)/float64(shrinkSteps-1))
	size := 4 - int(t*3+0.5)
	fromX, fromY := float64((width-bigWidth)/2), float64((height-bigHeight)/2-1)
	x, y := int(fromX+(2-fromX)*t+0.5), int(fromY+(1-fromY)*t+0.5)
	g := newHues(width, height)
	g.wordmark(x, y, size)
	return g.lines(a.boot)
}

// header is the header the arrival lands on, as the real screen has it: a
// blank line, then the wordmark, its lines typed in beside it up to typed
// beats, each a few beats behind the line above.
func (a *Arrival) header(typed int) []string {
	beside := make([]string, 3)
	for r, m := range a.meta() {
		text := []rune(m)
		text = text[:min(len(text), max(typed-r*3, 0))]
		beside[r] = look.Muted(string(text))
		if r == 0 {
			beside[r] = look.Strong(string(text))
		}
	}
	return append([]string{""}, look.Head(beside...)...)
}

// View is nothing in place: the arrival has the whole window.
func (a *Arrival) View(int, int) []string { return nil }

// Key starts the decode, at the greeting; while it plays, skips to the
// header; ctrl+c stops it.
func (a *Arrival) Key(k ask.Key) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case k.Is("ctrl+c"):
		a.stopped = true
		return true
	case !a.started:
		a.started = true
		close(a.keyed)
	case !a.skipped:
		a.skipped = true
		close(a.skip)
	}
	return false
}

// Leaves is the header it landed on, when it keeps it.
func (a *Arrival) Leaves(int) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.keep || !a.landed {
		return nil
	}
	return a.header(a.typing())
}

func (a *Arrival) Changes() <-chan struct{} { return a.changed }

// hue is one of the arrival's cells: a glyph in a strength of amber, or in
// a colour of kit's.
type hue struct {
	r rune
	s look.Strength
	c color.Color
}

// same is whether h and o paint alike.
func (h hue) same(o hue) bool {
	if h.c == nil || o.c == nil {
		return h.c == nil && o.c == nil && h.s == o.s
	}
	r1, g1, b1, _ := h.c.RGBA()
	r2, g2, b2, _ := o.c.RGBA()
	return r1 == r2 && g1 == g2 && b1 == b2
}

// hues are a window of cells.
type hues [][]hue

func newHues(width, height int) hues {
	g := make(hues, height)
	for y := range g {
		g[y] = make([]hue, width)
	}
	return g
}

func (g hues) set(x, y int, h hue) {
	if y >= 0 && y < len(g) && x >= 0 && x < len(g[y]) {
		g[y][x] = h
	}
}

func (g hues) text(x, y int, text string, h hue) {
	for i, r := range []rune(text) {
		h.r = r
		g.set(x+i, y, h)
	}
}

// glitch shifts n rows from y0 sideways by shift.
func (g hues) glitch(y0, n, shift int) {
	for y := y0; y < min(y0+n, len(g)); y++ {
		row := make([]hue, len(g[y]))
		for x := range row {
			if sx := x - shift; sx >= 0 && sx < len(row) {
				row[x] = g[y][sx]
			}
		}
		g[y] = row
	}
}

// wordmark draws the wordmark with its top left at x0, y0, each of its
// pixels size cells wide and size half-rows high, along kit's gradient: at
// size 1, as the header has it.
func (g hues) wordmark(x0, y0, size int) {
	for r := range (6*size + 1) / 2 {
		for c := range 17 * size {
			px := c / size
			top, bottom := look.WordmarkPixel(px, 2*r/size), look.WordmarkPixel(px, (2*r+1)/size)
			var glyph rune
			switch {
			case top && bottom:
				glyph = '█'
			case top:
				glyph = '▀'
			case bottom:
				glyph = '▄'
			default:
				continue
			}
			g.set(x0+c, y0+r, hue{r: glyph, c: look.Gradient(float64(px) / 16)})
		}
	}
}

// lines are the cells painted, a line a row, each run of cells alike
// painted together.
func (g hues) lines(b look.Boot) []string {
	out := make([]string, len(g))
	for y, row := range g {
		var line strings.Builder
		for x := 0; x < len(row); {
			run := x
			var text strings.Builder
			for run < len(row) && row[run].same(row[x]) {
				r := row[run].r
				if r == 0 {
					r = ' '
				}
				text.WriteRune(r)
				run++
			}
			if row[x].c != nil {
				line.WriteString(look.Paint(row[x].c, text.String()))
			} else {
				line.WriteString(b.Paint(row[x].s, text.String()))
			}
			x = run
		}
		out[y] = line.String()
	}
	return out
}
