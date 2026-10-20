// Package timemachine is Time Machine as a backup tool's driver: its
// backups checked through its settings and tmutil, and [backup exclusions]
// kept out of them as its own exclusions, which a tool following its list,
// as Arq does, leaves out too.
package timemachine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/backup/tool"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// ID names Time Machine in [backups].
const ID = "time-machine"

// Prefs is Time Machine's settings, as defaults names the domain: its
// backup disks, and its fixed-path exclusions (SkipPaths). Read through
// macOS's settings service, which has a change the moment it's made, where
// the file on disk lags behind.
const Prefs = "/Library/Preferences/com.apple.TimeMachine"

// The thresholds, as bin/health had them: a backup at least every 3 hours,
// and none stuck in one phase longer.
const (
	every = 3 * time.Hour
	stuck = 3 * time.Hour
)

var (
	mountPoint   = regexp.MustCompile(`(?m)^Mount Point\s*:`)
	stateChanged = regexp.MustCompile(`DateOfStateChange = "([^"]+)"`)
	backupPhase  = regexp.MustCompile(`BackupPhase = (\w+);`)
)

// Driver is Time Machine.
type Driver struct{}

func (Driver) About() tool.About {
	return tool.About{
		ID: ID, Name: "Time Machine",
		SetUp: tool.SetUp{
			Title: "Add a Time Machine disk",
			Why:   "Time Machine has nowhere to back up to.",
			Steps: []string{"Plug in your backup disk", "System Settings › General › Time Machine › Add Backup Disk, and choose it"},
			Watch: "Time Machine has a disk",
			Do:    "Plug in your backup disk, then System Settings › General › Time Machine › Add Backup Disk",
		},
	}
}

// Look checks a backup disk is set up, automatic backups are on, the last
// backup is within 3 hours, and none is stuck; a disk just added, its first
// backup to come, is fine for now.
func (Driver) Look(ctx context.Context, run runner.Runner, now time.Time) (tool.Status, error) {
	prefs, err := settings(ctx, run)
	if _, exited := errors.AsType[*runner.ExitError](err); exited {
		return tool.Status{NotSetUp: true}, nil
	}
	if err != nil {
		return tool.Status{}, err
	}
	destinations, _ := prefs["Destinations"].([]any)
	if len(destinations) == 0 {
		return tool.Status{NotSetUp: true}, nil
	}
	var st tool.Status
	if !plist.Equal(prefs["AutoBackup"], true) {
		st.Problems = append(st.Problems, tool.Problem{Key: "off", What: "automatic backups are off", Do: "System Settings › General › Time Machine › Options: back up every hour"})
	}
	last, tried := lastBackup(destinations)
	st.Backups = []tool.Backup{{Last: last}}
	status, err := output(ctx, run, "tmutil", "status")
	st.Running = err == nil && strings.Contains(status, "Running = 1")
	// away says the backup disk isn't connected, when tmutil says so.
	away := func() string {
		if info, err := output(ctx, run, "tmutil", "destinationinfo"); err == nil && !mountPoint.MatchString(info) {
			return " (the backup disk isn't connected)"
		}
		return ""
	}
	switch {
	// A disk just added is fine for now: its first backup is under way, or,
	// none tried yet and the disk there, soon.
	case last.IsZero() && st.Running:
	case last.IsZero():
		if where := away(); tried || where != "" {
			st.Problems = append(st.Problems, tool.Problem{Key: "never", What: "no completed backup yet" + where, Do: "connect the backup disk and back up now"})
		} else {
			st.Summary = "first backup soon"
		}
	case now.Sub(last) > every:
		st.Problems = append(st.Problems, tool.Problem{Key: "stale", What: "last backup " + check.Ago(last, now) + away(), Do: "connect the backup disk; if it is connected, check System Settings › General › Time Machine"})
	}
	if st.Running {
		if p, ok := stuckIn(status, now); ok {
			st.Problems = append(st.Problems, p)
		}
	}
	return st, nil
}

// lastBackup is when the last of destinations' backups finished, zero for
// none, and whether a backup to any was tried.
func lastBackup(destinations []any) (last time.Time, tried bool) {
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
	return last, tried
}

