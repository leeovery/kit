package secret_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/relay"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/screen/screentest"
)

// shortDir is a folder of the test's own in the temporary directory, where
// a test may make a socket, its path short enough for one, as t.TempDir's
// may not be.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kit-state-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// oneStore is the secrets of a home whose one secret, B, is in d, the one
// store kit knows, with its program there, its commands run as a run's
// are, kit secret signin's relay running d's when it holds d's sign-in;
// and the fake runner, and kit's state folder.
func oneStore(t *testing.T, d store.Driver) (*secret.Secrets, *runnertest.Fake, string) {
	t.Helper()
	secret.UseStores(t, d)
	run := runnertest.New(t)
	run.On("fakevault")
	stateDir := shortDir(t)
	s := secret.New(relay.Client(run), t.TempDir(), stateDir)
	if _, err := s.Values(config.List{Kind: "secret", Declared: true, Entries: []config.Entry{{Name: "B", Value: "vault:keys/b", Section: "secrets"}}}); err != nil {
		t.Fatal(err)
	}
	return s, run, stateDir
}

// lockedSays is what kit says of the fake store, its approval needing the
// person at the Mac's screen, while that screen is locked.
const lockedSays = "Fake Vault asks for your approval on the Mac's screen, which is locked; sign in with its password"

// Before a store asks the person for its approval, kit reads whether the
// Mac's screen is locked, when the store says its approval needs them at
// that screen: locked, readying it or syncing fails at once, in the store's
// words, nothing asked; unlocked, it asks as ever. A store whose approval
// needs no screen is asked with the screen never read.
func TestALockedScreenFailsAtOnce(t *testing.T) {
	for _, c := range []struct {
		name     string
		atScreen bool
		// screen is how the screen stands: "" when it's never to be read.
		screen string
		want   string
	}{
		{"locked", true, "locked", lockedSays},
		{"unlocked", true, "unlocked", ""},
		{"no screen needed", false, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeStore{session: store.SignedIn, atScreen: c.atScreen, values: map[string]string{"vault:keys/b": "b"}}
			s, run, _ := oneStore(t, f)
			if c.screen != "" {
				screentest.Shows(run, c.screen == "locked")
			}
			err := s.Ready(t.Context(), f)
			if c.want != "" {
				if err == nil || err.Error() != c.want || f.unlocks != 0 {
					t.Errorf("Ready() = %v, unlocked %d times; want %q at once, nothing asked", err, f.unlocks, c.want)
				}
			} else if err != nil || f.unlocks != 1 {
				t.Errorf("Ready() = %v, unlocked %d times; want it asked once", err, f.unlocks)
			}
			report, err := s.Sync(t.Context())
			switch {
			case c.want != "" && (!engine.IsNotYet(err) || err.Error() != c.want || len(f.reads) > 0):
				t.Errorf("Sync() = %+v, %v, read %q; want not yet, %q, nothing read", report, err, f.reads, c.want)
			case c.want == "" && (err != nil || report.Synced != 1):
				t.Errorf("Sync() = %+v, %v; want B synced", report, err)
			}
		})
	}
}

// passwordFake is a store whose approval needs the person at the screen,
// signed in with no session open, that signs in with a password.
func passwordFake() *passwordStore {
	return &passwordStore{fakeStore: &fakeStore{session: store.SignedIn, atScreen: true}}
}

// session is the session kit secret signin holds of the fake store.
const session = "fake-session-0123456789"

