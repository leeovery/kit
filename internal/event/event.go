// Package event is what happens in a run, as kit's core emits it: the faces
// show it, and the run log records it. The core never prints.
package event

import (
	"cmp"
	"context"
	"errors"
	"fmt"
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
	// Only are the steps named, as in kit apply remote-session: none when the
	// run takes every step.
	Only []string
}

// Step names a step, and titles it for people.
type Step struct {
	Name  string
	Title string
	// Area is what the step is about, as the at-a-glance view groups steps:
	// Backups, Mac, Drift, Config, Checks.
	Area string
	// Part is what the step counts towards in a view rolling its area up:
	// Packages, Settings, Files, Secrets, or the config repository.
	Part string `json:",omitempty"`
	// Waiting is what the step says while it waits its turn, when that's
	// more than that it's waiting: what it'll need of you, or do.
	Waiting string `json:",omitempty"`
	// Waits are the steps it waits for, by name: those it needs and those
	// it's applied after.
	Waits []string `json:",omitempty"`
}

// Preparing is kit getting a run ready, before it starts, as applying works
// out what will need an administrator's password: a face shows the run's
// heading at once, and what kit's doing. Doing "" says it's done, as before
// a question at the terminal.
type Preparing struct {
	Time    time.Time
	Command string
	Machine string
	Doing   string
	// Steps are the steps the run will run, when they're known before it
	// starts: a face can show the run's lights meanwhile.
	Steps []Step
}

func (e Preparing) At() time.Time { return e.Time }

// StepStarted is a step's check, or its apply, starting.
type StepStarted struct {
	Time time.Time
	Step string
	// Doing is what's starting: checking or applying.
	Doing string
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

// SelfTest is what a new Mac's boot found as it started: the Mac's name on
// the network, and its tests, in order.
type SelfTest struct {
	Time  time.Time
	Mac   string
	Tests []Test
}

// Test is something the self-test found: ok when it's there, attention when
// kit needs it and it isn't there yet, failed when the boot can't go on
// without it. Says is what it says, a part each.
type Test struct {
	Name  string
	State check.State
	Says  []string
}

// Doing is a step saying what it's doing, as it goes.
type Doing struct {
	Time time.Time
	Step string
	Says string
	// Todo is what the person's to do meanwhile, when there's something:
	// what the step waits on, as macOS asking before an app first opens.
	Todo string
}

// DeviceCode is a sign-in waiting for its code to be entered: where, the
// code, and when it expires.
type DeviceCode struct {
	Time    time.Time
	Step    string
	URI     string
	Code    string
	Expires time.Time
}

func (e SelfTest) At() time.Time   { return e.Time }
func (e Doing) At() time.Time      { return e.Time }
func (e DeviceCode) At() time.Time { return e.Time }

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

// WithChanging returns ctx for a step changing things, applying or adding:
// what its commands print may be shown as it comes.
func WithChanging(ctx context.Context) context.Context {
	return context.WithValue(ctx, changingKey{}, true)
}

// Changing reports whether ctx is a step changing things.
func Changing(ctx context.Context) bool {
	changing, _ := ctx.Value(changingKey{}).(bool)
	return changing
}

type changingKey struct{}

// Output is a line a command printed, as it printed it, while its step
// changed things: never a secret's, and with anything shaped like a token
// hidden.
type Output struct {
	Time time.Time
	Step string
	// Command is the command that printed it.
	Command string
	Line    string
}

func (e Output) At() time.Time { return e.Time }

// StepOf is the step ctx is running, or "".
func StepOf(ctx context.Context) string {
	step, _ := ctx.Value(stepKey{}).(string)
	return step
}

// Command is what a runner reports of a command, as an event, attributed to
// the step ctx is running, with secrets, and anything shaped like a token,
// hidden. The command's standard input is never part of it, nor the output
// of one whose output is a secret.
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
	if rep.Command.Secret {
		e.Stdout = fmt.Sprintf("(a secret, %d bytes, not kept)", len(rep.Result.Stdout))
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
