// Package hold is time limits that wait while the person at the Mac is
// asked something only they can answer: a window of macOS's own, which holds
// up the command that brought it however long it's left.
package hold

import (
	"context"
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

// WithTimeout is context.WithTimeout, but its time doesn't count while
// ctx's limits are held.
func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	held, _ := ctx.Value(heldKey{}).(func() bool)
	if held == nil {
		return context.WithTimeout(ctx, d)
	}
	inner, cancel := context.WithCancel(ctx)
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
				if !held() {
					left -= now.Sub(last)
				}
				last = now
			}
		}
		c.expired.Store(true)
		cancel()
	}()
	return c, cancel
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
