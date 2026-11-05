package cli

import (
	"context"
	"errors"
	"fmt"
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
	settings, err := a.keptSettings(r, kindName)
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
			res := installAndDeclare(ctx, r, c, k, scope, name, opts, isIn, prepared[name])
			if settings != nil && !isIn && res.State == check.OK {
				res.Summary += settings(ctx)
			}
			return res
		})
	}
	c.sync(ctx, commitMessage("add", kindName, c.whoWrote(names), r.machine, opts.note))
	return c.finish()
}

// keptSettings is what restores, straight after kit installs apps of the
// kind called kindName, the settings kit prefs kept for apps not installed
// whose apps are installed now, before the person first opens them,
// saying what it did, after a semicolon, or nothing when there were none:
// nil unless the kind's things are apps and kit prefs is on for this Mac.
func (a *app) keptSettings(r *run, kindName string) (func(context.Context) string, error) {
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
	return func(ctx context.Context) string {
		report, err := p.RestoreInstalled(ctx)
		switch {
		case err != nil:
			return "; its saved settings weren't restored: " + err.Error()
		case len(report.Restored) > 0:
			return "; its saved settings restored"
		}
		return ""
	}, nil
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
	where, err := r.where(kindName, name)
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
// once, here, unless it's declared already, temporary or in a file of the
// kind's own, which declare no line, or its value's read from it
// installed and it isn't yet.
func prepareAdding(ctx context.Context, r *run, k kind.Kind, scope, name string, opts addOptions, isIn bool) (adding, error) {
	mine, err := declaredFor(r, k.Name(), name, opts.shared)
	if err != nil {
		return adding{}, err
	}
	p := adding{mine: mine}
	_, declarer := k.(kind.Declarer)
	_, valued := k.(kind.Valued)
	if len(mine) > 0 || opts.temp || declarer || valued && !isIn {
		return p, nil
	}
	if p.line, err = addedLine(ctx, k, name, opts); err != nil {
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
	if len(p.mine) > 0 {
		return check.Result{State: check.OK, Summary: verb + "; declared already, in " + p.mine[0].Scope}
	}
	if opts.temp {
		return temporarily(ctx, r, c, k, scope, name, verb)
	}
	if d, ok := k.(kind.Declarer); ok {
		file, err := d.Declare(name)
		if err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
		}
		r.changedFile(c, file)
		return check.Result{State: check.OK, Summary: verb + "; declared in " + r.files.Tilde(file)}
	}
	if !p.ready {
		var err error
		if p.line, err = addedLine(ctx, k, name, opts); err == nil {
			err = r.holdLine(ctx, k, scope, &p)
		}
		if err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
		}
	}
	said, err := c.declare(kindName, scope, p.line)
	if err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
	}
	return check.Result{State: check.OK, Summary: fmt.Sprintf("%s; declared in %s%s%s", verb, scope, said, p.said)}
}

// holdLine has p's line, declared in scope's declarations, go in its kind's
// section held for the secret k says installing it needs (kind.Private),
// which every Mac the line reaches must declare, p.said saying so; or that k
// couldn't tell, the line then declared as it is. It's judged again so.
// Refused, what finding out changed on the Mac is put back; stopped, it
// fails, rather than declare what it didn't check.
func (r *run) holdLine(ctx context.Context, k kind.Kind, scope string, p *adding) error {
	private, ok := k.(kind.Private)
	if !ok {
		return nil
	}
	need, err := private.NeedsSecret(ctx, p.line.Name)
	if ctx.Err() != nil {
		return fmt.Errorf("stopped checking whether it needs a secret: %w", ctx.Err())
	}
	if err != nil {
		p.said = "; couldn't check whether it needs a secret: " + engine.Reason(err)
	} else if need.Secret != "" {
		p.line.Hold, p.said = config.SecretHold(need.Secret), "; held for "+need.Secret+": "+need.Why
	}
	refused := r.secretDeclared(scope, need)
	if refused == nil {
		refused = r.checkDeclare(k.Name(), scope, p.line)
	}
	if refused != nil && need.Undo != nil {
		if err := need.Undo(ctx); err != nil {
			refused = fmt.Errorf("%w; %s", refused, engine.Reason(err))
		}
	}
	return refused
}

