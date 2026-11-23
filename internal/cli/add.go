package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/steps"
)

// addOptions are adding's flags, and which names wait for an
// administrator's password, as settled before anything's installed.
type addOptions struct {
	shared, temp bool
	note, group  string
	waiting      map[string]bool
	secret       secretOptions
}

// add installs and declares each of names, each on its own: each is
// judged first, before anything's installed or asked for (prepareAdding),
// one refused failing alone, and the password's held for what's left.
func (a *app) add(ctx context.Context, r *run, kindName string, names []string, opts addOptions) error {
	k, err := r.kindNamed(kindName)
	if err != nil {
		return err
	}
	scope := r.scope(opts.shared)
	if names, err = a.find(ctx, k, once(names)); err != nil {
		return err
	}
	names = once(names)
	missing, err := notInstalled(ctx, k, names)
	if err != nil {
		return err
	}
	prepared := make(map[string]adding, len(names))
	refused := make(map[string]error)
	for _, name := range names {
		if prepared[name], err = prepareAdding(ctx, r, k, scope, name, opts, !slices.Contains(missing, name)); err != nil {
			refused[name] = err
		}
	}
	missing = slices.DeleteFunc(missing, func(name string) bool { return refused[name] != nil })
	settings, err := a.keptSettings(r, kindName, false)
	if err != nil {
		return err
	}
	ctx, held, stop := a.holdAdmin(ctx, r, map[string][]string{kindName: missing})
	defer stop()
	opts.waiting = waitingForAdmin(ctx, r, kindName, missing, held)
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			if err := refused[name]; err != nil {
				return check.Result{State: check.Failed, Reason: "couldn't declare: " + err.Error()}
			}
			isIn := !slices.Contains(missing, name)
			install := func() check.Result { return installAndDeclare(ctx, r, c, k, scope, name, opts, isIn, prepared[name]) }
			if settings == nil || isIn {
				return install()
			}
			return settings(ctx, install)
		})
	}
	c.sync(ctx, commitMessage("add", kindName, c.whoWrote(names), r.machine, opts.note))
	return c.finish()
}

// keptSettings is what installs apps of the kind called kindName, as
// install does, then restores the settings kit prefs kept for each, till
// it was installed, before the person first opens it, saying what became
// of them, after a semicolon, or nothing when there were none: nil unless
// the kind's things are apps and kit prefs is on for this Mac. The apps
// are those that weren't there before the install, whether or not it all
// went: each named, with named, else its. One that failed says so after
// why.
func (a *app) keptSettings(r *run, kindName string, named bool) (func(context.Context, func() check.Result) check.Result, error) {
	if d, _ := kinds.Named(kindName); !d.Apps {
		return nil, nil
	}
	features, err := r.features()
	if err != nil || !features.on(steps.FeaturePrefs) {
		return nil, err
	}
	p, err := a.prefs(r)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, install func() check.Result) check.Result {
		before := p.Apps()
		res := install()
		var said []string
		for _, id := range slices.DeleteFunc(p.Apps(), func(id string) bool { return slices.Contains(before, id) }) {
			whose := "its"
			if named {
				whose = p.AppName(id) + "'s"
			}
			report, err := p.RestoreKept(ctx, []string{id})
			if s := keptSaid(report, err, whose); s != "" {
				said = append(said, s)
			}
		}
		switch {
		case len(said) == 0:
			return res
		case res.State == check.Failed:
			res.Reason = strings.Join(append([]string{res.Reason}, said...), "; ")
			return res
		}
		return res.Adding(said...)
	}, nil
}

// keptSaid is what became of the settings kept for an app just installed,
// whose they are, as report, or err, says: "" when none were kept.
func keptSaid(report prefs.RestoreReport, err error, whose string) string {
	switch {
	case err != nil:
		return whose + " saved settings weren't restored: " + err.Error()
	case len(report.Failed) > 0:
		return whose + " saved settings didn't come back: " + report.Failed[slices.Sorted(maps.Keys(report.Failed))[0]]
	case len(report.ToOpen) > 0:
		return whose + " saved settings come back once it's been opened and quit"
	case len(report.Waiting) > 0:
		return whose + " saved settings come back once it's quit"
	case len(report.Restored) > 0:
		return whose + " saved settings restored"
	}
	return ""
}

