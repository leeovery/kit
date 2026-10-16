package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// The features: the pieces with nothing to list, switched on for a Mac in
// its [features] section.
const (
	FeatureTimeMachine = "time-machine"
	FeatureArq         = "arq"
	FeatureScratch     = "scratch"
	FeaturePrefs       = "prefs"
	FeatureOhMyZsh     = "oh-my-zsh"
)

// Features are the features kit knows.
var Features = []string{FeatureArq, FeatureFileSharing, FeatureOhMyZsh, FeaturePrefs, FeatureRemoteLogin, FeatureScratch, FeatureTimeMachine, FeatureTouchIDSudo}

// The backup checks' thresholds, as bin/health had them.
const (
	timeMachineEvery = 3 * time.Hour
	timeMachineStuck = 3 * time.Hour
	arqEvery         = 26 * time.Hour
)

// timeMachinePrefs is Time Machine's settings, as defaults names the domain:
// read through macOS's settings service, which has a change the moment it's
// made, where the file on disk lags behind.
const timeMachinePrefs = "/Library/Preferences/com.apple.TimeMachine"

// Arqc is Arq's command, inside its app.
const Arqc = "/Applications/Arq.app/Contents/Resources/arqc"

// when says when t was, from now: the time today, else the day too.
func when(t, now time.Time) string {
	t = t.In(now.Location())
	if t.Format("2006-01-02") == now.Format("2006-01-02") {
		return t.Format("15:04")
	}
	return t.Format("2 Jan 15:04")
}

