package cli

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/secret"
)

func newSecretsCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Secrets, from 1Password",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "sync",
		Short: "Read every secret from 1Password, and put each in place",
		Long: `Read every secret kit-config's secrets sections declare from 1Password, in
one sitting, and put each in place: ~/.secrets.zsh, and the files. Run it
after changing a value in 1Password. A value that fails to read keeps its
previous one. Values are never printed or logged.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := a.prepare("secrets sync", "secrets-sync")
			if err != nil {
				return err
			}
			err = a.secretsSync(cmd.Context(), r)
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	})
	return cmd
}

// secretsSync syncs every secret, as a run of one step.
func (a *app) secretsSync(ctx context.Context, r *run) error {
	k, ok := r.kindsByName["secret"].(*secret.Secrets)
	if !ok || !slices.Contains(r.kinds, "secret") {
		return errors.New("no secrets are declared: kit-config's [secrets] declares them")
	}
	r.sink.Emit(event.RunStarted{Time: r.now, Command: r.command, Machine: r.machine, Version: r.version, Steps: []event.Step{{Name: "secret", Title: k.Title(), Area: "Drift"}}})
	started := time.Now()
	r.sink.Emit(event.StepStarted{Time: started, Step: "secret", Doing: "syncing"})
	report, err := k.Sync(event.WithStep(ctx, "secret"))
	res := check.Result{State: check.OK, Summary: report.Summary()}
	switch {
	case err != nil:
		res = check.Result{State: check.Failed, Reason: err.Error()}
	case len(report.Failed) > 0:
		res = check.Result{State: check.Failed, Reason: report.Error()}
	}
	r.sink.Emit(event.StepFinished{Time: time.Now(), Step: "secret", Result: res, Duration: time.Since(started)})
	counts := event.Tally(res)
	r.sink.Emit(event.RunFinished{Time: time.Now(), Duration: time.Since(started), Counts: counts})
	if res.State != check.OK {
		return attention{}
	}
	return nil
}