// signedInElsewhere has kit secret signin hold d's sign-in, open in another
// terminal: its relay listening in kit's state, stateDir, its commands run
// by a fake of their own, as script has them, till the test ends. The fake,
// and why the relay ended, once it has.
func signedInElsewhere(t *testing.T, stateDir string, d store.Passworded, script func(run *runnertest.Fake)) (*runnertest.Fake, <-chan relay.Ended) {
	t.Helper()
	l, err := relay.Listen(relay.Socket(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	run := runnertest.New(t)
	run.On("fakevault", "whoami")
	run.On("fakevault", "signout")
	script(run)
	ctx, stop := context.WithCancel(context.Background())
	ended, finished := make(chan relay.Ended, 1), make(chan struct{})
	go func() {
		ended <- l.Serve(ctx, run, []relay.Held{{Store: d, Session: session}})
		close(finished)
	}()
	t.Cleanup(func() {
		stop()
		<-finished
	})
	return run, ended
}

// A store kit secret signin holds the sign-in to, open in another terminal,
// is open: nothing's asked of the person, nor the screen read, and each of
// its commands runs at the relay, in the session, none here.
func TestASignInHeldElsewhereIsUsed(t *testing.T) {
	p := passwordFake()
	s, here, stateDir := oneStore(t, p)
	screentest.Shows(here, true)
	there, _ := signedInElsewhere(t, stateDir, p, func(run *runnertest.Fake) {
		run.On("fakevault", "read", "vault:keys/b").Prints("b")
	})
	if err := s.Ready(t.Context(), p); err != nil || p.unlocks != 0 {
		t.Errorf("Ready() = %v, unlocked %d times; want it open, nothing asked", err, p.unlocks)
	}
	if v, err := s.Fetch(t.Context(), p, "B", "vault:keys/b"); err != nil || v != "b" {
		t.Errorf("Fetch() = %q, %v", v, err)
	}
	if report, err := s.Sync(t.Context()); err != nil || report.Synced != 1 {
		t.Errorf("Sync() = %+v, %v", report, err)
	}
	if want := []string{"fakevault whoami", "fakevault read vault:keys/b", "fakevault read vault:keys/b"}; !slices.Equal(there.Calls(), want) {
		t.Errorf("ran at the relay %q, want %q", there.Calls(), want)
	}
	for _, c := range there.Commands() {
		if !slices.Contains(c.Env, "FAKE_SESSION="+session) {
			t.Errorf("%s ran at the relay with %q: want the session", c, c.Env)
		}
	}
	if ranHere := slices.DeleteFunc(here.Calls(), func(c string) bool { return !strings.HasPrefix(c, "fakevault") && !strings.HasPrefix(c, "ioreg") }); len(ranHere) > 0 {
		t.Errorf("ran here %q: want the store's commands at the relay, and the screen never read", ranHere)
	}
}

// A sign-in held elsewhere that the store says is over ends kit secret
// signin, and kit goes on as without it: the screen read, and, locked,
// readying the store fails at once.
func TestASignInOverElsewhereFallsBack(t *testing.T) {
	p := passwordFake()
	s, here, stateDir := oneStore(t, p)
	screentest.Shows(here, true)
	_, ended := signedInElsewhere(t, stateDir, p, func(run *runnertest.Fake) {
		run.On("fakevault", "whoami").Exits(1)
	})
	if err := s.Ready(t.Context(), p); err == nil || err.Error() != lockedSays || p.unlocks != 0 {
		t.Errorf("Ready() = %v, unlocked %d times; want %q at once", err, p.unlocks, lockedSays)
	}
	if why := <-ended; why != "Fake Vault ended the sign-in" {
		t.Errorf("kit secret signin ended as %q", why)
	}
}

// The stores to sign in to with a password are those the secrets are in
// that have such a sign-in: none, it says why, naming them.
func TestPassworded(t *testing.T) {
	p := passwordFake()
	s, _, _ := oneStore(t, p)
	if got, err := s.Passworded(); err != nil || !slices.Equal(got, []store.Passworded{p}) {
		t.Errorf("Passworded() = %v, %v; want the password store", got, err)
	}
	s, _, _ = oneStore(t, &fakeStore{})
	if _, err := s.Passworded(); err == nil || err.Error() != "your secrets' stores, Fake Vault, have no sign-in with a password: approve them at the Mac" {
		t.Errorf("Passworded(), none = %v", err)
	}
	if _, err := secret.New(runnertest.New(t), t.TempDir(), t.TempDir()).Passworded(); err == nil || err.Error() != "no secrets are declared: kit-config's [secrets] declares them" {
		t.Errorf("Passworded(), no secrets = %v", err)
	}
}
