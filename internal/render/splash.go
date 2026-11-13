package render

import (
	"context"
	"math"
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

// Full is the splash at its frame: the decode in amber alone, then the
// tagline typed out under the wordmark, and the prompt, blinking.
func (s *Splash) Full(width, height int) []string {
	s.mu.Lock()
	frame, ready := s.frame, s.ready
	s.mu.Unlock()
	p := math.Min(1, float64(frame)/float64(splashFrames-1))
	if ready {
		p = 1
	}
	g := decode(width, height, frame, p, false)
	ox, oy := wordmarkAt(width, height)
	put := func(y int, text string, strength look.Strength) {
		g.text(ox+(bigWidth-len([]rune(text)))/2, y, text, hue{s: strength})
	}
	typed := []rune(Tagline)
	put(oy+bigHeight+2, string(typed[:int(float64(len(typed))*smoothstep(0.84, 0.97, p))]), look.Middle)
	if p >= 1 {
		put(oy+bigHeight+4, "press enter  ", look.Dimmed)
		if (frame/12)%2 == 0 {
			g.set(ox+(bigWidth-len("press enter  "))/2+len("press enter "), oy+bigHeight+4, hue{r: '▌', s: look.Bright})
		}
	}
	return g.lines(s.look)
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
