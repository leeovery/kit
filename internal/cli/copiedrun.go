package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/redact"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/steps"
)

// runLine runs l through the program of d's kind, then has each name in
// it declared, installed, or undeclared, uninstalled, a step each, and one
// commit and push of the config's changes; a dry run, none. A kind that
// finds what's typed, as an App Store app by its id, has each name found
// first, as kit <kind> add finds it, and goes by the name found.
func (a *app) runLine(ctx context.Context, r *run, d kind.Descriptor, l copiedLine) error {
	k, liner, ks, err := copiedKinds(r, d, l.args)
	if err != nil {
		return err
	}
	switch l.names, err = a.find(ctx, k, l.names); {
	case errors.Is(err, ask.ErrCancelled):
		// It says nothing was done itself.
		return err
	case err != nil:
		return fmt.Errorf("%w; nothing was %s", err, lineWords(d, l).Done)
	}
	l.names = onceBy(l.names, kind.KeyOf(k))
	switch {
	case l.dryRun:
		return a.dryLine(ctx, r, d, liner, ks, l)
	case l.installs:
		return a.installLine(ctx, r, d, liner, ks, l)
	}
	return a.uninstallLine(ctx, r, d, liner, ks, l)
}

// startLine starts the run of l, its program's step first, doing what its
// verb does, then a step for each of its names, doing doing.
func startLine(r *run, d kind.Descriptor, l copiedLine, doing string) *changes {
	program := d.Help.Line.Program
	steps := []event.Step{{Name: program, Title: cmp.Or(d.Tool, program)}}
	doings := map[string]string{program: lineWords(d, l).Doing}
	for _, name := range l.names {
		steps = append(steps, event.Step{Name: name, Title: name})
		doings[name] = doing
	}
	return startChangesOf(r, steps, doings)
}

// lineWords are what l does, in d's words: installing or uninstalling.
func lineWords(d kind.Descriptor, l copiedLine) kind.Words {
	if l.installs {
		return d.Installs()
	}
	return d.Removes()
}

// ranLine is how running line through liner went, for its program's step,
// said in words: the values of the secrets ctx sets for it hidden.
func ranLine(ctx context.Context, liner kind.Liner, line kind.CopiedLine, words kind.Words) check.Result {
	if err := liner.Line(event.WithChanging(ctx), line); err != nil {
		return check.Result{State: check.Failed, Reason: redact.Text(couldnt(words)+engine.Reason(err), runner.SecretValues(ctx)...)}
	}
	return check.Result{State: check.OK, Summary: words.Done}
}

// dryLine runs l, a dry run, of ks, its program saying what it would do,
// with the secrets the lines declared for its names hold them for: nothing
// is readied, declared or undeclared, and kit-config isn't touched.
func (a *app) dryLine(ctx context.Context, r *run, d kind.Descriptor, liner kind.Liner, ks []kind.Kind, l copiedLine) error {
	names := l.names
	l.names = nil
	c := startLine(r, d, l, "")
	c.step(ctx, d.Help.Line.Program, func(ctx context.Context) check.Result {
		env, err := dryEnv(ctx, r, ks, names)
		if err != nil {
			return check.Result{State: check.Failed, Reason: saysCouldnt(lineWords(d, l), err.Error())}
		}
		line := kind.CopiedLine{Verb: l.typed, Args: l.args, Installs: l.installs, DryRun: true}
		res := ranLine(runner.WithSecretEnv(ctx, env...), liner, line, lineWords(d, l))
		if res.State == check.OK {
			res.Summary = "a dry run: nothing declared"
			if !l.installs {
				res.Summary = "a dry run: nothing undeclared"
			}
		}
		return res
	})
	return c.finish()
}

// planned is a name in an install line as kit found it before the line
// ran: the kind it's installed as, or would install as, nil when no kind
// knows it; whether it's installed; and what declaring it declares, judged
// as kit <kind> add judges it.
type planned struct {
	k    kind.Kind
	isIn bool
	p    adding
}

