package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/linked"
)

func newRemoveCommand(a *app) *cobra.Command {
	var shared bool
	cmd := &cobra.Command{
		Use:   "remove <kind> <name>...",
		Short: "Uninstall packages, and undeclare them",
		Long: `Uninstall packages, and take them out of this Mac's file. One declared for
every Mac needs --shared, which takes it out of the shared file: every Mac's
list. When something installed still needs a package, nothing changes, and
Homebrew's reason is passed on. The change is committed and pushed to the
config repository.

kit remove file <path>... puts a copy of a linked file back in place of its
link, and takes it out of kit-config. kit remove path <dir>... takes a
directory off the PATH.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := a.prepare("remove", "remove")
			if err != nil {
				return err
			}
			err = a.remove(cmd.Context(), r, args[0], args[1:], shared)
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&shared, "shared", false, "take it out of the shared file, every Mac's list")
	return cmd
}

func (a *app) remove(ctx context.Context, r *run, kindName string, names []string, shared bool) error {
	if kindName == linked.StepName {
		return a.removeFiles(ctx, r, names, shared)
	}
	if kindName == pathsKind {
		return a.removePaths(ctx, r, names, shared)
	}
	if _, line := lineKinds[kindName]; line {
		return a.removeLines(ctx, r, kindName, names, shared)
	}
	k, err := r.kindNamed(kindName)
	if err != nil {
		return err
	}
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return removeOne(ctx, r, c, k, name, shared)
		})
	}
	c.sync(ctx, commitMessage("remove", kindName, names, r.machine, ""))
	return c.finish()
}

// removeDeclared uninstalls name, a thing of a kind declared in a file of
// its own, when it's installed, and takes it out of that file.
func removeDeclared(ctx context.Context, r *run, c *changes, k kind.Kind, d kind.Declarer, name string) check.Result {
	isIn, err := installed(ctx, k, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	verb := "wasn't installed"
	if isIn {
		if err := k.Remove(ctx, []string{name}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't uninstall: " + err.Error()}
		}
		verb = "uninstalled"
	}
	where, err := r.where(k.Name(), name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	if len(where) == 0 {
		return check.Result{State: check.OK, Summary: verb + "; it wasn't declared"}
	}
	file, err := d.Undeclare(name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't undeclare: " + err.Error()}
	}
	r.changedFile(c, file)
	return check.Result{State: check.OK, Summary: verb + "; out of " + r.files.Tilde(file)}
}

// removeOne uninstalls name, when it's installed, and takes it out of this
// Mac's file, and the shared one with shared.
func removeOne(ctx context.Context, r *run, c *changes, k kind.Kind, name string, shared bool) check.Result {
	kindName := k.Name()
	if d, ok := k.(kind.Declarer); ok {
		return removeDeclared(ctx, r, c, k, d, name)
	}
	own, sharedScope := r.scope(false), r.scope(true)
	where, err := r.where(kindName, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	var inShared, inOwn bool
	for _, e := range where {
		inShared = inShared || e.Scope == sharedScope
		inOwn = inOwn || e.Scope == own
	}
	if inShared && !shared {
		return check.Result{State: check.Failed, Reason: "declared for every Mac, in " + sharedScope + ": --shared takes it out of every Mac's list"}
	}
	isIn, err := installed(ctx, k, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	verb := "wasn't installed"
	if isIn {
		if err := k.Remove(ctx, []string{name}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't uninstall: " + err.Error()}
		}
		verb = "uninstalled"
	}
	var from []string
	for _, scope := range []string{own, sharedScope} {
		if scope == own && !inOwn || scope == sharedScope && !inShared {
			continue
		}
		if err := r.cfg.Undeclare(kindName, scope, name); err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't undeclare: " + err.Error()}
		}
		c.changed(config.DeclFile(scope))
		from = append(from, scope)
	}
	switch {
	case len(from) > 0:
		return check.Result{State: check.OK, Summary: verb + "; out of " + strings.Join(from, " and ")}
	case isIn:
		return check.Result{State: check.OK, Summary: verb + "; it wasn't declared"}
	}
	return check.Result{State: check.OK, Summary: "neither installed nor declared: nothing to do"}
}
