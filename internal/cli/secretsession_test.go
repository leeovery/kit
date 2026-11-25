package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/kind/secret/onepassword/optest"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/screen/screentest"
)

// token is the session's token 1Password gives the tests' sign-in.
const token = "tok-en-0123456789abcdef"

// signedIn is what op signin prints the tests' sign-in as: the session's
// variable, for a shell to set.
const signedIn = "export OP_SESSION_ABC123=\"" + token + "\"\n# This command is meant to be used with your shell's eval function.\n"

// leaks fails the test when anything kit printed, or anything in its logs
// or state, holds the session's token or a secret's value.
func (w *world) leaks(t *testing.T, printed ...string) {
	t.Helper()
	for _, p := range printed {
		if strings.Contains(p, token) || strings.Contains(p, "the value") {
			t.Errorf("kit printed the session's token or a value:\n%s", p)
		}
	}
	for _, dir := range []string{filepath.Join(w.home, "Library", "Logs", "kit"), filepath.Join(w.env["XDG_STATE_HOME"], "kit")} {
		files, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, f := range files {
			if data, _ := os.ReadFile(f); strings.Contains(string(data), token) || strings.Contains(string(data), "the value") {
				t.Errorf("%s holds the session's token or a value", f)
			}
		}
	}
}

// opRan is the last run of op with args, as the fake has it.
func opRan(t *testing.T, w *world, args ...string) runner.Command {
	t.Helper()
	for _, c := range slices.Backward(w.fake.Commands()) {
		if c.Name == "op" && slices.Equal(c.Args, args) {
			return c
		}
	}
	t.Fatalf("op %q never ran: ran %q", args, w.fake.Calls())
	return runner.Command{}
}

// shortState is a folder for kit's state, as XDG_STATE_HOME names it, in
// the temporary directory, short enough for kit secret signin's socket.
func shortState(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kit-state-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// stateIn has kit keep its state in w under state (XDG_STATE_HOME), what
// the world keeps there moved with it, as the Mac's name, unless another
// world's there already.
func (w *world) stateIn(t *testing.T, state string) {
	t.Helper()
	w.env["XDG_STATE_HOME"] = state
	if _, err := os.Stat(filepath.Join(state, "kit")); err == nil {
		return
	}
	if err := os.CopyFS(filepath.Join(state, "kit"), os.DirFS(w.stateDir())); err != nil {
		t.Fatal(err)
	}
}

// socketIn is kit secret signin's socket, kit's state under state.
func socketIn(state string) string { return filepath.Join(state, "kit", "secret-signin.sock") }

// printed is what a run of kit printed, its output's words run together
// (flat), and the status it exited with.
type printed struct {
	out, errOut string
	code        int
}

// flat is what kit drew at a terminal, out, as plain words: its lines,
// wrapped, run together, without the rule down their left.
func flat(out string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(ansi.Strip(out), "│", " ")), " ")
}

// signingIn runs kit secret signin at a terminal in w, its state in state,
// op signing the person in, till ctx ends, or the test does, or it ends by
// itself: what it printed, once it has. It's waited for till its socket's
// there.
func signingIn(t *testing.T, ctx context.Context, w *world, state string) <-chan printed {
	t.Helper()
	w.terminal = true
	w.stateIn(t, state)
	w.ctx = ctx
	w.fake.On("op", "signin").Prints(signedIn)
	w.fake.On("op", "whoami")
	w.fake.On("op", "signout")
	done, finished := make(chan printed, 1), make(chan struct{})
	go func() {
		out, errOut, code := w.run(t, "secret", "signin")
		done <- printed{flat(out), errOut, code}
		close(finished)
	}()
	t.Cleanup(func() { <-finished })
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(socketIn(state)); err == nil {
			return done
		}
		if time.Now().After(deadline) {
			t.Fatal("kit secret signin never listened")
		}
	}
}

