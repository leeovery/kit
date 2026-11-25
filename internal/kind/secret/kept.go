package secret

import (
	"context"
	"errors"
	"strings"

	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/screen"
	"github.com/leeovery/kit/internal/state"
)

// sessionsName is the record of the sessions kit keeps from the stores'
// password sign-ins, each store's by its ID: the store's own data, a
// secret, never shown or logged, in a file the person alone can read.
const sessionsName = "secret-sessions.json"

// sessions are the sessions kit keeps, each store's by its ID: none when
// the record doesn't read, as a sign-in makes it again.
func (s *Secrets) sessions() map[string]string {
	kept, err := state.Load[map[string]string](s.stateDir, sessionsName)
	if err != nil {
		return nil
	}
	return kept
}

// kept is d using the session kit keeps for it, and whether it keeps one:
// d as it is when it doesn't, or d has no password sign-in.
func (s *Secrets) kept(d store.Driver) (store.Driver, bool) {
	p, ok := d.(store.Passworded)
	session, kept := s.sessions()[d.About().ID]
	if !ok || !kept {
		return d, false
	}
	return p.Using(session), true
}

// driver is d as kit drives it: using the session kit keeps for it, if any.
func (s *Secrets) driver(d store.Driver) store.Driver {
	d, _ = s.kept(d)
	return d
}

// standing is how d stands, and d as kit drives it: using the session kit
// keeps for it while that's open; one d says is over is dropped, and d
// asked again without it.
func (s *Secrets) standing(ctx context.Context, d store.Driver) (store.Driver, store.Session) {
	if using, kept := s.kept(d); kept {
		if session := using.Session(ctx, s.run); session == store.Open {
			return using, session
		}
		// A copy not dropped is found over and dropped again next time.
		_ = s.drop(d)
	}
	return d, d.Session(ctx, s.run)
}

// cantAsk is why d can't ask the person for its approval now: it needs them
// at the Mac's screen (store.About's AtScreen), which is locked, as with
// the person away from the Mac. Nil when it can, or the screen doesn't say.
func (s *Secrets) cantAsk(ctx context.Context, d store.Driver) error {
	a := d.About()
	if !a.AtScreen || !screen.Locked(ctx, s.run) {
		return nil
	}
	return errors.New(a.Name + " asks for your approval on the Mac's screen, which is locked; " + a.Locked)
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
// for it at kit's terminal, and keeps the session d gives back, in place of
// any kept before.
func (s *Secrets) SignIn(ctx context.Context, d store.Passworded) error {
	session, err := d.SignIn(ctx, s.run)
	if err != nil {
		return err
	}
	return state.Update(s.stateDir, sessionsName, func(kept *map[string]string) {
		if *kept == nil {
			*kept = map[string]string{}
		}
		(*kept)[d.About().ID] = session
	})
}

// Kept are the stores kit keeps a session for, of those kit knows, in
// turn, whatever the secrets declared now.
func (s *Secrets) Kept() []store.Passworded {
	kept := s.sessions()
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
	session, ok := s.sessions()[d.About().ID]
	if !ok {
		return nil
	}
	ended := d.SignOut(ctx, s.run, session)
	return errors.Join(ended, s.drop(d))
}

// drop deletes the session kit keeps for d.
func (s *Secrets) drop(d store.Driver) error {
	return state.Update(s.stateDir, sessionsName, func(kept *map[string]string) { delete(*kept, d.About().ID) })
}
