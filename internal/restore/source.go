package restore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/runner"
)

// Opener is where a restore copies from, to open as it starts: the old Mac
// over SSH, its Time Machine disk, or the folders Arq restored into.
type Opener interface {
	Open(ctx context.Context) (*Opened, error)
}

// Opened is a source opened to copy from, till it's closed.
type Opened struct {
	// Home is the old Mac's home folder there, absolute.
	Home string
	// Folders are where the listed paths are there, by path, when they
	// aren't in Home, as in the folders Arq restored each into; Lacks, the
	// paths it hasn't, as the person says of Arq's backup, whatever's in
	// their folders.
	Folders map[string]string
	Lacks   []string
	// Host, Shell and Rsync reach the old Mac over SSH, as Source has them:
	// Host "" for a source on this Mac.
	Host, Shell, Rsync string
	// Look says what's at each of the listed paths on the old Mac, and what
	// in each its backups leave out, by path, in one go, as each look over
	// SSH costs a connection: nil for a source on this Mac, where Stat
	// looks.
	Look func(ctx context.Context, paths []string) (map[string]There, error)
	// Live is whether what's there can change as it's copied, as on the old
	// Mac in use: the second pass copies again what changed, and counts it,
	// where a backup's must find nothing.
	Live bool
	// Frees is whether a path's copy there goes once the path's restored,
	// freeing its room on this Mac's disk, as Arq's holding folders do.
	Frees bool
	// Origin is what the record keeps of it; Replaces, the source asked
	// for, gone, that it stands in for, as a later backup of Time Machine's
	// does for one thinned away mid-restore: zero for none.
	Origin, Replaces Origin
	// Close closes it, as a snapshot's unmounted: even with ctx done, so
	// nothing's left open when kit's stopped.
	Close func(ctx context.Context) error
}

// Kind is what's at a listed path at a source.
type Kind int

const (
	Missing Kind = iota
	File
	Folder
)

// There is what's at a listed path at a source, and what in it the source
// leaves out, each as a line's --skip has it: relative to the path, escaped
// where rsync would take it as a pattern.
type There struct {
	Kind  Kind
	Skips []string
}

// Source is path, a line's, from ~, there, as rsync copies from it.
func (o *Opened) Source(path string) Source {
	if at, ok := o.Folders[path]; ok {
		return Source{Path: at}
	}
	return Source{Path: filepath.Join(o.Home, strings.TrimPrefix(path, "~/")), Host: o.Host, Shell: o.Shell, Rsync: o.Rsync}
}

// Stat says what's at path, a line's, from ~, there, as os.Lstat does: a
// folder, a file (a link, as rsync copies one, is one), or nothing, as for
// one it Lacks. A holding folder of Arq's not holding path is an error
// saying what it holds, never nothing there (heldIn). A source on another
// Mac is Look's to look at.
func (o *Opened) Stat(path string) (There, error) {
	if o.Host != "" {
		return There{}, errors.New("a path on another Mac is looked at through Look")
	}
	if slices.Contains(o.Lacks, path) {
		return There{Kind: Missing}, nil
	}
	at := o.Source(path).Path
	if _, ok := o.Folders[path]; ok {
		if err := heldIn(filepath.Dir(at), path); err != nil {
			return There{}, err
		}
	}
	info, err := os.Lstat(at)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return There{Kind: Missing}, nil
	case err != nil:
		return There{}, err
	case info.IsDir():
		return There{Kind: Folder}, nil
	}
	return There{Kind: File}, nil
}

// Says is o as kit names it: Time Machine's backup of 8 Nov 2026 14:19, the
// folder Arq restored into, or the old Mac.
func (o Origin) Says() string {
	switch o.Kind {
	case FromTimeMachine:
		if t, ok := timemachine.Taken(o.Detail); ok {
			return "Time Machine's backup of " + t.Format(dayLayout)
		}
		return strings.TrimSpace("Time Machine's backup " + o.Detail)
	case FromArq:
		if o.Detail == "" {
			return "Arq's restore"
		}
		return "Arq's restore in " + o.Detail
	}
	if o.Detail == "" {
		return "the old Mac"
	}
	return o.Detail
}

