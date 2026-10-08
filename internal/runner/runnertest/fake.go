// Package runnertest is a stand-in for the programs kit runs, for tests: a
// runner that answers only the commands a test scripts, and fails the test
// on any other.
package runnertest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	s := &Script{argv: slices.Concat([]string{name}, args), answers: []*answer{{}}}
	f.scripts = append(f.scripts, s)
	return s
}

// Script is the scripted answers to a command: the first to its first run,
// the next to its next, and the last to every run after.
type Script struct {
	argv    []string
	answers []*answer
	runs    int
	// answered are what the command was given when it asked.
	answered []string
}

// answer is what one run of a command does.
type answer struct {
	stdout   string
	stderr   string
	exit     int
	err      error
	duration time.Duration
	does     func()
	asks     int
}

func (s *Script) last() *answer {
	return s.answers[len(s.answers)-1]
}

// Prints has the command print text on its standard output.
func (s *Script) Prints(text string) *Script {
	s.last().stdout = text
	return s
}

// PrintsToStderr has the command print text on its standard error.
func (s *Script) PrintsToStderr(text string) *Script {
	s.last().stderr = text
	return s
}

// Exits has the command exit with code.
func (s *Script) Exits(code int) *Script {
	s.last().exit = code
	return s
}

// Fails has the command not run at all, Run returning err, as for a program
// that isn't installed (runner.ErrNotFound).
func (s *Script) Fails(err error) *Script {
	s.last().err = err
	return s
}

// Does has the command do f when it runs, as a real program changes what's
// on the Mac: a file it writes, say.
func (s *Script) Does(f func()) *Script {
	s.last().does = f
	return s
}

// Takes has the command report it took d.
func (s *Script) Takes(d time.Duration) *Script {
	s.last().duration = d
	return s
}

// Asks has the command ask times for what its Answer gives, as sudo -S
// asks for a password, before it does as scripted: one not given ends it,
// exit 1. Answered says what it was given.
func (s *Script) Asks(times int) *Script {
	s.last().asks = times
	return s
}

// Answered are what the command was given when it asked, in order.
func (s *Script) Answered() []string {
	return slices.Clone(s.answered)
}

// Then scripts the command's next run, as when what it reports changes,
// such as a listing once something's installed: by default, nothing printed
// and exit 0.
func (s *Script) Then() *Script {
	s.answers = append(s.answers, &answer{})
	return s
}

// Has reports whether the program name is installed, as the test has it:
// it is when a command of it is scripted, unless that command fails as a
// program that isn't installed.
func (f *Fake) Has(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range slices.Backward(f.scripts) {
		if s.argv[0] == name {
			return !errors.Is(s.answers[0].err, runner.ErrNotFound)
		}
	}
	return false
}

// Run answers cmd as scripted, as a real runner would, noting the call.
func (f *Fake) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	s := f.script(cmd)
	var a *answer
	if s != nil {
		a = s.answers[min(s.runs, len(s.answers)-1)]
		s.runs++
	}
	f.mu.Unlock()
	if s == nil {
		f.t.Errorf("runnertest: unscripted command %s", cmd)
		return runner.Result{ExitCode: -1}, fmt.Errorf("%s: %w (unscripted)", cmd.Name, runner.ErrNotFound)
	}
	if a.err != nil {
		return runner.Result{ExitCode: -1}, a.err
	}
	for asked := range a.asks {
		if cmd.Answer == nil {
			f.t.Errorf("runnertest: %s asks, and nothing answers it", cmd)
			break
		}
		answer, err := cmd.Answer(ctx, asked)
		if err != nil {
			return runner.Result{ExitCode: 1}, &runner.ExitError{Command: cmd.String(), Code: 1, Stderr: "no answer given"}
		}
		f.mu.Lock()
		s.answered = append(s.answered, answer)
		f.mu.Unlock()
	}
	if a.does != nil {
		a.does()
	}
	if cmd.Lines != nil && !cmd.Interactive {
		for _, text := range []string{a.stdout, a.stderr} {
			for line := range strings.Lines(text) {
				if line = strings.TrimRight(line, "\n"); strings.TrimSpace(line) != "" {
					cmd.Lines(line)
				}
			}
		}
	}
	res := runner.Result{Stdout: []byte(a.stdout), Stderr: []byte(a.stderr), ExitCode: a.exit, Duration: a.duration}
	if a.exit != 0 {
		return res, &runner.ExitError{Command: cmd.String(), Code: a.exit, Stderr: a.stderr}
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
