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
// over SSH, its Time Machine disk, or the folder Arq restored into.
type Opener interface {
	Open(ctx context.Context) (*Opened, error)
}

// Opened is a source opened to copy from, till it's closed.
type Opened struct {
	// Home is the old Mac's home folder there, absolute.
	Home string
	// Host, Shell and Rsync reach the old Mac over SSH, as Source has them:
	// Host "" for a source on this Mac.
	Host, Shell, Rsync string
	// Live is whether what's there can change as it's copied, as on the old
	// Mac in use: the second pass copies again what changed, and counts it,
	// where a backup's must find nothing.
	Live bool
	// Origin is what the record keeps of it.
	Origin Origin
	// Close closes it, as a snapshot's unmounted: even with ctx done, so
	// nothing's left open when kit's stopped.
	Close func(ctx context.Context) error
}

// Source is path, a line's, from ~, there, as rsync copies from it.
func (o *Opened) Source(path string) Source {
	return Source{Path: filepath.Join(o.Home, strings.TrimPrefix(path, "~/")), Host: o.Host, Shell: o.Shell, Rsync: o.Rsync}
}

// Stat says what's at path, a line's, from ~, there, as os.Lstat does: a
// folder, a file, or nothing (fs.ErrNotExist).
func (o *Opened) Stat(path string) (fs.FileInfo, error) {
	if o.Host != "" {
		return nil, errors.New("looking at a path on another Mac isn't built yet")
	}
	return os.Lstat(o.Source(path).Path)
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
// called User, Time Machine's backups mounted at MountPoint.
type Sources struct {
	Run              runner.Runner
	User, MountPoint string
}

// For is the source o names, to open: Time Machine's backups, the latest
// taken at or before at, else, at zero, the snapshot o names, as a restore
// resumed has it, else the latest. Arq's restore and the old Mac over SSH
// aren't built yet: opening either says so.
func (s Sources) For(o Origin, at time.Time) Opener {
	switch o.Kind {
	case FromTimeMachine:
		return TimeMachine{Run: s.Run, User: s.User, MountPoint: s.MountPoint, At: at, Snapshot: o.Detail}
	case FromArq:
		return notBuilt("restoring through Arq isn't built yet")
	}
	return notBuilt(fmt.Sprintf("copying from %s over SSH isn't built yet: kit apply --restore-from time-machine restores from its Time Machine disk", o.Says()))
}

// notBuilt is a source kit can't copy from yet, saying so as it's opened.
type notBuilt string

func (n notBuilt) Open(context.Context) (*Opened, error) { return nil, errors.New(string(n)) }

// TimeMachine is the old Mac's backups on a Time Machine disk attached to
// this one, the home folder of the user called User in them, mounted
// read-only at MountPoint, an empty folder: the latest at or before At,
// else the snapshot called Snapshot, else the latest.
type TimeMachine struct {
	Run              runner.Runner
	User, MountPoint string
	At               time.Time
	Snapshot         string
}

// Open mounts the backup to copy from. Backups on more than one disk are
// an error naming them: which is the old Mac's can't be told.
func (t TimeMachine) Open(ctx context.Context) (*Opened, error) {
	found, err := timemachine.Find(ctx, t.Run, t.User, t.MountPoint)
	if err != nil {
		return nil, err
	}
	if len(found) > 1 {
		disks := make([]string, len(found))
		for i, c := range found {
			disks[i] = fmt.Sprintf("%s (%s)", c.Volume, c.Device)
		}
		return nil, fmt.Errorf("backups of /Users/%s are on more than one Time Machine disk, %s: attach the old Mac's alone", t.User, strings.Join(disks, " and "))
	}
	c := found[0]
	p, err := t.pick(c)
	if err != nil {
		return nil, err
	}
	s, err := timemachine.Open(ctx, t.Run, c, p, t.User, t.MountPoint)
	if err != nil {
		return nil, err
	}
	return &Opened{Home: s.Home, Origin: Origin{Kind: FromTimeMachine, Detail: p.Name}, Close: s.Close}, nil
}

// pick is the one of c's snapshots to open, as t says.
func (t TimeMachine) pick(c timemachine.Candidate) (timemachine.Point, error) {
	if !t.At.IsZero() || t.Snapshot == "" {
		return c.Pick(t.At)
	}
	if i := slices.IndexFunc(c.Snapshots, func(p timemachine.Point) bool { return p.Name == t.Snapshot }); i >= 0 {
		return c.Snapshots[i], nil
	}
	return timemachine.Point{}, fmt.Errorf("%s, which the restore began from, isn't on %s any more", Origin{Kind: FromTimeMachine, Detail: t.Snapshot}.Says(), c.Volume)
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
