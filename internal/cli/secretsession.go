package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/render"
)

// secretsSignIn signs the person in with their password to each store the
// secrets are in that has such a sign-in, as a run of one step: each
// store's program asks for the password at the terminal, the face stepped
// aside meanwhile, and kit keeps the session it gives back. Without a
// terminal, there's no one to ask.
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
		for _, d := range stores {
			if err := asideFor(r.face, func() error { return k.SignIn(ctx, d) }); err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
		}
		return check.Result{State: check.OK, Summary: "signed in to " + storeNames(stores) + ", till kit secret signout"}
	})
}

// secretsSignOut ends each session kit keeps, at its store, and deletes
// kit's copy, as a run of one step: with none kept, it says so.
func (a *app) secretsSignOut(ctx context.Context, r *run) error {
	k, err := r.secrets()
	if err != nil {
		return err
	}
	kept := k.Kept()
	return a.oneStep(ctx, r, secretsStep, "signing out", func(ctx context.Context) check.Result {
		if len(kept) == 0 {
			return check.Result{State: check.OK, Summary: "kit keeps no sign-in: nothing to sign out of"}
		}
		var errs []error
		for _, d := range kept {
			errs = append(errs, k.SignOut(ctx, d))
		}
		if err := errors.Join(errs...); err != nil {
			return check.Result{State: check.Failed, Reason: "kit's copy is deleted, but " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "signed out of " + storeNames(kept) + ", kit's copy deleted"}
	})
}

// asideFor runs do with face stepped aside, for a program asking at the
// terminal itself.
func asideFor(face render.Face, do func() error) error {
	if f, ok := face.(interface{ StepAside() func() }); ok {
		defer f.StepAside()()
	}
	return do()
}

// storeNames names stores, as in 1Password, or 1Password and another.
func storeNames(stores []store.Passworded) string {
	names := make([]string, len(stores))
	for i, d := range stores {
		names[i] = d.About().Name
	}
	return strings.Join(names, " and ")
}