// ago says how long ago t was, roughly.
func ago(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

var (
	mountPoint   = regexp.MustCompile(`(?m)^Mount Point\s*:`)
	stateChanged = regexp.MustCompile(`DateOfStateChange = "([^"]+)"`)
	backupPhase  = regexp.MustCompile(`BackupPhase = (\w+);`)
)

// TimeMachine checks this Mac's Time Machine backups: a backup disk set up,
// automatic backups on, the last backup within 3 hours, and none stuck; a
// disk just added, its first backup to come, is fine for now.
func TimeMachine(run runner.Runner, now func() time.Time) engine.Step {
	return engine.Step{
		Name: FeatureTimeMachine, Title: "Time Machine", Area: AreaBackups,
		Check: func(ctx context.Context) check.Result {
			out, err := output(ctx, run, "defaults", "export", timeMachinePrefs, "-")
			if _, exited := errors.AsType[*runner.ExitError](err); exited {
				return timeMachineNoDisk()
			}
			if err != nil {
				return failed(err)
			}
			v, err := plist.Read(out)
			prefs, _ := v.(map[string]any)
			if err != nil || prefs == nil {
				return failed(errors.New("couldn't read Time Machine's settings"))
			}
			destinations, _ := prefs["Destinations"].([]any)
			if len(destinations) == 0 {
				return timeMachineNoDisk()
			}
			var problems [][3]string
			if !plist.Equal(prefs["AutoBackup"], true) {
				problems = append(problems, [3]string{"off", "automatic backups are off", "System Settings › General › Time Machine › Options: back up every hour"})
			}
			var last time.Time
			tried := false
			for _, d := range destinations {
				dest, _ := d.(map[string]any)
				dates, _ := dest["SnapshotDates"].([]any)
				for _, x := range dates {
					if t, ok := x.(time.Time); ok && t.After(last) {
						last = t
					}
				}
				attempts, _ := dest["AttemptDates"].([]any)
				tried = tried || len(attempts) > 0
			}
			status, err := output(ctx, run, "tmutil", "status")
			running := err == nil && strings.Contains(status, "Running = 1")
			// away says the backup disk isn't connected, when tmutil says
			// so.
			away := func() string {
				if info, err := output(ctx, run, "tmutil", "destinationinfo"); err == nil && !mountPoint.MatchString(info) {
					return " (the backup disk isn't connected)"
				}
				return ""
			}
			summary := "no backup yet"
			switch {
			// A disk just added is fine for now: its first backup is under
			// way, or, none tried yet and the disk there, soon.
			case last.IsZero() && running:
				summary = "first backup under way"
			case last.IsZero():
				if where := away(); tried || where != "" {
					problems = append(problems, [3]string{"never", "no completed backup yet" + where, "connect the backup disk and back up now"})
				} else {
					summary = "first backup soon"
				}
			default:
				summary = "last backup " + when(last, now())
				if now().Sub(last) > timeMachineEvery {
					problems = append(problems, [3]string{"stale", "last backup " + ago(last, now()) + away(), "connect the backup disk; if it is connected, check System Settings › General › Time Machine"})
				}
			}
			if running {
				if m := stateChanged.FindStringSubmatch(status); m != nil {
					if since, err := time.Parse("2006-01-02 15:04:05 -0700", m[1]); err == nil && now().Sub(since) > timeMachineStuck {
						phase := "one phase"
						if p := backupPhase.FindStringSubmatch(status); p != nil {
							phase = p[1]
						}
						problems = append(problems, [3]string{"stuck", fmt.Sprintf("a backup has been in %s since %s", phase, when(since, now())), "skip it (Time Machine's menu › Skip This Backup) and let the next one start"})
					}
				}
			}
			if len(problems) > 0 {
				return problem(FeatureTimeMachine, summary, problems...)
			}
			if last.IsZero() {
				return check.Result{State: check.OK, Summary: summary, Glance: "Time Machine's " + summary}
			}
			return check.Result{State: check.OK, Summary: summary, Glance: "Time Machine " + when(last, now())}
		},
	}
}

// timeMachineDisk is the to-do of a Mac whose Time Machine has no backup
// disk.
var timeMachineDisk = check.ToDo{
	Title: "Add a Time Machine disk",
	Why:   "Time Machine has nowhere to back up to.",
	Steps: []string{"Plug in your backup disk", "System Settings › General › Time Machine › Add Backup Disk, and choose it"},
	Watch: "Time Machine has a disk",
	Group: groupBackups,
}

// timeMachineNoDisk is Time Machine's check on a Mac with no backup disk.
func timeMachineNoDisk() check.Result {
	return yours(FeatureTimeMachine, "not set up", "none", "Plug in your backup disk, then System Settings › General › Time Machine › Add Backup Disk", timeMachineDisk)
}

// arqStats is what arqc stats says, as far as the check needs.
type arqStats struct {
	BackupPlans []arqPlan `json:"backupPlans"`
}

// arqPlan is a backup plan, as arqc stats says.
type arqPlan struct {
	Name         string `json:"name"`
	LastBackedUp string `json:"lastBackedUp"`
	Schedule     struct {
		Type string `json:"type"`
	} `json:"schedule"`
}

// arqManual is the schedule of a plan Arq never runs by itself.
const arqManual = "manual"

// arqUnlicensed is what arqc says, failing, till Arq's licence agreement
// is accepted, as it is on first opening Arq: on a Mac it's just been
// installed on.
const arqUnlicensed = "acceptLicenseAgreement"

// Arq checks the backup plans Arq runs on its own schedules: one at least,
// each backed up within 26 hours. A plan with no schedule ("Manual") isn't
// expected to run, so it's left out, as a plan kept for its history is.
func Arq(run runner.Runner, now func() time.Time) engine.Step {
	return engine.Step{
		Name: FeatureArq, Title: "Arq", Area: AreaBackups,
		Check: func(ctx context.Context) check.Result {
			stats, unread, ok := arqStatus(ctx, run)
			if !ok {
				return unread
			}
			var problems [][3]string
			var good, times []string
			plans := 0
			for _, p := range stats.BackupPlans {
				if t := strings.ToLower(p.Schedule.Type); t == "" || t == arqManual {
					continue
				}
				plans++
				id := slug(p.Name)
				last, err := time.Parse(time.RFC3339, p.LastBackedUp)
				switch {
				case p.LastBackedUp == "" || err != nil:
					problems = append(problems, [3]string{"never:" + id, p.Name + ": no completed backup yet", "check Arq's activity, and the plan's schedule"})
				case now().Sub(last) > arqEvery:
					problems = append(problems, [3]string{"stale:" + id, p.Name + ": last backup " + ago(last, now()), "check Arq's activity, and the plan's schedule"})
				default:
					good = append(good, p.Name+" "+when(last, now()))
					times = append(times, when(last, now()))
				}
			}
			if plans == 0 {
				return problem(FeatureArq, "no plan scheduled", [3]string{"no-plan", "no backup plan runs on a schedule, so nothing is backed up offsite by itself", "in Arq, give the plan a schedule (its Schedule tab: daily)"})
			}
			slices.Sort(good)
			slices.Sort(times)
			// One plan is the step's alone, so needs no name; several are
			// named.
			summary := "last backup " + strings.Join(good, ", ")
			switch {
			case len(good) == 0:
				summary = "no recent backup"
			case plans == 1:
				summary = "last backup " + times[0]
			}
			if len(problems) > 0 {
				return problem(FeatureArq, summary, problems...)
			}
			return check.Result{State: check.OK, Summary: summary, Glance: "Arq " + strings.Join(times, ", ")}
		},
	}
}

// arqStatus is Arq's plans, as arqc stats says: ok is false, with the
// check's result, when there are none to look at. Arq not installed; never
// set up, its licence agreement not yet accepted or no plan made, as Time
// Machine without a backup disk isn't; or its status unreadable.
func arqStatus(ctx context.Context, run runner.Runner) (stats arqStats, unread check.Result, ok bool) {
	res, err := run.Run(ctx, runner.Command{Name: Arqc, Args: []string{"stats"}})
	switch {
	case errors.Is(err, runner.ErrNotFound):
		return stats, problem(FeatureArq, "not installed", [3]string{"missing", "Arq isn't installed", "install it: kit cask add arq"}), false
	case err != nil && strings.Contains(string(res.Stderr), arqUnlicensed):
		return stats, arqNotSetUp(), false
	case err == nil:
		err = json.Unmarshal(res.Stdout, &stats)
	}
	switch {
	case err != nil:
		return stats, problem(FeatureArq, "status unreadable", [3]string{"unreadable", "couldn't read Arq's status", "open Arq: is its agent running? If it complains, restart the Mac"}), false
	case len(stats.BackupPlans) == 0:
		return stats, arqNotSetUp(), false
	}
	return stats, check.Result{}, true
}

// arqPlanToDo is the to-do of a Mac with Arq installed and never set up.
var arqPlanToDo = check.ToDo{
	Title: "Set up Arq's backup plan",
	Why:   "Arq is in, but has nothing to back up to yet.",
	Steps: []string{"Open Arq", "Sign in to your Arq account", "Adopt this Mac's plan, and let it run"},
	Watch: "Arq has a plan",
	Group: groupBackups,
}

// arqNotSetUp is Arq's check on a Mac it's never been set up on.
func arqNotSetUp() check.Result {
	return yours(FeatureArq, "not set up", "none", "Open Arq, sign in to your Arq account, and adopt this Mac's plan", arqPlanToDo)
}

// slug is a name as part of an id: lowercase letters and digits, dashes
// between words.
func slug(name string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}), "-")
}
