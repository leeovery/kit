package cli

import (
	"context"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// sudoRefresh is how often kit renews sudo's hold on the password while it
// installs, well inside sudo's own five minutes.
const sudoRefresh = time.Minute

// adminWait is why an install waits, without an administrator's password.
const adminWait = "needs an administrator's password: run kit %s at a terminal"

// holdAdmin settles, before anything is installed, whether an administrator's
// password is at hand for the installs that need one, so no step asks
// mid-run: wanted are what's to be installed, by kind. At a terminal, when
// any of them needs the password, kit asks for it once, through sudo, and
// keeps sudo's hold on it fresh till stop is called. Without a terminal it
// asks nothing: sudo either holds a password already, or those installs
// wait. Every kind that can need the password is told how it stands, and
// held says too; it's nil when nothing wanted needs the password.
func (a *app) holdAdmin(ctx context.Context, r *run, wanted map[string][]string) (held func(context.Context) bool, stop func()) {
	stop = func() {}
	sudo := func(ctx context.Context, interactive bool, args ...string) bool {
		_, err := r.run.Run(ctx, runner.Command{Name: "sudo", Args: args, Interactive: interactive})
		return err == nil
	}
	var admins []kind.Admin
	for _, name := range r.allKinds {
		if ad, ok := r.kindsByName[name].(kind.Admin); ok {
			admins = append(admins, ad)
		}
	}
	tell := func(held func(context.Context) bool) {
		for _, ad := range admins {
			ad.SetAdmin(held)
		}
	}
	if !a.pretty(a.Stdout) {
		var once sync.Once
		var has bool
		held = func(ctx context.Context) bool {
			once.Do(func() { has = sudo(ctx, false, "-n", "true") })
			return has
		}
		tell(held)
		return held, stop
	}
	if !needsAdmin(ctx, r, wanted) {
		return nil, stop
	}
	has := sudo(ctx, true, "-v")
	held = func(context.Context) bool { return has }
	tell(held)
	if !has {
		return held, stop
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(sudoRefresh)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				sudo(ctx, false, "-n", "-v")
			}
		}
	})
	return held, func() {
		cancel()
		wg.Wait()
	}
}

// waitingForAdmin are which of names, of the kind called kindName, to
// install, can't be for want of an administrator's password, as held says.
func waitingForAdmin(ctx context.Context, r *run, kindName string, names []string, held func(context.Context) bool) map[string]bool {
	waiting := make(map[string]bool)
	ad, ok := r.kindsByName[kindName].(kind.Admin)
	if !ok || len(names) == 0 || held == nil {
		return waiting
	}
	needing, err := ad.NeedsAdmin(ctx, names)
	if err != nil || len(needing) == 0 || held(ctx) {
		return waiting
	}
	for _, name := range needing {
		waiting[name] = true
	}
	return waiting
}

// needsAdmin reports whether any of wanted, by kind, needs an
// administrator's password to install.
func needsAdmin(ctx context.Context, r *run, wanted map[string][]string) bool {
	for name, names := range wanted {
		ad, ok := r.kindsByName[name].(kind.Admin)
		if !ok || len(names) == 0 {
			continue
		}
		if needing, err := ad.NeedsAdmin(ctx, names); err == nil && len(needing) > 0 {
			return true
		}
	}
	return false
}
