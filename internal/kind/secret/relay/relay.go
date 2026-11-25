// Package relay is kit secret signin's relay. A store's sign-in with a
// password may hold only for what runs in the terminal the person signed in
// at (store.Passworded), so the kit that signed in stays running there,
// holding each session in memory alone, and runs the stores' commands for
// every other kit: an agent's, the hourly run's, the person's own. They
// reach it on a unix socket in kit's state (Socket) that only the person's
// user may use. A command is one a store's driver built, sent whole, and
// the relay runs it only if it's that store's program, in the store's
// session (store.Passworded's Apply), one at a time, giving back what it
// printed and how it exited. The relay knows no store of its own: each is
// a driver, picked by kit-config's secrets.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
)

// socketName is the socket's name, in kit's state.
const socketName = "secret-signin.sock"

// Socket is where the relay listens, in kit's state folder, stateDir.
func Socket(stateDir string) string { return filepath.Join(stateDir, socketName) }

// maxPath is the longest path a unix socket can have on macOS: sun_path
// holds 104 bytes, the last ending the path.
const maxPath = 103

// ErrOpen is why a relay doesn't listen: another answers at its socket.
var ErrOpen = errors.New("kit secret signin is open already, in another terminal")

// Relay is a relay listening, till it serves (Serve) or closes.
type Relay struct {
	ln net.Listener
	// owner is the user whose processes alone it answers: the person's.
	owner int
}

// Listen listens at socket, only the person's user able to connect:
// ErrOpen when a relay answers there already. A socket there that nothing
// answers was left by a relay that didn't end, and is replaced. Relays
// look and listen in turn, under a lock beside the socket, so that one
// never takes another's for one left.
func Listen(socket string) (*Relay, error) {
	if len(socket) > maxPath {
		return nil, fmt.Errorf("%s is longer than the %d bytes macOS allows a socket's path", socket, maxPath)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, fmt.Errorf("make kit's state folder: %w", err)
	}
	unlock, err := lock(socket + ".lock")
	if err != nil {
		return nil, fmt.Errorf("lock kit secret signin's socket: %w", err)
	}
	defer unlock()
	ln, err := net.Listen("unix", socket)
	if errors.Is(err, syscall.EADDRINUSE) {
		if Answers(context.Background(), socket) {
			return nil, ErrOpen
		}
		if err := os.Remove(socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("remove the socket left at %s: %w", socket, err)
		}
		ln, err = net.Listen("unix", socket)
	}
	if err != nil {
		return nil, fmt.Errorf("listen for kit's commands: %w", err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("keep the socket to you alone: %w", err)
	}
	return &Relay{ln: ln, owner: os.Getuid()}, nil
}

// lock takes the lock of the file at path, made for it: what gives it up.
func lock(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// Close stops listening, and removes the socket.
func (r *Relay) Close() error { return r.ln.Close() }

// Held is a store's sign-in, as the relay holds it: the store, and the
// session it gave.
type Held struct {
	Store   store.Passworded
	Session string
}

// SignOut signs out of each store held, ignoring what the stores say, as
// the relay ends either way.
func SignOut(ctx context.Context, run runner.Runner, held []Held) {
	for _, h := range held {
		_ = h.Store.SignOut(ctx, run, h.Session)
	}
}

// Ended is why a relay ended.
type Ended string

// Stopped is a relay's ending with the run it serves in, as with ctrl-C or
// its terminal closing; Asked, at a stop request (Stop).
const (
	Stopped Ended = "it was stopped"
	Asked   Ended = "kit secret signout asked"
)

// over is a relay's ending once the store called name says its session's
// over.
func over(name string) Ended { return Ended(name + " ended the sign-in") }

// Serve runs the commands other kits send for held's stores, one at a
// time, with run, till ctx ends, a stop request comes (Stop), or a store
// says its session's over (store.Passworded's Lasts), whether asked
// (Holds) or as a command fails. Then it stops listening, removing the
// socket, gives up what's running, signs out of each store, and says why
// it ended.
func (r *Relay) Serve(ctx context.Context, run runner.Runner, held []Held) Ended {
	s := &serving{relay: r, run: run, held: held, turn: make(chan struct{}, 1), end: make(chan Ended, 1), ended: make(chan struct{})}
	running, giveUp := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := r.ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() { s.answer(running, c) })
		}
	})
	var why Ended
	select {
	case <-ctx.Done():
		why = Stopped
	case why = <-s.end:
	}
	_ = r.Close()
	giveUp()
	SignOut(context.WithoutCancel(ctx), run, held)
	close(s.ended)
	wg.Wait()
	return why
}

