package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/kind/secret/onepassword/optest"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/screen/screentest"
)

// token is the session's token 1Password gives the tests' sign-in.
const token = "tok-en-0123456789abcdef"

// sessionsRecord is where kit keeps the stores' sessions, from the home.
var sessionsRecord = filepath.Join(".local", "state", "kit", "secret-sessions.json")

// leaks fails the test when anything kit printed, or anything in its logs,
// holds the session's token.
func (w *world) leaks(t *testing.T, printed ...string) {
	t.Helper()
	for _, p := range printed {
		if strings.Contains(p, token) {
			t.Errorf("kit printed the session's token:\n%s", p)
		}
	}
	logs, _ := filepath.Glob(filepath.Join(w.home, "Library", "Logs", "kit", "*"))
	for _, l := range logs {
		if data, _ := os.ReadFile(l); strings.Contains(string(data), token) {
			t.Errorf("%s holds the session's token", l)
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

// At a terminal, kit secret signin has 1Password's op ask for the password
// there itself, and keeps the session it gives in kit's state, readable by
// the person alone, never printing it or logging it. Every kit command
// after runs op in it; kit secret signout ends it, with op, and deletes
// kit's copy; with none kept, it says so, and succeeds.
func TestSecretSignInAndOut(t *testing.T) {
	w := secretsWorld(t)
	w.terminal = true
	w.fake.On("op", "signin").Prints("export OP_SESSION_ABC123=\"" + token + "\"\n# This command is meant to be used with your shell's eval function.\n")
	out, errOut, code := w.run(t, "secret", "signin")
	if code != 0 || !strings.Contains(ansi.Strip(out), "signed in to 1Password, till left unused or kit secret signout") {
		t.Errorf("kit secret signin printed\n%s%s exit %d", out, errOut, code)
	}
	if signin := opRan(t, w, "signin"); !signin.Terminal || !signin.Secret {
		t.Errorf("%s: at the terminal %v, its output kept from reports %v", signin, signin.Terminal, signin.Secret)
	}
	info, err := os.Stat(filepath.Join(w.home, sessionsRecord))
	if err != nil || info.Mode().Perm() != 0o600 || !strings.Contains(w.read(t, sessionsRecord), token) {
		t.Errorf("the record of sessions is %v, %v, holding %q; want the session, the person's alone", info, err, w.read(t, sessionsRecord))
	}

	w.fake.On("op", "whoami")
	optest.Holds(w.fake, map[string]string{"op://vault/Item/Section/token": "the value"})
	syncOut, syncErr, code := w.run(t, "secret", "sync")
	if session, _ := runner.LookupEnv(opRan(t, w, "read", "op://vault/Item/Section/token").Env, "OP_SESSION_ABC123"); code != 0 || session != token {
		t.Errorf("kit secret sync exit %d, op read in the session %q; want the session kit keeps", code, session)
	}

	w.fake.On("op", "signout")
	outOut, outErr, code := w.run(t, "secret", "signout")
	if session, _ := runner.LookupEnv(opRan(t, w, "signout").Env, "OP_SESSION_ABC123"); code != 0 || session != token || !strings.Contains(outOut, "signed out of 1Password, kit's copy deleted") {
		t.Errorf("kit secret signout printed\n%s%s exit %d, op signout in the session %q", outOut, outErr, code, session)
	}
	if strings.Contains(w.read(t, sessionsRecord), token) {
		t.Errorf("signed out, the record still holds the session")
	}
	again, _, code := w.run(t, "secret", "signout")
	if code != 0 || !strings.Contains(again, "kit keeps no sign-in: nothing to sign out of") {
		t.Errorf("kit secret signout, none kept, printed\n%s exit %d", again, code)
	}
	w.leaks(t, out, errOut, syncOut, syncErr, outOut, outErr, again)
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

// With 1Password signed in, no session open, and the Mac's screen locked,
// kit asks 1Password's app for nothing: a sync and a secret's add fail at
// once, saying to sign in with kit secret signin.
func TestALockedScreenFailsAtOnce(t *testing.T) {
	const says = "1Password asks for your approval on the Mac's screen, which is locked; sign in with your 1Password password instead: kit secret signin, in a terminal of your own"
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
