package cli

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// sudoRefresh is how often kit renews sudo's hold on the password while it
// applies, well inside sudo's own five minutes.
const sudoRefresh = time.Minute

func newApplyCommand(a *app) *cobra.Command {
	var plan bool
	cmd := &cobra.Command{
		Use:   "apply [step...]",
		Short: "Make this Mac match the config: install what's declared and missing",
		Long: `Make this Mac match its config: install what's declared and missing. Applying
never removes and never adopts anything: kit reconcile does those, as you
decide. --plan says what applying would do, and does nothing.

Name steps to apply only those, with what they need. A cask that installs
through a package needs an administrator's password: at a terminal, kit asks
for it once, before anything is applied; without one, those casks wait.
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
				stop := a.holdAdmin(cmd.Context(), r, args)
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

// holdAdmin settles, before anything is applied, whether an administrator's
// password is at hand for the casks that install through a package, so no
// step asks mid-run. At a terminal, when a missing cask needs one, kit asks
// for it once, through sudo, and keeps sudo's hold on it fresh till stop is
// called. Without a terminal it asks nothing: sudo either holds a password
// already, or those casks wait.
func (a *app) holdAdmin(ctx context.Context, r *run, only []string) (stop func()) {
	stop = func() {}
	sudo := func(ctx context.Context, interactive bool, args ...string) bool {
		_, err := r.run.Run(ctx, runner.Command{Name: "sudo", Args: args, Interactive: interactive})
		return err == nil
	}
	if !a.pretty(a.Stdout) {
		var once sync.Once
		var held bool
		r.homebrew.Admin = func(ctx context.Context) bool {
			once.Do(func() { held = sudo(ctx, false, "-n", "true") })
			return held
		}
		return stop
	}
	if len(only) > 0 && !slices.Contains(only, "cask") {
		return stop
	}
	res := kind.Compare(ctx, r.homebrew.Casks(), r.casks)
	var missing []string
	for _, it := range res.Items {
		if it.Action == kind.Install {
			missing = append(missing, it.Name)
		}
	}
	needing, err := r.homebrew.NeedsAdmin(ctx, missing)
	if err != nil || len(needing) == 0 {
		return stop
	}
	held := sudo(ctx, true, "-v")
	r.homebrew.Admin = func(context.Context) bool { return held }
	if !held {
		return stop
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(sudoRefresh)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				sudo(ctx, false, "-n", "-v")
			}
		}
	})
	return func() {
		cancel()
		wg.Wait()
	}
}
