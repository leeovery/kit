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

func TestWithTimeoutUnheld(t *testing.T) {
	// Without With, it's context.WithTimeout: a deadline.
	ctx, cancel := hold.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Error("no deadline, want context.WithTimeout's")
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