// elsewhere is a world whose kit's state is state: another terminal, or an
// agent's shell, on the same Mac.
func elsewhere(t *testing.T, state string) *world {
	t.Helper()
	w := secretsWorld(t)
	w.stateIn(t, state)
	return w
}

// At a terminal, kit secret signin has 1Password's op ask for the password
// there itself, then stays open, saying kit's commands elsewhere use the
// sign-in, and runs their runs of op in its session, held in memory alone:
// another kit's sync, on a locked screen, reads through it, asking nothing
// of 1Password's app, its log recording op as ever. kit secret signout ends
// it, signing out at 1Password; with none open, it says so. Nothing kit
// prints, logs or keeps holds the session's token or the value.
func TestSecretSignInRelaysOtherKitsCommands(t *testing.T) {
	state := shortState(t)
	relayed := secretsWorld(t)
	optest.Holds(relayed.fake, map[string]string{"op://vault/Item/Section/token": "the value"})
	signin := signingIn(t, t.Context(), relayed, state)

	agent := elsewhere(t, state)
	screentest.Shows(agent.fake, true)
	agent.fake.On("op", "whoami").Exits(1)
	syncOut, syncErr, code := agent.run(t, "secret", "sync")
	if code != 0 || !strings.Contains(syncOut, "secret ok 1 synced, 1 changed\n") {
		t.Errorf("kit secret sync printed\n%s%s exit %d", syncOut, syncErr, code)
	}
	if got := agent.read(t, ".secrets.zsh"); !strings.Contains(got, "export TOKEN='the value'   # a token\n") {
		t.Errorf("~/.secrets.zsh = %q", got)
	}
	if session, _ := runner.LookupEnv(opRan(t, relayed, "read", "op://vault/Item/Section/token").Env, "OP_SESSION_ABC123"); session != token {
		t.Errorf("op read ran at kit secret signin in the session %q; want its own", session)
	}
	if asked := slices.ContainsFunc(agent.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "op ") || strings.HasPrefix(c, "ioreg") }); asked {
		t.Errorf("the agent's kit ran %q: want op run by kit secret signin alone, the screen never read", agent.fake.Calls())
	}
	logs, _ := filepath.Glob(filepath.Join(agent.home, "Library", "Logs", "kit", "*secret-sync.jsonl"))
	if len(logs) != 1 || !strings.Contains(agent.read(t, strings.TrimPrefix(logs[0], agent.home)), "op read op://vault/Item/Section/token") {
		t.Errorf("the agent's logs %q: want op read recorded", logs)
	}

	outOut, outErr, code := agent.run(t, "secret", "signout")
	if code != 0 || !strings.Contains(outOut, "signed out of 1Password, and kit secret signin has ended") {
		t.Errorf("kit secret signout printed\n%s%s exit %d", outOut, outErr, code)
	}
	r := <-signin
	for _, says := range []string{
		"kit's commands elsewhere use your sign-in to 1Password while this stays open: ctrl-C ends it",
		"signed out of 1Password: kit secret signout asked",
	} {
		if r.code != 0 || !strings.Contains(r.out, says) {
			t.Errorf("kit secret signin printed\n%s%s exit %d; want %q", r.out, r.errOut, r.code, says)
		}
	}
	if signin := opRan(t, relayed, "signin"); !signin.Terminal || !signin.Secret {
		t.Errorf("%s: at the terminal %v, its output kept from reports %v", signin, signin.Terminal, signin.Secret)
	}
	if session, _ := runner.LookupEnv(opRan(t, relayed, "signout").Env, "OP_SESSION_ABC123"); session != token {
		t.Errorf("op signout ran in the session %q", session)
	}
	again, _, code := agent.run(t, "secret", "signout")
	if code != 0 || !strings.Contains(again, "kit secret signin isn't open: nothing to sign out of") {
		t.Errorf("kit secret signout, none open, printed\n%s exit %d", again, code)
	}
	for _, kept := range []string{socketIn(state), filepath.Join(state, "kit", "secret-sessions.json")} {
		if _, err := os.Stat(kept); !os.IsNotExist(err) {
			t.Errorf("%s is there: %v; want nothing kept of the sign-in", kept, err)
		}
	}
	relayed.leaks(t, r.out, r.errOut)
	agent.leaks(t, syncOut, syncErr, outOut, outErr, again)
}

