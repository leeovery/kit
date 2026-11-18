package capture

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
)

// Scenario is a clip: kit's screens played in real time, as kit draws
// them, from events and keys scripted into it. Width and Height are the
// terminal it's made for, as its tape records it.
type Scenario struct {
	Name, About   string
	Width, Height int
	// start is the moment its clock reads as it starts, running on from
	// there in real time.
	start time.Time
	// pace is how many times faster than written its own pauses go: kit's
	// animations keep their own time.
	pace float64
	play func(*stage) error
}

// Scenarios are every scenario, by name.
func Scenarios() []Scenario { return scenarios }

// ScenarioNamed is the scenario called name.
func ScenarioNamed(name string) (Scenario, error) {
	for _, s := range scenarios {
		if s.Name == name {
			return s, nil
		}
	}
	return Scenario{}, fmt.Errorf("no scenario %q (there are %s)", name, names(scenarios, func(s Scenario) string { return s.Name }))
}

// Faster is s with its own pauses by times shorter, as a test plays it:
// kit's animations, and the keys waiting on what they show, keep their own
// time.
func (s Scenario) Faster(by float64) Scenario {
	s.pace = by
	return s
}

// Screen is the terminal a scenario plays at: what's drawn goes to Out,
// and Size says how many columns and lines it has.
type Screen struct {
	Out  io.Writer
	Size func() (width, height int)
}

// Play plays s at screen till ctx is done: its cues in turn, then its last
// screen held, still live, telling held once it is. The keys it presses
// reach kit's faces through a pipe, as they'd come from the keyboard, each
// once what it answers is on the screen; the keyboard itself is never read.
// Stopped before its cues are played, it ends there, without an error.
func (s Scenario) Play(ctx context.Context, screen Screen, held func()) error {
	return perform(ctx, screen, &clock{start: s.start, began: time.Now(), pace: cmp.Or(s.pace, 1)}, held, s.play)
}

// perform does play at a stage set at screen, telling the time by c,
// telling held once it holds its last screen; then stops the keys it was
// pressing meanwhile, and waits for them. Stopped, it ends without an
// error.
func perform(ctx context.Context, screen Screen, c *clock, held func(), play func(*stage) error) error {
	in, keys, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("make the keys' pipe: %w", err)
	}
	defer func() { _ = in.Close() }()
	defer func() { _ = keys.Close() }()
	shown := &shown{out: screen.Out, more: make(chan struct{})}
	playing, stop := context.WithCancel(ctx)
	defer stop()
	st := &stage{
		term: ask.Terminal{In: in, Out: shown, Size: screen.Size}, shown: shown,
		ctx: playing, clock: c, keys: keys, held: held,
	}
	err = play(st)
	// What's still waiting to answer the screen, once play has returned,
	// waits for nothing more: played through, or ended early on an error.
	stop()
	st.wg.Wait()
	if err == nil {
		err = st.pressErr()
	}
	if ctx.Err() != nil && (errors.Is(err, ctx.Err()) || errors.Is(err, ask.ErrCancelled)) {
		return nil
	}
	return err
}

// shown is what's drawn at the screen, passed on, and kept as text, so the
// script can wait for what a face shows before it answers it.
type shown struct {
	out  io.Writer
	mu   sync.Mutex
	text strings.Builder
	// more is closed, and made anew, each time more is drawn.
	more chan struct{}
}

// Write passes p on, then keeps it, so what's waited for is on the screen
// by the time since sees it.
func (w *shown) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	w.mu.Lock()
	w.text.WriteString(ansi.Strip(string(p)))
	close(w.more)
	w.more = make(chan struct{})
	w.mu.Unlock()
	return n, err
}

// mark is where what's drawn has got to, for since to look from.
func (w *shown) mark() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text.Len()
}

// since waits till text is drawn after mark, or ctx is done.
func (w *shown) since(ctx context.Context, mark int, text string) {
	for {
		w.mu.Lock()
		drawn, more := strings.Contains(w.text.String()[mark:], text), w.more
		w.mu.Unlock()
		if drawn {
			return
		}
		select {
		case <-more:
		case <-ctx.Done():
			return
		}
	}
}

// stage is where a scenario plays, or a fixture's drawn: the terminal
// kit's faces draw at, and read keys from, what they've drawn, a clock,
// and the keys the script presses.
type stage struct {
	term  ask.Terminal
	shown *shown
	ctx   context.Context
	clock *clock
	keys  *os.File
	held  func()
	// shows is what a still shows once drawn, as its tape waits for.
	shows string

	wg       sync.WaitGroup
	mu       sync.Mutex
	pressing error
}

