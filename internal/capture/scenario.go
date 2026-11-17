package capture

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/ask"
)

// Scenario is a clip: kit's screens played in real time, as kit draws
// them, from events and keys scripted at moments into it. Width and Height
// are the terminal it's made for, as its tape records it.
type Scenario struct {
	Name, About   string
	Width, Height int
	// start is the moment its clock reads as it starts, running on from
	// there in real time.
	start time.Time
	play  func(*stage) error
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

// Screen is the terminal a scenario plays at: what's drawn goes to Out,
// and Size says how many columns and lines it has.
type Screen struct {
	Out  io.Writer
	Size func() (width, height int)
}

// Play plays s at screen till ctx is done: its cues in turn, then its last
// screen held, still live, telling held once it is. The keys it presses
// reach kit's faces through a pipe, as they'd come from the keyboard; the
// keyboard itself is never read. Stopped before its cues are played, it
// ends there, without an error.
func (s Scenario) Play(ctx context.Context, screen Screen, held func()) error {
	in, keys, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("make the keys' pipe: %w", err)
	}
	defer func() { _ = in.Close() }()
	defer func() { _ = keys.Close() }()
	st := &stage{
		term: ask.Terminal{In: in, Out: screen.Out, Size: screen.Size},
		ctx:  ctx, start: s.start, began: time.Now(), keys: keys, held: held,
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

// stage is where a scenario plays: the terminal kit's faces draw at, and
// read keys from, a clock, and the keys the script presses.
type stage struct {
	term ask.Terminal
	ctx  context.Context
	// start is when the clock reads, as it's begun.
	start, began time.Time
	keys         *os.File
	held         func()

	wg       sync.WaitGroup
	mu       sync.Mutex
	pressing error
}

// now is the scenario's clock: its start, and the time since, in real
// time.
func (s *stage) now() time.Time { return s.start.Add(time.Since(s.began)) }

// pause waits d, or till the scenario's stopped.
func (s *stage) pause(d time.Duration) {
	t := time.NewTimer(d)
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

// pressAfter presses keys d from now, while the script goes on: for what a
// face waits on while it plays, as the splash waits for enter.
func (s *stage) pressAfter(d time.Duration, keys ...string) {
	s.wg.Go(func() {
		s.pause(d)
		if s.ctx.Err() == nil {
			s.press(keys...)
		}
	})
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
