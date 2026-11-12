package cli

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/macapp"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/steps"
)

// jobTimeout is how long a job of the user's own may run.
const jobTimeout = time.Hour

// reportFile is the report kit nightly leaves, in kit's logs folder.
const reportFile = "report.txt"

func newNightlyCommand(a *app) *cobra.Command {
	var plan, scheduled bool
	cmd := &cobra.Command{
		Use:         "nightly [job...]",
		Short:       "Run the scheduled jobs that are due, then every check: what the hourly launch runs",
		Annotations: map[string]string{brief: "run the jobs that are due, then every check"},
		Long: `Run the jobs that are due, then every check, as kit status does. The hourly
jobs run every time; the nightly ones once a day, when the nightly run falls
due (nightly_at in kit.toml, 03:00 unless it says), or on the first run after
it's been missed, as by a Mac asleep then. The jobs run in order, each after
the one before, and one that fails never stops the others; the checks follow.
Nothing is installed or removed.

kit apply installs the hourly launch: launchd opens kit.app on the hour, and
kit.app runs kit nightly, so that Full Disk Access is kit.app's. Its runs
notify no one: what they find is in kit status, the log, and the report each
run leaves in kit's logs folder (report.txt).

Name jobs to run only those, now. --plan says what's due, and runs nothing.
kit nightly add and kit nightly remove declare and undeclare nightly jobs (so
no job is named add or remove).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			command, logName := "nightly", "nightly"
			if plan {
				command, logName = "nightly --plan", "nightly-plan"
			}
			a.scheduled = scheduled
			var report bytes.Buffer
			r, err := a.prepareWith(command, logName, render.Tee(render.NewPlain(&report), a.face()))
			if err != nil {
				return err
			}
			if scheduled {
				recordAccess(r)
			}
			err = a.nightly(cmd.Context(), r, args, plan)
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			if writeErr := steps.WriteWhole(filepath.Join(r.logsDir, reportFile), report.Bytes(), 0o700); writeErr != nil && err == nil {
				err = writeErr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&plan, "plan", false, "say what's due, and run nothing")
	// kit.app's: what its run finds of Full Disk Access is the app's.
	cmd.Flags().BoolVar(&scheduled, "scheduled", false, "run by the hourly launch, through kit.app")
	_ = cmd.Flags().MarkHidden("scheduled")
	// Nightly jobs are declared here too: add and remove, never a job's name.
	cmd.AddCommand(newLineAdd(a, config.NightlyKind, "Declare a nightly job"), newLineRemove(a, config.NightlyKind))
	return cmd
}

// recordAccess records whether the kit.app in kit's data folder has Full
// Disk Access, as this run, its child, finds by reading what only that
// shows: nothing, with none of that on the Mac to read. Unrecorded, the run
// goes on, its log saying why.
func recordAccess(r *run) {
	granted, found := steps.ProbeFullDiskAccess(r.homeDir)
	if !found {
		return
	}
	access := nightly.Access{At: r.now, App: macapp.Built(r.dataDir), Granted: granted}
	if err := nightly.Update(r.stateDir, func(rec *nightly.Record) { rec.Access = access }); err != nil {
		r.sink.Emit(event.Logged{Time: r.now, Line: "kit.app's Full Disk Access went unrecorded: " + err.Error(), Alert: true})
	}
}

// nightly runs the jobs due, or those named, then every check. A run of
// them all records the nightly run, when it's due; the hourly launch's
// (a.scheduled) records the hourly run too, so that one by hand never
// hides a launch that's stopped.
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
	all := len(names) == 0 && !plan
	hourlyRun, fullRun := all && a.scheduled, all && full
	if hourlyRun || fullRun {
		if err := nightly.Update(r.stateDir, func(rec *nightly.Record) {
			if hourlyRun {
				rec.Hourly = nightly.Run{Started: r.now}
			}
			if fullRun {
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
	if hourlyRun || fullRun {
		finished := a.Now()
		if err := nightly.Update(r.stateDir, func(rec *nightly.Record) {
			if hourlyRun {
				rec.Hourly.Finished = finished
			}
			if fullRun {
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
// checked, and applied only as far as a kind lets the scheduled run install
// things by itself (new backup exclusions); it removes nothing.
func nightlyPipeline(jobs []nightly.Job, due map[string]bool, r *run, now func() time.Time) (*engine.Pipeline, error) {
	all := nightly.Steps(jobs, due, r.stateDir, r.now, now)
	for _, s := range r.pipeline.Steps() {
		s.Apply = nil
		if k, ok := r.kindsByName[s.Name]; ok && slices.Contains(r.kinds, s.Name) {
			s.Apply = kind.UnattendedApply(k)
		}
		if len(jobs) > 0 {
			s.After = append(slices.Clone(s.After), jobs[len(jobs)-1].Name)
		}
		all = append(all, s)
	}
	return engine.New(all...)
}

// jobs are the jobs this Mac runs: its own hourly ones, then its own
// nightly ones, each named for its section, as in hourly:asimov; then the
// built-in ones its features switch on, settings capture last of the
// nightly ones, so it can never hold up the rest.
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
				return nil, nil, fmt.Errorf("%s: %s: %w", e.Pos(), e.Name, err)
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
	if features.on(steps.FeaturePrefs) {
		p, err := a.prefs(r)
		if err != nil {
			return nil, nil, err
		}
		hourly = append(hourly, nightly.RetryPending(func(ctx context.Context) error {
			if rec, err := p.Load(); err != nil || rec.Pending == nil {
				return err
			}
			report, err := p.Restore(ctx, prefs.RestoreOptions{Pending: true, Unattended: true})
			if err == nil && report.Failures() > 0 {
				err = fmt.Errorf("%s: kit prefs pending lists them", report.Summary())
			}
			return err
		}))
		daily = append(daily, nightly.CaptureSettings(func(ctx context.Context) error {
			if res := captureResult(p.Capture(ctx)); res.State != check.OK {
				return errors.New(cmp.Or(res.Reason, res.Summary))
			}
			return nil
		}))
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