// clock is what a stage tells the time by: from start, the time since it
// began, in real time, a scenario's, plus the time lapsed; or, stepped, a
// fixture's, start moved on only by its pauses, which take no time.
type clock struct {
	start, began time.Time
	// pace is how many times as fast as written a scenario's pauses go.
	pace    float64
	stepped bool

	mu sync.Mutex
	// moved is how far the clock's moved on beyond real time: a stepped
	// clock's pauses, or the time a scenario's lapsed.
	moved time.Duration
	// lapse is how many times as long as written a scenario's pauses move
	// its clock on, beyond the time they take: a run of half an hour told
	// in seconds, as its faces count it.
	lapse float64
}

// stepped is a clock reading at, moved on only by pauses.
func stepped(at time.Time) *clock { return &clock{start: at, stepped: true} }

// now is the time by the clock, as kit's faces and the events tell it.
func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stepped {
		return c.start.Add(c.moved)
	}
	return c.start.Add(time.Since(c.began) + c.moved)
}

// move moves the clock on by d's share of what's lapsed: all of d, on a
// stepped clock.
func (c *clock) move(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stepped {
		c.moved += d
		return
	}
	c.moved += time.Duration(float64(d) * c.lapse)
}

// now is the stage's clock, as kit's faces and the events tell the time.
func (s *stage) now() time.Time { return s.clock.now() }

// lapsing has each of a scenario's pauses from now on move its clock on by
// times as long again as written, beyond the time it takes; 0, none.
func (s *stage) lapsing(times float64) {
	s.clock.mu.Lock()
	defer s.clock.mu.Unlock()
	s.clock.lapse = times
}

// pause waits d, at the scenario's pace, or till it's stopped, moving the
// clock on by what's lapsed; on a stepped clock, it moves the clock on d,
// at once.
func (s *stage) pause(d time.Duration) { s.pauseIn(s.ctx, d) }

// pauseIn is pause, stopped as ctx is done.
func (s *stage) pauseIn(ctx context.Context, d time.Duration) {
	defer s.clock.move(d)
	if s.clock.stepped {
		return
	}
	t := time.NewTimer(time.Duration(float64(d) / s.clock.pace))
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// keyBytes are what the keyboard sends for the keys a script names.
var keyBytes = map[string]string{"enter": "\r", "esc": "\x1b", "up": "\x1b[A", "down": "\x1b[B", "tab": "\t"}

// press presses keys, each by its name, as enter or down, or as the text
// it types.
func (s *stage) press(keys ...string) {
	for _, k := range keys {
		b, ok := keyBytes[k]
		if !ok {
			b = k
		}
		if _, err := s.keys.WriteString(b); err != nil {
			s.mu.Lock()
			s.pressing = cmp.Or(s.pressing, fmt.Errorf("press %q: %w", k, err))
			s.mu.Unlock()
		}
	}
}

// typingEvery is how long a key typed takes, as a person types.
const typingEvery = 75 * time.Millisecond

// typeIn types text, a key at a time.
func (s *stage) typeIn(text string) {
	for _, r := range text {
		s.press(string(r))
		s.pause(typingEvery)
	}
}

// answer waits till text is drawn after mark, as a face asking something
// draws it, then, after a pause for it to be read, does what answers it.
// Stopped meanwhile, it answers nothing.
func (s *stage) answer(mark int, text string, read time.Duration, does func()) {
	s.answerIn(s.ctx, mark, text, read, does)
}

// answerIn is answer, stopped as ctx is done, as a question that's ended
// before it's answered stops what waits to answer it.
func (s *stage) answerIn(ctx context.Context, mark int, text string, read time.Duration, does func()) {
	s.shown.since(ctx, mark, text)
	s.pauseIn(ctx, read)
	if ctx.Err() == nil {
		does()
	}
}

// asked asks question while what answers it waits for text drawn after
// mark, then answers it, as answer does: once question returns, answered
// or ended early, nothing waits on.
func (s *stage) asked(question func() error, mark int, text string, read time.Duration, does func()) error {
	ctx, stop := context.WithCancel(s.ctx)
	defer stop()
	answered := make(chan error, 1)
	go func() {
		defer stop()
		answered <- question()
	}()
	s.answerIn(ctx, mark, text, read, does)
	return <-answered
}

// meanwhile does f while the script goes on, as a key's pressed while a
// face that waits for it plays.
func (s *stage) meanwhile(f func()) { s.wg.Go(f) }

// holdShown holds the still once what it shows is drawn, its last screen.
func (s *stage) holdShown() {
	s.shown.since(s.ctx, 0, s.shows)
	s.hold()
}

// hold keeps the scenario's last screen up, still live, till it's stopped.
func (s *stage) hold() {
	if s.held != nil {
		s.held()
	}
	<-s.ctx.Done()
}

func (s *stage) pressErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pressing
}
