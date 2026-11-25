package relay_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/kind/secret/relay"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// session is the session the vault's sign-in gives.
const session = "vault-session-0123456789"

// vault is a store signed in to with a password, its program vault: a
// session applies to a command as VAULT_SESSION; whether one lasts is vault
// whoami's to say, run in it; and signing out runs vault signout in it.
type vault struct{ store.Driver }

func (vault) About() store.About {
	return store.About{ID: "vault", Name: "The Vault", Program: "vault"}
}

func (vault) SignIn(context.Context, runner.Runner) (string, error) { return session, nil }

func (vault) Apply(session string, c runner.Command) runner.Command {
	c.Env = append(slices.Clone(c.Env), "VAULT_SESSION="+session)
	return c
}

func (v vault) Lasts(ctx context.Context, run runner.Runner, session string) (bool, error) {
	_, err := run.Run(ctx, v.Apply(session, runner.Command{Name: "vault", Args: []string{"whoami"}}))
	if _, over := errors.AsType[*runner.ExitError](err); over {
		return false, nil
	}
	return err == nil, err
}

func (v vault) SignOut(ctx context.Context, run runner.Runner, session string) error {
	_, err := run.Run(ctx, v.Apply(session, runner.Command{Name: "vault", Args: []string{"signout"}}))
	return err
}

// shortDir is a folder of the test's own in the temporary directory, where
// a test may make a socket, its path short enough for one, as t.TempDir's
// may not be.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kit-relay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// served is a relay serving the vault's sign-in: its socket, the fake that
// runs its commands, and why it ended, once done.
type served struct {
	socket string
	run    *runnertest.Fake
	relay  *relay.Relay
	stop   context.CancelFunc
	done   chan struct{}
	why    relay.Ended
}

// serve has a relay serve the vault's sign-in, at a socket of the test's
// own, till the test ends; script, given the fake, before it starts.
func serve(t *testing.T, script func(run *runnertest.Fake)) *served {
	t.Helper()
	s := listen(t, script)
	s.start(t)
	return s
}

// listen has a relay listen for the vault's sign-in, as serve does, till
// it starts.
func listen(t *testing.T, script func(run *runnertest.Fake)) *served {
	t.Helper()
	socket := filepath.Join(shortDir(t), "relay.sock")
	l, err := relay.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	s := &served{socket: socket, run: runnertest.New(t), relay: l, done: make(chan struct{})}
	s.run.On("vault", "whoami")
	s.run.On("vault", "signout")
	if script != nil {
		script(s.run)
	}
	return s
}

// start has the relay serve till the test ends.
func (s *served) start(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	s.stop = stop
	go func() {
		s.why = s.relay.Serve(ctx, s.run, []relay.Held{{Store: vault{}, Session: session}})
		close(s.done)
	}()
	t.Cleanup(func() {
		stop()
		<-s.done
	})
}

// ended waits for the relay to end, and says why.
func (s *served) ended(t *testing.T) relay.Ended {
	t.Helper()
	select {
	case <-s.done:
		return s.why
	case <-time.After(5 * time.Second):
		t.Fatal("the relay is still serving")
		return ""
	}
}

// signedOut fails the test unless the relay signed out of the vault, in
// its session, and removed its socket.
func (s *served) signedOut(t *testing.T) {
	t.Helper()
	signout := slices.IndexFunc(s.run.Commands(), func(c runner.Command) bool { return c.String() == "vault signout" })
	if signout < 0 || !slices.Contains(s.run.Commands()[signout].Env, "VAULT_SESSION="+session) {
		t.Errorf("ran %q: want vault signout, in the session", s.run.Calls())
	}
	if _, err := os.Stat(s.socket); !os.IsNotExist(err) {
		t.Errorf("the socket's still there: %v", err)
	}
}

// ranHere fails the test when anything ran here, rather than at the relay.
func ranHere(t *testing.T, here *runnertest.Fake) {
	t.Helper()
	if calls := here.Calls(); len(calls) > 0 {
		t.Errorf("ran here %q: want the relay to run it", calls)
	}
}

// waitFor waits till the fake has run command.
func waitFor(t *testing.T, run *runnertest.Fake, command string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if slices.Contains(run.Calls(), command) {
			return
		}
	}
	t.Fatalf("%s never ran: ran %q", command, run.Calls())
}

