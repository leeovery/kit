package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/steps"
)

// jobTimeout is how long a job of the user's own may run.
const jobTimeout = time.Hour

// reportFile is the report kit nightly leaves, in kit's logs folder, for a
// notification's click to open.
const reportFile = "report.txt"

func newNightlyCommand(a *app) *cobra.Command {
	var plan, alerts bool
	cmd := &cobra.Command{
		Use:   "nightly [job...]",
		Short: "Run the scheduled jobs that are due, then every check: what the hourly launch runs",
		Long: `Run the jobs that are due, then every check, as kit status does. The hourly
jobs run every time; the nightly ones once a day, when the nightly run falls
due (nightly_at in kit.toml, 03:00 unless it says), or on the first run after
it's been missed, as by a Mac asleep then. The jobs run in order, each after
the one before, and one that fails never stops the others; the checks follow.
Nothing is installed or removed.

Name jobs to run only those, now. --plan says what's due, and runs nothing.

Each run leaves its report, kit status's plain lines, in kit's logs folder
(report.txt). --alerts prints, in place of the run, what to notify about, as
JSON, for the app that runs kit hourly: each problem at once, an alert each,
and the drift that needs attention as one digest, changing at most once a
day; an alert's id stays the same while it's the same problem.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			command, logName := "nightly", "nightly"
			if plan {
				command, logName = "nightly --plan", "nightly-plan"
			}
			var report bytes.Buffer
			collector := &render.Collector{}
			faces := []render.Face{render.NewPlain(&report), collector}
			if !alerts {
				faces = append(faces, a.face())
			}
			r, err := a.prepareWith(command, logName, render.Tee(faces...))
			if err != nil {
				return err
			}
			err = a.nightly(cmd.Context(), r, args, plan)
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			if writeErr := writeAtomic(filepath.Join(r.logsDir, reportFile), report.Bytes()); writeErr != nil && err == nil {
				err = writeErr
			}
			if alerts {
				list, alertsErr := nightly.Alerts(collector.Document(), r.stateDir, a.Now())
				if alertsErr != nil {
					return alertsErr
				}
				if list == nil {
					list = []nightly.Alert{}
				}
				if printErr := render.WriteJSON(a.Stdout, list); printErr != nil {
					return printErr
				}
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&plan, "plan", false, "say what's due, and run nothing")
	cmd.Flags().BoolVar(&alerts, "alerts", false, "print what to notify about, as JSON, for the hourly launch's app")
	return cmd
}

// writeAtomic writes data to path, whole or not at all, making its folder.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// nightly runs the jobs due, or those named, then every check.
func (a *app) nightly(ctx context.Context, r *run, names []string, plan bool) error {
	hourly, daily, err := a.jobs(r)
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

// jobs are the jobs this Mac runs: its own hourly ones, then its own
// nightly ones, each named for its section, as in hourly:asimov; then the
// built-in nightly ones its features switch on, the Scratch clean-up and,
// last, so it can never hold up the rest, settings capture.
func (a *app) jobs(r *run) (hourly, daily []nightly.Job, err error) {
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
	features, err := r.features()
	if err != nil {
		return nil, nil, err
	}
	if features[steps.FeatureScratch] {
		daily = append(daily, nightly.CleanScratch(filepath.Join(a.Scratch, "tmp"), home, a.UID, a.Now))
	}
	if features[steps.FeatureSettingsCapture] {
		daily = append(daily, nightly.CaptureSettings(r.run))
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
