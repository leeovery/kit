package hold_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/hold"
)

func TestWithTimeoutEndsAsAnyTimeLimit(t *testing.T) {
	defer hold.SetEvery(5 * time.Millisecond)()
	ctx, cancel := hold.WithTimeout(hold.With(context.Background(), func() bool { return false }), 30*time.Millisecond)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("not ended a second on")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want the deadline exceeded", ctx.Err())
	}
}

func TestWithTimeoutWaitsWhileHeld(t *testing.T) {
	defer hold.SetEvery(5 * time.Millisecond)()
	var held atomic.Bool
	held.Store(true)
	ctx, cancel := hold.WithTimeout(hold.With(context.Background(), held.Load), 30*time.Millisecond)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatal("ended while held")
	case <-time.After(150 * time.Millisecond):
	}
	held.Store(false)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("not ended once let go")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want the deadline exceeded", ctx.Err())
	}
}

func TestWithTimeoutCancelled(t *testing.T) {
	ctx, cancel := hold.WithTimeout(hold.With(context.Background(), func() bool { return true }), time.Hour)
	cancel()
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("Err() = %v, want cancelled", ctx.Err())
	}
}

// Without With, a time limit still ends.
func TestWithTimeoutUnheld(t *testing.T) {
	defer hold.SetEvery(5 * time.Millisecond)()
	ctx, cancel := hold.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("not ended a second on")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want the deadline exceeded", ctx.Err())
	}
}

// While what runs under a time limit waits its turn, the limit waits, held
// or not, every limit it runs under, the one around it as well as its own;
// once the turn's come, the limits count again, from where they were.
func TestWithTimeoutWaitsForATurn(t *testing.T) {
	defer hold.SetEvery(5 * time.Millisecond)()
	for _, tt := range []struct {
		name string
		ctx  context.Context
	}{
		{"unheld", context.Background()},
		{"held by nothing now", hold.With(context.Background(), func() bool { return false })},
	} {
		t.Run(tt.name, func(t *testing.T) {
			outer, cancelOuter := hold.WithTimeout(tt.ctx, 30*time.Millisecond)
			defer cancelOuter()
			inner, cancelInner := hold.WithTimeout(outer, time.Hour)
			defer cancelInner()
			done := hold.Waiting(inner)
			select {
			case <-outer.Done():
				t.Fatal("the limit around ended while what ran under it waited its turn")
			case <-time.After(150 * time.Millisecond):
			}
			done()
			done()
			select {
			case <-outer.Done():
			case <-time.After(time.Second):
				t.Fatal("not ended once the turn had come")
			}
			if !errors.Is(outer.Err(), context.DeadlineExceeded) {
				t.Errorf("Err() = %v, want the deadline exceeded", outer.Err())
			}
		})
	}
	// Outside any limit, waiting holds nothing up.
	hold.Waiting(context.Background())()
}

// A limit on going quiet counts from the last thing heard: what keeps
// saying something runs on past it; what then says nothing for it ends.
func TestWithQuiet(t *testing.T) {
	defer hold.SetEvery(5 * time.Millisecond)()
	ctx, heard, cancel := hold.WithQuiet(context.Background(), 60*time.Millisecond)
	defer cancel()
	for range 10 {
		heard()
		select {
		case <-ctx.Done():
			t.Fatal("ended while what ran under it was still saying something")
		case <-time.After(20 * time.Millisecond):
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("not ended once it had gone quiet")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want the deadline exceeded", ctx.Err())
	}
}

// During says whether the time limits were held at any look while what it
// ran ran: held for a moment in the middle, held as it ends, or never; and
// never, without With.
func TestDuring(t *testing.T) {
	defer hold.SetEvery(5 * time.Millisecond)()
	for _, tt := range []struct {
		name  string
		held  func(held *atomic.Bool)
		wants bool
	}{
		{"never", func(*atomic.Bool) { time.Sleep(30 * time.Millisecond) }, false},
		{"for a moment", func(held *atomic.Bool) {
			time.Sleep(10 * time.Millisecond)
			held.Store(true)
			time.Sleep(30 * time.Millisecond)
			held.Store(false)
			time.Sleep(10 * time.Millisecond)
		}, true},
		{"as it ends", func(held *atomic.Bool) { held.Store(true) }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var held atomic.Bool
			if got := hold.During(hold.With(context.Background(), held.Load), func() { tt.held(&held) }); got != tt.wants {
				t.Errorf("During() = %v, want %v", got, tt.wants)
			}
		})
	}
	ran := false
	if hold.During(context.Background(), func() { ran = true }) || !ran {
		t.Error("without With, During() said held, or didn't run what it was given")
	}
}
