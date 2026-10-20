// Package runner runs the programs kit drives. It's the one place a process
// starts: with kit's own PATH and environment, a timeout, and its output
// captured, so every command can be reported and every program faked in
// tests (runner/runnertest).
package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// DefaultTimeout ends a command that runs longer, unless it sets its own;
// Unlimited is the timeout of one that runs as long as it takes, as sudo
// waiting for a person's password.
const (
	DefaultTimeout               = 2 * time.Minute
	Unlimited      time.Duration = -1
)

// ErrNotFound is returned for a program that isn't on kit's PATH.
var ErrNotFound = errors.New("not found")

// ErrAnsweredLate is in the error of a command that ended before what it
// asked for could be given, as sudo does once its own time to wait for a
// password is up: the answer came too late, and wasn't heard.
var ErrAnsweredLate = errors.New("it ended before it was answered")

// Command is a program to run, and how.
type Command struct {
	// Name is the program: a name, found on kit's PATH, or a path to it.
	Name string
	Args []string
	// Dir is the directory it runs in: kit's own when "".
	Dir string
	// Input is what it reads on its standard input: nothing when "".
	Input string
	// Timeout ends it if it runs longer: DefaultTimeout when 0, never when
	// Unlimited.
	Timeout time.Duration
	// Interactive runs it at kit's own terminal, reading from it and writing
	// to it directly, for a program that asks a person something, such as
	// sudo its password; nothing it prints is captured.
	Interactive bool
	// Secret is whether what it prints is a secret, as a secrets store's
	// values are: kept from every report, the log included.
	Secret bool
	// Env is added to the environment the command runs in, its own values
	// winning over the runner's.
	Env []string
	// Asks, with Answer, is what the command prints on its standard error
	// when it asks for something, as sudo -S -p's prompt: each time it
	// prints it, Answer is called with how many times it asked before, and
	// what Answer returns is written to the command's standard input, a
	// line, kept nowhere; an error from Answer ends the command's input.
	// One that ended while Answer was getting its answer fails with
	// ErrAnsweredLate. What it printed is reported without the prompts.
	Asks   string
	Answer func(ctx context.Context, asked int) (string, error)
	// Lines, when set, is given each line the command prints, its output and
	// its errors, as it prints them: a line a carriage return starts again
	// is given as it ends. Never for an interactive command.
	Lines func(line string)
	// SharesSudo runs it in kit's own session, as kit's own sudo runs, so
	// that the sudo it runs finds the password kit gave sudo: no asking
	// again, and no Touch ID. Only for a program whose sudo never asks at
	// the terminal, as Homebrew's, which asks kit (SUDO_ASKPASS, -A).
	SharesSudo bool
}

// String is the command as a shell would take it, each argument quoted
// where it needs to be.
func (c Command) String() string {
	words := make([]string, 0, 1+len(c.Args))
	for _, w := range append([]string{c.Name}, c.Args...) {
		words = append(words, quote(w))
	}
	return strings.Join(words, " ")
}

