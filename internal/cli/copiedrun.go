package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/check"
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
// commit and push of the config's changes; a dry run, none.
func (a *app) runLine(ctx context.Context, r *run, d kind.Descriptor, l copiedLine) error {
	liner, ks, err := copiedKinds(r, d, l.args)
	switch {
	case err != nil:
		return err
	case l.dryRun:
		return a.dryLine(ctx, r, d, liner, l)
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

// dryLine runs l, a dry run, its program saying what it would do: nothing
// is declared or undeclared.
func (a *app) dryLine(ctx context.Context, r *run, d kind.Descriptor, liner kind.Liner, l copiedLine) error {
	l.names = nil
	c := startLine(r, d, l, "")
	c.step(ctx, d.Help.Line.Program, func(ctx context.Context) check.Result {
		res := ranLine(ctx, liner, kind.CopiedLine{Verb: l.verb, Args: l.args, Installs: l.installs}, lineWords(d, l))
		if res.State == check.OK {
			res.Summary = "a dry run: nothing declared or undeclared"
		}
		return res
	})
	c.sync(ctx, "")
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
	plans, err := planInstall(ctx, r, ks, l)
	if err != nil {
		return err
	}
	missing := missingOf(plans, l.names)
	ctx, held, stop := a.holdAdmin(ctx, r, missing)
	defer stop()
	if err := adminRefused(ctx, r, d, l, missing, held); err != nil {
		return err
	}
	settings, err := a.lineSettings(r, missing)
	if err != nil {
		return err
	}
	c := startLine(r, d, l, "declaring")
	c.step(ctx, d.Help.Line.Program, func(ctx context.Context) check.Result {
		install := func() check.Result {
			env, err := lineEnv(ctx, r, plans, l.names)
			if err != nil {
				return check.Result{State: check.Failed, Reason: couldnt(lineWords(d, l)) + err.Error()}
			}
			return ranLine(runner.WithSecretEnv(ctx, env...), liner, kind.CopiedLine{Verb: l.verb, Args: l.args, Installs: true, New: missing}, lineWords(d, l))
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
// whole. A name no kind knows is left to the line, which says so.
func planInstall(ctx context.Context, r *run, ks []kind.Kind, l copiedLine) (map[string]planned, error) {
	for _, k := range ks {
		if rd, ok := k.(kind.Readier); ok {
			if err := rd.Ready(ctx, l.names); err != nil {
				return nil, fmt.Errorf("%w; nothing was installed", err)
			}
		}
	}
	before := newLookup(ks, l.names)
	scope := r.scope(l.shared)
	opts := addOptions{shared: l.shared, note: l.note}
	plans := make(map[string]planned, len(l.names))
	var refused []error
	for _, name := range l.names {
		k, isIn, err := before.settle(ctx, name)
		if err != nil {
			return nil, err
		}
		pl := planned{k: k, isIn: isIn}
		if k != nil {
			if pl.p, err = prepareAdding(ctx, r, k, scope, name, opts, isIn); err == nil && pl.p.ready {
				err = r.holdLine(ctx, k, scope, &pl.p)
			}
			if err != nil {
				refused = append(refused, fmt.Errorf("%s can't be declared: %w", name, err))
			}
		}
		plans[name] = pl
	}
	if len(refused) > 0 {
		return nil, fmt.Errorf("%w; nothing was installed", kind.Joined(refused))
	}
	return plans, nil
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
				refused = append(refused, fmt.Errorf("%s %s", name, fmt.Sprintf(adminWait, d.Name+" "+strings.Join(l.verb, " "))))
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
			return a.keptSettings(r, kindName)
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
		pl := plans[name]
		if pl.k == nil || pl.isIn {
			continue
		}
		line := pl.p.line
		if len(pl.p.mine) > 0 {
			line = pl.p.mine[0]
		}
		secrets := line.Secrets()
		if len(secrets) == 0 {
			continue
		}
		waits, vars, err := r.needing(secrets)
		if err != nil {
			return nil, err
		}
		held, err := kind.HeldEnv(ctx, kind.Held{Hold: line.Hold, Waits: waits, Env: vars}, []string{name})
		if err != nil {
			return nil, err
		}
		env = append(env, held...)
	}
	return env, nil
}

// declareInstalled declares name, a name in l, as the thing after finds
// it installed as, as kit <kind> add declares a thing installed already,
// as pl, its plan, judged it, when it's that kind's still; one that isn't
// installed isn't declared.
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
	p := pl.p
	if pl.k == nil || pl.k.Name() != k.Name() {
		mine, err := declaredFor(r, k.Name(), name, l.shared)
		if err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
		}
		p = adding{mine: mine}
	}
	return declareAdding(ctx, r, c, k, r.scope(l.shared), name, addOptions{shared: l.shared, note: l.note}, verb, p)
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

// uninstallLine runs l, an uninstall line, once each name in it is found
// by whichever names it's declared by, one declared for every Mac refusing
// the line whole without --shared; then undeclares each name it took off,
// kit's own clean-up done for the apps among them, as kit cask remove does
// it, l's --zap removing what they leave.
func (a *app) uninstallLine(ctx context.Context, r *run, d kind.Descriptor, liner kind.Liner, ks []kind.Kind, l copiedLine) error {
	plans, err := planUninstall(ctx, r, ks, l)
	if err != nil {
		return err
	}
	zap := d.Help.Line.Zap != "" && slices.Contains(l.args, d.Help.Line.Zap)
	p, err := a.lineLeftovers(ctx, r, l, plans, zap)
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
	c.step(ctx, d.Help.Line.Program, func(ctx context.Context) check.Result {
		for _, name := range l.names {
			// Deleted while the app's there, as kit cask remove --zap does.
			if rm := plans[name]; zap && rm.lo != nil {
				rm.forgot = p.Forget(ctx, rm.lo.saved)
			}
		}
		return ranLine(ctx, liner, kind.CopiedLine{Verb: l.verb, Args: l.args}, words)
	})
	after := newLookup(ks, l.names)
	for _, name := range l.names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return undeclareUninstalled(ctx, c, after, name, plans[name], l, words, p, zap)
		})
	}
	c.sync(ctx, commitMessage(strings.Join(l.verb, " "), d.Name, c.whoWrote(l.names), r.machine, ""))
	return c.finish()
}

// planUninstall finds each name in l, of ks, before the line runs: what
// it's installed as, and the names it's declared by (declaredAs). One
// declared for every Mac refuses the line whole without --shared.
func planUninstall(ctx context.Context, r *run, ks []kind.Kind, l copiedLine) (map[string]*removing, error) {
	before := newLookup(ks, l.names)
	plans := make(map[string]*removing, len(l.names))
	for _, name := range l.names {
		k, err := before.as(ctx, name)
		if err != nil {
			return nil, err
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
				return nil, err
			}
			for name, forms := range as {
				if len(forms) > 0 {
					plans[name].forms[k.Name()] = forms
				}
			}
		}
	}
	if l.shared {
		return plans, nil
	}
	for _, name := range l.names {
		for _, k := range ks {
			for _, form := range plans[name].forms[k.Name()] {
				inShared, err := declaredShared(r, k.Name(), form)
				if err != nil {
					return nil, err
				}
				if inShared {
					return nil, fmt.Errorf("%s is %s; nothing was uninstalled", form, everyMac)
				}
			}
		}
	}
	return plans, nil
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

// undeclareUninstalled takes name, a name in l, out of this Mac's
// declarations, and the shared ones with --shared, by each name it's
// declared by, once after finds it installed as none of its kinds: one
// still installed stays declared, the saved settings deleted for it put
// back. words are what l does.
func undeclareUninstalled(ctx context.Context, c *changes, after *lookup, name string, rm *removing, l copiedLine, words kind.Words, p *prefs.Prefs, zap bool) check.Result {
	k, err := after.as(ctx, name)
	switch {
	case err != nil:
		return check.Result{State: check.Failed, Reason: "couldn't tell whether it's installed: " + err.Error()}
	case k != nil:
		why := errStillInstalled
		if zap && rm.lo != nil {
			why = putBack(ctx, p, rm.lo.saved, why)
		}
		return check.Result{State: check.Failed, Reason: "couldn't undeclare: " + why.Error()}
	}
	var from []string
	for _, k := range after.kinds {
		for _, form := range rm.forms[k.Name()] {
			out, err := c.undeclareHere(k.Name(), form, l.shared)
			if err != nil {
				return check.Result{State: check.Failed, Reason: words.Done + ", but couldn't undeclare: " + err.Error()}
			}
			from = append(from, out...)
		}
	}
	said := rm.said(ctx, p, words, zap)
	switch {
	case len(from) > 0:
		return check.Result{State: check.OK}.Saying(append(said, "out of "+strings.Join(once(from), " and "))...)
	case rm.was != nil:
		return check.Result{State: check.OK}.Saying(append(said, "it wasn't declared")...)
	}
	return check.Result{State: check.OK, Summary: "wasn't installed or declared: nothing to undeclare"}
}

// said is what became of rm's name, taken off, in parts, as words say it:
// an app saying what became of what it leaves, as kit cask remove says it,
// with zap removed, else kept.
func (rm *removing) said(ctx context.Context, p *prefs.Prefs, words kind.Words, zap bool) []string {
	var said []string
	switch lo := rm.lo; {
	case rm.was == nil:
		return []string{"wasn't " + kind.InstallWords.Done}
	case lo == nil:
		said = []string{words.Done}
	case zap:
		said = lo.zappedSaid(ctx, p, rm.forgot)
	case len(lo.files)+len(lo.saved) > 0:
		said = []string{lo.done + ", " + lo.kept()}
	default:
		said = []string{lo.done}
	}
	if rm.unread != nil {
		said = append(said, "its saved settings weren't looked at: "+rm.unread.Error())
	}
	return said
}
