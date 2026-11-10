package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/kind/composer"
	"github.com/leeovery/kit/internal/kind/ghext"
	"github.com/leeovery/kit/internal/kind/gotool"
	"github.com/leeovery/kit/internal/kind/npm"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/reinstall"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/restore/oldmac"
	"github.com/leeovery/kit/internal/steps"
)

// restoreFlags are kit apply's flags naming the data restore's source:
// --restore-from, and --at, picking the backup.
type restoreFlags struct {
	from restoreSource
	at   restoreAt
}

// chosen is the source the flags name, checked: --at only with a backup's.
func (f restoreFlags) chosen() (steps.RestoreFrom, error) {
	if !f.at.at.IsZero() && !slices.Contains(backupSources, string(f.from)) {
		return steps.RestoreFrom{}, usageError{fmt.Errorf("--at picks a backup: give it with --restore-from %s", strings.Join(backupSources, " or "))}
	}
	return steps.RestoreFrom{Kind: string(f.from), At: f.at.at}, nil
}

// restoreSources are the sources --restore-from can name: the Mac this one
// started from, and the backups (backupSources).
var (
	backupSources  = []string{restore.FromTimeMachine, restore.FromArq}
	restoreSources = slices.Concat([]string{restore.FromMac}, backupSources)
)

// restoreSource is --restore-from's value.
type restoreSource string

func (s *restoreSource) String() string { return string(*s) }

func (s *restoreSource) Type() string { return strings.Join(restoreSources, "|") }

func (s *restoreSource) Set(v string) error {
	if !slices.Contains(restoreSources, v) {
		last := len(restoreSources) - 1
		return fmt.Errorf("%q isn't a source: %s or %s", v, strings.Join(restoreSources[:last], ", "), restoreSources[last])
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
// flags'; or a path's restore part-way, which carries on, from where it
// began or the source the flags name; or, for kit agent order, a new
// Mac's run started from this one, whose lines it carries. Lines that
// don't read make a step whose check says why; flags naming a source for
// no lines, or the old Mac for a Mac started from none, are an error.
// Copying from the old Mac over SSH, with the key kit's secrets write, it
// comes after them.
func (a *app) restoreStep(r *run, dirs config.Dirs, prefsOn bool) (*engine.Step, error) {
	list, err := r.cfg.List(config.RestoreKind, r.machine)
	if err != nil {
		return nil, err
	}
	flag, err := a.restoring.chosen()
	switch {
	case err != nil:
		return nil, err
	case len(list.Entries) == 0 && flag.Kind != "":
		return nil, usageError{fmt.Errorf("--restore-from restores the paths [restore] lists, and none are listed for %s", r.machine)}
	case len(list.Entries) == 0:
		return nil, nil
	}
	from := r.cfg.Macs[r.machine].From
	if flag.Kind == restore.FromMac && from == "" {
		return nil, usageError{fmt.Errorf("--restore-from mac copies from the Mac %s started from (from, in kit.toml), and it names none", r.machine)}
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
	user := filepath.Base(r.homeDir)
	sources := restore.Sources{Run: r.run, User: user, MountPoint: restore.MountPoint(dirs.State), Mac: func(from string) restore.Opener {
		return &oldmac.Source{
			Finder: oldmac.Finder{Run: r.run, StateDir: dirs.State, HostKey: r.cfg.Macs[from].HostKey}, From: from, User: user, Home: r.homeDir,
			HostKey: func(ctx context.Context) (string, error) { return fetchedHostKey(ctx, r.repo, from) },
		}
	}}
	folders := a.appFolders(r.homeDir)
	restoring := steps.Restoring{
		Lines: lines,
		Owned: func(ctx context.Context) (restore.Owned, error) { return restore.ReadOwned(ctx, mac) },
		Home:  r.homeDir, StateDir: dirs.State, Began: began, From: from, Flag: flag,
		Sources: sources.For, Disks: sources.Disks, Raise: r.raise, Run: r.run,
		ArqAwait: []engine.Wait{{Thing: "Arq", Ready: func(context.Context) bool { return folders.Has(arq.Bundle) }}},
		Rsync:    restore.NewRsync(r.run), Space: boot.Space, Now: a.Now,
	}
	overSSH, err := restoring.OverSSH()
	if err != nil {
		return nil, err
	}
	step := steps.Restore(restoring)
	if (overSSH || a.newMacRun && flag.Kind == "") && slices.Contains(r.kinds, secret.Descriptor.Name) {
		step.After = append(step.After, secret.Descriptor.Name)
	}
	return &step, nil
}

// fetchedHostKey is the host key kit-config's remote records for the Mac
// called from, fetched now, as kit apply on that Mac may have recorded it
// since this run began: nothing merged here.
func fetchedHostKey(ctx context.Context, repo gitrepo.Repo, from string) (string, error) {
	data, err := repo.Fetched(ctx, config.File)
	if err != nil {
		return "", err
	}
	return config.HostKeyIn(data, from)
}

// reinstallStep is the step reinstalling the dependencies of the projects
// the restore brought back, when the Mac has one (steps.ReinstallOn),
// restoring saying the data restore's in the run, as on a new Mac's first,
// before anything's recorded; for kit agent order, wherever the restore
// is. Failed installs run again when kit apply names it. It comes after
// the restore, and the kinds that install the tools it runs and the
// secrets private packages need (reinstallAfter).
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
	for _, name := range r.kinds {
		if slices.Contains(reinstallAfter, name) {
			step.After = append(step.After, name)
		}
	}
	return &step, nil
}

// reinstallAfter are the kinds the reinstall comes after, those the run
// has: the kinds that install the tools projects use, and the secrets,
// which write what private packages need. Never the App Store's, whose
// sign-in is by hand, nor login items.
var reinstallAfter = []string{
	brew.FormulaeDescriptor.Name, brew.CasksDescriptor.Name, npm.Descriptor.Name, composer.Descriptor.Name,
	gotool.Descriptor.Name, ghext.Descriptor.Name, secret.Descriptor.Name,
}