// kit secret signin ends with its run, as at ctrl-C or its terminal
// closing: it signs out at 1Password, says so, and takes its socket away.
func TestSecretSignInEndsWithItsRun(t *testing.T) {
	state := shortState(t)
	ctx, stop := context.WithCancel(t.Context())
	w := secretsWorld(t)
	signin := signingIn(t, ctx, w, state)
	stop()
	r := <-signin
	if !strings.Contains(r.out, "signed out of 1Password: it was stopped") {
		t.Errorf("kit secret signin printed\n%s%s exit %d", r.out, r.errOut, r.code)
	}
	if session, _ := runner.LookupEnv(opRan(t, w, "signout").Env, "OP_SESSION_ABC123"); session != token {
		t.Errorf("op signout ran in the session %q", session)
	}
	if _, err := os.Stat(socketIn(state)); !os.IsNotExist(err) {
		t.Errorf("the socket's still there: %v", err)
	}
}

// One kit secret signin at a time: another, with one open, says so, and
// asks for no password.
func TestSecretSignInOneAtATime(t *testing.T) {
	state := shortState(t)
	signingIn(t, t.Context(), secretsWorld(t), state)
	second := elsewhere(t, state)
	second.terminal = true
	out, errOut, code := second.run(t, "secret", "signin")
	if code != 0 || !strings.Contains(ansi.Strip(out), "kit secret signin is open already, in another terminal") {
		t.Errorf("a second kit secret signin printed\n%s%s exit %d", out, errOut, code)
	}
	if calls := second.fake.Calls(); slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "op ") }) {
		t.Errorf("ran %q: want no sign-in", calls)
	}
}

// Without a terminal, kit secret signin has no one to ask for the
// password: it fails, saying what to do, op never run.
func TestSecretSignInWithoutATerminal(t *testing.T) {
	w := secretsWorld(t)
	_, errOut, code := w.run(t, "secret", "signin")
	ranOp := slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "op ") })
	if code == 0 || !strings.Contains(errOut, "kit secret signin asks for a password at a terminal: have the person run it in a terminal of their own") || ranOp {
		t.Errorf("printed %q, exit %d, ran %q", errOut, code, w.fake.Calls())
	}
}

// With 1Password signed in, no session open, no kit secret signin open, and
// the Mac's screen locked, kit asks 1Password's app for nothing: a sync and
// a secret's add fail at once, saying to sign in with kit secret signin and
// leave it open.
func TestALockedScreenFailsAtOnce(t *testing.T) {
	const says = "1Password asks for your approval on the Mac's screen, which is locked; sign in with your 1Password password in a terminal of your own, and leave it open: kit secret signin"
	w := secretsWorld(t)
	w.fake.On("op", "whoami").Exits(1)
	w.fake.On("op", "account", "list", "--format", "json").Prints(`[{"url": "my.1password.com"}]`)
	screentest.Shows(w.fake, true)
	out, _, _ := w.run(t, "secret", "sync")
	if !strings.Contains(out, says) {
		t.Errorf("kit secret sync printed\n%s", out)
	}
	w.stdin = "the value"
	_, errOut, code := w.run(t, "secret", "add", "NEW_ONE", "--stdin", "--field", "Section/new")
	if code == 0 || !strings.Contains(errOut, says) {
		t.Errorf("kit secret add printed %q, exit %d", errOut, code)
	}
	if asked := slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "op read") || strings.HasPrefix(c, "op vault") }); asked {
		t.Errorf("ran %q: want nothing asked of 1Password's app", w.fake.Calls())
	}
}
