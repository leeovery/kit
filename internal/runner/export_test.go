package runner

import (
	"context"
	"time"
)

// Timed is e keeping a command's time by limit and after, its output waited
// for wait, in place of the real clock: for tests, which can't wait as long
// as kit gives a command.
func Timed(e Exec, limit func(context.Context, time.Duration) (context.Context, context.CancelFunc), after func(time.Duration, func()) func() bool, wait time.Duration) Exec {
	e.clock = &clock{limit: limit, after: after, wait: wait}
	return e
}