// installLine runs l, an install line, once each name in it is judged as
// kit <kind> add judges it, and an administrator's password is at hand for
// the casks that need it, the line refused whole otherwise; then declares
// each name it installed, or found installed already.
func (a *app) installLine(ctx context.Context, r *run, d kind.Descriptor, liner kind.Liner, ks []kind.Kind, l copiedLine) error {
	plans, undo, err := planInstall(ctx, r, liner, ks, l)
	if err != nil {
		return undone(ctx, undo, err)
	}
	missing := missingOf(plans, l.names)
	ctx, held, stop := a.holdAdmin(ctx, r, missing)
	defer stop()
	if err := adminRefused(ctx, r, d, l, missing, held); err != nil {
		return undone(ctx, undo, err)
	}
	settings, err := a.lineSettings(r, missing)
	if err != nil {
		return undone(ctx, undo, err)
	}
	c := startLine(r, d, l, "declaring")
	words := lineWords(d, l)
	c.step(ctx, d.Help.Line.Program, func(ctx context.Context) check.Result {
		install := func() check.Result {
			env, err := lineEnv(ctx, r, plans, l.names)
			if err != nil {
				return check.Result{State: check.Failed, Reason: saysCouldnt(words, err.Error())}
			}
			return ranLine(runner.WithSecretEnv(ctx, env...), liner, kind.CopiedLine{Verb: l.typed, Args: l.args, Installs: true, New: missing}, words)
		}
		if settings == nil {
			return install()
		}
		return settings(ctx, install)
	})
	after := newLookup(ks, l.names)
	for _, name := range l.names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return declareInstalled(ctx, r, c, after, name, plans[name], l)
		})
	}
	c.sync(ctx, commitMessage(strings.Join(l.verb, " "), d.Name, c.whoWrote(l.names), r.machine, l.note))
	return c.finish()
}

// planInstall judges each name in l, of ks, before the line runs, as kit
// <kind> add does: its tap added first, as kit's installs add it, so it's
// known; one installed or known that can't be declared refuses the line
// whole, as does what liner, its program's kind, refuses of it. A name no
// kind knows is left to the line, which says so. undo puts back what
// readying the names changed, for a line refused.
func planInstall(ctx context.Context, r *run, liner kind.Liner, ks []kind.Kind, l copiedLine) (_ map[string]planned, undo func(context.Context) error, _ error) {
	undo, err := readyLine(ctx, ks, l.names)
	if err != nil {
		return nil, undo, fmt.Errorf("%w; nothing was installed", err)
	}
	before := newLookup(ks, l.names)
	scope := r.scope(l.shared)
	opts := addOptions{shared: l.shared, note: l.note}
	plans := make(map[string]planned, len(l.names))
	var refused []error
	for _, name := range l.names {
		k, isIn, err := before.settle(ctx, name)
		if err != nil {
			return nil, undo, err
		}
		pl := planned{k: k, isIn: isIn}
		if k != nil {
			if pl.p, err = judgedAdding(ctx, r, k, scope, name, opts, isIn); err != nil {
				refused = append(refused, fmt.Errorf("%s can't be declared: %w", name, err))
			}
		}
		plans[name] = pl
	}
	if err := liner.Refuses(ctx, kind.CopiedLine{Verb: l.typed, Args: l.args, Installs: true, New: missingOf(plans, l.names)}); err != nil {
		refused = append(refused, err)
	}
	if len(refused) > 0 {
		return nil, undo, fmt.Errorf("%w; nothing was installed", kind.Joined(refused))
	}
	return plans, undo, nil
}

// readyLine readies what names, of ks, need before they're installed, as
// the taps they're from, once for kinds sharing a program, as readying
// one readies the others'; and returns what puts back what that changed,
// as far as it got (kind.Unreadier).
func readyLine(ctx context.Context, ks []kind.Kind, names []string) (func(context.Context) error, error) {
	var readied []string
	var undos []func(context.Context) error
	undo := func(ctx context.Context) error {
		var errs []error
		for _, u := range slices.Backward(undos) {
			errs = append(errs, u(ctx))
		}
		return errors.Join(errs...)
	}
	for _, k := range ks {
		rd, ok := k.(kind.Readier)
		if !ok || slices.Contains(readied, k.Name()) {
			continue
		}
		var err error
		if ur, ok := rd.(kind.Unreadier); ok {
			var u func(context.Context) error
			if u, err = ur.Readied(ctx, names); u != nil {
				undos = append(undos, u)
			}
		} else {
			err = rd.Ready(ctx, names)
		}
		if err != nil {
			return undo, err
		}
		readied = append(readied, k.Name())
		if s, ok := k.(kind.Sharing); ok {
			readied = append(readied, s.Shares()...)
		}
	}
	return undo, nil
}

