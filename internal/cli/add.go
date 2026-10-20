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
)

// addOptions are adding's flags, and which names wait for an
// administrator's password, as settled before anything's installed.
type addOptions struct {
	shared, temp bool
	note, group  string
	waiting      map[string]bool
	secret       secretOptions
}

func (a *app) add(ctx context.Context, r *run, kindName string, names []string, opts addOptions) error {
	k, err := r.kindNamed(kindName)
	if err != nil {
		return err
	}
	scope := r.scope(opts.shared)
	if names, err = a.find(ctx, k, names); err != nil {
		return err
	}
	missing, err := notInstalled(ctx, k, names)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := checkAdding(ctx, r, k, scope, name, opts, !slices.Contains(missing, name)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	ctx, held, stop := a.holdAdmin(ctx, r, map[string][]string{kindName: missing})
	defer stop()
	opts.waiting = waitingForAdmin(ctx, r, kindName, missing, held)
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return addOne(ctx, r, c, k, scope, name, opts)
		})
	}
	c.sync(ctx, commitMessage("add", kindName, names, r.machine, opts.note))
	return c.finish()
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
	kindName := k.Name()
	isIn, err := installed(ctx, k, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	if err := checkAdding(ctx, r, k, scope, name, opts, isIn); err != nil {
		return check.Result{State: check.Failed, Reason: "couldn't declare: " + err.Error()}
	}
	verb := "already installed"
	if !isIn {
		if opts.waiting[name] {
			return check.Result{State: check.Failed, Reason: fmt.Sprintf(adminWait, kindName+" add")}
		}
		if err := k.Install(event.WithChanging(ctx), []string{name}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't install: " + engine.Reason(err)}
		}
		verb = "installed"
	}
	mine, err := declaredFor(r, kindName, name, opts.shared)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	if len(mine) > 0 {
		return check.Result{State: check.OK, Summary: verb + "; declared already, in " + mine[0].Scope}
	}
	if opts.temp {
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
	if d, ok := k.(kind.Declarer); ok {
		file, err := d.Declare(name)
		if err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
		}
		r.changedFile(c, file)
		return check.Result{State: check.OK, Summary: verb + "; declared in " + r.files.Tilde(file)}
	}
	e, err := addedLine(ctx, k, name, opts)
	if err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
	}
	moved, err := c.declare(kindName, scope, e)
	if err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
	}
	return check.Result{State: check.OK, Summary: fmt.Sprintf("%s; declared in %s%s", verb, scope, outOf(moved))}
}

// checkAdding says why declaring name, as adding it would, would be refused:
// nil when it wouldn't, or when adding declares nothing (declared already,
// temporary, or in a file of the kind's own), or when its line's value
// isn't read yet, as it's read from it installed, or doesn't read, which
// declaring says.
func checkAdding(ctx context.Context, r *run, k kind.Kind, scope, name string, opts addOptions, isIn bool) error {
	_, declarer := k.(kind.Declarer)
	_, valued := k.(kind.Valued)
	if opts.temp || declarer || valued && !isIn {
		return nil
	}
	mine, err := declaredFor(r, k.Name(), name, opts.shared)
	if err != nil || len(mine) > 0 {
		return err
	}
	if e, err := addedLine(ctx, k, name, opts); err == nil {
		return r.checkDeclare(k.Name(), scope, e)
	}
	return nil
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
