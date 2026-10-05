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
// hours, when there are hourly jobs; a nightly run finished in the last 26,
// and none stuck, when there are nightly ones; and each job's last outcome.
// Until kit nightly has run on a schedule, it says so, and nothing's due.
func Check(jobs []Job, hourly bool, nightly bool, stateDir string, now func() time.Time) engine.Step {
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
			if hourly && !rec.Hourly.Started.IsZero() {
				last := slices.MaxFunc([]time.Time{rec.Hourly.Started, rec.Hourly.Finished}, time.Time.Compare)
				if t.Sub(last) > hourlyWithin {
					add("hourly", "the hourly run last ran "+ago(last, t), "the hourly launch may have stopped: check it's loaded")
				}
				glance = append(glance, "hourly "+last.In(t.Location()).Format("15:04"))
			}
			if nightly && !rec.Full.Started.IsZero() {
				switch {
				case rec.Full.Finished.IsZero() || rec.Full.Finished.Before(rec.Full.Started):
					if t.Sub(rec.Full.Started) > fullStuck {
						add("stuck", "the nightly run from "+rec.Full.Started.In(t.Location()).Format("2 Jan 15:04")+" never finished", "kit log shows where it stopped; a job may have hung")
					}
				case t.Sub(rec.Full.Finished) > fullWithin:
					add("stale", "the nightly run last finished "+ago(rec.Full.Finished, t), "the hourly launch may have stopped: check it's loaded")
				}
				glance = append(glance, "nightly "+rec.Full.Started.In(t.Location()).Format("15:04"))
			}
			for _, j := range jobs {
				if o, ok := rec.Jobs[j.Name]; ok && !o.OK {
					add(j.Name, j.Title+" failed: "+o.Said, "kit nightly "+j.Title+" runs it again; kit log shows its last run")
				}
			}
			summary := "not run on a schedule yet"
			if len(glance) > 0 {
				summary = "last ran: " + strings.Join(glance, " · ")
			}
			if len(problems) > 0 {
				return check.Result{State: check.Attention, Summary: summary, Items: problems}
			}
			return check.Result{State: check.OK, Summary: summary, Glance: strings.Join(glance, " · ")}
		},
	}
}

// ago says how long ago t was, from now, roughly.
func ago(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
