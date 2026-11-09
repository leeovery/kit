package timemachine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// ErrNoBackup is in the error of a search that found no Time Machine backup
// holding the home folder looked for.
var ErrNoBackup = errors.New("no Time Machine backup found")

// noHomeError is a backup without the home folder looked for.
type noHomeError struct {
	backup       time.Time
	volume, user string
}

func (e *noHomeError) Error() string {
	return fmt.Sprintf("the backup of %s on %s holds no /Users/%s", day(e.backup), e.volume, e.user)
}

// snapshotPrefix and snapshotSuffix frame the name of each snapshot Time
// Machine keeps on its backup volume, its date between them.
const (
	snapshotPrefix = "com.apple.TimeMachine."
	snapshotSuffix = ".backup"
	snapshotDate   = "2006-01-02-150405"
)

// Candidate is a Mac's backups on an attached Time Machine disk: a backup
// volume, by its name and device (disk7s2), and its snapshots, oldest first.
type Candidate struct {
	Volume, Device string
	Snapshots      []Point
}

// Point is one of a backup volume's snapshots: its name, and when it was
// taken.
type Point struct {
	Name string
	Time time.Time
}

// Snapshot is a backup opened to read from: mounted read-only and hidden,
// with the old Mac's home folder inside it. Close unmounts it.
type Snapshot struct {
	Volume, Device, Name string
	Time                 time.Time
	// Home is the old Mac's home folder, its full path in the mount.
	Home string

	run    runner.Runner
	mount  string
	closed bool
}

// Find finds the backups, on the disks attached, holding a home folder for
// user, each checked by opening its latest snapshot at mountPoint, an empty
// folder, and closing it again: a locked volume can't be read, and is left
// out. None is an error, ErrNoBackup, saying what was looked at.
func Find(ctx context.Context, run runner.Runner, user, mountPoint string) ([]Candidate, error) {
	volumes, err := backupVolumes(ctx, run)
	if err != nil {
		return nil, err
	}
	if len(volumes) == 0 {
		return nil, fmt.Errorf("%w: no Time Machine disk is attached", ErrNoBackup)
	}
	var found []Candidate
	var passed []string
	for _, v := range volumes {
		c, why, err := holding(ctx, run, v, user, mountPoint)
		if err != nil {
			return nil, err
		}
		if why != "" {
			passed = append(passed, v.name+", "+why)
			continue
		}
		found = append(found, c)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w for /Users/%s: %s", ErrNoBackup, user, strings.Join(passed, "; "))
	}
	return found, nil
}

// holding is v as a candidate holding user's home folder, or why it isn't
// one.
func holding(ctx context.Context, run runner.Runner, v volume, user, mountPoint string) (Candidate, string, error) {
	if v.locked {
		return Candidate{}, "locked: unlock it with its password in Disk Utility", nil
	}
	points, err := snapshots(ctx, run, v.device)
	if err != nil {
		return Candidate{}, "", err
	}
	if len(points) == 0 {
		return Candidate{}, "no backups", nil
	}
	c := Candidate{Volume: v.name, Device: v.device, Snapshots: points}
	latest := points[len(points)-1]
	s, err := Open(ctx, run, c, latest, user, mountPoint)
	if _, missing := errors.AsType[*noHomeError](err); missing {
		return Candidate{}, "not in its latest backup, " + day(latest.Time), nil
	}
	if err != nil {
		return Candidate{}, "", err
	}
	return c, "", s.Close(ctx)
}

// Pick is the latest of c's snapshots, or, at given, the latest taken at or
// before it: one before them all is an error naming the earliest.
func (c Candidate) Pick(at time.Time) (Point, error) {
	if len(c.Snapshots) == 0 {
		return Point{}, fmt.Errorf("%s holds no backups", c.Volume)
	}
	if at.IsZero() {
		return c.Snapshots[len(c.Snapshots)-1], nil
	}
	for _, p := range slices.Backward(c.Snapshots) {
		if !p.Time.After(at) {
			return p, nil
		}
	}
	return Point{}, fmt.Errorf("%s holds no backup from %s or before: its earliest is %s", c.Volume, day(at), day(c.Snapshots[0].Time))
}

