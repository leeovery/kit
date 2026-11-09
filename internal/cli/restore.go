package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/reinstall"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/steps"
)

// restoreFlags are kit apply's flags naming the data restore's source:
// --restore-from, and --at, picking Time Machine's backup.
type restoreFlags struct {
	from restoreSource
	at   restoreAt
}

// chosen is the source the flags name, checked: --at only with Time
// Machine's backups.
func (f restoreFlags) chosen() (steps.RestoreFrom, error) {
	if !f.at.at.IsZero() && f.from != restore.FromTimeMachine {
		return steps.RestoreFrom{}, usageError{fmt.Errorf("--at picks a Time Machine backup: give it with --restore-from %s", restore.FromTimeMachine)}
	}
	return steps.RestoreFrom{Kind: string(f.from), At: f.at.at}, nil
}

// restoreSources are the sources --restore-from can name.
var restoreSources = []string{restore.FromTimeMachine, restore.FromArq}

// restoreSource is --restore-from's value.
type restoreSource string

func (s *restoreSource) String() string { return string(*s) }

func (s *restoreSource) Type() string { return strings.Join(restoreSources, "|") }

func (s *restoreSource) Set(v string) error {
	if !slices.Contains(restoreSources, v) {
		return fmt.Errorf("%q isn't a source: %s", v, strings.Join(restoreSources, " or "))
	}
	*s = restoreSource(v)
	return nil
}

// restoreAt is --at's value: a time, read in the Mac's own time zone, as
// now's location; a date or a time to the minute means the end of it, so a
// backup taken in it counts.
type restoreAt struct {
	now  func() time.Time
	at   time.Time
	text string
}

// atForms are the forms --at reads, each with how long a time in it spans.
var atForms = []struct {
	layout string
	span   func(time.Time) time.Time
}{
	{"2006-01-02", func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }},
	{"2006-01-02 15:04", func(t time.Time) time.Time { return t.Add(time.Minute) }},
	{"2006-01-02 15:04:05", func(t time.Time) time.Time { return t.Add(time.Second) }},
}

func (a *restoreAt) String() string { return a.text }

func (a *restoreAt) Type() string { return "date" }

func (a *restoreAt) Set(v string) error {
	for _, form := range atForms {
		if t, err := time.ParseInLocation(form.layout, strings.TrimSpace(v), a.now().Location()); err == nil {
			a.at, a.text = form.span(t).Add(-time.Nanosecond), v
			return nil
		}
	}
	return fmt.Errorf("%q isn't a date, as 2026-11-08, or a time, as \"2026-11-08 14:30\"", v)
}

// restoreStep is the data restore's step, when the Mac has one: [restore]
// lines, and a source for them, the Mac it started from or kit apply's
// flags'; or a path's restore part-way, which carries on from where it
// began whatever the flags; or, for kit agent order, a new Mac's run
// started from this one, whose lines it carries. Lines that don't read
// make a step whose check says why.
func (a *app) restoreStep(r *run, dirs config.Dirs, prefsOn bool) (*engine.Step, error) {
	list, err := r.cfg.List(config.RestoreKind, r.machine)
	if err != nil || len(list.Entries) == 0 {
		return nil, err
	}
	from := r.cfg.Macs[r.machine].From
	flag, err := a.restoring.chosen()
	if err != nil {
		return nil, err
	}
	lines, unread := restore.Lines(list)
	rec, err := restore.LoadRecord(dirs.State)
	if err != nil {
		return nil, err
	}
	if from == "" && flag.Kind == "" && !a.newMacRun && !rec.PartWay(lines) {
		return nil, nil
	}
	if unread != nil {
		step := steps.Restore(steps.Restoring{})
		broken(&step, unread.Error())
		return &step, nil
	}
	began, err := boot.Began(dirs.State)
	if err != nil {
		return nil, err
	}
	homed := make([]kind.Kind, len(r.allKinds))
	for i, name := range r.allKinds {
		homed[i] = r.kindsByName[name]
	}
	mac := restore.Mac{Config: r.cfg, Name: r.machine, Home: r.homeDir, Dirs: dirs, Linked: r.files, Prefs: prefsOn, Kinds: homed}
	sources := restore.Sources{Run: r.run, User: filepath.Base(r.homeDir), MountPoint: restore.MountPoint(dirs.State)}
	step := steps.Restore(steps.Restoring{
		Lines: lines,
		Owned: func(ctx context.Context) (restore.Owned, error) { return restore.ReadOwned(ctx, mac) },
		Home:  r.homeDir, StateDir: dirs.State, Began: began, From: from, Flag: flag,
		Sources: sources.For, Rsync: restore.NewRsync(r.run), Space: boot.Space, Now: a.Now,
	})
	return &step, nil
}

// reinstallStep is the step reinstalling the dependencies of the projects
// the restore brought back, when the Mac has one (steps.ReinstallOn),
// restoring saying the data restore's in the run, as on a new Mac's first,
// before anything's recorded; for kit agent order, wherever the restore
// is. Failed installs run again when kit apply names it. It comes after
// the restore, and the secrets, which write what private packages need.
func (a *app) reinstallStep(r *run, dirs config.Dirs, restoring bool) (*engine.Step, error) {
	on, err := steps.ReinstallOn(dirs.State, restoring)
	if err != nil {
		return nil, err
	}
	if !on && (!restoring || !a.newMacRun) {
		return nil, nil
	}
	step := steps.Reinstall(reinstall.Reinstaller{Run: r.run, Home: r.homeDir, StateDir: dirs.State, Now: a.Now, Retry: slices.Contains(a.named, steps.ReinstallName)})
	if restoring {
		step.After = append(step.After, steps.RestoreName)
	}
	if slices.Contains(r.kinds, secret.Descriptor.Name) {
		step.After = append(step.After, secret.Descriptor.Name)
	}
	return &step, nil
}
