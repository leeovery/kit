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
	"github.com/leeovery/kit/internal/runner"
)

// The features: the pieces with nothing to list, switched on for a Mac in
// its [features] section.
const (
	FeatureTimeMachine     = "time-machine"
	FeatureArq             = "arq"
	FeatureScratch         = "scratch"
	FeatureSettingsCapture = "settings-capture"
	FeatureOhMyZsh         = "oh-my-zsh"
)

// Features are the features kit knows.
var Features = []string{FeatureArq, FeatureOhMyZsh, FeatureScratch, FeatureSettingsCapture, FeatureTimeMachine}

// The backup checks' thresholds, as bin/health had them.
const (
	timeMachineEvery = 3 * time.Hour
	timeMachineStuck = 3 * time.Hour
	arqEvery         = 26 * time.Hour
)

// timeMachinePrefs is Time Machine's settings.
const timeMachinePrefs = "/Library/Preferences/com.apple.TimeMachine.plist"

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
// automatic backups on, the last backup within 3 hours, and none stuck.
func TimeMachine(run runner.Runner, now func() time.Time) engine.Step {
	return engine.Step{
		Name: FeatureTimeMachine, Title: "Time Machine", Area: AreaBackups,
		Check: func(ctx context.Context) check.Result {
			out, err := output(ctx, run, "plutil", "-convert", "xml1", "-o", "-", timeMachinePrefs)
			if _, exited := errors.AsType[*runner.ExitError](err); exited {
				return problem(FeatureTimeMachine, "not set up", [3]string{"none", "no backup disk is set up", "System Settings › General › Time Machine › Add Backup Disk"})
			}
			if err != nil {
				return failed(err)
			}
			v, err := readPlist(out)
			prefs, _ := v.(map[string]any)
			if err != nil || prefs == nil {
				return failed(errors.New("couldn't read Time Machine's settings"))
			}
			destinations, _ := prefs["Destinations"].([]any)
			if len(destinations) == 0 {
				return problem(FeatureTimeMachine, "not set up", [3]string{"none", "no backup disk is set up", "System Settings › General › Time Machine › Add Backup Disk"})
			}
			var problems [][3]string
			if auto := prefs["AutoBackup"]; auto != true && auto != 1.0 {
				problems = append(problems, [3]string{"off", "automatic backups are off", "System Settings › General › Time Machine › Options: back up every hour"})
			}
			var last time.Time
			for _, d := range destinations {
				dest, _ := d.(map[string]any)
				dates, _ := dest["SnapshotDates"].([]any)
				for _, x := range dates {
					if t, ok := x.(time.Time); ok && t.After(last) {
						last = t
					}
				}
			}
			summary := "no backup yet"
			switch {
			case last.IsZero():
				problems = append(problems, [3]string{"never", "no completed backup yet", "connect the backup disk and back up now"})
			default:
				summary = "last backup " + when(last, now())
				if now().Sub(last) > timeMachineEvery {
					where := ""
					if info, err := output(ctx, run, "tmutil", "destinationinfo"); err == nil && !mountPoint.MatchString(info) {
						where = " (the backup disk isn't connected)"
					}
					problems = append(problems, [3]string{"stale", "last backup " + ago(last, now()) + where, "connect the backup disk; if it is connected, check System Settings › General › Time Machine"})
				}
			}
			if status, err := output(ctx, run, "tmutil", "status"); err == nil && strings.Contains(status, "Running = 1") {
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
			return check.Result{State: check.OK, Summary: summary, Glance: "Time Machine " + when(last, now())}
		},
	}
}

// arqStats is what arqc stats says, as far as the check needs.
type arqStats struct {
	BackupPlans []struct {
		Name         string `json:"name"`
		LastBackedUp string `json:"lastBackedUp"`
		Schedule     struct {
			Type string `json:"type"`
		} `json:"schedule"`
	} `json:"backupPlans"`
}

// arqManual is the schedule of a plan Arq never runs by itself.
const arqManual = "manual"

// Arq checks the backup plans Arq runs on its own schedules: one at least,
// each backed up within 26 hours. A plan with no schedule ("Manual") isn't
// expected to run, so it's left out, as a plan kept for its history is.
func Arq(run runner.Runner, now func() time.Time) engine.Step {
	return engine.Step{
		Name: FeatureArq, Title: "Arq", Area: AreaBackups,
		Check: func(ctx context.Context) check.Result {
			out, err := output(ctx, run, Arqc, "stats")
			if errors.Is(err, runner.ErrNotFound) {
				return problem(FeatureArq, "not installed", [3]string{"missing", "Arq isn't installed", "install it: kit add cask arq"})
			}
			var stats arqStats
			if err == nil {
				err = json.Unmarshal([]byte(out), &stats)
			}
			if err != nil {
				return problem(FeatureArq, "status unreadable", [3]string{"unreadable", "couldn't read Arq's status", "open Arq: is its agent running? If it complains, restart the Mac"})
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
			summary := "last backup " + strings.Join(good, ", ")
			if len(good) == 0 {
				summary = "no recent backup"
			}
			if len(problems) > 0 {
				return problem(FeatureArq, summary, problems...)
			}
			slices.Sort(times)
			return check.Result{State: check.OK, Summary: summary, Glance: "Arq " + strings.Join(times, ", ")}
		},
	}
}

// slug is a name as part of an id: lowercase letters and digits, dashes
// between words.
func slug(name string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}), "-")
}
