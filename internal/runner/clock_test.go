package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The real clock keeps a command's time as kit gives it: its output waited
// for waitDelay; a grace that ends once its time has passed, and not
// before, unless it's stopped first; and a time limit that runs out.
func TestTheRealClock(t *testing.T) {
	if realClock.wait != waitDelay {
		t.Errorf("the output's waited for %v, want %v", realClock.wait, waitDelay)
	}

	const grace = 50 * time.Millisecond
	began := time.Now()
	over := make(chan time.Duration, 1)
	realClock.after(grace, func() { over <- time.Since(began) })
	select {
	case took := <-over:
		if took < grace {
			t.Errorf("the grace ended after %v, want %v", took, grace)
		}
	case <-time.After(5 * time.Second):
		t.Error("the grace never ended")
	}

	stopped := make(chan struct{}, 1)
	stop := realClock.after(grace, func() { stopped <- struct{}{} })
	if !stop() {
		t.Error("stop() = false, want the grace stopped before it ended")
	}
	select {
	case <-stopped:
		t.Error("a grace stopped still ended")
	case <-time.After(4 * grace):
	}

	ctx, cancel := realClock.limit(t.Context(), grace)
	defer cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Errorf("the limit ended with %v, want the deadline exceeded", ctx.Err())
		}
	case <-time.After(5 * time.Second):
		t.Error("the time limit never ran out")
	}
}
