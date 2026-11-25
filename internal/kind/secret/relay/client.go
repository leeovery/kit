package relay

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/leeovery/kit/internal/hold"
	"github.com/leeovery/kit/internal/runner"
)

// ErrNone is why a relay wasn't asked: none answers at its socket.
var ErrNone = errors.New("kit secret signin isn't open")

// errUnanswered is why a request has no answer, the relay having ended
// meanwhile.
var errUnanswered = errors.New("kit secret signin ended before it answered")

// holdsWait is how long a client waits to hear whether a relay holds a
// store's sign-in: the store answers within a time of its own, and a relay
// stopped, as with ctrl-Z, never does.
const holdsWait = 30 * time.Second

// lateBy is how much longer than a command's own timeout a client waits for
// a relay's answer: what the relay takes to give it up and answer.
const lateBy = 10 * time.Second

// Holds reports whether a relay answers at socket, holding a sign-in that
// lasts to the store whose ID is store: one that doesn't say so within
// holdsWait is taken not to.
func Holds(ctx context.Context, socket, store string) bool {
	ctx, cancel := context.WithTimeout(ctx, holdsWait)
	defer cancel()
	res, err := ask(ctx, socket, request{Verb: holding, Store: store})
	return err == nil && res.Holds
}

// Stop has the relay at socket end, as it ends by itself (Serve): the
// names of the stores it signed out of. ErrNone when none answers.
func Stop(ctx context.Context, socket string) ([]string, error) {
	res, err := ask(ctx, socket, request{Verb: stopping})
	return res.Ended, err
}

// Via is run, each command it runs sent to the relay at socket, to run in
// the session it holds of the store whose ID is store, by the Client
// beneath it: for a store the relay was found to hold (Holds).
func Via(run runner.Runner, socket, store string) runner.Runner {
	return via{runner: run, to: to{socket: socket, store: store}}
}

// to is where a command's sent: the relay at socket, for the store whose
// ID is store.
type to struct{ socket, store string }

type toKey struct{}

type via struct {
	runner runner.Runner
	to     to
}

func (v via) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	return v.runner.Run(context.WithValue(ctx, toKey{}, v.to), cmd)
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
	if t, ok := ctx.Value(toKey{}).(to); ok {
		return t.run(ctx, cmd)
	}
	return c.here.Run(ctx, cmd)
}

func (c client) Find(name string) (string, error) { return runner.Find(c.here, name) }

func (c client) Has(name string) bool { return runner.Has(c.here, name) }

func (c client) Environ(ctx context.Context, cmd runner.Command) ([]string, bool) {
	return runner.Environ(ctx, c.here, cmd)
}

// run has the relay run cmd, in this process's folder unless cmd names
// one, as a path cmd gives may be relative to it, waiting for its answer
// no longer than cmd's timeout and lateBy.
func (t to) run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	failed := runner.Result{ExitCode: -1}
	if cmd.Dir == "" {
		cmd.Dir, _ = os.Getwd()
	}
	if cmd.Timeout != runner.Unlimited {
		var cancel context.CancelFunc
		ctx, cancel = hold.WithTimeout(ctx, cmp.Or(cmd.Timeout, runner.DefaultTimeout)+lateBy)
		defer cancel()
	}
	res, err := ask(ctx, t.socket, request{Verb: running, Store: t.store, Command: sent(cmd, runner.SecretEnv(ctx))})
	if err != nil {
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

// ask sends req to the relay at socket, and reads its answer: ErrNone when
// none answers there; errUnanswered when it ends before answering; ctx's
// error once it ends first, the request given up.
func ask(ctx context.Context, socket string, req request) (response, error) {
	var res response
	c, err := connect(ctx, socket)
	if err != nil {
		return res, err
	}
	defer func() { _ = c.Close() }()
	defer context.AfterFunc(ctx, func() { _ = c.Close() })()
	err = json.NewEncoder(c).Encode(req)
	if err == nil {
		err = json.NewDecoder(c).Decode(&res)
	}
	switch {
	case err == nil:
		return res, nil
	case ctx.Err() != nil:
		return res, ctx.Err()
	}
	return res, errUnanswered
}

// connect connects to the relay at socket: ErrNone when none answers, a
// socket there that refuses, left by a relay that didn't end, removed.
func connect(ctx context.Context, socket string) (net.Conn, error) {
	if len(socket) > maxPath {
		return nil, ErrNone
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if errors.Is(err, syscall.ECONNREFUSED) {
		_ = os.Remove(socket)
	}
	if err != nil {
		return nil, ErrNone
	}
	return c, nil
}