// undone is refused, a line's refusal, once undo has put back what
// readying it changed, saying so if it couldn't.
func undone(ctx context.Context, undo func(context.Context) error, refused error) error {
	if err := undo(ctx); err != nil {
		return fmt.Errorf("%w; %s", refused, engine.Reason(err))
	}
	return refused
}

// judgedAdding is what adding name, of k, declares, judged as kit <kind>
// add judges it (prepareAdding), and held for the secret installing it
// needs (holdLine).
func judgedAdding(ctx context.Context, r *run, k kind.Kind, scope, name string, opts addOptions, isIn bool) (adding, error) {
	p, err := prepareAdding(ctx, r, k, scope, name, opts, isIn)
	if err == nil && p.ready {
		err = r.holdLine(ctx, k, scope, &p)
	}
	return p, err
}

// missingOf are those of names plans has as not installed, of a kind that
// knows them, by that kind.
func missingOf(plans map[string]planned, names []string) map[string][]string {
	missing := map[string][]string{}
	for _, name := range names {
		if pl := plans[name]; pl.k != nil && !pl.isIn {
			missing[pl.k.Name()] = append(missing[pl.k.Name()], name)
		}
	}
	return missing
}

// adminRefused says why l is refused, as an administrator's password isn't
// at hand, as held has it, for what of missing needs one: without a
// terminal to ask at, kit <kind> add says the same of each.
func adminRefused(ctx context.Context, r *run, d kind.Descriptor, l copiedLine, missing map[string][]string, held func(context.Context) bool) error {
	var refused []error
	for _, kindName := range slices.Sorted(maps.Keys(missing)) {
		waiting := waitingForAdmin(ctx, r, kindName, missing[kindName], held)
		for _, name := range missing[kindName] {
			if waiting[name] {
				refused = append(refused, fmt.Errorf("%s %s", name, fmt.Sprintf(adminWait, d.Name+" "+strings.Join(l.typed, " "))))
			}
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("%w; nothing was installed", kind.Joined(refused))
	}
	return nil
}

// lineSettings is what installs the apps of missing, as keptSettings has
// it, so the settings kit prefs kept for them are in place before each is
// first opened: nil unless one's an app, of a kind kit prefs keeps
// settings for.
func (a *app) lineSettings(r *run, missing map[string][]string) (func(context.Context, func() check.Result) check.Result, error) {
	for kindName := range missing {
		if d, _ := kinds.Named(kindName); d.Apps {
			return a.keptSettings(r, kindName, true)
		}
	}
	return nil, nil
}

// lineEnv is the environment a line installing names runs in: a variable
// for each secret one not installed yet needs, held for it, from kit's own
// secrets, as kit <kind> add installs it (installAdding).
func lineEnv(ctx context.Context, r *run, plans map[string]planned, names []string) ([]string, error) {
	var env []string
	for _, name := range names {
		if pl := plans[name]; pl.k != nil && !pl.isIn {
			held, err := r.heldEnv(ctx, pl.p.declared(), name)
			if err != nil {
				return nil, err
			}
			env = append(env, held...)
		}
	}
	return env, nil
}

// dryEnv is the environment a dry run of a line of names, of ks, runs in:
// a variable for each secret the line declared for one, for this Mac,
// holds it for, as an install of it would have it; nothing is found out
// that changes the Mac, so a name not declared runs without.
func dryEnv(ctx context.Context, r *run, ks []kind.Kind, names []string) ([]string, error) {
	var env []string
	for _, name := range names {
		for _, k := range ks {
			mine, err := declaredFor(r, k.Name(), name, false)
			if err != nil {
				return nil, err
			}
			if len(mine) == 0 {
				continue
			}
			held, err := r.heldEnv(ctx, mine[0], name)
			if err != nil {
				return nil, err
			}
			env = append(env, held...)
			break
		}
	}
	return env, nil
}

// heldEnv is the environment installing name, of e, its line, runs in:
// a variable for each secret e is held for, from kit's own secrets.
func (r *run) heldEnv(ctx context.Context, e config.Entry, name string) ([]string, error) {
	h, held, err := r.heldBy(e)
	if err != nil || !held {
		return nil, err
	}
	return kind.HeldEnv(ctx, h, []string{name})
}

// declareInstalled declares name, a name in l, as the thing after finds
// it installed as, as kit <kind> add declares a thing installed already,
// as pl, its plan, judged it, when it's that kind's still, else judged
// now as the plan was; one that isn't installed isn't declared.
func declareInstalled(ctx context.Context, r *run, c *changes, after *lookup, name string, pl planned, l copiedLine) check.Result {
	k, err := after.as(ctx, name)
	switch {
	case err != nil:
		return check.Result{State: check.Failed, Reason: "couldn't tell whether it's installed: " + err.Error()}
	case k == nil:
		return check.Result{State: check.Failed, Reason: "couldn't declare: it isn't installed"}
	}
	verb := descriptorOf(k).Installs().Done
	if pl.isIn {
		verb = "already " + verb
	}
	if h := descriptorOf(k).Help; len(after.kinds) > 1 && h != nil && h.One != "" {
		verb += " as " + h.One
	}
	scope, opts, p := r.scope(l.shared), addOptions{shared: l.shared, note: l.note}, pl.p
	if pl.k == nil || pl.k.Name() != k.Name() {
		if p, err = judgedAdding(ctx, r, k, scope, name, opts, true); err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
		}
	}
	return declareAdding(ctx, r, c, k, scope, name, opts, verb, p)
}

