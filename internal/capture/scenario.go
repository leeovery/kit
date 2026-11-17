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
	in, keys, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("make the keys' pipe: %w", err)
	}
	defer func() { _ = in.Close() }()
	defer func() { _ = keys.Close() }()
	shown := &shown{out: screen.Out, more: make(chan struct{})}
	st := &stage{
		term: ask.Terminal{In: in, Out: shown, Size: screen.Size}, shown: shown,
		ctx: ctx, start: s.start, began: time.Now(), pace: cmp.Or(s.pace, 1), keys: keys, held: held,
	}
	err = s.play(st)
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

func (w *shown) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.text.WriteString(ansi.Strip(string(p)))
	close(w.more)
	w.more = make(chan struct{})
	w.mu.Unlock()
	return w.out.Write(p)
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

// stage is where a scenario plays: the terminal kit's faces draw at, and
// read keys from, what they've drawn, a clock, and the keys the script
// presses.
type stage struct {
	term  ask.Terminal
	shown *shown
	ctx   context.Context
	// start is when the clock reads as it's begun, began when it was.
	start, began time.Time
	pace         float64
	keys         *os.File
	held         func()

	wg       sync.WaitGroup
	mu       sync.Mutex
	pressing error
}

// now is the scenario's clock: its start, and the time since, in real
// time.
func (s *stage) now() time.Time { return s.start.Add(time.Since(s.began)) }

// pause waits d, at the scenario's pace, or till it's stopped.
func (s *stage) pause(d time.Duration) {
	t := time.NewTimer(time.Duration(float64(d) / s.pace))
	defer t.Stop()
	select {
	case <-t.C:
	case <-s.ctx.Done():
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
const typingEvery = 110 * time.Millisecond

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
	s.shown.since(s.ctx, mark, text)
	s.pause(read)
	if s.ctx.Err() == nil {
		does()
	}
}

// meanwhile does f while the script goes on, as a key's pressed while a
// face that waits for it plays.
func (s *stage) meanwhile(f func()) { s.wg.Go(f) }

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
