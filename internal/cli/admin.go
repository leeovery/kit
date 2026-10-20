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
// wanted needs the password. The installs are to run in installing, for
// them to wait for the password, should sudo forget it (kind.Password).
func (a *app) holdAdmin(ctx context.Context, r *run, wanted map[string][]string) (installing context.Context, held func(context.Context) bool, stop func()) {
	if !a.pretty(a.Stdout) {
		held = sudoHolds(r)
		r.tellAdmin(held)
		return ctx, held, func() {}
	}
	if !needsAdmin(ctx, r, wanted) {
		return ctx, nil, func() {}
	}
	h := newAdminHold(r.run, r.askpass)
	h.settle(a.askAdmin(ctx, r, wanted))
	r.tellAdmin(h.held)
	installing = kind.WithPassword(ctx, h)
	if !h.Held() {
		return installing, h.held, func() {}
	}
	return installing, h.held, r.keepSudo(ctx, h)
}

// holdAdminAlongside is holdAdmin for a run that goes on while kit asks, as
// kit apply's does: at a terminal, the question's asked under the run's
// heading, which holds still meanwhile, the run going on behind it, and
// what needs the password waits for the answer (adminHold), however long
// it takes.
func (a *app) holdAdminAlongside(ctx context.Context, r *run, wanted map[string][]string) (stop func()) {
	if !a.pretty(a.Stdout) {
		_, _, stop = a.holdAdmin(ctx, r, wanted)
		return stop
	}
	if !needsAdmin(ctx, r, wanted) {
		return func() {}
	}
	h := newAdminHold(r.run, r.askpass)
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
		h.settle(a.giveAdmin(ctx, r, about))
		back()
		if h.Held() {
			keep = r.keepSudo(ctx, h)
		}
	})
	return func() {
		cancel()
		wg.Wait()
		keep()
	}
}

// adminHold is how an administrator's password stands for a run: being
// asked for, first or again, till answered is closed, then held or not, as
// has says. It's kind.Password, for what needs it to wait for it.
type adminHold struct {
	mu       sync.Mutex
	answered chan struct{}
	has      atomic.Bool
	// keeper keeps sudo's hold on it fresh, once it's held.
	keeper *sudoKeeper
}

// newAdminHold is a password being asked for, kept, once held, through
// run, given again through asks.
func newAdminHold(run runner.Runner, asks *askpass.Server) *adminHold {
	h := &adminHold{answered: make(chan struct{})}
	h.keeper = &sudoKeeper{run: run, asks: asks, h: h}
	return h
}

// turn is what's closed once kit's asked.
func (h *adminHold) turn() chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.answered
}

// asking reports whether kit's asking.
func (h *adminHold) asking() bool {
	select {
	case <-h.turn():
		return false
	default:
		return true
	}
}

// askAgain notes kit asking for the password again, as sudo forgot it: it
// isn't held meanwhile.
func (h *adminHold) askAgain() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.has.Store(false)
	select {
	case <-h.answered:
		h.answered = make(chan struct{})
	default:
	}
}

// settle notes kit's asked: has, whether the password's held.
func (h *adminHold) settle(has bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.has.Store(has)
	select {
	case <-h.answered:
	default:
		close(h.answered)
	}
}

// Held reports whether the password's held.
func (h *adminHold) Held() bool {
	return h.has.Load()
}

// held reports whether the password's at hand, as the kinds are told: while
// kit's asking, a check takes it as coming, so what needs it is still to
// install, and anything else as not yet.
func (h *adminHold) held(ctx context.Context) bool {
	if h.asking() {
		return !event.Changing(ctx)
	}
	return h.Held()
}

// Wait waits, for the step ctx is running, while kit asks, first or again,
// for the answer, the step saying so and giving its job up meanwhile; it
// reports whether the password's held.
func (h *adminHold) Wait(ctx context.Context) bool {
	said := false
	for {
		turn := h.turn()
		select {
		case <-turn:
			// Answered, unless kit's begun asking again since.
			if held := h.Held(); held || !h.asking() {
				if said {
					event.Say(ctx, event.Doing{Says: "applying"})
				}
				return held
			}
			continue
		default:
		}
		if !said {
			event.Say(ctx, event.Doing{Says: "waiting for an administrator's password"})
			said = true
		}
		back := engine.Release(ctx)
		select {
		case <-turn:
		case <-ctx.Done():
		}
		back()
		if ctx.Err() != nil {
			return h.Held()
		}
	}
}

// Forgotten has kit find out at once whether sudo's forgotten the password,
// as a command run through sudo -n said, and ask for it again if so
// (sudoKeeper's renew), or, while kit's asking, waits for the answer; it
// reports whether the password's held.
func (h *adminHold) Forgotten(ctx context.Context) bool {
	if h.asking() {
		return h.Wait(ctx)
	}
	if !h.Held() {
		return false
	}
	return h.keeper.renew(ctx)
}

