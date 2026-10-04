package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/kind"
)

// addOptions are kit add's flags, and which names wait for an
// administrator's password, as settled before anything's installed.
type addOptions struct {
	shared, temp bool
	note, group  string
	waiting      map[string]bool
}

func newAddCommand(a *app) *cobra.Command {
	var opts addOptions
	cmd := &cobra.Command{
		Use:   "add <kind> <name>...",
		Short: "Install packages, and declare them for this Mac (--shared: every Mac)",
		Long: `Install packages, and declare them: in this Mac's declarations file, named
after it, unless --shared declares them for every Mac, in the shared file,
which takes them out of each Mac's own. The change is committed and pushed to
the config repository.

At a terminal, kit asks which group of the kind's section each goes in ("To be
sorted" first); --group answers without asking, and without a terminal they go
in "To be sorted". --note records why, after the name. --temp installs without
declaring, for a throwaway: it's quiet for 7 days, then kit reconcile asks.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := a.prepare("add", "add")
			if err != nil {
				return err
			}
			err = a.add(cmd.Context(), r, args[0], args[1:], opts)
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&opts.shared, "shared", false, "declare for every Mac, not this one alone")
	cmd.Flags().BoolVar(&opts.temp, "temp", false, "install without declaring: quiet for 7 days, then reconcile asks")
	cmd.Flags().StringVar(&opts.note, "note", "", "why it's declared, kept after its name")
	cmd.Flags().StringVar(&opts.group, "group", "", "the group it goes in, without asking")
	return cmd
}

func (a *app) add(ctx context.Context, r *run, kindName string, names []string, opts addOptions) error {
	k, err := r.kindNamed(kindName)
	if err != nil {
		return err
	}
	if d, ok := k.(kind.Declarer); ok && !opts.temp {
		return errors.New(d.HowToDeclare(names[0]) + "; --temp installs without declaring")
	}
	file := r.file(opts.shared)
	if names, err = a.find(ctx, k, names); err != nil {
		return err
	}
	groups, err := a.groupsFor(ctx, r, kindName, file, names, opts)
	if err != nil {
		return err
	}
	missing, err := notInstalled(ctx, k, names)
	if err != nil {
		return err
	}
	held, stop := a.holdAdmin(ctx, r, map[string][]string{kindName: missing})
	defer stop()
	opts.waiting = waitingForAdmin(ctx, r, kindName, missing, held)
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return addOne(ctx, r, c, k, file, name, groups[name], opts)
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
			labels := make([]string, len(found))
			for i, f := range found {
				labels[i] = f.Label
			}
			i, err := a.Choose(ctx, fmt.Sprintf("%s: which is %s?", k.Title(), t), labels)
			if errors.Is(err, ask.ErrCancelled) {
				return nil, errors.New("cancelled: nothing was installed or declared")
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

// groupsFor asks, before anything is installed, which group of file each
// name kit will declare goes in: --group answers for all; without a
// terminal, they go in "To be sorted".
func (a *app) groupsFor(ctx context.Context, r *run, kindName, file string, names []string, opts addOptions) (map[string]string, error) {
	groups := make(map[string]string)
	if opts.temp || !config.Grouped(kindName) {
		return groups, nil
	}
	headings, err := r.cfg.Groups(kindName, file)
	if err != nil {
		return nil, err
	}
	options := []string{config.ToBeSorted}
	for _, h := range headings {
		if h != config.ToBeSorted {
			options = append(options, h)
		}
	}
	for _, name := range names {
		mine, err := declaredFor(r, kindName, name, opts.shared)
		if err != nil {
			return nil, err
		}
		switch {
		case len(mine) > 0:
		case opts.group != "":
			groups[name] = opts.group
		case a.pretty(a.Stdout):
			i, err := a.Choose(ctx, fmt.Sprintf("Which group of [%s] in %s for %s?", config.Header(kindName), file, name), options)
			if errors.Is(err, ask.ErrCancelled) {
				return nil, errors.New("cancelled: nothing was installed or declared")
			}
			if err != nil {
				return nil, err
			}
			groups[name] = options[i]
		}
	}
	return groups, nil
}

// declaredFor are where name is declared already as adding it would: for
// every Mac, with shared; else for this Mac, shared or its own.
func declaredFor(r *run, kindName, name string, shared bool) ([]config.Entry, error) {
	where, err := r.where(kindName, name)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(where, func(e config.Entry) bool {
		return e.File != config.Shared && (shared || e.File != r.machine)
	}), nil
}

// addOne installs name, unless it's installed, and declares it in file, in
// group, unless it's declared already, or temporary.
func addOne(ctx context.Context, r *run, c *changes, k kind.Kind, file, name, group string, opts addOptions) check.Result {
	kindName := k.Name()
	isIn, err := installed(ctx, k, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	verb := "already installed"
	if !isIn {
		if opts.waiting[name] {
			return check.Result{State: check.Failed, Reason: fmt.Sprintf(adminWait, "add")}
		}
		if err := k.Install(ctx, []string{name}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't install: " + err.Error()}
		}
		verb = "installed"
	}
	mine, err := declaredFor(r, kindName, name, opts.shared)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	if len(mine) > 0 {
		return check.Result{State: check.OK, Summary: verb + "; declared already, in " + mine[0].File}
	}
	if opts.temp {
		full := name
		if resolved, err := k.Resolve(ctx, []string{name}); err == nil && resolved[name] != "" {
			full = resolved[name]
		}
		if err := drift.Update(r.stateDir, func(rec *drift.Record) { rec.Temporarily(kindName+":"+full, r.now) }); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		return check.Result{State: check.OK, Summary: verb + ", for now: not declared, quiet for 7 days, then kit reconcile asks"}
	}
	note := opts.note
	if d, ok := k.(kind.Describer); ok {
		if what := d.Describe(ctx, name); what != "" {
			note = strings.TrimSuffix(what+": "+note, ": ")
		}
	}
	e := config.Entry{Name: name, Note: note}
	if v, ok := k.(kind.Valued); ok {
		if e.Value, err = v.Value(ctx, name); err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
		}
	}
	if err := r.cfg.Declare(kindName, file, e, group); err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
	}
	c.changed(file)
	summary := fmt.Sprintf("%s; declared in %s", verb, file)
	if config.Grouped(kindName) {
		summary += " (" + groupOr(group) + ")"
	}
	if opts.shared {
		where, err := r.where(kindName, name)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		var moved []string
		for _, e := range where {
			if e.File == file {
				continue
			}
			if err := r.cfg.Undeclare(kindName, e.File, name); err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			c.changed(e.File)
			moved = append(moved, e.File)
		}
		if len(moved) > 0 {
			summary += ", out of " + strings.Join(moved, " and ")
		}
	}
	return check.Result{State: check.OK, Summary: summary}
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

func groupOr(group string) string {
	if group == "" {
		return config.ToBeSorted
	}
	return group
}
