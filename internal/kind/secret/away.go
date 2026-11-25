package secret

import (
	"context"
	"errors"
	"strings"

	"github.com/leeovery/kit/internal/kind/secret/relay"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/screen"
)

// standing is how d stands: open when kit secret signin, open in another
// terminal, holds d's sign-in (isRelayed); else as d says.
func (s *Secrets) standing(ctx context.Context, d store.Driver) store.Session {
	if s.isRelayed(ctx, d) {
		return store.Open
	}
	return d.Session(ctx, s.run)
}

// isRelayed reports whether d's commands go to the relay of kit secret
// signin, open in another terminal, which holds a sign-in to d that lasts:
// asked once a run, d's commands going there or staying here for the rest
// of it.
func (s *Secrets) isRelayed(ctx context.Context, d store.Driver) bool {
	if _, ok := d.(store.Passworded); !ok {
		return false
	}
	id := d.About().ID
	s.mu.Lock()
	defer s.mu.Unlock()
	held, asked := s.relayed[id]
	if !asked {
		held = relay.Holds(ctx, relay.Socket(s.stateDir), id)
		s.relayed[id] = held
	}
	return held
}

// through is the runner d's commands go through: to the relay holding d's
// sign-in, when one does (isRelayed).
func (s *Secrets) through(ctx context.Context, d store.Driver) runner.Runner {
	if s.isRelayed(ctx, d) {
		return relay.Via(s.run, relay.Socket(s.stateDir), d.About().ID)
	}
	return s.run
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
