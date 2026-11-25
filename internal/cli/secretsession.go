package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/secret/relay"
	"github.com/leeovery/kit/internal/kind/secret/store"
)

// secretsSignIn signs the person in with their password to each store the
// secrets are in that has such a sign-in, then relays those stores'
// commands for every other kit while it runs, as a run of one step (relay):
// each store's program asks for the password at the terminal, the face
// stepped aside meanwhile, and the sessions are held in memory alone.
// Without a terminal, there's no one to ask; with kit secret signin open
// already, in another terminal, there's no need.
func (a *app) secretsSignIn(ctx context.Context, r *run) error {
	k, err := r.secrets()
	if err != nil {
		return err
	}
	stores, err := k.Passworded()
	if err != nil {
		return err
	}
	if !a.Terminal(a.Stdin) {
		return errors.New("kit secret signin asks for a password at a terminal: have the person run it in a terminal of their own")
	}
	return a.oneStep(ctx, r, secretsStep, "signing in", func(ctx context.Context) check.Result {
		l, err := relay.Listen(relay.Socket(r.stateDir))
		switch {
		case errors.Is(err, relay.ErrOpen):
			return check.Result{State: check.OK, Summary: err.Error()}
		case err != nil:
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		defer func() { _ = l.Close() }()
		held, err := signIn(ctx, r, stores)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		names := storeNames(stores)
		event.Say(ctx, event.Doing{Says: "signed in to " + names, Todo: "kit's commands elsewhere use your sign-in to " + names + " while this stays open: ctrl-C ends it"})
		why := l.Serve(ctx, r.run, held)
		return check.Result{State: check.OK, Summary: "signed out of " + names + ": " + string(why)}
	})
}

// signIn signs the person in to each of stores, its program asking for
// their password at the terminal, the face stepped aside: each store's
// sign-in, held. One that fails signs out of those signed in to already.
func signIn(ctx context.Context, r *run, stores []store.Passworded) ([]relay.Held, error) {
	var held []relay.Held
	for _, d := range stores {
		back := stepAside(r.face)
		session, err := d.SignIn(ctx, r.run)
		back()
		if err != nil {
			relay.SignOut(context.WithoutCancel(ctx), r.run, held)
			return nil, err
		}
		held = append(held, relay.Held{Store: d, Session: session})
	}
	return held, nil
}

// secretsSignOut ends kit secret signin, open in another terminal, as a
// run of one step: it signs out of its stores, and stops relaying their
// commands. With none open, it says so.
func (a *app) secretsSignOut(ctx context.Context, r *run) error {
	return a.oneStep(ctx, r, secretsStep, "signing out", func(ctx context.Context) check.Result {
		ended, err := relay.Stop(ctx, relay.Socket(r.stateDir))
		switch {
		case errors.Is(err, relay.ErrNone):
			return check.Result{State: check.OK, Summary: "kit secret signin isn't open: nothing to sign out of"}
		case err != nil:
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		return check.Result{State: check.OK, Summary: "signed out of " + strings.Join(ended, " and ") + ", and kit secret signin has ended"}
	})
}

// storeNames names stores, as in 1Password, or 1Password and another.
func storeNames(stores []store.Passworded) string {
	names := make([]string, len(stores))
	for i, d := range stores {
		names[i] = d.About().Name
	}
	return strings.Join(names, " and ")
}
