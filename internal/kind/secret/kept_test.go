package secret_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/screen/screentest"
)

// oneStore is the secrets of a home whose one secret, B, is in d, the one
// store kit knows, with its program there; and the fake runner, and kit's
// state folder.
func oneStore(t *testing.T, d store.Driver) (*secret.Secrets, *runnertest.Fake, string) {
	t.Helper()
	secret.UseStores(t, d)
	run := runnertest.New(t)
	run.On("fakevault")
	stateDir := t.TempDir()
	s := secret.New(run, t.TempDir(), stateDir)
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
// signed in with no session open, holding B, that signs in with a password,
// giving sessions in turn.
func passwordFake(sessions ...string) *passwordStore {
	return &passwordStore{fakeStore: &fakeStore{session: store.SignedIn, atScreen: true, values: map[string]string{"vault:keys/b": "b"}}, gives: sessions}
}

// A session kit keeps from a store's password sign-in is used in every run
// after: the store's open, so nothing's asked of anyone, nor the screen
// read, and each value's read in it.
func TestAKeptSessionIsUsed(t *testing.T) {
	p := passwordFake("session-1")
	s, _, _ := oneStore(t, p)
	if err := s.SignIn(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(t.Context(), p); err != nil || p.unlocks != 0 {
		t.Errorf("Ready() = %v, unlocked %d times; want it open, nothing asked", err, p.unlocks)
	}
	if v, err := s.Fetch(t.Context(), p, "B", "vault:keys/b"); err != nil || v != "b" {
		t.Errorf("Fetch() = %q, %v", v, err)
	}
	if report, err := s.Sync(t.Context()); err != nil || report.Synced != 1 {
		t.Errorf("Sync() = %+v, %v", report, err)
	}
	if want := []string{"session-1", "session-1"}; !slices.Equal(p.readWith, want) {
		t.Errorf("read in %q, want %q: each read in the session kit keeps", p.readWith, want)
	}
}

// A kept session the store says is over is dropped, and kit goes on as
// without one: the screen read, and, locked, readying or syncing fails at
// once.
func TestAnExpiredSessionIsDropped(t *testing.T) {
	p := passwordFake("session-1", "session-2")
	s, run, _ := oneStore(t, p)
	screentest.Shows(run, true)
	if err := s.SignIn(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	p.expire("session-1")
	if err := s.Ready(t.Context(), p); err == nil || err.Error() != lockedSays || p.unlocks != 0 || len(s.Kept()) != 0 {
		t.Errorf("Ready() = %v, unlocked %d times, keeping %d; want the session dropped, then %q", err, p.unlocks, len(s.Kept()), lockedSays)
	}

	if err := s.SignIn(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	p.expire("session-2")
	if _, err := s.Sync(t.Context()); !engine.IsNotYet(err) || err.Error() != lockedSays || len(p.reads) > 0 || len(s.Kept()) != 0 {
		t.Errorf("Sync() = %v, read %q, keeping %d; want the session dropped, then %q", err, p.reads, len(s.Kept()), lockedSays)
	}
}

// Signing in keeps the session the store gives in kit's state, in a file
// the person alone can read, in place of one kept before; one the store
// refuses keeps nothing. Signing out ends the session at the store and
// deletes kit's copy, even when the store doesn't answer.
func TestSignInAndOut(t *testing.T) {
	p := passwordFake("session-1", "session-2", "session-3")
	s, _, stateDir := oneStore(t, p)
	record := filepath.Join(stateDir, "secret-sessions.json")
	holds := func(session string) bool {
		data, _ := os.ReadFile(record)
		return strings.Contains(string(data), session)
	}
	for _, session := range []string{"session-1", "session-2"} {
		if err := s.SignIn(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(record)
		if err != nil || info.Mode().Perm() != 0o600 || !holds(session) {
			t.Fatalf("signed in: the record is %v, %v, holding %s %v; want it the person's alone", info, err, session, holds(session))
		}
	}
	if holds("session-1") || !slices.Equal(s.Kept(), []store.Passworded{p}) {
		t.Errorf("signed in again: the record holds session-1 %v, kept %v; want the new session alone", holds("session-1"), s.Kept())
	}
	if err := s.SignOut(t.Context(), p); err != nil || !slices.Equal(p.ended, []string{"session-2"}) || holds("session-2") || len(s.Kept()) != 0 {
		t.Errorf("SignOut() = %v, ended %q, kept %v; want session-2 ended and deleted", err, p.ended, s.Kept())
	}

	if err := s.SignIn(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	p.failsSignOut = true
	if err := s.SignOut(t.Context(), p); err == nil || holds("session-3") || len(s.Kept()) != 0 {
		t.Errorf("SignOut(), the store not answering = %v, kept %v; want why, and kit's copy deleted", err, s.Kept())
	}

	p.refused = true
	if err := s.SignIn(t.Context(), p); err == nil || len(s.Kept()) != 0 {
		t.Errorf("SignIn(), refused = %v, kept %v; want why, nothing kept", err, s.Kept())
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