// A command sent to the relay runs there, as the client built it, in the
// store's session, from the client's folder, giving back what it printed
// and how it ended: nothing runs here. A command not sent runs here.
func TestARelayRunsAClientsCommandInTheSession(t *testing.T) {
	here := runnertest.New(t)
	for _, c := range []struct {
		name   string
		args   []string
		script func(s *runnertest.Script)
		stdout string
		// fails is the error the client's given, "" for none; exit, its
		// exit code.
		fails string
		exit  int
	}{
		{"done", []string{"read", "the/ref"}, func(s *runnertest.Script) { s.Prints("the value") }, "the value", "", 0},
		{"exits", []string{"read", "missing"}, func(s *runnertest.Script) { s.PrintsToStderr("no such item\n").Exits(2) }, "", "vault read missing exited 2: no such item", 2},
		{"doesn't run", []string{"read", "gone"}, func(s *runnertest.Script) { s.Fails(errors.New("vault: not found on kit's PATH")) }, "", "vault: not found on kit's PATH", -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := serve(t, func(run *runnertest.Fake) { c.script(run.On("vault", c.args...)) })
			run := relay.Via(relay.Client(here), s.socket, "vault")
			cmd := runner.Command{Name: "vault", Args: c.args, Input: "given", Timeout: time.Minute, Secret: true, Env: []string{"VAULT_OWN=1"}}
			res, err := run.Run(t.Context(), cmd)
			switch {
			case c.fails == "" && err != nil, c.fails != "" && (err == nil || err.Error() != c.fails):
				t.Errorf("Run() error = %v, want %q", err, c.fails)
			case string(res.Stdout) != c.stdout || res.ExitCode != c.exit:
				t.Errorf("Run() = %q, exit %d; want %q, exit %d", res.Stdout, res.ExitCode, c.stdout, c.exit)
			}
			if _, exited := errors.AsType[*runner.ExitError](err); exited != (c.exit > 0) {
				t.Errorf("Run() error = %#v: an exit's error %v, want %v", err, exited, c.exit > 0)
			}
			wd, _ := os.Getwd()
			ran := s.run.Commands()[0]
			if !slices.Equal(ran.Env, []string{"VAULT_OWN=1", "VAULT_SESSION=" + session}) || ran.Input != "given" || !ran.Secret || ran.Timeout != time.Minute || ran.Dir != wd {
				t.Errorf("the relay ran %s in %s, with %q, given %q, a secret %v, timed %v; want it as sent, in the session", ran, ran.Dir, ran.Env, ran.Input, ran.Secret, ran.Timeout)
			}
		})
	}
	ranHere(t, here)
	here.On("vault", "read", "local")
	if _, err := relay.Client(here).Run(t.Context(), runner.Command{Name: "vault", Args: []string{"read", "local"}}); err != nil || !slices.Equal(here.Calls(), []string{"vault read local"}) {
		t.Errorf("a command not sent: %v, ran here %q", err, here.Calls())
	}
}

// The relay runs only a store's own program, for a store it holds the
// sign-in to: anything else it refuses, running nothing.
func TestARelayRefusesWhatsNotTheStores(t *testing.T) {
	s := serve(t, nil)
	for _, c := range []struct {
		name, store, program, says string
	}{
		{"another program", "vault", "sh", "kit secret signin runs only vault, for The Vault"},
		{"another store", "other", "vault", "kit secret signin holds no sign-in to other"},
	} {
		t.Run(c.name, func(t *testing.T) {
			here := runnertest.New(t)
			_, err := relay.Via(relay.Client(here), s.socket, c.store).Run(t.Context(), runner.Command{Name: c.program, Args: []string{"-c", "true"}})
			if err == nil || err.Error() != c.says {
				t.Errorf("Run() error = %v, want %q", err, c.says)
			}
			ranHere(t, here)
		})
	}
	if calls := s.run.Calls(); len(calls) > 0 {
		t.Errorf("the relay ran %q", calls)
	}
}

// Asked whether it holds a store's sign-in, the relay says so for one it
// holds that lasts, as the store says, and not for another.
func TestARelaySaysWhichSignInsItHolds(t *testing.T) {
	s := serve(t, nil)
	if !relay.Holds(t.Context(), s.socket, "vault") || relay.Holds(t.Context(), s.socket, "other") {
		t.Error("Holds(): want the vault's sign-in held, and no other")
	}
	if ran := s.run.Commands(); len(ran) != 1 || ran[0].String() != "vault whoami" || !slices.Contains(ran[0].Env, "VAULT_SESSION="+session) {
		t.Errorf("ran %q: want vault whoami in the session", s.run.Calls())
	}
}

