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
Store app): at a terminal, kit asks for it once, before anything is applied;
without one, those installs wait.
Exits 0 when nothing needs attention after applying, 1 when something does,
and 2 when kit couldn't apply.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			command, logName := "apply", "apply"
			if plan {
				command, logName = "apply --plan", "apply-plan"
			}
			r, err := a.prepare(command, logName)
			if err != nil {
				return err
			}
			var report engine.Report
			if plan {
				report, err = r.pipeline.Check(cmd.Context(), r.sink, r.options(a, args))
			} else {
				// The heading shows at once, and what kit's doing while it works
				// out what will need an administrator's password; taken down
				// before sudo asks for it.
				if len(args) == 0 {
					r.sink.Emit(event.Preparing{Time: r.now, Command: command, Machine: r.machine, Doing: "checking what will need an administrator's password"})
				}
				wanted := toInstall(cmd.Context(), r, args)
				if len(args) == 0 {
					r.sink.Emit(event.Preparing{Time: r.now, Command: command, Machine: r.machine})
				}
				_, stop := a.holdAdmin(cmd.Context(), r, wanted)
				report, err = r.pipeline.Apply(cmd.Context(), r.sink, r.options(a, args))
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
			if decide, missing := needs(report); !plan && a.pretty(a.Stdout) && len(decide) > len(missing) {
				if err := a.reconcileNow(cmd.Context(), r.face); err != nil {
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

// reconcileNow asks, under what applying showed, whether to settle what it
// left differing from the config, which applying never does: yes, and kit
// becomes kit reconcile.
func (a *app) reconcileNow(ctx context.Context, face render.Face) error {
	var shown []string
	if f, ok := face.(interface{ Shown() []string }); ok {
		shown = f.Shown()
	}
	if len(shown) > 0 && shown[0] == "" {
		shown = shown[1:]
	}
	i, err := a.Choose(ctx, shown, ask.Question{
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
