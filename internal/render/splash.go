package render

import (
	"context"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/look"
)

// Splash is kit powering on, before the boot log (kit-look, rule 22): the
// whole window, the bare machine's noise resolving into the wordmark, a cell
// at a time; "computers are fun" typed under it; then "press enter", which
// starts the boot. Any key before then skips to the prompt; esc stops.
type Splash struct {
	look  look.Boot
	every time.Duration

	mu      sync.Mutex
	frame   int
	ready   bool
	stopped bool
	changed chan struct{}
}

// splashFrames is how many frames the noise takes to resolve; Tagline is
// what types out under the wordmark.
const (
	splashFrames = 64
	Tagline      = "computers are fun"
)

// The wordmark, as the splash draws it: its pixels four cells wide and two
// high, so they're square.
const (
	bigWidth, bigHeight = 17 * 4, 6 * 2
)

// NewSplash is the splash for a terminal whose background is dark or not.
func NewSplash(dark bool) *Splash {
	return &Splash{look: look.NewBoot(dark), every: 38 * time.Millisecond, changed: make(chan struct{}, 1)}
}

// Play plays the splash at t, the whole window, till enter is pressed at
// its end; then the window's own screen comes back as it was. A terminal
// too small for the wordmark skips it.
func (s *Splash) Play(ctx context.Context, t ask.Terminal) error {
	if t.Size != nil {
		if w, h := t.Size(); w < bigWidth+4 || h < bigHeight+8 {
			return nil
		}
	}
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		tick := time.NewTicker(s.every)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case <-tick.C:
				s.mu.Lock()
				s.frame++
				s.mu.Unlock()
				select {
				case s.changed <- struct{}{}:
				default:
				}
			}
		}
	}()
	return ask.Live(ctx, t, s)
}

// Stopped is whether esc stopped it, rather than enter.
func (s *Splash) Stopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// noiseGlyphs are what the bare machine's noise is made of.
var noiseGlyphs = []rune(`░▒▓#%&@*+=-:.01/\|<>[]{}~^`)

func smoothstep(a, b, x float64) float64 {
	t := math.Max(0, math.Min(1, (x-a)/(b-a)))
	return t * t * (3 - 2*t)
}

// cell is one of the splash's cells: a glyph, in a strength of amber.
type cell struct {
	r rune
	s look.Strength
}

// Full is the splash at its frame: the noise thinning, the wordmark's cells
// locking in, a hot flash as each does, a band of rows glitching sideways now
// and then; then the tagline typed out, and the prompt, blinking.
func (s *Splash) Full(width, height int) []string {
	s.mu.Lock()
	frame, ready := s.frame, s.ready
	s.mu.Unlock()
	p := math.Min(1, float64(frame)/float64(splashFrames-1))
	if ready {
		p = 1
	}
	ox, oy := (width-bigWidth)/2, (height-bigHeight)/2-1
	locks := rand.New(rand.NewPCG(7, 7))
	flicker := rand.New(rand.NewPCG(uint64(frame), 9))
	density := 0.5 * (1 - smoothstep(0.2, 0.85, p))
	grid := make([][]cell, height)
	for y := range grid {
		grid[y] = make([]cell, width)
		for x := range grid[y] {
			lock := 0.22 + 0.55*locks.Float64()
			mx, my := x-ox, y-oy
			in := my >= 0 && my < bigHeight && mx >= 0 && mx < bigWidth && look.WordmarkPixel(mx/4, my/2)
			glyph := noiseGlyphs[flicker.IntN(len(noiseGlyphs))]
			switch {
			case in && p >= lock+0.05:
				grid[y][x] = cell{'█', look.Bright}
			case in && p >= lock:
				grid[y][x] = cell{'█', look.Glowing}
			case in:
				if flicker.Float64() < 0.75 {
					grid[y][x] = cell{glyph, []look.Strength{look.Bright, look.Glowing}[flicker.IntN(2)]}
				}
			case flicker.Float64() < density:
				grid[y][x] = cell{glyph, []look.Strength{look.Dimmed, look.Dimmed, look.Dimmed, look.Middle}[flicker.IntN(4)]}
			}
		}
	}
	if p > 0.3 && p < 0.8 && flicker.Float64() < 0.35 {
		y0, n, shift := flicker.IntN(height), 1+flicker.IntN(3), flicker.IntN(17)-8
		for y := y0; y < min(y0+n, height); y++ {
			row := make([]cell, width)
			for x := range width {
				if sx := x - shift; sx >= 0 && sx < width {
					row[x] = grid[y][sx]
				}
			}
			grid[y] = row
		}
	}
	put := func(y int, text string, strength look.Strength) {
		x := ox + (bigWidth-len([]rune(text)))/2
		for i, r := range []rune(text) {
			if y >= 0 && y < height && x+i >= 0 && x+i < width {
				grid[y][x+i] = cell{r, strength}
			}
		}
	}
	typed := []rune(Tagline)
	put(oy+bigHeight+2, string(typed[:int(float64(len(typed))*smoothstep(0.84, 0.97, p))]), look.Middle)
	if p >= 1 {
		put(oy+bigHeight+4, "press enter  ", look.Dimmed)
		if (frame/12)%2 == 0 {
			x := ox + (bigWidth-len("press enter  "))/2 + len("press enter ")
			if y := oy + bigHeight + 4; y < height && x < width {
				grid[y][x] = cell{'▌', look.Bright}
			}
		}
	}
	lines := make([]string, height)
	for y, row := range grid {
		var b strings.Builder
		for x := 0; x < len(row); {
			run := x
			for run < len(row) && row[run].s == row[x].s {
				run++
			}
			var text strings.Builder
			for _, c := range row[x:run] {
				if c.r == 0 {
					c.r = ' '
				}
				text.WriteRune(c.r)
			}
			b.WriteString(s.look.Paint(row[x].s, text.String()))
			x = run
		}
		lines[y] = b.String()
	}
	return lines
}

// View is nothing in place: the splash has the whole window.
func (s *Splash) View(int, int) []string { return nil }

// Key skips to the prompt; at the prompt, enter ends it, and esc stops.
func (s *Splash) Key(k ask.Key) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k.Is("esc", "ctrl+c") {
		s.stopped = true
		return true
	}
	if !s.ready && s.frame < splashFrames-1 {
		s.ready = true
		return false
	}
	return k.Is("enter")
}

func (s *Splash) Leaves(int) []string { return nil }

func (s *Splash) Changes() <-chan struct{} { return s.changed }
