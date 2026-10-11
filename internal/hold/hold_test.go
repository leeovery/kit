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