// Open mounts p, one of c's snapshots, read-only and hidden at mountPoint,
// an empty folder, made if it's missing, and finds user's home folder in
// it. What an earlier run left mounted there is used, when it's p, or
// unmounted; anything else there is an error, never mounted over. When
// anything after the mount fails, it's unmounted again.
func Open(ctx context.Context, run runner.Runner, c Candidate, p Point, user, mountPoint string) (*Snapshot, error) {
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		return nil, fmt.Errorf("make the folder to mount Time Machine's backup at: %w", err)
	}
	at, err := filepath.EvalSymlinks(mountPoint)
	if err != nil {
		return nil, fmt.Errorf("find the folder to mount Time Machine's backup at: %w", err)
	}
	s := &Snapshot{Volume: c.Volume, Device: c.Device, Name: p.Name, Time: p.Time, run: run, mount: at}
	if err := s.mountHere(ctx); err != nil {
		return nil, err
	}
	home, err := s.home(user)
	if err != nil {
		return nil, errors.Join(err, s.Close(ctx))
	}
	s.Home = home
	return s, nil
}

// mountHere mounts s at its folder, unless an earlier run left it there:
// a stale snapshot of another is unmounted first.
func (s *Snapshot) mountHere(ctx context.Context) error {
	from, mounted, err := mountedAt(ctx, s.run, s.mount)
	if err != nil {
		return err
	}
	switch {
	case mounted && from == s.source():
		return nil
	case mounted && strings.HasPrefix(from, snapshotPrefix):
		if err := unmount(ctx, s.run, s.mount); err != nil {
			return fmt.Errorf("unmount the backup an earlier run left: %w", err)
		}
	case mounted:
		return fmt.Errorf("%s is mounted at %s, where kit mounts Time Machine's backups", from, s.mount)
	}
	entries, err := os.ReadDir(s.mount)
	if err != nil {
		return fmt.Errorf("read the folder to mount Time Machine's backup at: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s isn't empty, so Time Machine's backup can't be mounted there", s.mount)
	}
	_, err = s.run.Run(ctx, runner.Command{Name: "mount_apfs", Args: []string{"-o", "rdonly,nobrowse", "-s", s.Name, "/dev/" + s.Device, s.mount}})
	if err != nil {
		return fmt.Errorf("mount the backup of %s from %s: %w", day(s.Time), s.Volume, err)
	}
	return nil
}

// source is s as the mount table names what's mounted from it.
func (s *Snapshot) source() string {
	return s.Name + "@/dev/" + s.Device
}

// home is user's home folder in s, mounted: in the folder of the
// backup's date, in the folder of the volume it backed up, whose name is
// the old Mac's, as Macintosh HD - Data. It must be there, and readable.
func (s *Snapshot) home(user string) (string, error) {
	backup := filepath.Join(s.mount, strings.TrimPrefix(s.Name, snapshotPrefix))
	entries, err := os.ReadDir(backup)
	if err != nil {
		return "", fmt.Errorf("read the backup of %s: %w", day(s.Time), err)
	}
	for _, e := range entries {
		home := filepath.Join(backup, e.Name(), "Users", user)
		if info, err := os.Stat(home); err == nil && info.IsDir() {
			return home, readable(home)
		}
	}
	return "", &noHomeError{backup: s.Time, volume: s.Volume, user: user}
}

// readable is an error when the folder at path can't be read.
func readable(path string) error {
	if _, err := os.ReadDir(path); err != nil {
		return fmt.Errorf("read %s in the backup: %w", path, err)
	}
	return nil
}

// Close unmounts s, once: even when ctx is done, as when kit is stopped,
// so no mount is left behind.
func (s *Snapshot) Close(ctx context.Context) error {
	if s.closed {
		return nil
	}
	if err := unmount(context.WithoutCancel(ctx), s.run, s.mount); err != nil {
		return fmt.Errorf("unmount the backup of %s: %w", day(s.Time), err)
	}
	s.closed = true
	return nil
}

