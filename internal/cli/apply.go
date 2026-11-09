package cli

import (
	"context"
	"errors"
	"slices"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

func newApplyCommand(a *app) *cobra.Command {
	var plan bool
	cmd := &cobra.Command{
		Use:         "apply [step...]",
		Short:       "Make this Mac match the config: install what's declared and missing",
		Annotations: map[string]string{brief: "install what's declared and missing"},
		Long: `Make this Mac match its config: install what's declared and missing. Applying
never removes and never adopts anything: kit reconcile does those, as you
decide. --plan says what applying would do, and does nothing.

Name steps to apply only those, with what they need. Some installs need an
administrator's password (a cask that installs through a package, an App
Store app): at a terminal, kit asks for it once, as it starts, for as long
as it takes, the rest applying meanwhile; without one, those installs wait.
Exits 0 when nothing needs attention after applying, 1 when something does,
and 2 when kit couldn't apply.

On a Mac started from another (from, in kit.toml), the data restore copies
the paths [restore] lists from that Mac, over SSH; should it not answer, a
card asks for its Time Machine disk, Arq, or trying it again (without a
terminal, it fails: --restore-from answers it). --restore-from restores them
from a backup instead, on any Mac: Time Machine's, on a disk attached, or
Arq's, by hand in its app, a card saying what to restore where. --at picks
the backup: the latest at or before a date, as 2026-11-08 (by the end of
that day), or a time, as "2026-11-08 14:30" (by the end of that minute); the
latest when not given. A restore stopped part-way
carries on from where it began, flags or not; should that backup have gone
from the disk, --restore-from time-machine carries on from another. A path
the source hasn't is left out: --restore-from brings it later from one that
has it, while it's still absent. The projects restored have their
dependencies installed again: kit apply reinstall tries those that failed
again.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := a.restoring.chosen(); err != nil {
				return err
			}
			a.named = args
			command, logName := "apply", "apply"
			if plan {
				command, logName = "apply --plan", "apply-plan"
			}
			r, err := a.prepare(command, logName)
			if err != nil {
				return err
			}
			ctx, halt := a.stoppable(cmd.Context(), r.face)
			defer halt()
			var report engine.Report
			if plan {
				report, err = r.pipeline.Check(ctx, r.sink, r.options(a, args))
			} else {
				// The heading shows at once, with the run's lights, and what kit's
				// doing while it works out what will need an administrator's
				// password; taken down only if sudo asks for it.
				if len(args) == 0 {
					steps, _ := r.pipeline.Planned(r.options(a, args))
					r.sink.Emit(event.Preparing{Time: r.now, Command: command, Machine: r.machine, Doing: "checking what will need an administrator's password", Steps: steps})
				}
				stop := a.holdAdminAlongside(ctx, r, toInstall(ctx, r, args))
				report, err = r.pipeline.Apply(ctx, r.sink, r.options(a, args))
				stop()
			}
			if err == nil {
				err = r.remember(report)
			}
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
			// A run stopped offers nothing more.
			if decide, missing := needs(report); !plan && ctx.Err() == nil && a.pretty(a.Stdout) && len(decide) > len(missing) {
				if err := a.reconcileNow(ctx); err != nil {
					return err
				}
			}
			if report.Attention() || (plan && actions(report)) {
				return attention{}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&plan, "plan", false, "say what applying would do, and do nothing")
	a.restoring.at.now = a.Now
	cmd.Flags().Var(&a.restoring.from, "restore-from", "restore [restore]'s paths from a backup")
	cmd.Flags().Var(&a.restoring.at, "at", "with --restore-from, the latest backup by that date or time")
	return cmd
}

// actions reports whether any step of report has something applying would
// do.
func actions(report engine.Report) bool {
	for _, res := range report.Results {
		if res.Actions() {
			return true
		}
	}
	return false
}

// toInstall are what applying the steps only names (every step, when none)
// would install, by kind, for the kinds whose installs can need an
// administrator's password; and what the steps that need one would do.
func toInstall(ctx context.Context, r *run, only []string) map[string][]string {
	wanted := make(map[string][]string)
	for _, step := range r.adminSteps {
		if len(only) > 0 && !slices.Contains(only, step.Name) {
			continue
		}
		for _, it := range step.Check(ctx).Items {
			if it.Action != "" {
				wanted[step.Name] = append(wanted[step.Name], it.Name)
			}
		}
	}
	for _, name := range r.kinds {
		k := r.kindsByName[name]
		if _, ok := k.(kind.Admin); !ok || len(only) > 0 && !slices.Contains(only, name) {
			continue
		}
		for _, it := range kind.Compare(ctx, k, r.lists[name]).Items {
			if it.Action == kind.Install {
				wanted[name] = append(wanted[name], it.Name)
			}
		}
	}
	return wanted
}

// reconcileNow asks, under applying's report, whether to settle what it
// left differing from the config, which applying never does: yes, and kit
// becomes kit reconcile.
func (a *app) reconcileNow(ctx context.Context) error {
	i, err := a.Choose(ctx, ask.Question{
		About:   look.Row{State: look.NeedsYou, Name: "Reconcile now?"},
		Answers: []look.Choice{{Label: "Yes"}, {Label: "No"}},
	})
	switch {
	case errors.Is(err, ask.ErrCancelled):
		return nil
	case err != nil:
		return err
	case i == 0:
		return a.Become([]string{"reconcile"})
	}
	return nil
}

// needs is what a run found differing from the config: the things kit
// reconcile decides on, by name, and those of them kit apply sees to.
func needs(report engine.Report) (decide, missing []string) {
	for _, s := range report.Steps {
		for _, it := range report.Results[s.Name].Items {
			if it.Quiet != "" || !render.IsDrift(it) {
				continue
			}
			decide = append(decide, it.Name)
			if it.Action != "" || it.State == "missing" {
				missing = append(missing, it.Name)
			}
		}
	}
	return decide, missing
}
