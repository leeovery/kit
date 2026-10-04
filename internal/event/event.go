// Package event is what happens in a run, as kit's core emits it: the faces
// show it, and the run log records it. The core never prints.
package event

import (
	"cmp"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/redact"
	"github.com/leeovery/kit/internal/runner"
)

// Event is something that happened in a run.
type Event interface {
	// At is when it happened.
	At() time.Time
}

// RunStarted is a run starting.
type RunStarted struct {
	Time time.Time
	// Command is kit's command, as in status.
	Command string
	Machine string
	Version string
	// Steps are the steps the run will take, in order.
	Steps []Step
}

// Step names a step, and titles it for people.
type Step struct {
	Name  string
	Title string
}

// StepStarted is a step's check, or its apply, starting.
type StepStarted struct {
	Time time.Time
	Step string
}

// StepFinished is a step done, checked, failed or deferred: its Result says
// which.
type StepFinished struct {
	Time     time.Time
	Step     string
	Result   check.Result
	Duration time.Duration
}

// CommandRan is a program kit ran, for a step, as runner reports it,
// secrets hidden.
type CommandRan struct {
	Time time.Time
	Step string
	// Command is the program and its arguments, as a shell would take them.
	Command  string
	Exit     int
	Duration time.Duration
	Stdout   string
	Stderr   string
	// Error is what went wrong, when it didn't run or exited other than 0.
	Error string
}

// RunFinished is a run done.
type RunFinished struct {
	Time     time.Time
	Duration time.Duration
	// Counts are its steps by how they stand.
	Counts map[check.State]int
}

func (e RunStarted) At() time.Time   { return e.Time }
func (e StepStarted) At() time.Time  { return e.Time }
func (e StepFinished) At() time.Time { return e.Time }
func (e CommandRan) At() time.Time   { return e.Time }
func (e RunFinished) At() time.Time  { return e.Time }

// Sink receives a run's events.
type Sink interface {
	Emit(e Event)
}

// Fanout sends each event to every sink, one event at a time, so sinks see
// events whole and in one order however many goroutines emit them.
type Fanout struct {
	mu    sync.Mutex
	sinks []Sink
}

// NewFanout returns a fanout to sinks.
func NewFanout(sinks ...Sink) *Fanout {
	return &Fanout{sinks: sinks}
}

func (f *Fanout) Emit(e Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sinks {
		s.Emit(e)
	}
}

type stepKey struct{}

// WithStep returns ctx for running step's check or apply: what runs in it is
// attributed to the step.
func WithStep(ctx context.Context, step string) context.Context {
	return context.WithValue(ctx, stepKey{}, step)
}

// StepOf is the step ctx is running, or "".
func StepOf(ctx context.Context) string {
	step, _ := ctx.Value(stepKey{}).(string)
	return step
}

// Command is what a runner reports of a command, as an event, attributed to
// the step ctx is running, with secrets, and anything shaped like a token,
// hidden. The command's standard input is never part of it.
func Command(ctx context.Context, rep runner.Report, secrets ...string) CommandRan {
	e := CommandRan{
		Time:     rep.Started,
		Step:     StepOf(ctx),
		Command:  redact.Text(rep.Command.String(), secrets...),
		Exit:     rep.Result.ExitCode,
		Duration: rep.Result.Duration,
		Stdout:   redact.Text(string(rep.Result.Stdout), secrets...),
		Stderr:   redact.Text(string(rep.Result.Stderr), secrets...),
	}
	if rep.Err != nil {
		e.Error = redact.Text(rep.Err.Error(), secrets...)
	}
	return e
}

// Tally counts results by how they stand.
func Tally(results ...check.Result) map[check.State]int {
	counts := make(map[check.State]int)
	for _, r := range results {
		counts[cmp.Or(r.State, check.Failed)]++
	}
	return counts
}

// ErrStopped is the reason a step gives when the run was stopped before it.
var ErrStopped = errors.New("the run was stopped")