func unmount(ctx context.Context, run runner.Runner, at string) error {
	_, err := run.Run(ctx, runner.Command{Name: "umount", Args: []string{at}})
	return err
}

// mountedAt is what's mounted at the folder at, as the mount table names
// its source, and whether anything is.
func mountedAt(ctx context.Context, run runner.Runner, at string) (from string, mounted bool, err error) {
	table, err := output(ctx, run, "mount")
	if err != nil {
		return "", false, fmt.Errorf("read what's mounted: %w", err)
	}
	for line := range strings.Lines(table) {
		if from, _, ok := strings.Cut(line, " on "+at+" ("); ok {
			return from, true, nil
		}
	}
	return "", false, nil
}

// volume is an APFS volume, as diskutil lists it.
type volume struct {
	name, device string
	locked       bool
}

// backupVolumes are the attached disks' Time Machine backup volumes: APFS's
// Backup role, Time Machine's alone.
func backupVolumes(ctx context.Context, run runner.Runner) ([]volume, error) {
	listed, err := diskutil(ctx, run, "apfs", "list", "-plist")
	if err != nil {
		return nil, err
	}
	containers, _ := listed["Containers"].([]any)
	var out []volume
	for _, c := range containers {
		container, _ := c.(map[string]any)
		volumes, _ := container["Volumes"].([]any)
		for _, v := range volumes {
			vol, _ := v.(map[string]any)
			roles, _ := vol["Roles"].([]any)
			if !slices.Contains(roles, any("Backup")) {
				continue
			}
			name, _ := vol["Name"].(string)
			device, _ := vol["DeviceIdentifier"].(string)
			locked, _ := vol["Locked"].(bool)
			out = append(out, volume{name: name, device: device, locked: locked})
		}
	}
	return out, nil
}

// snapshots are Time Machine's snapshots on the volume at device, oldest
// first.
func snapshots(ctx context.Context, run runner.Runner, device string) ([]Point, error) {
	listed, err := diskutil(ctx, run, "apfs", "listSnapshots", "-plist", device)
	if err != nil {
		return nil, err
	}
	all, _ := listed["Snapshots"].([]any)
	var out []Point
	for _, s := range all {
		snap, _ := s.(map[string]any)
		name, _ := snap["SnapshotName"].(string)
		if t, ok := taken(name); ok {
			out = append(out, Point{Name: name, Time: t})
		}
	}
	slices.SortFunc(out, func(a, b Point) int { return a.Time.Compare(b.Time) })
	return out, nil
}

// taken is when the snapshot name was taken, and whether it's Time
// Machine's. The date in its name is the backed-up Mac's local time
// (checked: a snapshot named 12:02:16 in June is 11:02:16 UTC in Time
// Machine's own record), so it's read as this Mac's, the old one's
// stand-in.
func taken(name string) (time.Time, bool) {
	date, ok := strings.CutPrefix(name, snapshotPrefix)
	if !ok {
		return time.Time{}, false
	}
	if date, ok = strings.CutSuffix(date, snapshotSuffix); !ok {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(snapshotDate, date, time.Now().Location())
	return t, err == nil
}

// diskutil runs diskutil, reading the property list it prints.
func diskutil(ctx context.Context, run runner.Runner, args ...string) (map[string]any, error) {
	out, err := output(ctx, run, "diskutil", args...)
	if err != nil {
		return nil, err
	}
	v, err := plist.Read(out)
	listed, _ := v.(map[string]any)
	if err != nil || listed == nil {
		return nil, fmt.Errorf("couldn't read what diskutil %s printed", strings.Join(args, " "))
	}
	return listed, nil
}

// day is t as an error says it, years apart as backups can be.
func day(t time.Time) string {
	return t.Format("2 Jan 2006 15:04")
}