// removing is a name in an uninstall line as kit found it before the line
// ran: the kind it's installed as, nil for none; the names it's declared
// by, by kind; and, an app, what it leaves, with why kit prefs' saved
// settings for it weren't read, if they weren't, and, removing them, why
// they couldn't be, if they couldn't.
type removing struct {
	was    kind.Kind
	forms  map[string][]string
	lo     *leftover
	unread error
	forgot error
}

// zapping is what an uninstall line does with what its apps leave: on, it
// removes it, as flag, the program's flag for it, says (LineHelp.Zap);
// else it's kept.
type zapping struct {
	on   bool
	flag string
}

// kept says lo, what an app left, is kept, and what removes it: the
// program's flag, in a line; else, for a program without one, that its
// saved settings come back if it's installed again, as, the line having
// undeclared it, nothing kit has removes them now.
func (z zapping) kept(lo leftover) string {
	if z.flag != "" {
		return lo.kept(z.flag)
	}
	return "its saved settings kept, to come back if it's installed again"
}

// uninstallLine runs l, an uninstall line, once each name in it is found
// by whichever names it's declared by, one declared for every Mac refusing
// the line whole without --shared; then undeclares each name it took off,
// kit's own clean-up done for the apps among them, as kit cask remove does
// it, l's --zap removing what they leave.
func (a *app) uninstallLine(ctx context.Context, r *run, d kind.Descriptor, liner kind.Liner, ks []kind.Kind, l copiedLine) error {
	plans, before, err := planUninstall(ctx, r, ks, l)
	if err != nil {
		return err
	}
	zap := zapping{on: d.Help.Line.Zap != "" && slices.Contains(l.args, d.Help.Line.Zap), flag: d.Help.Line.Zap}
	p, err := a.lineLeftovers(ctx, r, l, plans, zap.on)
	if err != nil {
		return err
	}
	installed := map[string][]string{}
	for _, name := range l.names {
		if k := plans[name].was; k != nil {
			installed[k.Name()] = append(installed[k.Name()], name)
		}
	}
	ctx, _, stop := a.holdAdmin(ctx, r, installed)
	defer stop()
	c := startLine(r, d, l, "undeclaring")
	words := lineWords(d, l)
	ran := c.step(ctx, d.Help.Line.Program, func(ctx context.Context) check.Result {
		for _, name := range l.names {
			// Deleted while the app's there, as kit cask remove --zap does.
			if rm := plans[name]; zap.on && rm.lo != nil {
				rm.forgot = p.Forget(ctx, rm.lo.saved)
			}
		}
		return ranLine(ctx, liner, kind.CopiedLine{Verb: l.typed, Args: l.args}, words)
	})
	u := unlined{before: before, after: newLookup(ks, l.names)}
	if ran.State == check.Failed {
		u.failed = d.Help.Line.Program
	}
	for _, name := range l.names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return undeclareUninstalled(ctx, c, u, name, plans[name], l, words, p, zap)
		})
	}
	c.sync(ctx, commitMessage(strings.Join(l.verb, " "), d.Name, c.whoWrote(l.names), r.machine, ""))
	return c.finish()
}