// The relay answers only processes of the user it serves: any other's
// request is closed on, unanswered, and the relay serves on.
func TestARelayAnswersItsUserAlone(t *testing.T) {
	s := listen(t, nil)
	relay.SetOwner(s.relay, os.Getuid()+1)
	s.start(t)
	if relay.Holds(t.Context(), s.socket, "vault") {
		t.Error("Holds(), another user's = true")
	}
	if _, err := relay.Stop(t.Context(), s.socket); err == nil || errors.Is(err, relay.ErrNone) {
		t.Errorf("Stop(), another user's = %v; want it unanswered", err)
	}
	select {
	case <-s.done:
		t.Errorf("the relay ended: %s", s.why)
	default:
	}
	if calls := s.run.Calls(); len(calls) > 0 {
		t.Errorf("the relay ran %q", calls)
	}
}

// The relay listens at a socket only the person's user may use.
func TestARelaysSocketIsThePersonsAlone(t *testing.T) {
	s := serve(t, nil)
	info, err := os.Stat(s.socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Errorf("the socket is %v, %v; want a socket, the person's alone", info, err)
	}
}

// Asked to stop, the relay signs out of its stores, removes its socket,
// says which it signed out of, and ends: after, none answers.
func TestStopEndsTheRelay(t *testing.T) {
	s := serve(t, nil)
	ended, err := relay.Stop(t.Context(), s.socket)
	if err != nil || !slices.Equal(ended, []string{"The Vault"}) {
		t.Errorf("Stop() = %q, %v; want The Vault signed out of", ended, err)
	}
	if why := s.ended(t); why != relay.Asked {
		t.Errorf("ended as %q, want %q", why, relay.Asked)
	}
	s.signedOut(t)
	if _, err := relay.Stop(t.Context(), s.socket); !errors.Is(err, relay.ErrNone) {
		t.Errorf("Stop(), ended = %v, want %v", err, relay.ErrNone)
	}
}

// The relay ends with the run it serves in, as at ctrl-C or its terminal
// closing: it signs out all the same, and removes its socket.
func TestTheRelayEndsWithItsRun(t *testing.T) {
	s := serve(t, nil)
	s.stop()
	if why := s.ended(t); why != relay.Stopped {
		t.Errorf("ended as %q, want %q", why, relay.Stopped)
	}
	s.signedOut(t)
}