// stuckIn is the problem of a backup in one phase for over 3 hours, as
// tmutil's status says, and whether there's one.
func stuckIn(status string, now time.Time) (tool.Problem, bool) {
	m := stateChanged.FindStringSubmatch(status)
	if m == nil {
		return tool.Problem{}, false
	}
	since, err := time.Parse("2006-01-02 15:04:05 -0700", m[1])
	if err != nil || now.Sub(since) <= stuck {
		return tool.Problem{}, false
	}
	phase := "one phase"
	if p := backupPhase.FindStringSubmatch(status); p != nil {
		phase = p[1]
	}
	return tool.Problem{Key: "stuck", What: fmt.Sprintf("a backup has been in %s since %s", phase, check.When(since, now)), Do: "skip it (Time Machine's menu › Skip This Backup) and let the next one start"}, true
}

// settings are Time Machine's settings: a runner.ExitError when it has
// none, never set up (absent), or they couldn't be read.
func settings(ctx context.Context, run runner.Runner) (map[string]any, error) {
	out, err := output(ctx, run, "defaults", "export", Prefs, "-")
	if err != nil {
		return nil, err
	}
	v, err := plist.Read(out)
	prefs, _ := v.(map[string]any)
	if err != nil || prefs == nil {
		return nil, errors.New("couldn't read Time Machine's settings")
	}
	return prefs, nil
}

// absent reports whether err is defaults saying Time Machine's settings
// domain doesn't exist, as on a Mac whose Time Machine was never set up.
func absent(err error) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && strings.Contains(exit.Stderr, "does not exist")
}

// output runs a command, returning what it printed.
func output(ctx context.Context, run runner.Runner, name string, args ...string) (string, error) {
	res, err := run.Run(ctx, runner.Command{Name: name, Args: args})
	return string(res.Stdout), err
}

// Excluder keeps the exclusions as Time Machine's own.
func (Driver) Excluder() tool.Excluder { return exclusions{} }

// exclusions are Time Machine's exclusions, through tmutil: fixed-path
// ones, which hold before their folder exists, need root; one where a
// folder is now (sticky) needs no password.
type exclusions struct{}

func (exclusions) Program() string { return "tmutil" }

// Excluded are Time Machine's fixed-path exclusions, and those of here
// excluded where they are. A Mac whose Time Machine has never been set up
// has no settings, so none: tmutil keeps exclusions without a backup disk,
// as a Mac using Arq alone needs.
func (exclusions) Excluded(ctx context.Context, run runner.Runner, here []string) ([]string, error) {
	prefs, err := settings(ctx, run)
	if absent(err) {
		prefs, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	skip, _ := prefs["SkipPaths"].([]any)
	var out []string
	fixed := map[string]bool{}
	for _, p := range skip {
		if path, ok := p.(string); ok {
			out = append(out, path)
			fixed[path] = true
		}
	}
	var sticky []string
	for _, path := range here {
		if !fixed[path] {
			sticky = append(sticky, path)
		}
	}
	if len(sticky) == 0 {
		return out, nil
	}
	res, err := output(ctx, run, "tmutil", append([]string{"isexcluded"}, sticky...)...)
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(res) {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "[Excluded]"); ok {
			out = append(out, strings.TrimSpace(path))
		}
	}
	return out, nil
}

// Exclude excludes path: fixed, through sudo, which needs Full Disk Access
// for the terminal kit runs in; else where it is.
func (exclusions) Exclude(ctx context.Context, run runner.Runner, path string, fixed bool) error {
	if fixed {
		return sudoTmutil(ctx, run, "addexclusion", path)
	}
	_, err := run.Run(ctx, runner.Command{Name: "tmutil", Args: []string{"addexclusion", path}})
	return err
}

// Include stops excluding path's fixed-path exclusion.
func (exclusions) Include(ctx context.Context, run runner.Runner, path string) error {
	return sudoTmutil(ctx, run, "removeexclusion", path)
}

// NeedsAdmin is whether the exclusion is fixed: tmutil needs root for
// those.
func (exclusions) NeedsAdmin(fixed bool) bool { return fixed }

func sudoTmutil(ctx context.Context, run runner.Runner, verb, path string) error {
	_, err := run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "tmutil", verb, "-p", path}})
	return err
}

var _ tool.Driver = Driver{}
