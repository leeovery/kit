package secret

import (
	"context"
	"errors"
	"maps"
	"strings"

	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/screen"
	"github.com/leeovery/kit/internal/state"
)

// sessionsName is the record of the sessions kit keeps from the stores'
// password sign-ins, each store's by its ID: the store's own data, a
// secret, never shown or logged, in a file the person alone can read.
const sessionsName = "secret-sessions.json"

// kept are the sessions kit keeps, each store's by its ID, read once a
// run: none when the record doesn't read, as a sign-in makes it again.
func (s *Secrets) kept() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		read, err := state.Load[map[string]string](s.stateDir, sessionsName)
		if err != nil || read == nil {
			read = map[string]string{}
		}
		s.sessions = read
	}
	return maps.Clone(s.sessions)
}

// change changes the record of sessions, under its lock, the run reading
// it again after.
func (s *Secrets) change(edit func(kept map[string]string)) error {
	defer func() {
		s.mu.Lock()
		s.sessions = nil
		s.mu.Unlock()
	}()
	return state.Update(s.stateDir, sessionsName, func(kept *map[string]string) {
		if *kept == nil {
			*kept = map[string]string{}
		}
		edit(*kept)
	})
}

// session is the session kit keeps for d, and d's password sign-in: false
// when there's none, or d has no such sign-in.
func (s *Secrets) session(d store.Driver) (store.Passworded, string, bool) {
	p, ok := d.(store.Passworded)
	session, kept := s.kept()[d.About().ID]
	return p, session, ok && kept
}

// driver is d as kit drives it: using the session kit keeps for it, if any.
func (s *Secrets) driver(d store.Driver) store.Driver {
	if p, session, ok := s.session(d); ok {
		return p.Using(session)
	}
	return d
}

// standing is how d stands, and d as kit drives it: open, in the session
// kit keeps for it, while d says that lasts; one d says is over is dropped,
// and d asked again without it. When d doesn't answer whether it lasts,
// the session's kept, and that is why d can't be used now.
func (s *Secrets) standing(ctx context.Context, d store.Driver) (store.Driver, store.Session, error) {
	if p, session, ok := s.session(d); ok {
		lasts, err := p.Lasts(ctx, s.run, session)
		switch {
		case err != nil:
			return d, store.SignedOut, errors.New(d.About().Name + " didn't answer: " + err.Error() + "; kit keeps your sign-in for the next try")
		case lasts:
			return p.Using(session), store.Open, nil
		}
		// A copy not dropped is found over and dropped again next time.
		_ = s.drop(d, session)
	}
	return d, d.Session(ctx, s.run), nil
}

// cantAsk is why d can't ask the person for its approval now: it needs them
// at the Mac's screen (store.About's AtScreen), which is locked, as with
// the person away from the Mac. Nil when it can, or the screen doesn't say.
func (s *Secrets) cantAsk(ctx context.Context, d store.Driver) error {
	a := d.About()
	if !a.AtScreen || !screen.Locked(ctx, s.run) {
		return nil
	}
	return errors.New(a.Name + " asks for your approval on the Mac's screen, which is locked; " + a.WhenLocked)
}

// Passworded are the stores the secrets declared are in that the person
// can sign in to with a password (store.Passworded): why not, when none
// can, or none's declared.
func (s *Secrets) Passworded() ([]store.Passworded, error) {
	used := s.used()
	if len(used) == 0 {
		return nil, errors.New("no secrets are declared: kit-config's [secrets] declares them")
	}
	var out []store.Passworded
	names := make([]string, len(used))
	for i, d := range used {
		names[i] = d.About().Name
		if p, ok := d.(store.Passworded); ok {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("your secrets' stores, " + strings.Join(names, " and ") + ", have no sign-in with a password: approve them at the Mac")
	}
	return out, nil
}

// SignIn signs the person in to d with their password, d's program asking
// for it at kit's terminal, and keeps the session d gives back. A session
// kept before is ended first, whether or not d answers.
func (s *Secrets) SignIn(ctx context.Context, d store.Passworded) error {
	if _, old, ok := s.session(d); ok {
		_ = d.SignOut(ctx, s.run, old)
	}
	session, err := d.SignIn(ctx, s.run)
	if err != nil {
		return err
	}
	return s.change(func(kept map[string]string) { kept[d.About().ID] = session })
}

// Kept are the stores kit keeps a session for, of those kit knows, in
// turn, whatever the secrets declared now.
func (s *Secrets) Kept() []store.Passworded {
	kept := s.kept()
	var out []store.Passworded
	for _, d := range stores() {
		if p, ok := d.(store.Passworded); ok && kept[d.About().ID] != "" {
			out = append(out, p)
		}
	}
	return out
}

// SignOut ends the session kit keeps for d at d, and deletes kit's copy,
// whether or not d ended it.
func (s *Secrets) SignOut(ctx context.Context, d store.Passworded) error {
	_, session, ok := s.session(d)
	if !ok {
		return nil
	}
	ended := d.SignOut(ctx, s.run, session)
	return errors.Join(ended, s.drop(d, session))
}

// drop deletes session, kept for d, unless it's been replaced since, as by
// a sign-in meanwhile.
func (s *Secrets) drop(d store.Driver, session string) error {
	return s.change(func(kept map[string]string) {
		if kept[d.About().ID] == session {
			delete(kept, d.About().ID)
		}
	})
}