// dayLayout is when a backup was taken, as kit says it, years apart as
// backups can be.
const dayLayout = "2 Jan 2006 15:04"

// Sources are the restore's sources, opened through Run, for the user
// called User, Time Machine's backups mounted at MountPoint; the old Mac
// as Mac opens the one called from.
type Sources struct {
	Run              runner.Runner
	User, MountPoint string
	Mac              func(from string) Opener
}

// For is the source o names, to open: Time Machine's backups, as
// TimeMachine has them, o's snapshot the one the restore began from, at
// picking one, chosen saying whether kit apply's flags named them; or the
// old Mac, as Mac has it. Arq's restore is by hand, through the restore's
// card, never opened here.
func (s Sources) For(o Origin, at time.Time, chosen bool) Opener {
	switch o.Kind {
	case FromTimeMachine:
		return TimeMachine{Run: s.Run, User: s.User, MountPoint: s.MountPoint, At: at, Snapshot: o.Detail, Chosen: chosen}
	case FromArq:
		return unopened("Arq's restore is by hand, through the restore's card")
	}
	return s.Mac(o.Detail)
}

// unopened is a source that can't be opened, saying why as it's tried.
type unopened string

func (u unopened) Open(context.Context) (*Opened, error) { return nil, errors.New(string(u)) }

// Disk is a Time Machine disk attached holding the person's backups, by its
// volume's name, to open: its latest backup.
type Disk struct {
	Name string
	Opener
}

// Disks are the Time Machine disks attached for the restore to fall back
// on: those holding the person's backups, each to open, in the order
// diskutil lists them; and those locked, by name, which can't be read till
// they're unlocked.
type Disks struct {
	Found  []Disk
	Locked []string
}

// Disks finds the Time Machine disks attached for the restore to fall back
// on. Two of one name are told apart by their devices.
func (s Sources) Disks(ctx context.Context) (Disks, error) {
	found, passed, err := timemachine.Look(ctx, s.Run, s.User, s.MountPoint)
	if err != nil {
		return Disks{}, err
	}
	var d Disks
	for _, c := range found {
		name := c.Volume
		if slices.ContainsFunc(found, func(o timemachine.Candidate) bool { return o.Volume == c.Volume && o.Device != c.Device }) {
			name = fmt.Sprintf("%s (%s)", c.Volume, c.Device)
		}
		d.Found = append(d.Found, Disk{Name: name, Opener: TimeMachine{Run: s.Run, User: s.User, MountPoint: s.MountPoint, Device: c.Device}})
	}
	for _, p := range passed {
		if p.Locked {
			d.Locked = append(d.Locked, p.Volume)
		}
	}
	return d, nil
}

// TimeMachine is the old Mac's backups on a Time Machine disk attached to
// this one, the home folder of the user called User in them, mounted
// read-only at MountPoint, an empty folder: the latest at or before At,
// else the snapshot called Snapshot, the one a restore began from, else
// the latest. Snapshot gone from the disk is an error saying how to carry
// on from another; with Chosen, as when kit apply's flags name Time
// Machine's backups again, the latest at or before At stands in for it.
// Device, when the person chose one of several disks, is the one to open.
type TimeMachine struct {
	Run              runner.Runner
	User, MountPoint string
	At               time.Time
	Snapshot         string
	Chosen           bool
	Device           string
}

// Open mounts the backup to copy from.
func (t TimeMachine) Open(ctx context.Context) (*Opened, error) {
	found, err := timemachine.Find(ctx, t.Run, t.User, t.MountPoint)
	if err != nil {
		return nil, err
	}
	c, err := t.disk(found)
	if err != nil {
		return nil, err
	}
	p, gone, err := t.pick(c)
	if err != nil {
		return nil, err
	}
	s, err := timemachine.Open(ctx, t.Run, c, p, t.User, t.MountPoint)
	if err != nil {
		return nil, err
	}
	o := &Opened{Home: s.Home, Origin: Origin{Kind: FromTimeMachine, Detail: p.Name}, Close: s.Close}
	if gone {
		o.Replaces = Origin{Kind: FromTimeMachine, Detail: t.Snapshot}
	}
	return o, nil
}

