// Package runnertest is a stand-in for the programs kit runs, for tests: a
// runner that answers only the commands a test scripts, and fails the test
// on any other.
package runnertest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/leeovery/kit/internal/runner"
)

// Fake is a runner that answers the commands scripted with On, and fails the
// test on any other. It's safe to use from several goroutines.
type Fake struct {
	t testing.TB
	// Env is the environment its commands run in, as a real runner's: none
	// unless a test sets it, before running anything.
	Env []string

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

// OnMatch scripts the answer to the program name run with any args match
// accepts, as On does: for a command whose arguments vary from run to run,
// as a path made for it, or that its program reads loosely.
func (f *Fake) OnMatch(name string, match func(args []string) bool) *Script {
	s := f.On(name)
	s.match = match
	return s
}

// Script is the scripted answers to a command: the first to its first run,
// the next to its next, and the last to every run after.
type Script struct {
	argv    []string
	match   func(args []string) bool
	answers []*answer
	runs    int
	// answered are what the command was given when it asked.
	answered []string
}

// answer is what one run of a command does.
type answer struct {
	stdout string
	stderr string
	exit   int
	err    error
	does   func()
	writes map[string]string
	partly bool
	filter func(input string) (string, error)
	asks   int
	late   bool
	hangs  bool
	group  int
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

// Writes has the command write content to the file the argument after flag
// names, as op read --out-file <path> writes a value: only when it
// succeeds. A relative path is in the folder it runs in (runner.Command's
// Dir).
func (s *Script) Writes(flag, content string) *Script {
	a := s.last()
	if a.writes == nil {
		a.writes = map[string]string{}
	}
	a.writes[flag] = content
	return s
}

// WritesPartly has the command write content as Writes does, but whatever
// it exits with, as a program that fails partway leaves what it wrote.
func (s *Script) WritesPartly(flag, content string) *Script {
	s.last().partly = true
	return s.Writes(flag, content)
}

// Filters has the command print what f makes of what it reads on its
// standard input, as a filter such as op inject does: when f fails, it
// prints the error on its standard error instead, and exits 1.
func (s *Script) Filters(f func(input string) (string, error)) *Script {
	s.last().filter = f
	return s
}

// Asks has the command ask times for what its Answer gives, as sudo -S
// asks for a password, before it does as scripted: one not given ends it,
// exit 1. Answered says what it was given.
func (s *Script) Asks(times int) *Script {
	s.last().asks = times
	return s
}

// AnswersLate has the command, having asked, end before its last answer's
// given, as sudo does once its own time to wait for a password is up: it
// exits 1, the answer too late (runner.ErrAnsweredLate), and isn't kept in
// Answered.
func (s *Script) AnswersLate() *Script {
	s.last().late = true
	return s
}

// Hangs has the command, having printed what it prints, wait for good,
// till its context is done, then fail as the runner ends one: with the
// context's error.
func (s *Script) Hangs() *Script {
	s.last().hangs = true
	return s
}

// InGroup has the command run in process group n (runner.Result's Group).
func (s *Script) InGroup(n int) *Script {
	s.last().group = n
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

// Environ is the environment cmd runs in, as a real runner's: Env, cmd's
// own variables over it, then those ctx sets.
func (f *Fake) Environ(ctx context.Context, cmd runner.Command) ([]string, bool) {
	return slices.Concat(f.Env, cmd.Env, runner.SecretEnv(ctx)), true
}

// Run answers cmd as scripted, as a real runner would, noting the call: its
// environment with the variables ctx sets, as a real runner sets them.
func (f *Fake) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	if env := runner.SecretEnv(ctx); len(env) > 0 {
		cmd.Env = slices.Concat(cmd.Env, env)
	}
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
		return runner.Result{ExitCode: -1, Group: a.group}, a.err
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
		if a.late && asked == a.asks-1 {
			return runner.Result{ExitCode: 1}, errors.Join(&runner.ExitError{Command: cmd.String(), Code: 1}, runner.ErrAnsweredLate)
		}
		f.mu.Lock()
		s.answered = append(s.answered, answer)
		f.mu.Unlock()
	}
	if a.does != nil {
		a.does()
	}
	stdout, stderr, exit := a.output(cmd.Input)
	if cmd.Lines != nil {
		for _, text := range []string{stdout, stderr} {
			for _, line := range given(cmd, text) {
				cmd.Lines(line)
			}
		}
		if cmd.LinesOnly {
			stdout = ""
		}
	}
	if a.hangs {
		<-ctx.Done()
		return runner.Result{ExitCode: -1, Group: a.group}, fmt.Errorf("%s: %w", cmd, ctx.Err())
	}
	if exit == 0 || a.partly {
		f.write(cmd, a.writes)
	}
	res := runner.Result{Stdout: []byte(stdout), Stderr: []byte(stderr), ExitCode: exit, Group: a.group}
	if exit != 0 {
		return res, runner.NewExitError(cmd.String(), exit, res.Stderr)
	}
	return res, nil
}

// given are the lines of text cmd's Lines is given: each a new line ends,
// and, with Redraws, each a carriage return ends too, as a real runner gives
// them; never a blank one.
func given(cmd runner.Command, text string) []string {
	var lines []string
	for line := range strings.Lines(text) {
		drawn := []string{strings.TrimRight(line, "\n")}
		if cmd.Redraws {
			drawn = strings.Split(drawn[0], "\r")
		}
		for _, l := range drawn {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
	}
	return lines
}

// write writes each file of writes, by the flag before its path in cmd's
// arguments, as Writes says.
func (f *Fake) write(cmd runner.Command, writes map[string]string) {
	for flag, content := range writes {
		i := slices.Index(cmd.Args, flag)
		if i < 0 || i+1 >= len(cmd.Args) {
			f.t.Errorf("runnertest: %s has no path after %s to write", cmd, flag)
			continue
		}
		path := cmd.Args[i+1]
		if !filepath.IsAbs(path) {
			path = filepath.Join(cmd.Dir, path)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			f.t.Errorf("runnertest: %s couldn't write %s: %v", cmd, path, err)
		}
	}
}

// output is what the run prints, and exits with, given input.
func (a *answer) output(input string) (stdout, stderr string, exit int) {
	if a.filter == nil {
		return a.stdout, a.stderr, a.exit
	}
	out, err := a.filter(input)
	if err != nil {
		return "", err.Error(), 1
	}
	return out, a.stderr, a.exit
}

// script is the latest script for cmd, or nil.
func (f *Fake) script(cmd runner.Command) *Script {
	argv := slices.Concat([]string{cmd.Name}, cmd.Args)
	for _, s := range slices.Backward(f.scripts) {
		if s.match != nil && s.argv[0] == cmd.Name && s.match(cmd.Args) || s.match == nil && slices.Equal(s.argv, argv) {
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
