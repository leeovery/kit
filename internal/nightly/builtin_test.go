package nightly_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/nightly"
)

func TestCaptureSettings(t *testing.T) {
	if err := nightly.CaptureSettings(func(context.Context) error { return nil }).Run(t.Context()); err != nil {
		t.Errorf("Run() = %v", err)
	}
	paused := errors.New("paused: capture isn't switched on for this Mac")
	if err := nightly.CaptureSettings(func(context.Context) error { return paused }).Run(t.Context()); !errors.Is(err, paused) {
		t.Errorf("Run() = %v, want capture's error", err)
	}
	slow := nightly.CaptureSettings(func(ctx context.Context) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 15*time.Minute {
			t.Errorf("deadline %v, want 15 minutes", deadline)
		}
		return context.DeadlineExceeded
	})
	if err := slow.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "stopped after 15 minutes") {
		t.Errorf("Run() = %v", err)
	}
}