// secretDeclared says why a line in scope's declarations can't be held for
// the secret need names, if it can't: a Mac the line reaches doesn't
// declare it (this Mac, or, for the shared declarations, any).
func (r *run) secretDeclared(scope string, need kind.Need) error {
	if need.Secret == "" {
		return nil
	}
	macs := []string{r.machine}
	if scope == config.Shared {
		macs = r.cfg.MacNames()
	}
	var without []string
	for _, mac := range macs {
		list, err := r.cfg.List(secretKind, mac)
		if err != nil {
			return err
		}
		if !slices.Contains(list.Names(), need.Secret) {
			without = append(without, mac)
		}
	}
	switch {
	case len(without) == 0:
		return nil
	case scope == config.Shared:
		return fmt.Errorf("%s, so it needs %s, which no [secrets] section declares for %s: declare it for every Mac first (kit secret add %s --shared)", need.Why, need.Secret, strings.Join(without, " and "), need.Secret)
	}
	return fmt.Errorf("%s, so it needs %s, which no [secrets] section declares: declare it first (kit secret add %s)", need.Why, need.Secret, need.Secret)
}

// installAdding installs name, of k, which adding declares as p says: held
// in a section for secrets, as declared or to be, it's installed as kit
// apply installs that section's lines, once they're in place, with them
// set from kit's own secrets.
func (r *run) installAdding(ctx context.Context, k kind.Kind, name string, p adding) error {
	line := p.line
	if len(p.mine) > 0 {
		line = p.mine[0]
	}
	secrets := line.Secrets()
	if len(secrets) == 0 {
		return k.Install(ctx, []string{name})
	}
	waits, env, err := r.needing(secrets)
	if err != nil {
		return err
	}
	list := r.lists[k.Name()]
	if !slices.Contains(list.Names(), name) {
		list.Entries = append(slices.Clone(list.Entries), config.Entry{Name: name, Hold: line.Hold})
	}
	return kind.InstallDeclared(ctx, k, list, []kind.Held{{Hold: line.Hold, Waits: waits, Env: env}}, []string{name})
}

// temporarily marks name, installed, as not declared for now: quiet for 7
// days, then reconcile asks, its kind looked after so it can.
func temporarily(ctx context.Context, r *run, c *changes, k kind.Kind, scope, name, verb string) check.Result {
	kindName := k.Name()
	full := name
	if resolved, err := k.Resolve(ctx, []string{name}); err == nil && resolved[name] != "" {
		full = resolved[name]
	}
	if err := drift.Update(r.stateDir, func(rec *drift.Record) { rec.Temporarily(kindName+":"+full, r.now) }); err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	summary := verb + ", for now: not declared, quiet for 7 days, then kit reconcile asks"
	// reconcile asks only of a kind looked after: one with a section.
	if d, _ := kinds.Named(kindName); d.Section != nil && !r.lists[kindName].Declared {
		added, err := r.cfg.AddSection(kindName, scope)
		if err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't add its kind's section: " + err.Error()}
		}
		if added {
			c.changed(config.DeclFile(scope))
			summary += "; [" + d.Section.Header + "] added to " + scope + ", empty"
		}
	}
	return check.Result{State: check.OK, Summary: summary}
}

// addedLine is the line adding name declares: its value, for a kind whose
// lines have one, as it's installed; the note given, after what the kind
// says it is, or that alone as the note kit makes up (DefaultNote).
func addedLine(ctx context.Context, k kind.Kind, name string, opts addOptions) (config.Entry, error) {
	e := config.Entry{Name: name, Note: opts.note}
	if d, ok := k.(kind.Describer); ok {
		if what := d.Describe(ctx, name); what != "" && opts.note != "" {
			e.Note = what + ": " + opts.note
		} else {
			e.DefaultNote = what
		}
	}
	if v, ok := k.(kind.Valued); ok {
		var err error
		if e.Value, err = v.Value(ctx, name); err != nil {
			return e, err
		}
	}
	return e, nil
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
