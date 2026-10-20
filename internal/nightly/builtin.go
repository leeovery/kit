package nightly

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// captureTimeout is how long settings capture may take: seconds, usually;
// a hang (a stuck defaults, a slow push) is cut off so it can't hold up the
// next night's run.
const captureTimeout = 15 * time.Minute

// The built-in jobs' names, as their steps and the record name them.
const (
	CaptureSettingsJob = "capture-settings"
	RetryPendingJob    = "retry-pending"
)

// CaptureSettings is the job capturing apps' settings, through capture,
// stopped after 15 minutes: what went wrong, else nothing.
func CaptureSettings(capture func(ctx context.Context) error) Job {
	return Job{Name: CaptureSettingsJob, Title: "Settings capture", Run: func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, captureTimeout)
		defer cancel()
		err := capture(ctx)
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return fmt.Errorf("stopped after %d minutes without finishing", int(captureTimeout.Minutes()))
		}
		return err
	}}
}

// RetryPending is the hourly job restoring the apps' settings that wait
// for their apps to be installed and closed, or to have their settings
// folders, opening none, through retry: what went wrong, else nothing.
func RetryPending(retry func(ctx context.Context) error) Job {
	return Job{Name: RetryPendingJob, Title: "Pending settings", Run: retry}
}