// planUninstall finds each name in l, of ks, before the line runs: what
// it's installed as, and the names it's declared by (declaredAs). One
// declared for every Mac refuses the line whole without --shared. before
// is how it found them.
func planUninstall(ctx context.Context, r *run, ks []kind.Kind, l copiedLine) (_ map[string]*removing, before *lookup, _ error) {
	before = newLookup(ks, l.names)
	plans := make(map[string]*removing, len(l.names))
	for _, name := range l.names {
		k, err := before.as(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		plans[name] = &removing{was: k, forms: map[string][]string{}}
	}
	// By the names given first, in every kind; only a name declared by
	// none of them is resolved, as resolving asks the program.
	for _, resolve := range []bool{false, true} {
		var names []string
		for _, name := range l.names {
			if !resolve || len(plans[name].forms) == 0 {
				names = append(names, name)
			}
		}
		for _, k := range ks {
			as, err := declaredAs(ctx, r, k, names, resolve)
			if err != nil {
				return nil, nil, err
			}
			for name, forms := range as {
				if len(forms) > 0 {
					plans[name].forms[k.Name()] = forms
				}
			}
		}
	}
	if l.shared {
		return plans, before, nil
	}
	for _, name := range l.names {
		for _, k := range ks {
			for _, form := range plans[name].forms[k.Name()] {
				inShared, err := declaredShared(r, k.Name(), form)
				if err != nil {
					return nil, nil, err
				}
				if inShared {
					return nil, nil, fmt.Errorf("%s is %s; nothing was uninstalled", form, everyMac)
				}
			}
		}
	}
	return plans, before, nil
}

// lineLeftovers finds what each app plans has installed leaves, as kit
// cask remove finds it (leftoverOf), and returns kit prefs, which keeps
// their settings: nil when none's an app.
func (a *app) lineLeftovers(ctx context.Context, r *run, l copiedLine, plans map[string]*removing, zap bool) (*prefs.Prefs, error) {
	var p *prefs.Prefs
	saving := false
	for _, name := range l.names {
		rm := plans[name]
		lv, ok := rm.was.(kind.Leaver)
		if !ok {
			continue
		}
		if p == nil {
			features, err := r.features()
			if err != nil {
				return nil, err
			}
			if p, err = a.prefs(r); err != nil {
				return nil, err
			}
			saving = features.on(steps.FeaturePrefs)
		}
		lo, unread, err := leftoverOf(ctx, r, rm.was, lv, p, saving, name)
		if err != nil && !zap {
			// Not knowing what it leaves, it's taken off as any thing is.
			continue
		}
		rm.lo, rm.unread = &lo, unread
	}
	return p, nil
}

// errStillInstalled is why a name still installed isn't undeclared.
var errStillInstalled = errors.New("it's still installed")

// unlined is how an uninstall line's names stand: before, as found before
// it ran; after, once it had; and failed, its program, when the line
// failed, "" when it didn't.
type unlined struct {
	before, after *lookup
	failed        string
}

// undeclareUninstalled takes name, a name in l, out of this Mac's
// declarations, and the shared ones with --shared, by each name it's
// declared by that's gone (gone): one still installed as it was stays
// declared, the saved settings deleted for it put back, even when what's
// installed can't be told; and, the line failed, one that wasn't installed
// before it stays declared. words are what l does.
func undeclareUninstalled(ctx context.Context, c *changes, u unlined, name string, rm *removing, l copiedLine, words kind.Words, p *prefs.Prefs, zap zapping) check.Result {
	still := false
	var err error
	if rm.was != nil {
		still, err = u.after.lists(ctx, rm.was, name, true)
	}
	if err != nil || still {
		why := cmp.Or(err, errStillInstalled)
		if zap.on && rm.lo != nil {
			why = putBack(ctx, p, rm.lo.saved, why)
		}
		if err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't tell whether it's installed: " + why.Error()}
		}
		return check.Result{State: check.Failed, Reason: "couldn't undeclare: " + why.Error()}
	}
	declared := slices.ContainsFunc(u.after.kinds, func(k kind.Kind) bool { return len(rm.forms[k.Name()]) > 0 })
	switch {
	case !declared && rm.was == nil:
		return check.Result{State: check.OK, Summary: "wasn't installed or declared"}
	case u.failed != "" && rm.was == nil:
		return check.Result{State: check.OK, Summary: "wasn't installed, and " + u.failed + " failed: it stays declared"}
	}
	from, kept, err := u.undeclare(ctx, c, rm, l.shared)
	if err != nil {
		return check.Result{State: check.Failed, Reason: words.Done + ", but couldn't undeclare: " + err.Error()}
	}
	said := rm.said(ctx, p, words, zap)
	switch {
	case len(from) > 0:
		said = append(said, "out of "+strings.Join(once(from), " and "))
	case len(kept) == 0:
		said = append(said, "it wasn't declared")
	}
	for _, why := range slices.Sorted(maps.Keys(kept)) {
		said = append(said, strings.Join(kept[why], " and ")+" stays declared"+why)
	}
	return check.Result{State: check.OK}.Saying(said...)
}

