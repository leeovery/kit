// Package runnertest is a stand-in for the programs kit runs, for tests: a
// runner that answers only the commands a test scripts, and fails the test
// on any other.
package runnertest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// Fake is a runner that answers the commands scripted with On, and fails the
// test on any other. It's safe to use from several goroutines.
type Fake struct {
	t testing.TB

	mu      sync.Mutex
	scripts []*Script
	calls   []runner.Command
}

// New returns a fake with nothing scripted.
func New(t testing.TB) *Fake {
	return &Fake{t: t}
}

// On scripts the answer to the program name run with exactly args: by
// default, nothing printed and exit 0. Scripted again, the later answer
// wins.
func (f *Fake) On(name string, args ...string) *Script {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &Script{argv: slices.Concat([]string{name}, args)}
	f.scripts = append(f.scripts, s)
	return s
}

// Script is the scripted answer to a command.
type Script struct {
	argv     []string
	stdout   string
	stderr   string
	exit     int
	err      error
	duration time.Duration
}

// Prints has the command print text on its standard output.
func (s *Script) Prints(text string) *Script {
	s.stdout = text
	return s
}

// PrintsToStderr has the command print text on its standard error.
func (s *Script) PrintsToStderr(text string) *Script {
	s.stderr = text
	return s
}

// Exits has the command exit with code.
func (s *Script) Exits(code int) *Script {
	s.exit = code
	return s
}

// Fails has the command not run at all, Run returning err, as for a program
// that isn't installed (runner.ErrNotFound).
func (s *Script) Fails(err error) *Script {
	s.err = err
	return s
}

// Takes has the command report it took d.
func (s *Script) Takes(d time.Duration) *Script {
	s.duration = d
	return s
}

// Run answers cmd as scripted, as a real runner would, noting the call.
func (f *Fake) Run(_ context.Context, cmd runner.Command) (runner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	s := f.script(cmd)
	f.mu.Unlock()
	if s == nil {
		f.t.Errorf("runnertest: unscripted command %s", cmd)
		return runner.Result{ExitCode: -1}, fmt.Errorf("%s: %w (unscripted)", cmd.Name, runner.ErrNotFound)
	}
	if s.err != nil {
		return runner.Result{ExitCode: -1}, s.err
	}
	res := runner.Result{Stdout: []byte(s.stdout), Stderr: []byte(s.stderr), ExitCode: s.exit, Duration: s.duration}
	if s.exit != 0 {
		return res, &runner.ExitError{Command: cmd.String(), Code: s.exit, Stderr: s.stderr}
	}
	return res, nil
}

// script is the latest script for cmd, or nil.
func (f *Fake) script(cmd runner.Command) *Script {
	argv := slices.Concat([]string{cmd.Name}, cmd.Args)
	for _, s := range slices.Backward(f.scripts) {
		if slices.Equal(s.argv, argv) {
			return s
		}
	}
	return nil
}

// Calls are the commands run, in order, as a shell would take them.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make([]string, len(f.calls))
	for i, c := range f.calls {
		calls[i] = c.String()
	}
	return calls
}

// Commands are the commands run, in order.
func (f *Fake) Commands() []runner.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}
