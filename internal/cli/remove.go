package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/kind"
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
config repository.`,
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

// removeOne uninstalls name, when it's installed, and takes it out of this
// Mac's file, and the shared one with shared.
func removeOne(ctx context.Context, r *run, c *changes, k kind.Kind, name string, shared bool) check.Result {
	kindName := k.Name()
	if d, ok := k.(kind.ReadOnly); ok {
		where, err := r.where(kindName, name)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		if len(where) > 0 {
			return check.Result{State: check.Failed, Reason: fmt.Sprintf("declared in %s, line %d: %s", where[0].File, where[0].Line, d.HowToDeclare(where[0].Name))}
		}
	}
	d := r.decls(kindName)
	own, sharedFile := d.file(false), d.file(true)
	where, err := r.where(kindName, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	var inShared, inOwn bool
	for _, e := range where {
		inShared = inShared || e.File == sharedFile
		inOwn = inOwn || e.File == own
	}
	if inShared && !shared {
		return check.Result{State: check.Failed, Reason: "declared for every Mac, in " + sharedFile + ": --shared takes it out of every Mac's list"}
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
	for _, file := range []string{own, sharedFile} {
		if file == own && !inOwn || file == sharedFile && !inShared {
			continue
		}
		if err := d.undeclare(file, name); err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't undeclare: " + err.Error()}
		}
		c.changed(file)
		from = append(from, file)
	}
	switch {
	case len(from) > 0:
		return check.Result{State: check.OK, Summary: verb + "; out of " + strings.Join(from, " and ")}
	case isIn:
		return check.Result{State: check.OK, Summary: verb + "; it wasn't declared"}
	}
	return check.Result{State: check.OK, Summary: "neither installed nor declared: nothing to do"}
}