// once is names, each once, in the order first given.
func once(names []string) []string {
	var out []string
	for _, name := range names {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// onceBy is names, each thing once, as key matches them, in any case: the
// first given of each, as npm's typescript@5 after typescript is the same
// package.
func onceBy(names []string, key func(string) string) []string {
	var out []string
	for _, name := range names {
		if !slices.ContainsFunc(out, func(had string) bool { return strings.EqualFold(key(had), key(name)) }) {
			out = append(out, name)
		}
	}
	return out
}

// find settles what each typed name means, for a kind that finds things
// (an App Store app from its name), before anything's asked or installed:
// one match is it; several are a choice at a terminal, and without one an
// error listing them by the names that say which.
func (a *app) find(ctx context.Context, k kind.Kind, typed []string) ([]string, error) {
	f, ok := k.(kind.Finder)
	if !ok {
		return typed, nil
	}
	names := make([]string, 0, len(typed))
	for _, t := range typed {
		found, err := f.Find(ctx, t)
		if err != nil {
			return nil, err
		}
		switch {
		case len(found) == 1:
			names = append(names, found[0].Name)
		case a.pretty(a.Stdout):
			answers := make([]look.Choice, len(found))
			for i, f := range found {
				answers[i] = look.Choice{Label: f.Label}
			}
			i, err := a.Choose(ctx, ask.Question{
				About:   look.Row{State: look.NeedsYou, Name: t, Says: look.Says(look.Muted(kinds.Label(k.Name())), look.Orange("which is it?"))},
				Answers: answers,
			})
			if errors.Is(err, ask.ErrCancelled) {
				return nil, fmt.Errorf("%w: nothing was installed or declared", ask.ErrCancelled)
			}
			if err != nil {
				return nil, err
			}
			names = append(names, found[i].Name)
		default:
			var b strings.Builder
			fmt.Fprintf(&b, "%s could be any of these: name one", t)
			for _, f := range found {
				fmt.Fprintf(&b, "\n  %s   %s", f.Name, f.Label)
			}
			return nil, errors.New(b.String())
		}
	}
	return names, nil
}

// declaredFor are where name is declared already as adding it would: for
// every Mac, with shared; else for this Mac, shared or its own.
func declaredFor(r *run, kindName, name string, shared bool) ([]config.Entry, error) {
	where, err := r.cfg.Where(kindName, name)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(where, func(e config.Entry) bool {
		return e.Scope != config.Shared && (shared || e.Scope != r.machine)
	}), nil
}

// addOne installs name, unless it's installed, and declares it in scope's
// declarations, unless it's declared already, or temporary: a declare
// that would be refused is refused before anything's installed.
func addOne(ctx context.Context, r *run, c *changes, k kind.Kind, scope, name string, opts addOptions) check.Result {
	isIn, err := installed(ctx, k, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	p, err := prepareAdding(ctx, r, k, scope, name, opts, isIn)
	if err != nil {
		return check.Result{State: check.Failed, Reason: "couldn't declare: " + err.Error()}
	}
	return installAndDeclare(ctx, r, c, k, scope, name, opts, isIn, p)
}

// adding is what adding a name declares, settled before it's installed:
// where it's declared already, if it is; and the line it declares, judged,
// unless its value's read from it installed and it isn't yet (ready), with
// what declaring it says of the section it's held in (holdLine).
type adding struct {
	mine  []config.Entry
	line  config.Entry
	said  string
	ready bool
}

// prepareAdding settles what adding name declares, and judges it, as the
// declare will: why it would be refused, when it would. Its line's built
// once, here, unless it's declared already or temporary, which declare no
// line, or its value's read from it installed and it isn't yet.
func prepareAdding(ctx context.Context, r *run, k kind.Kind, scope, name string, opts addOptions, isIn bool) (adding, error) {
	mine, err := declaredFor(r, k.Name(), name, opts.shared)
	if err != nil {
		return adding{}, err
	}
	p := adding{mine: mine}
	_, valued := k.(kind.Valued)
	if len(mine) > 0 || opts.temp || valued && !isIn {
		return p, nil
	}
	if p.line, err = kind.Line(ctx, k, name, opts.note); err != nil {
		return p, err
	}
	p.ready = true
	return p, r.checkDeclare(k.Name(), scope, p.line)
}

// installAndDeclare installs name, unless it's installed, then declares it as p
// says, unless it's declared already, or temporary: in the section held for
// a secret its install needs, found first (holdLine).
func installAndDeclare(ctx context.Context, r *run, c *changes, k kind.Kind, scope, name string, opts addOptions, isIn bool, p adding) check.Result {
	kindName := k.Name()
	if !isIn && opts.waiting[name] {
		return check.Result{State: check.Failed, Reason: fmt.Sprintf(adminWait, kindName+" add")}
	}
	if p.ready {
		err := r.holdLine(ctx, k, scope, &p)
		event.Say(ctx, event.Doing{Says: c.doing(name)})
		if err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't declare: " + err.Error()}
		}
	}
	installs := descriptorOf(k).Installs()
	verb := "already " + installs.Done
	if !isIn {
		if err := r.installAdding(event.WithChanging(ctx), k, name, p); err != nil {
			return check.Result{State: check.Failed, Reason: couldnt(installs) + engine.Reason(err)}
		}
		verb = installs.Done
	}
	return declareAdding(ctx, r, c, k, scope, name, opts, verb, p)
}

// declareAdding declares name, installed, as verb says, as p says, unless
// it's declared already, or temporary: its line built and held now, unless
// it was before the install (p.ready).
func declareAdding(ctx context.Context, r *run, c *changes, k kind.Kind, scope, name string, opts addOptions, verb string, p adding) check.Result {
	if len(p.mine) > 0 {
		return check.Result{State: check.OK}.Saying(verb, "declared already, in "+p.mine[0].Scope)
	}
	if opts.temp {
		return temporarily(ctx, r, c, k, scope, name, verb)
	}
	if !p.ready {
		var err error
		if p.line, err = kind.Line(ctx, k, name, opts.note); err == nil {
			err = r.holdLine(ctx, k, scope, &p)
		}
		if err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
		}
	}
	said, err := c.declare(k.Name(), scope, p.line)
	if err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
	}
	return check.Result{State: check.OK}.Saying(verb, "declared in "+scope+said, p.said)
}

