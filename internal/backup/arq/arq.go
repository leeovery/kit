// Package arq is Arq as a backup tool's driver: the backup plans it runs on
// its own schedules, checked through arqc. It keeps no exclusions of its
// own: set to "Skip items excluded by Time Machine rules", it leaves out
// what Time Machine's list does, a folder excluded where it is (sticky) as
// a fixed-path one (tested on 6 Oct 2026).
package arq

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/backup/tool"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/runner"
)

// ID names Arq in [backups].
const ID = "arq"

// Arqc is Arq's command, inside its app.
const Arqc = "/Applications/Arq.app/Contents/Resources/arqc"

// every is how often a plan backs up at least, as bin/health had it.
const every = 26 * time.Hour

// manual is the schedule of a plan Arq never runs by itself.
const manual = "manual"

// unlicensed is what arqc says, failing, till Arq's licence agreement is
// accepted, as it is on first opening Arq: on a Mac it's just been
// installed on.
const unlicensed = "acceptLicenseAgreement"

// stats is what arqc stats says, as far as the check needs.
type stats struct {
	BackupPlans []plan `json:"backupPlans"`
}

// plan is a backup plan, as arqc stats says.
type plan struct {
	Name         string `json:"name"`
	LastBackedUp string `json:"lastBackedUp"`
	Schedule     struct {
		Type string `json:"type"`
	} `json:"schedule"`
}

// scheduled reports whether Arq runs the plan by itself.
func (p plan) scheduled() bool {
	t := strings.ToLower(p.Schedule.Type)
	return t != "" && t != manual
}

// Driver is Arq.
type Driver struct{}

func (Driver) About() tool.About {
	return tool.About{
		ID: ID, Name: "Arq",
		SetUp: tool.SetUp{
			Title: "Set up Arq's backup plan",
			Why:   "Arq is in, but has nothing to back up to yet.",
			Steps: []string{"Open Arq", "Sign in to your Arq account", "Adopt this Mac's plan, and let it run"},
			Watch: "Arq has a plan",
			Do:    "Open Arq, sign in to your Arq account, and adopt this Mac's plan",
		},
		Follows: timemachine.ID,
	}
}

// Look checks the plans Arq runs on its own schedules: one at least, each
// backed up within 26 hours. A plan with no schedule ("Manual") isn't
// expected to run, so it's left out, as a plan kept for its history is. Arq
// just installed, its licence agreement not yet accepted, or with no plan
// made, isn't set up, as Time Machine without a backup disk isn't.
func (Driver) Look(ctx context.Context, run runner.Runner, now time.Time) (tool.Status, error) {
	res, err := run.Run(ctx, runner.Command{Name: Arqc, Args: []string{"stats"}})
	var s stats
	switch {
	case errors.Is(err, runner.ErrNotFound):
		return tool.Status{Summary: "not installed", Problems: []tool.Problem{{Key: "missing", What: "Arq isn't installed", Do: "install it: kit cask add arq"}}}, nil
	case err != nil && strings.Contains(string(res.Stderr), unlicensed):
		return tool.Status{NotSetUp: true}, nil
	case err == nil:
		err = json.Unmarshal(res.Stdout, &s)
	}
	switch {
	case err != nil:
		return tool.Status{Summary: "status unreadable", Problems: []tool.Problem{{Key: "unreadable", What: "couldn't read Arq's status", Do: "open Arq: is its agent running? If it complains, restart the Mac"}}}, nil
	case len(s.BackupPlans) == 0:
		return tool.Status{NotSetUp: true}, nil
	}
	plans := slices.DeleteFunc(s.BackupPlans, func(p plan) bool { return !p.scheduled() })
	if len(plans) == 0 {
		return tool.Status{Summary: "no plan scheduled", Problems: []tool.Problem{{Key: "no-plan", What: "no backup plan runs on a schedule, so nothing is backed up offsite by itself", Do: "in Arq, give the plan a schedule (its Schedule tab: daily)"}}}, nil
	}
	var st tool.Status
	for _, p := range plans {
		id := slug(p.Name)
		last, err := time.Parse(time.RFC3339, p.LastBackedUp)
		switch {
		case p.LastBackedUp == "" || err != nil:
			st.Problems = append(st.Problems, tool.Problem{Key: "never:" + id, What: p.Name + ": no completed backup yet", Do: "check Arq's activity, and the plan's schedule"})
		case now.Sub(last) > every:
			st.Problems = append(st.Problems, tool.Problem{Key: "stale:" + id, What: p.Name + ": last backup " + check.Ago(last, now), Do: "check Arq's activity, and the plan's schedule"})
		default:
			// One plan is the tool's alone, so needs no name; several are
			// named.
			name := p.Name
			if len(plans) == 1 {
				name = ""
			}
			st.Backups = append(st.Backups, tool.Backup{Plan: name, Last: last})
		}
	}
	if len(st.Backups) == 0 {
		st.Summary = "no recent backup"
	}
	return st, nil
}

// Excluder is none: Arq leaves out what Time Machine's list does
// (About.Follows).
func (Driver) Excluder() tool.Excluder { return nil }

// slug is a name as part of a key: lowercase letters and digits, dashes
// between words.
func slug(name string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}), "-")
}

var _ tool.Driver = Driver{}
