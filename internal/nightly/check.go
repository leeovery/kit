package nightly

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
)

// CheckName names the step checking the scheduled runs.
const CheckName = "nightly"

// How long the runs may go without happening, or finishing.
const (
	hourlyWithin = 2 * time.Hour
	fullWithin   = 26 * time.Hour
	fullStuck    = 6 * time.Hour
)

// Check is the step checking kit's scheduled runs happen, and their jobs
// go well, as kit's record of them says: an hourly run in the last two
// hours, or the hourly launch loaded in them, its first run to come; a
// nightly run finished in the last 26, and none stuck, when there are
// nightly jobs; and each job's last outcome. Never run on a schedule, and
// the launch never loaded, is a problem: kit apply installs it.
func Check(jobs []Job, nightly bool, stateDir string, now func() time.Time) engine.Step {
	return engine.Step{
		Name: CheckName, Title: "Scheduled runs", Area: Area,
		Check: func(context.Context) check.Result {
			rec, err := Load(stateDir)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			var problems []check.Item
			add := func(id, what, todo string) {
				problems = append(problems, check.Item{ID: CheckName + ":" + id, Name: what, State: "problem", Detail: todo})
			}
			var glance []string
			t := now()
			last := slices.MaxFunc([]time.Time{rec.Hourly.Started, rec.Hourly.Finished}, time.Time.Compare)
			since := slices.MaxFunc([]time.Time{last, rec.Scheduled}, time.Time.Compare)
			switch {
			case since.IsZero():
				add("never", "kit has never run on a schedule", "kit apply installs the hourly launch")
			case t.Sub(since) <= hourlyWithin:
			case last.IsZero():
				add("hourly", "no hourly run since its launch was loaded "+ago(since, t), stopped)
			default:
				add("hourly", "the hourly run last ran "+ago(last, t), stopped)
			}
			if !last.IsZero() {
				glance = append(glance, "hourly "+last.In(t.Location()).Format("15:04"))
			}
			if nightly && !rec.Full.Started.IsZero() {
				switch {
				case rec.Full.Finished.IsZero() || rec.Full.Finished.Before(rec.Full.Started):
					if t.Sub(rec.Full.Started) > fullStuck {
						add("stuck", "the nightly run from "+rec.Full.Started.In(t.Location()).Format("2 Jan 15:04")+" never finished", "kit log shows where it stopped; a job may have hung")
					}
				case t.Sub(rec.Full.Finished) > fullWithin:
					add("stale", "the nightly run last finished "+ago(rec.Full.Finished, t), stopped)
				}
				glance = append(glance, "nightly "+rec.Full.Started.In(t.Location()).Format("15:04"))
			}
			for _, j := range jobs {
				if o, ok := rec.Jobs[j.Name]; ok && !o.OK {
					add(j.Name, j.Title+" failed: "+o.Said, "kit nightly "+j.Title+" runs it again; kit log shows its last run")
				}
			}
			summary := "never run on a schedule"
			switch {
			case len(glance) > 0:
				summary = "last ran: " + strings.Join(glance, " · ")
			case len(problems) == 0:
				summary = "its first run comes on the hour"
			}
			if len(problems) > 0 {
				return check.Result{State: check.Attention, Summary: summary, Items: problems}
			}
			return check.Result{State: check.OK, Summary: summary, Glance: strings.TrimPrefix(summary, "last ran: ")}
		},
	}
}

// stopped is what to do when the scheduled runs have stopped.
const stopped = "kit apply puts the hourly launch back in place; kit log shows the last run"

// ago says how long ago t was, from now, roughly.
func ago(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