// holdLine has p's line, declared in scope's declarations, held for the
// secret installing it needs, p.said saying so, as kind.Hold does, judged
// again as declaring it will.
func (r *run) holdLine(ctx context.Context, k kind.Kind, scope string, p *adding) error {
	said, err := kind.Hold(ctx, k, r.cfg, scope, r.machine, &p.line, func(e config.Entry) error {
		return r.checkDeclare(k.Name(), scope, e)
	})
	p.said = said
	return err
}

// installAdding installs name, of k, which adding declares as p says: held
// in a section for secrets, as declared or to be, it's installed as kit
// apply installs that section's lines, once they're in place, with them
// set from kit's own secrets.
func (r *run) installAdding(ctx context.Context, k kind.Kind, name string, p adding) error {
	line := p.declared()
	h, held, err := r.heldBy(line)
	switch {
	case err != nil:
		return err
	case !held:
		return k.Install(ctx, []string{name})
	}
	list := r.lists[k.Name()]
	if !slices.Contains(list.Names(), name) {
		list.Entries = append(slices.Clone(list.Entries), config.Entry{Name: name, Hold: line.Hold})
	}
	return kind.InstallDeclared(ctx, k, list, []kind.Held{h}, []string{name})
}

// declared is the line adding declares, or the one declared already.
func (p adding) declared() config.Entry {
	if len(p.mine) > 0 {
		return p.mine[0]
	}
	return p.line
}

// heldBy is how e, a line to install, is held for the secrets installing
// it needs, set from kit's own secrets: false when it needs none.
func (r *run) heldBy(e config.Entry) (kind.Held, bool, error) {
	secrets := e.Secrets()
	if len(secrets) == 0 {
		return kind.Held{}, false, nil
	}
	waits, env, err := r.needing(secrets)
	return kind.Held{Hold: e.Hold, Waits: waits, Env: env}, true, err
}

// temporarily marks name, installed, as not declared for now: quiet for 7
// days, then reconcile asks, its kind looked after so it can.
func temporarily(ctx context.Context, r *run, c *changes, k kind.Kind, scope, name, verb string) check.Result {
	kindName := k.Name()
	full := name
	if resolved, err := kind.Resolve(ctx, k, []string{name}); err == nil && resolved[name] != "" {
		full = resolved[name]
	}
	if err := drift.Update(r.stateDir, func(rec *drift.Record) { rec.Temporarily(kindName+":"+full, r.now) }); err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	says := []string{verb + ", for now: not declared, quiet for 7 days, then kit reconcile asks"}
	// reconcile asks only of a kind looked after: one this Mac declares.
	if !r.lists[kindName].Declared {
		added, err := r.cfg.AddSection(kindName, scope)
		if err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't add its kind's section: " + err.Error()}
		}
		if added {
			c.changed(config.DeclFile(scope))
			says = append(says, "["+descriptorOf(k).Section.Header+"] added to "+scope+", empty")
		}
	}
	return check.Result{State: check.OK}.Saying(says...)
}

// notInstalled are which of names k doesn't find installed.
func notInstalled(ctx context.Context, k kind.Kind, names []string) ([]string, error) {
	var missing []string
	for _, name := range names {
		isIn, err := installed(ctx, k, name)
		if err != nil {
			return nil, err
		}
		if !isIn {
			missing = append(missing, name)
		}
	}
	return missing, nil
}
