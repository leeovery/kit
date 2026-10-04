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

// addOptions are kit add's flags.
type addOptions struct {
	shared, temp bool
	note, group  string
}

func newAddCommand(a *app) *cobra.Command {
	var opts addOptions
	cmd := &cobra.Command{
		Use:   "add <kind> <name>...",
		Short: "Install packages, and declare them for this Mac (--shared: every Mac)",
		Long: `Install packages, and declare them: in this Mac's file (brew.<mac>) unless
--shared declares them for every Mac, which takes them out of each Mac's own
file. The change is committed and pushed to the config repository.

At a terminal, kit asks which group of the file each goes in ("To be sorted"
first); --group answers without asking, and without a terminal they go in "To
be sorted". --note records why, after the name. --temp installs without
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
	file := kindName + "." + r.machine
	if opts.shared {
		file = kindName
	}
	groups, err := a.groupsFor(ctx, r, kindName, file, names, opts)
	if err != nil {
		return err
	}
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return addOne(ctx, r, c, k, file, name, groups[name], opts)
		})
	}
	c.sync(ctx, commitMessage("add", kindName, names, r.machine, opts.note))
	return c.finish()
}

// groupsFor asks, before anything is installed, which group of file each
// name kit will declare goes in: --group answers for all; without a
// terminal, they go in "To be sorted".
func (a *app) groupsFor(ctx context.Context, r *run, kindName, file string, names []string, opts addOptions) (map[string]string, error) {
	groups := make(map[string]string)
	if opts.temp {
		return groups, nil
	}
	headings, err := r.cfg.Groups(file)
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
			i, err := a.Choose(ctx, fmt.Sprintf("Which group of %s for %s?", file, name), options)
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
	where, err := r.cfg.Where(kindName, name)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(where, func(e config.Entry) bool {
		return e.File != kindName && (shared || e.File != kindName+"."+r.machine)
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
	if err := r.cfg.Declare(file, name, group, opts.note); err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
	}
	c.changed(file)
	summary := fmt.Sprintf("%s; declared in %s (%s)", verb, file, groupOr(group))
	if opts.shared {
		where, err := r.cfg.Where(kindName, name)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		var moved []string
		for _, e := range where {
			if e.File == file {
				continue
			}
			if err := r.cfg.Undeclare(e.File, name); err != nil {
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

func groupOr(group string) string {
	if group == "" {
		return config.ToBeSorted
	}
	return group
}
