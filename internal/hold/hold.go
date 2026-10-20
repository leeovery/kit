// Package hold is time limits that wait while the person at the Mac is
// asked something only they can answer: a window of macOS's own, which holds
// up the command that brought it however long it's left; and while what
// runs under them waits its turn behind another's run.
package hold

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

type heldKey struct{}

// With returns ctx, its time limits waiting while held says so.
func With(ctx context.Context, held func() bool) context.Context {
	if held == nil {
		return ctx
	}
	return context.WithValue(ctx, heldKey{}, held)
}

// every is how often a time limit looks whether it's held.
var every = 500 * time.Millisecond

type waitsKey struct{}

// waitsOf are the time limits ctx runs under, each as how many waits for a
// turn hold it (Waiting).
func waitsOf(ctx context.Context) []*atomic.Int32 {
	waits, _ := ctx.Value(waitsKey{}).([]*atomic.Int32)
	return waits
}

// WithTimeout is context.WithTimeout, but its time doesn't count while
// ctx's limits are held, or while what runs under it waits its turn
// (Waiting).
func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return limit(ctx, d, nil)
}

// UntilHeard is WithTimeout, but lifted once heard is called: a limit on
// how long what runs under it may take to say anything at all.
func UntilHeard(ctx context.Context, d time.Duration) (_ context.Context, heard func(), _ context.CancelFunc) {
	said := new(atomic.Bool)
	c, cancel := limit(ctx, d, said)
	return c, func() { said.Store(true) }, cancel
}

// limit is ctx limited to d, lifted once said is set.
func limit(ctx context.Context, d time.Duration, said *atomic.Bool) (context.Context, context.CancelFunc) {
	held, _ := ctx.Value(heldKey{}).(func() bool)
	waits := new(atomic.Int32)
	inner, cancel := context.WithCancel(context.WithValue(ctx, waitsKey{}, append(slices.Clip(waitsOf(ctx)), waits)))
	c := &limited{Context: inner}
	look := every
	go func() {
		left, last := d, time.Now()
		for left > 0 {
			t := time.NewTimer(min(left, look))
			select {
			case <-inner.Done():
				t.Stop()
				return
			case now := <-t.C:
				if said != nil && said.Load() {
					return
				}
				if waits.Load() == 0 && (held == nil || !held()) {
					left -= now.Sub(last)
				}
				last = now
			}
		}
		if said != nil && said.Load() {
			return
		}
		c.expired.Store(true)
		cancel()
	}()
	return c, cancel
}

// Waiting has every time limit ctx runs under wait, as what runs in it
// waits its turn behind another's run, till done is called: a turn waited
// for isn't time taken, however long the other's run takes.
func Waiting(ctx context.Context) (done func()) {
	waits := waitsOf(ctx)
	for _, w := range waits {
		w.Add(1)
	}
	return sync.OnceFunc(func() {
		for _, w := range waits {
			w.Add(-1)
		}
	})
}

// limited is a context its time limit ended: its error says so, as
// context.WithTimeout's does.
type limited struct {
	context.Context
	expired atomic.Bool
}

func (c *limited) Err() error {
	if c.expired.Load() {
		return context.DeadlineExceeded
	}
	return c.Context.Err()
}

// During runs f, reporting whether ctx's time limits were held at any look
// while it ran, its end included: as when macOS asked the person at the Mac
// something meanwhile, which what f ran may have waited on, and given up
// waiting for.
func During(ctx context.Context, f func()) bool {
	held, _ := ctx.Value(heldKey{}).(func() bool)
	if held == nil {
		f()
		return false
	}
	var seen bool
	done, looked := make(chan struct{}), make(chan struct{})
	tick := time.NewTicker(every)
	go func() {
		defer close(looked)
		defer tick.Stop()
		for {
			seen = seen || held()
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	f()
	close(done)
	<-looked
	return seen || held()
}
