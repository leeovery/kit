package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
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
// any of them needs the password, kit asks for it once, through sudo, with
// no time limit, and keeps sudo's hold on it fresh till stop is called
// (renewSudo). Without a terminal it asks nothing: sudo either holds a
// password already, or those installs wait. Every kind that can need the
// password is told how it stands, and held says too; it's nil when nothing
// wanted needs the password.
func (a *app) holdAdmin(ctx context.Context, r *run, wanted map[string][]string) (held func(context.Context) bool, stop func()) {
	if !a.pretty(a.Stdout) {
		held = sudoHolds(r)
		r.tellAdmin(held)
		return held, func() {}
	}
	if !needsAdmin(ctx, r, wanted) {
		return nil, func() {}
	}
	var has atomic.Bool
	has.Store(a.askAdmin(ctx, r, wanted))
	held = func(context.Context) bool { return has.Load() }
	r.tellAdmin(held)
	if !has.Load() {
		return held, func() {}
	}
	return held, r.keepSudo(ctx, &has)
}

// holdAdminAlongside is holdAdmin for a run that goes on while kit asks, as
// kit apply's does: at a terminal, the question's asked under the run's
// heading, which holds still meanwhile, the run going on behind it, and
// what needs the password waits for the answer (adminHold), however long
// it takes.
func (a *app) holdAdminAlongside(ctx context.Context, r *run, wanted map[string][]string) (stop func()) {
	if !a.pretty(a.Stdout) {
		_, stop = a.holdAdmin(ctx, r, wanted)
		return stop
	}
	if !needsAdmin(ctx, r, wanted) {
		return func() {}
	}
	h := &adminHold{answered: make(chan struct{})}
	r.hold = h
	r.tellAdmin(h.held)
	about := a.readyToAsk(r, wanted)
	back := func() {}
	if f, ok := r.face.(interface{ HoldStill() func() }); ok {
		back = f.HoldStill()
	}
	ctx, cancel := context.WithCancel(ctx)
	keep := func() {}
	var wg sync.WaitGroup
	wg.Go(func() {
		h.has.Store(a.giveAdmin(ctx, r, about))
		close(h.answered)
		back()
		if h.has.Load() {
			keep = r.keepSudo(ctx, &h.has)
		}
	})
	return func() {
		cancel()
		wg.Wait()
		keep()
	}
}

// adminHold is how an administrator's password stands for a run that asks
// for it as it goes on: being asked for till answered is closed, then held
// or not, as has says.
type adminHold struct {
	answered chan struct{}
	has      atomic.Bool
}

// asking reports whether kit's still asking.
func (h *adminHold) asking() bool {
	select {
	case <-h.answered:
		return false
	default:
		return true
	}
}

// held reports whether the password's at hand: while kit's still asking, a
// check takes it as coming, so what needs it is still to install, and
// anything else as not yet.
func (h *adminHold) held(ctx context.Context) bool {
	if h.asking() {
		return !event.Changing(ctx)
	}
	return h.has.Load()
}

// wait waits, for the step ctx is running, while kit asks, for the answer,
// the step saying so and giving its job up meanwhile; it reports whether
// the password's held.
func (h *adminHold) wait(ctx context.Context) bool {
	if h.asking() {
		event.Say(ctx, event.Doing{Says: "waiting for an administrator's password"})
		back := engine.Release(ctx)
		select {
		case <-h.answered:
		case <-ctx.Done():
		}
		back()
		event.Say(ctx, event.Doing{Says: "applying"})
	}
	return h.has.Load()
}

// afterAdmin is apply, a step's whose apply needs an administrator's
// password, applied once kit has it, should kit still be asking for it.
func (r *run) afterAdmin(apply func(context.Context, check.Result) error) func(context.Context, check.Result) error {
	return func(ctx context.Context, found check.Result) error {
		if r.hold != nil {
			r.hold.wait(ctx)
		}
		return apply(ctx, found)
	}
}

// tellAdmin tells every kind that can need an administrator's password, and
// the steps that do, how it stands, as held says.
func (r *run) tellAdmin(held func(context.Context) bool) {
	for _, name := range r.allKinds {
		if ad, ok := r.kindsByName[name].(kind.Admin); ok {
			ad.SetAdmin(held)
		}
	}
	r.admin.Held = held
}