// quote quotes w for a shell, when it needs it.
func quote(w string) string {
	if w != "" && strings.IndexFunc(w, needsQuoting) < 0 {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// needsQuoting reports whether a shell would take r other than as itself.
func needsQuoting(r rune) bool {
	plain := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./_-", r)
	return !plain
}

// Result is what a command did.
type Result struct {
	Stdout []byte
	Stderr []byte
	// ExitCode is what it exited with: -1 when it didn't run, or was ended.
	ExitCode int
	Duration time.Duration
	// Group is the process group it ran in, its own, as kit runs a command
	// detached: what it left running is found in it. None for one that
	// didn't start, or ran at kit's terminal.
	Group int
}

// Runner runs commands. Run returns an error when the command couldn't run,
// was ended, or exited other than 0 (an *ExitError); the Result says as much
// as is known either way.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// Finder is a runner that can say whether a program is installed, without
// running it.
type Finder interface {
	Has(name string) bool
}

// Has reports whether r finds the program name, as it would to run it; a
// runner that can't say is taken to.
func Has(r Runner, name string) bool {
	if f, ok := r.(Finder); ok {
		return f.Has(name)
	}
	return true
}

// ExitError is the error for a command that ran and exited other than 0.
type ExitError struct {
	Command string
	Code    int
	// Stderr is the last of what it printed on its standard error.
	Stderr string
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s exited %d", e.Command, e.Code)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// lastLine is the last line of text that isn't blank, cut to a sensible
// length: what's worth putting in an error.
func lastLine(text []byte) string {
	lines := strings.Split(strings.TrimSpace(string(text)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(last) > 200 {
		last = last[:200] + "…"
	}
	return last
}

// Report is what a command did, as an Observer is told.
type Report struct {
	Command Command
	Started time.Time
	Result  Result
	Err     error
}

// Observer is told of every command a runner runs, once it's done, with the
// context it ran in.
type Observer func(ctx context.Context, r Report)

// Observed returns a runner that runs commands with r, telling observe of
// each one.
func Observed(r Runner, observe Observer, now func() time.Time) Runner {
	return observed{runner: r, observe: observe, now: now}
}

type observed struct {
	runner  Runner
	observe Observer
	now     func() time.Time
}

func (o observed) Has(name string) bool {
	return Has(o.runner, name)
}

func (o observed) Run(ctx context.Context, cmd Command) (Result, error) {
	started := o.now()
	res, err := o.runner.Run(ctx, cmd)
	o.observe(ctx, Report{Command: cmd, Started: started, Result: res, Err: err})
	return res, err
}

type secretEnvKey struct{}

// WithSecretEnv returns ctx for running commands that need secrets in their
// environment: env's variables, as in NAME=value, set for each command run
// in it, over any other value of theirs, and for no other command. Their
// values are secrets, to hide in anything said of those commands
// (SecretValues).
func WithSecretEnv(ctx context.Context, env ...string) context.Context {
	if len(env) == 0 {
		return ctx
	}
	return context.WithValue(ctx, secretEnvKey{}, slices.Concat(SecretEnv(ctx), env))
}

// SecretEnv are the variables ctx sets for each command run in it, as
// WithSecretEnv gave them.
func SecretEnv(ctx context.Context) []string {
	env, _ := ctx.Value(secretEnvKey{}).([]string)
	return env
}

// SecretValues are the values of the variables ctx sets for each command run
// in it: what to hide in anything said of those commands.
func SecretValues(ctx context.Context) []string {
	var values []string
	for _, kv := range SecretEnv(ctx) {
		if _, v, _ := strings.Cut(kv, "="); v != "" {
			values = append(values, v)
		}
	}
	return values
}

// WithEnv returns r with env's variables, as they stand when a command
// runs, added to each command's environment.
func WithEnv(r Runner, env func() []string) Runner {
	return withEnv{runner: r, env: env}
}

type withEnv struct {
	runner Runner
	env    func() []string
}

func (w withEnv) Has(name string) bool {
	return Has(w.runner, name)
}

func (w withEnv) Run(ctx context.Context, cmd Command) (Result, error) {
	if env := w.env(); len(env) > 0 {
		cmd.Env = append(slices.Clone(env), cmd.Env...)
	}
	return w.runner.Run(ctx, cmd)
}

// Streamed returns r with each line printed by a command run in a context
// want wants given to lines as it comes, with that context: never a
// secret's, or an interactive command's, which prints to the terminal
// itself.
func Streamed(r Runner, want func(ctx context.Context) bool, lines func(ctx context.Context, cmd Command, line string)) Runner {
	return streamed{runner: r, want: want, lines: lines}
}

type streamed struct {
	runner Runner
	want   func(ctx context.Context) bool
	lines  func(ctx context.Context, cmd Command, line string)
}

func (s streamed) Has(name string) bool {
	return Has(s.runner, name)
}

func (s streamed) Run(ctx context.Context, cmd Command) (Result, error) {
	if !cmd.Secret && !cmd.Interactive && s.want(ctx) {
		given := cmd.Lines
		cmd.Lines = func(line string) {
			if given != nil {
				given(line)
			}
			s.lines(ctx, cmd, line)
		}
	}
	return s.runner.Run(ctx, cmd)
}
