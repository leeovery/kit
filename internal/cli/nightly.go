package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/steps"
)

// jobTimeout is how long a job of the user's own may run.
const jobTimeout = time.Hour

func newNightlyCommand(a *app) *cobra.Command {
	var plan bool
	cmd := &cobra.Command{
		Use:   "nightly [job...]",
		Short: "Run the scheduled jobs that are due, then every check: what the hourly launch runs",
		Long: `Run the jobs that are due, then every check, as kit status does. The hourly
jobs run every time; the nightly ones once a day, when the nightly run falls
due (nightly_at in kit.toml, 03:00 unless it says), or on the first run after
it's been missed, as by a Mac asleep then. The jobs run in order, each after
the one before, and one that fails never stops the others; the checks follow.
Nothing is installed or removed.

Name jobs to run only those, now. --plan says what's due, and runs nothing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			command, logName := "nightly", "nightly"
			if plan {
				command, logName = "nightly --plan", "nightly-plan"
			}
			r, err := a.prepare(command, logName)
			if err != nil {
				return err
			}
			err = a.nightly(cmd.Context(), r, args, plan)
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&plan, "plan", false, "say what's due, and run nothing")
	return cmd
}

// nightly runs the jobs due, or those named, then every check.
func (a *app) nightly(ctx context.Context, r *run, names []string, plan bool) error {
	hourly, daily, err := r.jobs()
	if err != nil {
		return err
	}
	jobs := slices.Concat(hourly, daily)
	rec, err := nightly.Load(r.stateDir)
	if err != nil {
		return err
	}
	full := nightly.FullDue(rec, r.now, r.cfg.NightlyAt)
	due := make(map[string]bool)
	switch {
	case len(names) > 0:
		for _, name := range names {
			i := slices.IndexFunc(jobs, func(j nightly.Job) bool { return j.Name == name || j.Title == name })
			if i < 0 {
				return fmt.Errorf("no job named %s: %s", name, jobNames(jobs))
			}
			due[jobs[i].Name] = true
		}
	default:
		for _, j := range hourly {
			due[j.Name] = true
		}
		for _, j := range daily {
			due[j.Name] = full
		}
	}
	pipeline, err := nightlyPipeline(jobs, due, r, a.Now)
	if err != nil {
		return err
	}
	scheduled := len(names) == 0 && !plan
	if scheduled {
		if err := nightly.Update(r.stateDir, func(rec *nightly.Record) {
			rec.Hourly = nightly.Run{Started: r.now}
			if full {
				rec.Full = nightly.Run{Started: r.now}
			}
		}); err != nil {
			return err
		}
	}
	var report engine.Report
	if plan {
		report, err = pipeline.Check(ctx, r.sink, r.options(a, nil))
	} else {
		report, err = pipeline.Apply(ctx, r.sink, r.options(a, nil))
	}
	if err != nil {
		return err
	}
	if scheduled {
		finished := a.Now()
		if err := nightly.Update(r.stateDir, func(rec *nightly.Record) {
			rec.Hourly.Finished = finished
			if full {
				rec.Full.Finished = finished
			}
		}); err != nil {
			return err
		}
	}
	if err := r.remember(report); err != nil {
		return err
	}
	if report.Attention() || (plan && actions(report)) {
		return attention{}
	}
	return nil
}

// nightlyPipeline is the jobs, in order, then the run's other steps,
// checked only: kit nightly installs and removes nothing.
func nightlyPipeline(jobs []nightly.Job, due map[string]bool, r *run, now func() time.Time) (*engine.Pipeline, error) {
	all := nightly.Steps(jobs, due, r.stateDir, r.now, now)
	for _, s := range r.pipeline.Steps() {
		s.Apply = nil
		if len(jobs) > 0 {
			s.After = append(slices.Clone(s.After), jobs[len(jobs)-1].Name)
		}
		all = append(all, s)
	}
	return engine.New(all...)
}

// jobs are the user's own jobs this Mac declares: the hourly ones, then the
// nightly ones, each named for its section, as in hourly:asimov.
func (r *run) jobs() (hourly, daily []nightly.Job, err error) {
	home := r.homeDir
	for _, section := range []string{config.HourlyKind, config.NightlyKind} {
		list, err := r.cfg.List(section, r.machine)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range list.Entries {
			cmd, err := steps.OwnCommand(home, e.Value, jobTimeout)
			if err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %s: %w", e.File, e.Line, e.Name, err)
			}
			job := nightly.Job{Name: section + ":" + e.Name, Title: e.Name, Run: func(ctx context.Context) error {
				res, err := r.run.Run(ctx, cmd)
				if said := steps.Outcome(cmd, res, err); said != "" {
					return errors.New(said)
				}
				return nil
			}}
			if section == config.HourlyKind {
				hourly = append(hourly, job)
			} else {
				daily = append(daily, job)
			}
		}
	}
	return hourly, daily, nil
}

// jobNames says which jobs there are, for an error.
func jobNames(jobs []nightly.Job) string {
	if len(jobs) == 0 {
		return "this Mac declares none, in [hourly] or [nightly]"
	}
	names := make([]string, len(jobs))
	for i, j := range jobs {
		names[i] = j.Title
	}
	return "one of " + strings.Join(names, ", ")
}