// sudoHolds is whether sudo holds a password already, asked once, for a run
// without a terminal, which asks nothing.
func sudoHolds(r *run) func(context.Context) bool {
	var once sync.Once
	var has bool
	return func(ctx context.Context) bool {
		once.Do(func() {
			_, err := r.run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "true"}})
			has = err == nil
		})
		return has
	}
}

// keepSudo keeps sudo's hold on the run's password fresh (renewSudo) till
// the stop it returns, has forgetting it once sudo can't be given it again.
func (r *run) keepSudo(ctx context.Context, has *atomic.Bool) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		if !renewSudo(ctx, r.run, r.askpass, sudoRefresh, has.Load) {
			has.Store(false)
		}
	})
	return func() {
		cancel()
		wg.Wait()
	}
}

// renewSudo renews sudo's hold on the run's password every interval, while
// held says the run holds one, till ctx ends. When sudo has forgotten it,
// as after the Mac slept past sudo's five minutes, or sudo -K, it's given
// again, as asks answers sudo: the password kept, else the person's, in
// kit's field; the log has sudo -n -v failing, then sudo -v given it. It
// returns false once it couldn't be given again, the password forgotten
// then, for nothing to be given a wrong one.
func renewSudo(ctx context.Context, run runner.Runner, asks *askpass.Server, interval time.Duration, held func() bool) bool {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return true
		case <-tick.C:
		}
		if !held() {
			continue
		}
		if _, err := run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "-v"}}); err == nil || ctx.Err() != nil {
			continue
		}
		if asks != nil && giveSudo(ctx, run, asks.Answer()) {
			continue
		}
		if ctx.Err() != nil {
			return true
		}
		if asks != nil {
			asks.Keep("")
		}
		return false
	}
}

// sudoAsks is the prompt kit has sudo give on its standard error when it
// wants the password, for kit to ask for it in its own field.
const sudoAsks = "[kit: sudo wants the password]"

// giveSudo has sudo take an administrator's password, Touch ID first,
// where sudo has it; else, each time sudo asks, what answer gives, on its
// input, never on a command line or in the log. kit gives sudo no time
// limit: sudo's own, five minutes for the password, can run out while the
// person's away, so the answer that came too late is given to sudo asked
// again. Whether sudo took it.
func giveSudo(ctx context.Context, run runner.Runner, answer func(ctx context.Context, asked int) (string, error)) bool {
	var late string
	for {
		var last string
		_, err := run.Run(ctx, runner.Command{
			Name:    "sudo",
			Args:    []string{"-S", "-p", sudoAsks, "-v"},
			Timeout: runner.Unlimited,
			Asks:    sudoAsks,
			Answer: func(ctx context.Context, asked int) (string, error) {
				if asked == 0 && late != "" {
					return late, nil
				}
				given, err := answer(ctx, asked)
				last = given
				return given, err
			},
		})
		switch {
		case err == nil:
			return true
		case !errors.Is(err, runner.ErrAnsweredLate) || last == "" || ctx.Err() != nil:
			return false
		}
		late = last
	}
}

// askAdmin has sudo take an administrator's password for the installs
// wanted, by kind (giveAdmin), asked under the run's heading. Whether sudo
// took it.
func (a *app) askAdmin(ctx context.Context, r *run, wanted map[string][]string) bool {
	return a.giveAdmin(ctx, r, a.readyToAsk(r, wanted))
}

// readyToAsk takes down what kit was doing, under the run's heading, for
// the question, and returns the row it's asked in, naming what needs the
// password: the installs wanted, by kind.
func (a *app) readyToAsk(r *run, wanted map[string][]string) look.Row {
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
	return about
}

// giveAdmin has sudo take an administrator's password (giveSudo): each time
// sudo asks, kit asks in its own field, in the row about, nothing else
// asking meanwhile; what's typed is kept for the run, for what asks later.
// Whether sudo took it.
func (a *app) giveAdmin(ctx context.Context, r *run, about look.Row) bool {
	ask := func(ctx context.Context, asked int) (string, error) {
		row := about
		if asked > 0 {
			row.Says = look.Orange("that wasn't it: try again")
		}
		return a.ReadSecret(ctx, row)
	}
	took := giveSudo(ctx, r.run, func(ctx context.Context, asked int) (string, error) {
		if r.askpass == nil {
			return ask(ctx, asked)
		}
		return r.askpass.Ask(ctx, ask, asked)
	})
	if !took && r.askpass != nil {
		r.askpass.Keep("")
	}
	return took
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