// serving is a relay serving: what runs its commands, the stores' sign-ins
// it holds, its turn, one command at a time, how it's told to end, and
// what's closed once it has.
type serving struct {
	relay *Relay
	run   runner.Runner
	held  []Held
	turn  chan struct{}
	end   chan Ended
	ended chan struct{}
}

// finish has the relay end, for why: the first why it's given is kept.
func (s *serving) finish(why Ended) {
	select {
	case s.end <- why:
	default:
	}
}

// take waits for the turn to run a store's command: false once ctx ends
// first. give gives it up.
func (s *serving) take(ctx context.Context) bool {
	select {
	case s.turn <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *serving) give() { <-s.turn }

// heldFor is the sign-in held to the store whose ID is id.
func (s *serving) heldFor(id string) (Held, bool) {
	for _, h := range s.held {
		if h.Store.About().ID == id {
			return h, true
		}
	}
	return Held{}, false
}

// names are the names of the stores held.
func (s *serving) names() []string {
	names := make([]string, len(s.held))
	for i, h := range s.held {
		names[i] = h.Store.About().Name
	}
	return names
}

// requestWait is how long a relay waits for a client's request, which it
// sends as it connects: a client that sends none is closed on, rather than
// keep the relay from ending.
var requestWait = 5 * time.Second

// answer answers what's asked at c, one request, from a process of the
// person's user alone, each other closed on unanswered. It takes the
// request up at once (response's Ack), for the client to know it's not
// stopped, then answers it.
func (s *serving) answer(ctx context.Context, c net.Conn) {
	defer func() { _ = c.Close() }()
	if !s.relay.owners(c) {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(requestWait))
	var req request
	if err := json.NewDecoder(c).Decode(&req); err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	out := json.NewEncoder(c)
	if err := out.Encode(response{Ack: true}); err != nil {
		return
	}
	var res response
	switch req.Verb {
	case stopping:
		s.finish(Asked)
		<-s.ended
		res.Ended = s.names()
	case holding:
		res.Holds = s.holds(ctx, req.Store)
	case running:
		var answered bool
		if res, answered = s.command(ctx, c, req); !answered {
			return
		}
	}
	_ = out.Encode(res)
}

// owners reports whether the process at c's other end runs as the relay's
// owner.
func (r *Relay) owners(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	uid, err := peerUID(uc)
	return err == nil && uid == r.owner
}

// holds reports whether the relay holds a sign-in to the store whose ID is
// id that lasts, as the store says: one that doesn't answer is taken to,
// and one it says is over ends the relay. It doesn't wait for a command
// running, which can take longer than a client waits: that the command's
// running says the sign-in held as it began.
func (s *serving) holds(ctx context.Context, id string) bool {
	h, ok := s.heldFor(id)
	if !ok || ctx.Err() != nil {
		return false
	}
	select {
	case s.turn <- struct{}{}:
	default:
		return true
	}
	defer s.give()
	if lasts, err := h.Store.Lasts(ctx, s.run, h.Session); err == nil && !lasts {
		s.finish(over(h.Store.About().Name))
		return false
	}
	return true
}

// command runs req's command in its store's session, as the client would
// have run it, refusing one for a store it doesn't hold, or of another
// program than the store's. A command that fails has the store say whether
// its session lasts, and one that's over ends the relay. There's no one to
// answer, false, when the client hangs up first, as at its ctrl-C, or the
// relay ends, either giving the command up.
func (s *serving) command(ctx context.Context, c net.Conn, req request) (response, bool) {
	h, ok := s.heldFor(req.Store)
	if !ok {
		return response{Refused: "kit secret signin holds no sign-in to " + req.Store}, true
	}
	if a := h.Store.About(); req.Command.Name != a.Program {
		return response{Refused: "kit secret signin runs only " + a.Program + ", for " + a.Name}, true
	}
	ctx, hungUp := context.WithCancel(ctx)
	defer hungUp()
	go func() {
		// What's read past the request, its newline, is nothing: only the
		// client's end is.
		_, _ = io.Copy(io.Discard, c)
		hungUp()
	}()
	if !s.take(ctx) {
		return response{}, false
	}
	defer s.give()
	res, err := s.run.Run(runner.WithSecretEnv(ctx, req.Command.SecretEnv...), h.Store.Apply(h.Session, req.Command.runner()))
	if ctx.Err() != nil {
		return response{}, false
	}
	if _, failed := errors.AsType[*runner.ExitError](err); failed {
		if lasts, err := h.Store.Lasts(ctx, s.run, h.Session); err == nil && !lasts {
			s.finish(over(h.Store.About().Name))
		}
	}
	return ran(res, err), true
}

// verb is what a client asks: to run a command, whether a store's sign-in
// is held, or that the relay stop.
type verb string

const (
	running  verb = "run"
	holding  verb = "holds"
	stopping verb = "stop"
)

// request is what a client asks, one to a connection.
type request struct {
	Verb verb
	// Store is the ID of the store whose sign-in it's about.
	Store   string  `json:",omitempty"`
	Command command `json:",omitzero"`
}

// command is a command a store's driver built, as a client sends it: what
// of runner.Command a relay can run for it.
type command struct {
	Name           string
	Args           []string      `json:",omitempty"`
	Dir, Input     string        `json:",omitempty"`
	Timeout        time.Duration `json:",omitempty"`
	Secret         bool          `json:",omitempty"`
	Env, SecretEnv []string      `json:",omitempty"`
}

// sent is cmd as a client sends it, run in a context that sets secretEnv
// (runner.WithSecretEnv).
func sent(cmd runner.Command, secretEnv []string) command {
	return command{Name: cmd.Name, Args: cmd.Args, Dir: cmd.Dir, Input: cmd.Input, Timeout: cmd.Timeout, Secret: cmd.Secret, Env: cmd.Env, SecretEnv: secretEnv}
}

// runner is c as the relay runs it.
func (c command) runner() runner.Command {
	return runner.Command{Name: c.Name, Args: c.Args, Dir: c.Dir, Input: c.Input, Timeout: c.Timeout, Secret: c.Secret, Env: c.Env}
}

// response is a relay's answer.
type response struct {
	// Ack is the relay taking a request up, before its answer.
	Ack bool `json:",omitempty"`
	// Stdout, Stderr, Exit and Duration are what a command did, as
	// runner.Result has it; Err, why it didn't run, or finish, when it
	// didn't exit by itself.
	Stdout, Stderr []byte        `json:",omitempty"`
	Exit           int           `json:",omitempty"`
	Duration       time.Duration `json:",omitempty"`
	Err            string        `json:",omitempty"`
	// Refused is why the relay wouldn't run it.
	Refused string `json:",omitempty"`
	// Holds is whether the store's sign-in is held, and lasts.
	Holds bool `json:",omitempty"`
	// Ended are the names of the stores signed out of, the relay stopped.
	Ended []string `json:",omitempty"`
}

// ran is the answer for a command that did res, ending with err.
func ran(res runner.Result, err error) response {
	r := response{Stdout: res.Stdout, Stderr: res.Stderr, Exit: res.ExitCode, Duration: res.Duration}
	if _, exited := errors.AsType[*runner.ExitError](err); err != nil && !exited {
		r.Err = err.Error()
	}
	return r
}