// undeclare undeclares each name rm's declared by that's gone (gone), from
// this Mac's declarations, and the shared ones with shared: the files it's
// out of; and those kept, by why they're kept.
func (u unlined) undeclare(ctx context.Context, c *changes, rm *removing, shared bool) (from []string, kept map[string][]string, err error) {
	kept = map[string][]string{}
	for _, k := range u.after.kinds {
		for _, form := range rm.forms[k.Name()] {
			why, err := u.gone(ctx, rm, k, form)
			if err != nil {
				return nil, nil, err
			}
			if why != "" {
				kept[why] = append(kept[why], form)
				continue
			}
			out, err := c.undeclareHere(k.Name(), form, shared)
			if err != nil {
				return nil, nil, err
			}
			from = append(from, out...)
		}
	}
	return from, kept, nil
}

// gone says why form, a name rm's declared by, of k, stays declared, as
// what follows "stays declared": "" when it's gone, so it's undeclared. A
// form of the kind rm's name was installed as names it, found gone
// already; one of another kind is gone once k has it no more, and, the
// line failed, only when it was installed before the line, as the line
// may never have reached it.
func (u unlined) gone(ctx context.Context, rm *removing, k kind.Kind, form string) (string, error) {
	if rm.was != nil && rm.was.Name() == k.Name() {
		return "", nil
	}
	in, err := u.after.lists(ctx, k, form, true)
	switch {
	case err != nil:
		return "", fmt.Errorf("couldn't tell whether %s is installed: %w", form, err)
	case in:
		return ", still installed", nil
	case u.failed == "":
		return "", nil
	}
	was, err := u.before.lists(ctx, k, form, true)
	switch {
	case err != nil:
		return "", fmt.Errorf("couldn't tell whether %s was installed: %w", form, err)
	case !was:
		return ": it wasn't installed, and " + u.failed + " failed", nil
	}
	return "", nil
}

// said is what became of rm's name, taken off, in parts, as words say it:
// an app saying what became of what it leaves, as kit cask remove says it,
// with zap removed, else kept.
func (rm *removing) said(ctx context.Context, p *prefs.Prefs, words kind.Words, zap zapping) []string {
	var said []string
	switch lo := rm.lo; {
	case rm.was == nil:
		return []string{"wasn't " + kind.InstallWords.Done}
	case lo == nil:
		said = []string{words.Done}
	case zap.on:
		said = lo.zappedSaid(ctx, p, rm.forgot)
	case len(lo.files)+len(lo.saved) > 0:
		said = []string{lo.done + ", " + zap.kept(*lo)}
	default:
		said = []string{lo.done}
	}
	if rm.unread != nil {
		said = append(said, "its saved settings weren't looked at: "+rm.unread.Error())
	}
	return said
}