// afterAdmin is apply, a step's whose apply needs an administrator's
// password, applied once kit has it, should kit be asking for it.
func (r *run) afterAdmin(apply func(context.Context, check.Result) error) func(context.Context, check.Result) error {
	return func(ctx context.Context, found check.Result) error {
		if r.hold != nil {
			r.hold.Wait(ctx)
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

// keepSudo keeps sudo's hold on the run's password, as h holds it, fresh
// (sudoKeeper) till the stop it returns.
func (r *run) keepSudo(ctx context.Context, h *adminHold) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { h.keeper.keep(ctx, sudoRefresh) })
	return func() {
		cancel()
		wg.Wait()
	}
}

// holding is how the run holds its password, as a sudoKeeper keeps it.
type holding interface {
	Held() bool
	askAgain()
	settle(has bool)
}

// renewSudo keeps sudo's hold on the run's password fresh, as h holds it,
// every interval, till ctx ends (sudoKeeper).
func renewSudo(ctx context.Context, run runner.Runner, asks *askpass.Server, interval time.Duration, h holding) {
	(&sudoKeeper{run: run, asks: asks, h: h}).keep(ctx, interval)
}

// sudoKeeper keeps sudo's hold on the run's password, as h holds it, fresh,
// one renewal at a time, given again, through asks, when sudo forgets it.
type sudoKeeper struct {
	run  runner.Runner
	asks *askpass.Server
	h    holding
	mu   sync.Mutex
}

// keep renews sudo's hold every interval, while h holds the password, till
// ctx ends.
func (k *sudoKeeper) keep(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if k.h.Held() {
			k.renew(ctx)
		}
	}
}

// renew renews sudo's hold on the password. When sudo has forgotten it, as
// after the Mac slept past sudo's five minutes, or sudo -K, it's given
// again, as asks answers sudo: the password kept, else the person's, in
// kit's field; the log has sudo -n -v failing, then sudo -v given it. It
// isn't held meanwhile, and what sudo takes is kept; once it couldn't be
// given again, it's forgotten, for nothing to be given a wrong one. It
// reports whether it's held. A renewal under way already, as the person's
// asked, is waited for, the step ctx runs giving its job up meanwhile, and
// what it came to stands: one refused isn't asked again.
func (k *sudoKeeper) renew(ctx context.Context) bool {
	if !k.mu.TryLock() {
		event.Say(ctx, event.Doing{Says: "waiting for an administrator's password"})
		back := engine.Release(ctx)
		k.mu.Lock()
		back()
	}
	defer k.mu.Unlock()
	if !k.h.Held() {
		return false
	}
	if _, err := k.run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "-v"}}); err == nil || ctx.Err() != nil {
		return err == nil
	}
	k.h.askAgain()
	took := false
	if k.asks != nil {
		k.asks.Turn(func() {
			var given string
			if given, took = giveSudo(ctx, k.run, k.asks.Answer()); took && given != "" {
				k.asks.Keep(given)
			} else if !took && ctx.Err() == nil {
				k.asks.Keep("")
			}
		})
	}
	k.h.settle(took)
	return took
}

// sudoAsks is the prompt kit has sudo give on its standard error when it
// wants the password, for kit to ask for it in its own field.
const sudoAsks = "[kit: sudo wants the password]"

// giveSudo has sudo take an administrator's password, Touch ID first,
// where sudo has it; else, each time sudo asks, what answer gives, on its
// input, never on a command line or in the log. kit gives sudo no time
// limit: sudo's own, five minutes for the password, can run out while the
// person's away, so the answer that came too late is given to sudo asked
// again. Whether sudo took it, and what it took: none, for Touch ID.
func giveSudo(ctx context.Context, run runner.Runner, answer func(ctx context.Context, asked int) (string, error)) (given string, took bool) {
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
					last = late
					return late, nil
				}
				given, err := answer(ctx, asked)
				last = given
				return given, err
			},
		})
		switch {
		case err == nil:
			return last, true
		case !errors.Is(err, runner.ErrAnsweredLate) || last == "" || ctx.Err() != nil:
			return "", false
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
// asking meanwhile (askpass's Turn); what sudo takes is kept for the run,
// for what asks later. Whether sudo took it.
func (a *app) giveAdmin(ctx context.Context, r *run, about look.Row) (took bool) {
	ask := func(ctx context.Context, asked int) (string, error) {
		row := about
		if asked > 0 {
			row.Says = look.Orange("that wasn't it: try again")
		}
		return a.ReadSecret(ctx, row)
	}
	if r.askpass == nil {
		_, took = giveSudo(ctx, r.run, ask)
		return took
	}
	r.askpass.Turn(func() {
		var given string
		if given, took = giveSudo(ctx, r.run, ask); !took {
			r.askpass.Keep("")
		} else if given != "" {
			r.askpass.Keep(given)
		}
	})
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
