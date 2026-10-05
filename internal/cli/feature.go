package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/steps"
)

func newFeatureCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feature",
		Short: "Switch features on or off for this Mac: the pieces with nothing to list",
		Long: `Switch features on or off: the pieces kit has that come with nothing to list,
such as a backup tool, its checks and its jobs. A feature is on for this Mac
when its file or the shared one lists it in [features]; one of ` + strings.Join(steps.Features, ", ") + `.
The change is committed and pushed to the config repository.`,
	}
	for _, on := range []bool{true, false} {
		verb, short := "off", "Switch features off for this Mac (--shared: for every Mac)"
		if on {
			verb, short = "on", "Switch features on for this Mac (--shared: for every Mac)"
		}
		var shared bool
		sub := &cobra.Command{
			Use:   verb + " <feature>...",
			Short: short,
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := a.prepare("feature "+verb, "feature-"+verb)
				if err != nil {
					return err
				}
				err = a.switchFeatures(cmd.Context(), r, args, on, shared)
				if closeErr := r.close(); err == nil {
					err = closeErr
				}
				return err
			},
		}
		sub.Flags().BoolVar(&shared, "shared", false, "for every Mac, in the shared file")
		cmd.AddCommand(sub)
	}
	return cmd
}

// switchFeatures declares each feature named on, or takes it out, in this
// Mac's file or the shared one, then commits and pushes.
func (a *app) switchFeatures(ctx context.Context, r *run, names []string, on, shared bool) error {
	for _, name := range names {
		if !slices.Contains(steps.Features, name) {
			return fmt.Errorf("kit doesn't know the feature %s: one of %s", name, strings.Join(steps.Features, ", "))
		}
	}
	verb := "off"
	if on {
		verb = "on"
	}
	file := r.file(shared)
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(context.Context) check.Result {
			where, err := r.cfg.Where(config.FeaturesKind, name)
			if err != nil {
				return failed(err)
			}
			in := slices.ContainsFunc(where, func(e config.Entry) bool { return e.File == file })
			switch {
			case on && in:
				return check.Result{State: check.OK, Summary: "on already, in " + file}
			case on:
				if err := r.cfg.Declare(config.FeaturesKind, file, config.Entry{Name: name}, ""); err != nil {
					return failed(err)
				}
			case !in && slices.ContainsFunc(where, func(e config.Entry) bool { return e.File == config.Shared }):
				return check.Result{State: check.Failed, Reason: "on for every Mac, in " + config.Shared + ": --shared switches it off there"}
			case !in:
				return check.Result{State: check.OK, Summary: "off already"}
			default:
				if err := r.cfg.Undeclare(config.FeaturesKind, file, name); err != nil {
					return failed(err)
				}
			}
			c.changed(file)
			return check.Result{State: check.OK, Summary: verb + ", in " + file}
		})
	}
	c.sync(ctx, fmt.Sprintf("kit feature %s %s (%s)", verb, strings.Join(names, ", "), r.machine))
	return c.finish()
}

func failed(err error) check.Result {
	return check.Result{State: check.Failed, Reason: err.Error()}
}
