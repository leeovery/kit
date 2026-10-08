package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/look"
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
		r.admin.Held = held
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
	has := a.askAdmin(ctx, r, wanted)
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

// sudoAsks is the prompt kit has sudo give on its standard error when it
// wants the password, for kit to ask for it in its own field.
const sudoAsks = "[kit: sudo wants the password]"

// askAdmin has sudo take an administrator's password for the installs
// wanted, by kind: Touch ID first, where sudo has it; else, each time sudo
// asks, kit asks in its own field, under the run's heading, a row
// naming what needs it, and gives sudo what's typed, on its input, never on
// a command line or in the log. Whether sudo took it.
func (a *app) askAdmin(ctx context.Context, r *run, wanted map[string][]string) bool {
	// What kit was doing is taken down for the question.
	r.sink.Emit(event.Preparing{Time: r.now, Command: r.command, Machine: r.machine})
	var names []string
	for _, kindName := range slices.Sorted(maps.Keys(wanted)) {
		names = append(names, wanted[kindName]...)
	}
	about := look.Row{State: look.NeedsYou, Name: strings.Join(names, ", "), Says: look.Orange("needs an administrator's password")}
	switch {
	case len(names) > 3:
		about.Name, about.Says = fmt.Sprintf("%d installs", len(names)), look.Orange("need an administrator's password")
	case len(names) > 1:
		about.Says = look.Orange("need an administrator's password")
	}
	_, err := r.run.Run(ctx, runner.Command{
		Name:    "sudo",
		Args:    []string{"-S", "-p", sudoAsks, "-v"},
		Timeout: 5 * time.Minute,
		Asks:    sudoAsks,
		Answer: func(ctx context.Context, asked int) (string, error) {
			row := about
			if asked > 0 {
				row.Says = look.Orange("that wasn't it: try again")
			}
			typed, err := a.ReadSecret(ctx, row)
			// What's typed is kept for the run, for what asks later.
			if err == nil && r.askpass != nil {
				r.askpass.Keep(typed)
			}
			return typed, err
		},
	})
	if err != nil && r.askpass != nil {
		r.askpass.Keep("")
	}
	return err == nil
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
		if r.isAdminStep(name) && len(names) > 0 {
			return true
		}
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