// disk is the one of found, the disks holding backups of t's User, to open:
// t's Device; else the one alone; else, a restore resuming, the one alone
// holding its Snapshot. More than one is an error naming them: which is
// the old Mac's can't be told.
func (t TimeMachine) disk(found []timemachine.Candidate) (timemachine.Candidate, error) {
	switch {
	case t.Device != "":
		if i := slices.IndexFunc(found, func(c timemachine.Candidate) bool { return c.Device == t.Device }); i >= 0 {
			return found[i], nil
		}
		return timemachine.Candidate{}, fmt.Errorf("the Time Machine disk chosen, %s, no longer holds backups of /Users/%s: is it still attached?", t.Device, t.User)
	case len(found) == 1:
		return found[0], nil
	}
	holding := slices.DeleteFunc(slices.Clone(found), func(c timemachine.Candidate) bool {
		return !slices.ContainsFunc(c.Snapshots, func(p timemachine.Point) bool { return p.Name == t.Snapshot })
	})
	if t.Snapshot != "" && len(holding) == 1 {
		return holding[0], nil
	}
	disks := make([]string, len(found))
	for i, c := range found {
		disks[i] = fmt.Sprintf("%s (%s)", c.Volume, c.Device)
	}
	return timemachine.Candidate{}, fmt.Errorf("backups of /Users/%s are on more than one Time Machine disk, %s: attach the old Mac's alone", t.User, strings.Join(disks, " and "))
}

// pick is the one of c's snapshots to open, as t says, and whether it
// stands in for t's Snapshot, gone.
func (t TimeMachine) pick(c timemachine.Candidate) (timemachine.Point, bool, error) {
	i := slices.IndexFunc(c.Snapshots, func(p timemachine.Point) bool { return p.Name == t.Snapshot })
	switch {
	case t.Snapshot == "", !t.At.IsZero() && i >= 0:
		p, err := c.Pick(t.At)
		return p, false, err
	case i >= 0:
		return c.Snapshots[i], false, nil
	case t.Chosen:
		p, err := c.Pick(t.At)
		return p, true, err
	}
	return timemachine.Point{}, false, fmt.Errorf("%s, which the restore began from, isn't on %s any more: kit apply --restore-from %s carries on from the latest backup there, or the one --at picks",
		Origin{Kind: FromTimeMachine, Detail: t.Snapshot}.Says(), c.Volume, FromTimeMachine)
}

// The restore's own folders in kit's state, beside what it sets aside:
// where Time Machine's backups are mounted; what's copied of a file not
// finished, a folder each line's path; and the rules rsync reads, a file
// each.
const (
	mountFolder   = "restore/mount"
	partialFolder = "restore/partial"
	filterFolder  = "restore/filters"
)

// MountPoint is where, in kit's state, stateDir, Time Machine's backups
// are mounted to restore from.
func MountPoint(stateDir string) string {
	return filepath.Join(stateDir, filepath.FromSlash(mountFolder))
}

// Partial is the folder, in kit's state, stateDir, keeping what's been
// copied of l's files not finished (Copy's Partial).
func (l Line) Partial(stateDir string) string {
	return filepath.Join(stateDir, filepath.FromSlash(partialFolder), strings.TrimPrefix(l.Path, "~/"))
}

// FilterFile is the file, in kit's state, stateDir, holding the rules
// rsync reads restoring l (Copy's Filter).
func (l Line) FilterFile(stateDir string) string {
	return filepath.Join(stateDir, filepath.FromSlash(filterFolder), strings.TrimPrefix(l.Path, "~/"))
}
