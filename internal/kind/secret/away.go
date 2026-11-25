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
// terminal, holds d's sign-in (relayOf); else as d says.
func (s *Secrets) standing(ctx context.Context, d store.Driver) store.Session {
	if s.relayOf(ctx, d) != nil {
		return store.Open
	}
	return d.Session(ctx, s.run)
}

// relayOf is the line to the relay of kit secret signin, open in another
// terminal, when it holds a sign-in to d that lasts: asked once a run, d's
// commands going there, or staying here, for the rest of it. Nil when none
// does.
func (s *Secrets) relayOf(ctx context.Context, d store.Driver) *relay.Line {
	if _, ok := d.(store.Passworded); !ok {
		return nil
	}
	id := d.About().ID
	s.mu.Lock()
	defer s.mu.Unlock()
	line, asked := s.relayed[id]
	if !asked {
		line = relay.Holds(ctx, relay.Socket(s.stateDir), id)
		s.relayed[id] = line
	}
	return line
}

// through is the runner d's commands go through: to the relay holding d's
// sign-in, when one does (relayOf).
func (s *Secrets) through(ctx context.Context, d store.Driver) runner.Runner {
	if line := s.relayOf(ctx, d); line != nil {
		return relay.Via(s.run, line)
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
