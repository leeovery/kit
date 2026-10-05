// Package nightly is kit's scheduled work: jobs run every hour, and in the
// nightly run, once a day; what kit remembers of each run; and the steps
// that run the jobs, in order, each recording its outcome. The hourly launch
// runs kit nightly, which runs what's due, then every check.
package nightly

import (
	"context"
	"errors"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/state"
)

// file is the record of kit's runs, in its state directory.
const file = "nightly.json"

// Run is a run's start and finish: a zero finish while it's going, or when
// it never finished.
type Run struct {
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
}

// Outcome is how a job's last run went.
type Outcome struct {
	At time.Time `json:"at"`
	OK bool      `json:"ok"`
	// Said is what it said: what went wrong, when it failed.
	Said string `json:"said,omitempty"`
}

// Record is what kit remembers of its runs.
type Record struct {
	// Full is the last nightly run, and Hourly the last hourly one.
	Full   Run `json:"full"`
	Hourly Run `json:"hourly"`
	// Jobs are each job's last outcome, by job.
	Jobs map[string]Outcome `json:"jobs,omitempty"`
}

// Load reads the record in the state directory: an empty one when there's
// none.
func Load(stateDir string) (Record, error) {
	return state.Load[Record](stateDir, file)
}

// Update reads the record, changes it and writes it back, under a lock.
func Update(stateDir string, change func(*Record)) error {
	return state.Update(stateDir, file, change)
}

// LastDue is the most recent time the nightly run fell due, at or before
// now: today at at, or yesterday when that's still ahead.
func LastDue(now time.Time, at time.Duration) time.Time {
	y, m, d := now.Date()
	due := time.Date(y, m, d, 0, 0, 0, 0, now.Location()).Add(at)
	if due.After(now) {
		due = due.AddDate(0, 0, -1)
	}
	return due
}

// FullDue reports whether the nightly run is due at now: none has started
// since it last fell due. So a Mac asleep when it fell due runs it when it
// wakes.
func FullDue(r Record, now time.Time, at time.Duration) bool {
	return r.Full.Started.Before(LastDue(now, at))
}

// Job is a job kit runs: every hour, or in the nightly run.
type Job struct {
	// Name names the job's outcome in the record, and its step.
	Name  string
	Title string
	// Run does the job, saying what went wrong when it fails.
	Run func(ctx context.Context) error
}

// Area is the jobs' area, as the views group steps.
const Area = "Jobs"

// run is the action of a job that's due.
const run = "run"

// Steps are the steps that run jobs, in order, each after the one before:
// those in due run when the steps are applied, recording their outcomes in
// stateDir; the rest say they aren't due. A job run since started reports
// how it went.
func Steps(jobs []Job, due map[string]bool, stateDir string, started time.Time, now func() time.Time) []engine.Step {
	steps := make([]engine.Step, 0, len(jobs))
	for i, j := range jobs {
		s := engine.Step{
			Name: j.Name, Title: j.Title, Area: Area,
			Check: func(context.Context) check.Result {
				rec, err := Load(stateDir)
				if err != nil {
					return check.Result{State: check.Failed, Reason: err.Error()}
				}
				if o, ok := rec.Jobs[j.Name]; ok && !o.At.Before(started) {
					if !o.OK {
						return check.Result{State: check.Failed, Reason: o.Said}
					}
					return check.Result{State: check.OK, Summary: "ran"}
				}
				if !due[j.Name] {
					return check.Result{State: check.OK, Summary: "not due"}
				}
				return check.Result{State: check.OK, Summary: "due", Items: []check.Item{{ID: j.Name + ":run", Name: j.Title, State: "due", Action: run}}}
			},
			Apply: func(ctx context.Context, _ check.Result) error {
				err := j.Run(ctx)
				o := Outcome{At: now(), OK: err == nil}
				if err != nil {
					o.Said = err.Error()
				}
				if recErr := Update(stateDir, func(r *Record) {
					if r.Jobs == nil {
						r.Jobs = make(map[string]Outcome)
					}
					r.Jobs[j.Name] = o
				}); recErr != nil {
					return errors.Join(err, recErr)
				}
				return err
			},
		}
		if i > 0 {
			s.After = []string{jobs[i-1].Name}
		}
		steps = append(steps, s)
	}
	return steps
}
