package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// ErrNone is why a relay wasn't asked: none answers at its socket.
var ErrNone = errors.New("kit secret signin isn't open")

// errStalled is why a request wasn't taken up: the relay's process is
// stopped, as by ctrl-Z in its terminal, or frozen, as it takes each up at
// once otherwise.
var errStalled = errors.New("kit secret signin didn't take the request up: it may be stopped, as by ctrl-Z in its terminal")

// errLate is why a request has no answer: the relay didn't give one in
// time, as a relay stopped since it took the request up.
var errLate = errors.New("kit secret signin didn't answer in time")

// errUnanswered is why a request has no answer, the relay having ended
// meanwhile.
var errUnanswered = errors.New("kit secret signin ended before it answered")

// waits are how long a client waits on a relay: ack, for it to take a
// request up, which it does at once unless it's stopped; holds, for it to
// say whether it holds a sign-in, which its store answers within a time of
// its own; stop, for it to sign out and end; and answer, for a command's
// answer, the session applied, so it asks no one. A relay stopped costs a
// run one of these, once, at most: its line's down after (Line).
var waits = struct{ ack, holds, stop, answer time.Duration }{
	ack: 5 * time.Second, holds: 30 * time.Second, stop: 30 * time.Second, answer: time.Minute,
}

// Answers reports whether a relay answers at socket.
func Answers(ctx context.Context, socket string) bool {
	c, err := connect(ctx, socket)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// Line is a run's way to a relay holding a store's sign-in, for that
// store's commands (Via): down once the relay didn't take one up, or answer
// it, in time, every command after failing at once, rather than each
// waiting on a relay that's stopped.
type Line struct {
	socket, store string
	mu            sync.Mutex
	down          error
}

// Holds is the line to the relay at socket, when it holds a sign-in that
// lasts to the store whose ID is store: nil when none does, or none says
// so in time.
func Holds(ctx context.Context, socket, store string) *Line {
	res, err := ask(ctx, socket, request{Verb: holding, Store: store}, waits.holds)
	if err != nil || !res.Holds {
		return nil
	}
	return &Line{socket: socket, store: store}
}

// Stop has the relay at socket end, as it ends by itself (Serve): the
// names of the stores it signed out of. ErrNone when none answers.
func Stop(ctx context.Context, socket string) ([]string, error) {
	res, err := ask(ctx, socket, request{Verb: stopping}, waits.stop)
	return res.Ended, err
}

// Via is run, each command it runs sent along line to its relay, to run in
// the session it holds of line's store, by the Client beneath it.
func Via(run runner.Runner, line *Line) runner.Runner {
	return via{runner: run, line: line}
}

type lineKey struct{}

type via struct {
	runner runner.Runner
	line   *Line
}

func (v via) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	return v.runner.Run(context.WithValue(ctx, lineKey{}, v.line), cmd)
}

func (v via) Find(name string) (string, error) { return runner.Find(v.runner, name) }

func (v via) Has(name string) bool { return runner.Has(v.runner, name) }

func (v via) Environ(ctx context.Context, cmd runner.Command) ([]string, bool) {
	return runner.Environ(ctx, v.runner, cmd)
}

// Client is a runner that runs each command with here, this process's own,
// but a command sent to a relay (Via), which it sends there: it goes
// beneath what reports each command, so a run's log records that one as
// ever. One the relay doesn't answer fails, never run here instead: the
// store was found signed in through the relay, and here it would ask the
// person, who may be away.
func Client(here runner.Runner) runner.Runner { return client{here: here} }

type client struct{ here runner.Runner }

func (c client) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	if l, ok := ctx.Value(lineKey{}).(*Line); ok {
		return l.run(ctx, cmd)
	}
	return c.here.Run(ctx, cmd)
}

func (c client) Find(name string) (string, error) { return runner.Find(c.here, name) }

func (c client) Has(name string) bool { return runner.Has(c.here, name) }

func (c client) Environ(ctx context.Context, cmd runner.Command) ([]string, bool) {
	return runner.Environ(ctx, c.here, cmd)
}

// lateBy is how much longer than a command's own timeout a client waits for
// its answer: what the relay takes to give it up and say so.
const lateBy = 10 * time.Second

// run has the relay run cmd, in this process's folder unless cmd names
// one, as a path cmd gives may be relative to it, waiting for its answer
// no longer than waits.answer, or cmd's own timeout and lateBy, if that's
// shorter. The line goes down when the relay doesn't take it up, or answer
// it.
func (l *Line) run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	failed := runner.Result{ExitCode: -1}
	if err := l.isDown(); err != nil {
		return failed, fmt.Errorf("%s: %w", cmd, err)
	}
	if cmd.Dir == "" {
		cmd.Dir, _ = os.Getwd()
	}
	wait := waits.answer
	if cmd.Timeout > 0 {
		wait = min(wait, cmd.Timeout+lateBy)
	}
	res, err := ask(ctx, l.socket, request{Verb: running, Store: l.store, Command: sent(cmd, runner.SecretEnv(ctx))}, wait)
	if err != nil {
		if ctx.Err() == nil {
			l.goDown(err)
		}
		return failed, fmt.Errorf("%s: %w", cmd, err)
	}
	if res.Refused != "" {
		return failed, errors.New(res.Refused)
	}
	result := runner.Result{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.Exit, Duration: res.Duration}
	switch {
	case res.Err != "":
		return result, errors.New(res.Err)
	case res.Exit != 0:
		return result, runner.NewExitError(cmd.String(), res.Exit, res.Stderr)
	}
	return result, nil
}

// isDown is why the line's down: nil while it isn't.
func (l *Line) isDown() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.down
}

// goDown takes the line down, for why.
func (l *Line) goDown(why error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.down = why
}

// ask sends req to the relay at socket, and reads its answer, given wait
// once the relay's taken the request up, which it does at once: ErrNone
// when none answers there; errStalled when it doesn't take it up in time;
// errLate when it doesn't answer in time; errUnanswered when it ends
// first; ctx's error once ctx ends first, the request given up.
func ask(ctx context.Context, socket string, req request, wait time.Duration) (response, error) {
	var res response
	c, err := connect(ctx, socket)
	if err != nil {
		return res, err
	}
	defer func() { _ = c.Close() }()
	defer context.AfterFunc(ctx, func() { _ = c.Close() })()
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return res, unanswered(ctx, err, errUnanswered)
	}
	in := json.NewDecoder(c)
	_ = c.SetReadDeadline(time.Now().Add(waits.ack))
	if err := in.Decode(&res); err != nil || !res.Ack {
		return response{}, unanswered(ctx, err, errStalled)
	}
	res = response{}
	_ = c.SetReadDeadline(time.Now().Add(wait))
	if err := in.Decode(&res); err != nil {
		return res, unanswered(ctx, err, errLate)
	}
	return res, nil
}

// unanswered is why a request read err for its answer: ctx's error once ctx
// has ended; late, once the wait for it did; else that the relay ended.
func unanswered(ctx context.Context, err, late error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, os.ErrDeadlineExceeded):
		return late
	}
	return errUnanswered
}

// connect connects to the relay at socket: ErrNone when none answers. A
// socket there that refuses is left for a relay to replace (Listen).
func connect(ctx context.Context, socket string) (net.Conn, error) {
	if len(socket) > maxPath {
		return nil, ErrNone
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, ErrNone
	}
	return c, nil
}