// Once the store says its session's over, asked whether the relay holds
// it, or as a command fails, the relay ends, signing out: the command's
// client still has its answer.
func TestASessionOverEndsTheRelay(t *testing.T) {
	for _, c := range []struct {
		name string
		ask  func(t *testing.T, socket string)
	}{
		{"asked", func(t *testing.T, socket string) {
			if relay.Holds(t.Context(), socket, "vault") {
				t.Error("Holds(), the session over = true")
			}
		}},
		{"as a command fails", func(t *testing.T, socket string) {
			here := runnertest.New(t)
			_, err := relay.Via(relay.Client(here), socket, "vault").Run(t.Context(), runner.Command{Name: "vault", Args: []string{"read", "a"}})
			if _, exited := errors.AsType[*runner.ExitError](err); !exited {
				t.Errorf("Run() error = %v, want the command's exit", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := serve(t, func(run *runnertest.Fake) {
				run.On("vault", "whoami").Exits(1)
				run.On("vault", "read", "a").Exits(1)
			})
			c.ask(t, s.socket)
			if why, want := s.ended(t), relay.Ended("The Vault ended the sign-in"); why != want {
				t.Errorf("ended as %q, want %q", why, want)
			}
			s.signedOut(t)
		})
	}
}

// staleSocket leaves a socket at path that nothing answers, as a relay
// killed leaves one.
func staleSocket(t *testing.T, path string) {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
}

// A socket nothing answers is stale: a client removes it, going on as
// with no relay, and a relay listens in its place.
func TestAStaleSocketIsRemoved(t *testing.T) {
	socket := filepath.Join(shortDir(t), "relay.sock")
	gone := func(what string) {
		if _, err := os.Stat(socket); !os.IsNotExist(err) {
			t.Errorf("%s: the stale socket's still there: %v", what, err)
		}
	}
	staleSocket(t, socket)
	if relay.Holds(t.Context(), socket, "vault") {
		t.Error("Holds(), stale = true")
	}
	gone("Holds")
	staleSocket(t, socket)
	if _, err := relay.Stop(t.Context(), socket); !errors.Is(err, relay.ErrNone) {
		t.Errorf("Stop(), stale = %v, want %v", err, relay.ErrNone)
	}
	gone("Stop")
	staleSocket(t, socket)
	l, err := relay.Listen(socket)
	if err != nil {
		t.Fatalf("Listen(), stale = %v", err)
	}
	_ = l.Close()
	gone("Close")
}

// One relay at a time: another finding one answering at its socket
// doesn't listen, and the first serves on.
func TestOneRelayAtATime(t *testing.T) {
	s := serve(t, nil)
	if _, err := relay.Listen(s.socket); !errors.Is(err, relay.ErrOpen) {
		t.Errorf("Listen(), one answering = %v, want %v", err, relay.ErrOpen)
	}
	if !relay.Holds(t.Context(), s.socket, "vault") {
		t.Error("Holds(): want the first relay serving still")
	}
}

// A socket's path longer than macOS allows is refused, saying so, and no
// client finds a relay there.
func TestASocketPathTooLong(t *testing.T) {
	socket := filepath.Join(shortDir(t), strings.Repeat("d", 100), "relay.sock")
	if _, err := relay.Listen(socket); err == nil || !strings.Contains(err.Error(), "longer than the 103 bytes macOS allows a socket's path") {
		t.Errorf("Listen() = %v", err)
	}
	if relay.Holds(t.Context(), socket, "vault") {
		t.Error("Holds() = true")
	}
	if _, err := relay.Stop(t.Context(), socket); !errors.Is(err, relay.ErrNone) {
		t.Errorf("Stop() = %v, want %v", err, relay.ErrNone)
	}
}

// Clients at once have their commands run one at a time.
func TestARelayRunsOneCommandAtATime(t *testing.T) {
	var running, most atomic.Int32
	s := serve(t, func(run *runnertest.Fake) {
		run.On("vault", "read", "a").Does(func() {
			n := running.Add(1)
			for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
			}
			time.Sleep(10 * time.Millisecond)
			running.Add(-1)
		})
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			run := relay.Via(relay.Client(runnertest.New(t)), s.socket, "vault")
			if _, err := run.Run(context.Background(), runner.Command{Name: "vault", Args: []string{"read", "a"}}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if most.Load() != 1 || len(s.run.Calls()) != 4 {
		t.Errorf("ran %q, %d at most at once; want each alone", s.run.Calls(), most.Load())
	}
}

// A client that hangs up, as at its ctrl-C, gives its command up, and the
// relay serves on.
func TestAClientHangingUpGivesItsCommandUp(t *testing.T) {
	s := serve(t, func(run *runnertest.Fake) {
		run.On("vault", "read", "slow").Hangs()
		run.On("vault", "read", "next").Prints("next")
	})
	run := relay.Via(relay.Client(runnertest.New(t)), s.socket, "vault")
	ctx, hangUp := context.WithCancel(t.Context())
	given := make(chan error, 1)
	go func() {
		_, err := run.Run(ctx, runner.Command{Name: "vault", Args: []string{"read", "slow"}})
		given <- err
	}()
	waitFor(t, s.run, "vault read slow")
	hangUp()
	if err := <-given; !errors.Is(err, context.Canceled) {
		t.Errorf("Run(), hung up = %v, want it given up", err)
	}
	if res, err := run.Run(t.Context(), runner.Command{Name: "vault", Args: []string{"read", "next"}}); err != nil || string(res.Stdout) != "next" {
		t.Errorf("Run(), after = %q, %v; want the relay serving on", res.Stdout, err)
	}
}

// The relay ending while it runs a client's command gives the command up,
// and the client fails, saying so, never running it itself.
func TestTheRelayEndingFailsAClientsCommand(t *testing.T) {
	s := serve(t, func(run *runnertest.Fake) { run.On("vault", "read", "slow").Hangs() })
	here := runnertest.New(t)
	given := make(chan error, 1)
	go func() {
		_, err := relay.Via(relay.Client(here), s.socket, "vault").Run(t.Context(), runner.Command{Name: "vault", Args: []string{"read", "slow"}})
		given <- err
	}()
	waitFor(t, s.run, "vault read slow")
	s.stop()
	if err := <-given; err == nil || err.Error() != "vault read slow: kit secret signin ended before it answered" {
		t.Errorf("Run(), the relay ending = %v", err)
	}
	if why := s.ended(t); why != relay.Stopped {
		t.Errorf("ended as %q", why)
	}
	ranHere(t, here)
	if _, err := relay.Via(relay.Client(here), s.socket, "vault").Run(t.Context(), runner.Command{Name: "vault", Args: []string{"read", "a"}}); !errors.Is(err, relay.ErrNone) {
		t.Errorf("Run(), ended = %v, want %v", err, relay.ErrNone)
	}
	ranHere(t, here)
}
