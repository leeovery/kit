package timemachine_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The fixtures are diskutil's real output on macOS 26, trimmed, identifiers
// replaced: apfs list -plist, its internal disk's container and an attached
// Time Machine disk's; and apfs listSnapshots -plist for that disk's backup
// volume, five of its snapshots.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var (
	apfsList      = []string{"apfs", "list", "-plist"}
	latestOnDisk  = "com.apple.TimeMachine.2026-11-09-175758.backup"
	dataFolder    = "Macintosh HD - Data"
	errDiskutil   = "Could not find disk for disk7s2"
	otherMountsOn = "/dev/disk3s1s1 on / (apfs, sealed, local, read-only, journaled)\n/dev/disk7s2 on /Volumes/Time Machine (apfs, local, nodev, nosuid, journaled)\n"
)

func local(year int, month time.Month, d, h, m, s int) time.Time {
	return time.Date(year, month, d, h, m, s, 0, time.Now().Location())
}

func listSnapshots(device string) []string {
	return []string{"apfs", "listSnapshots", "-plist", device}
}

// mountPoint is an empty folder for the mount, its path as the mount table
// names it, links resolved.
func mountPoint(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "mnt")
}

// disk scripts mounting snapshot of device at mp, as mount_apfs does,
// showing the backup it holds (showing), and unmounting it, which empties
// mp again.
func disk(f *runnertest.Fake, device, snapshot, mp string, homes map[string][]string) {
	f.On("mount_apfs", mountArgs(device, snapshot, mp)...).Does(func() { showing(mp, snapshot, homes) })
	f.On("umount", mp).Does(func() { emptyFolder(mp) })
}

func mountArgs(device, snapshot, mp string) []string {
	return []string{"-o", "rdonly,nobrowse", "-s", snapshot, "/dev/" + device, mp}
}

// showing makes what a mount of snapshot at mp shows: the folder of its
// date, a folder in it for each volume backed up, each holding the home
// folders of the users given.
func showing(mp, snapshot string, homes map[string][]string) {
	backup := filepath.Join(mp, strings.TrimPrefix(snapshot, "com.apple.TimeMachine."))
	for folder, users := range homes {
		for _, u := range users {
			if err := os.MkdirAll(filepath.Join(backup, folder, "Users", u, "Code"), 0o755); err != nil {
				panic(err)
			}
		}
	}
}

// emptyFolder empties dir, as an unmount does, whatever's unreadable in it.
func emptyFolder(dir string) {
	_ = filepath.WalkDir(dir, func(path string, _ os.DirEntry, _ error) error { return os.Chmod(path, 0o755) })
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			panic(err)
		}
	}
}

// backupVolume is a backup volume as diskutil apfs list -plist has one.
type backupVolume struct {
	name, device string
	locked       bool
}

// apfsListOf is diskutil apfs list -plist with a container for each
// volume, each a Time Machine backup volume.
func apfsListOf(volumes ...backupVolume) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Containers</key>
	<array>
`)
	for _, v := range volumes {
		fmt.Fprintf(&b, `		<dict>
			<key>Volumes</key>
			<array>
				<dict>
					<key>DeviceIdentifier</key>
					<string>%s</string>
					<key>Locked</key>
					<%t/>
					<key>Name</key>
					<string>%s</string>
					<key>Roles</key>
					<array>
						<string>Backup</string>
					</array>
				</dict>
			</array>
		</dict>
`, v.device, v.locked, v.name)
	}
	b.WriteString("\t</array>\n</dict>\n</plist>\n")
	return b.String()
}

// snapshotsOf is diskutil apfs listSnapshots -plist listing names.
func snapshotsOf(names ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Snapshots</key>
	<array>
`)
	for _, n := range names {
		fmt.Fprintf(&b, "\t\t<dict>\n\t\t\t<key>SnapshotName</key>\n\t\t\t<string>%s</string>\n\t\t</dict>\n", n)
	}
	b.WriteString("\t</array>\n</dict>\n</plist>\n")
	return b.String()
}

// The real disk's listings: its one backup volume found, the internal
// disk's volumes passed over, its snapshots read, oldest first, each at
// the local time its name says; the latest opened to check the home
// folder's there, and closed again.
func TestFindOnTheRealListing(t *testing.T) {
	mp := mountPoint(t)
	f := runnertest.New(t)
	f.On("diskutil", apfsList...).Prints(fixture(t, "apfs-list.plist"))
	f.On("diskutil", listSnapshots("disk7s2")...).Prints(fixture(t, "snapshots.plist"))
	f.On("mount").Prints(otherMountsOn)
	disk(f, "disk7s2", latestOnDisk, mp, map[string][]string{dataFolder: {"someone"}})

	found, err := timemachine.Find(context.Background(), f, "someone", mp)
	if err != nil {
		t.Fatal(err)
	}
	want := []timemachine.Candidate{{Volume: "Time Machine", Device: "disk7s2", Snapshots: []timemachine.Point{
		{Name: "com.apple.TimeMachine.2025-04-16-105639.backup", Time: local(2025, 4, 16, 10, 56, 39)},
		{Name: "com.apple.TimeMachine.2025-04-23-105900.backup", Time: local(2025, 4, 23, 10, 59, 0)},
		{Name: "com.apple.TimeMachine.2026-11-09-154102.backup", Time: local(2026, 11, 9, 15, 41, 2)},
		{Name: "com.apple.TimeMachine.2026-11-09-165905.backup", Time: local(2026, 11, 9, 16, 59, 5)},
		{Name: latestOnDisk, Time: local(2026, 11, 9, 17, 57, 58)},
	}}}
	if !reflect.DeepEqual(found, want) {
		t.Errorf("found\n%+v\nwant\n%+v", found, want)
	}
	wantCalls := []string{
		"diskutil apfs list -plist",
		"diskutil apfs listSnapshots -plist disk7s2",
		"mount",
		"mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 " + mp,
		"umount " + mp,
	}
	if calls := f.Calls(); !slices.Equal(calls, wantCalls) {
		t.Errorf("calls\n%q\nwant\n%q", calls, wantCalls)
	}
	if entries, _ := os.ReadDir(mp); len(entries) > 0 {
		t.Errorf("the mount point holds %d entries after the find", len(entries))
	}
}

// Several backup volumes, as two Macs backing up to one disk: every one
// holding the home folder is a candidate, in the order diskutil lists
// them; a locked one, one with no backups, and one whose latest backup
// holds another user's home are passed over. Snapshots that aren't Time
// Machine's are left out, and the rest put in order.
func TestFindSeveral(t *testing.T) {
	mp := mountPoint(t)
	f := runnertest.New(t)
	f.On("diskutil", apfsList...).Prints(apfsListOf(
		backupVolume{name: "Backups", device: "disk7s2"},
		backupVolume{name: "Locked", device: "disk7s3", locked: true},
		backupVolume{name: "Empty", device: "disk7s4"},
		backupVolume{name: "Another's", device: "disk7s5"},
		backupVolume{name: "Backups of laptop", device: "disk9s1"},
	))
	f.On("diskutil", listSnapshots("disk7s2")...).Prints(snapshotsOf(
		"com.apple.TimeMachine.2026-11-02-090000.backup",
		"com.apple.TimeMachine.2026-11-01-090000.backup",
		"com.apple.os.update-0000",
	))
	f.On("diskutil", listSnapshots("disk7s4")...).Prints(snapshotsOf("com.apple.os.update-0000"))
	f.On("diskutil", listSnapshots("disk7s5")...).Prints(snapshotsOf("com.apple.TimeMachine.2026-11-03-090000.backup"))
	f.On("diskutil", listSnapshots("disk9s1")...).Prints(snapshotsOf("com.apple.TimeMachine.2026-10-30-210000.backup"))
	f.On("mount").Prints(otherMountsOn)
	disk(f, "disk7s2", "com.apple.TimeMachine.2026-11-02-090000.backup", mp, map[string][]string{dataFolder: {"someone"}})
	disk(f, "disk7s5", "com.apple.TimeMachine.2026-11-03-090000.backup", mp, map[string][]string{dataFolder: {"another"}})
	disk(f, "disk9s1", "com.apple.TimeMachine.2026-10-30-210000.backup", mp, map[string][]string{"Studio HD": {"another", "someone"}})

	found, err := timemachine.Find(context.Background(), f, "someone", mp)
	if err != nil {
		t.Fatal(err)
	}
	want := []timemachine.Candidate{
		{Volume: "Backups", Device: "disk7s2", Snapshots: []timemachine.Point{
			{Name: "com.apple.TimeMachine.2026-11-01-090000.backup", Time: local(2026, 11, 1, 9, 0, 0)},
			{Name: "com.apple.TimeMachine.2026-11-02-090000.backup", Time: local(2026, 11, 2, 9, 0, 0)},
		}},
		{Volume: "Backups of laptop", Device: "disk9s1", Snapshots: []timemachine.Point{
			{Name: "com.apple.TimeMachine.2026-10-30-210000.backup", Time: local(2026, 10, 30, 21, 0, 0)},
		}},
	}
	if !reflect.DeepEqual(found, want) {
		t.Errorf("found\n%+v\nwant\n%+v", found, want)
	}
	for _, c := range f.Calls() {
		if strings.Contains(c, "disk7s3") {
			t.Errorf("the locked volume was used: %s", c)
		}
	}
}

// None found is ErrNoBackup, saying what was looked for and why each
// volume looked at doesn't hold it.
func TestFindNone(t *testing.T) {
	for _, tt := range []struct {
		name   string
		script func(f *runnertest.Fake, mp string)
		want   string
	}{
		{"no Time Machine disk", func(f *runnertest.Fake, _ string) {
			f.On("diskutil", apfsList...).Prints(apfsListOf())
		}, "no Time Machine backup found: no Time Machine disk is attached"},
		{"the internal disk alone", func(f *runnertest.Fake, _ string) {
			f.On("diskutil", apfsList...).Prints(strings.Replace(fixture(t, "apfs-list.plist"), "<string>Backup</string>", "<string>Data</string>", 1))
		}, "no Time Machine backup found: no Time Machine disk is attached"},
		{"none holding it", func(f *runnertest.Fake, mp string) {
			f.On("diskutil", apfsList...).Prints(apfsListOf(
				backupVolume{name: "Backups", device: "disk7s2"},
				backupVolume{name: "Locked", device: "disk7s3", locked: true},
				backupVolume{name: "Empty", device: "disk7s4"},
			))
			f.On("diskutil", listSnapshots("disk7s2")...).Prints(snapshotsOf(latestOnDisk))
			f.On("diskutil", listSnapshots("disk7s4")...).Prints(snapshotsOf())
			f.On("mount").Prints(otherMountsOn)
			disk(f, "disk7s2", latestOnDisk, mp, map[string][]string{dataFolder: {"another"}})
		}, "no Time Machine backup found for /Users/someone: Backups, not in its latest backup, 9 Nov 2026 17:57; Locked, locked: unlock it with its password in Disk Utility; Empty, no backups"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mp := mountPoint(t)
			f := runnertest.New(t)
			tt.script(f, mp)
			found, err := timemachine.Find(context.Background(), f, "someone", mp)
			if !errors.Is(err, timemachine.ErrNoBackup) || err.Error() != tt.want {
				t.Errorf("Find = %v, %v\nwant error %q", found, err, tt.want)
			}
		})
	}
}

// A failure reading the disks, or opening a backup to look in it, fails
// the find: it isn't passed over as a backup without the home folder.
func TestFindFails(t *testing.T) {
	for _, tt := range []struct {
		name   string
		script func(f *runnertest.Fake, mp string)
		want   string
	}{
		{"diskutil fails", func(f *runnertest.Fake, _ string) {
			f.On("diskutil", apfsList...).Exits(1).PrintsToStderr("diskutil failed")
		}, "diskutil apfs list -plist exited 1: diskutil failed"},
		{"diskutil prints what isn't a list", func(f *runnertest.Fake, _ string) {
			f.On("diskutil", apfsList...).Prints("Could not find disk")
		}, "couldn't read what diskutil apfs list -plist printed"},
		{"the snapshots can't be listed", func(f *runnertest.Fake, _ string) {
			f.On("diskutil", apfsList...).Prints(apfsListOf(backupVolume{name: "Backups", device: "disk7s2"}))
			f.On("diskutil", listSnapshots("disk7s2")...).Exits(1).PrintsToStderr(errDiskutil)
		}, "diskutil apfs listSnapshots -plist disk7s2 exited 1: " + errDiskutil},
		{"the mount fails", func(f *runnertest.Fake, mp string) {
			f.On("diskutil", apfsList...).Prints(apfsListOf(backupVolume{name: "Backups", device: "disk7s2"}))
			f.On("diskutil", listSnapshots("disk7s2")...).Prints(snapshotsOf(latestOnDisk))
			f.On("mount").Prints(otherMountsOn)
			f.On("mount_apfs", mountArgs("disk7s2", latestOnDisk, mp)...).Exits(77).PrintsToStderr("mount_apfs: Operation not permitted")
		}, "mount the backup of 9 Nov 2026 17:57 from Backups: mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP exited 77: mount_apfs: Operation not permitted"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mp := mountPoint(t)
			f := runnertest.New(t)
			tt.script(f, mp)
			_, err := timemachine.Find(context.Background(), f, "someone", mp)
			want := strings.ReplaceAll(tt.want, "MP", mp)
			if err == nil || err.Error() != want || errors.Is(err, timemachine.ErrNoBackup) {
				t.Errorf("Find's error = %v\nwant %q", err, want)
			}
		})
	}
}

// Picking a snapshot: the latest, or the latest at or before a time given;
// one before them all is an error naming the earliest.
func TestPick(t *testing.T) {
	c := timemachine.Candidate{Volume: "Backups", Device: "disk7s2", Snapshots: []timemachine.Point{
		{Name: "first", Time: local(2026, 10, 1, 9, 0, 0)},
		{Name: "second", Time: local(2026, 11, 1, 9, 0, 0)},
		{Name: "third", Time: local(2026, 11, 2, 9, 0, 0)},
	}}
	for _, tt := range []struct {
		name string
		at   time.Time
		want string
		err  string
	}{
		{"the latest", time.Time{}, "third", ""},
		{"after them all", local(2026, 12, 1, 0, 0, 0), "third", ""},
		{"at one exactly", local(2026, 11, 1, 9, 0, 0), "second", ""},
		{"a second before one", local(2026, 11, 2, 8, 59, 59), "second", ""},
		{"at the earliest", local(2026, 10, 1, 9, 0, 0), "first", ""},
		{"before them all", local(2026, 10, 1, 8, 59, 59), "", "Backups holds no backup from 1 Oct 2026 08:59 or before: its earliest is 1 Oct 2026 09:00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := c.Pick(tt.at)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Errorf("Pick = %v, %v\nwant error %q", p, err, tt.err)
				}
				return
			}
			if err != nil || p.Name != tt.want {
				t.Errorf("Pick = %v, %v, want %s", p, err, tt.want)
			}
		})
	}
	if p, err := (timemachine.Candidate{Volume: "Backups"}).Pick(time.Time{}); err == nil || err.Error() != "Backups holds no backups" {
		t.Errorf("Pick with no snapshots = %v, %v", p, err)
	}
}

var opened = timemachine.Candidate{Volume: "Time Machine", Device: "disk7s2", Snapshots: []timemachine.Point{{Name: latestOnDisk, Time: local(2026, 11, 9, 17, 57, 58)}}}

// Opening a snapshot mounts it read-only and hidden at the folder given,
// made when missing, and finds the home folder in the folder of the
// backup's date and of the volume it backed up, whatever that's named;
// Close unmounts it, once.
func TestOpen(t *testing.T) {
	mp := mountPoint(t)
	f := runnertest.New(t)
	f.On("mount").Prints(otherMountsOn)
	disk(f, "disk7s2", latestOnDisk, mp, map[string][]string{"Old Mac HD": {"someone"}, "External": {"someone-else"}})

	s, err := timemachine.Open(context.Background(), f, opened, opened.Snapshots[0], "someone", mp)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mp, "2026-11-09-175758.backup", "Old Mac HD", "Users", "someone"); s.Home != want {
		t.Errorf("Home = %s, want %s", s.Home, want)
	}
	if s.Volume != "Time Machine" || s.Device != "disk7s2" || s.Name != latestOnDisk || !s.Time.Equal(local(2026, 11, 9, 17, 57, 58)) {
		t.Errorf("Snapshot = %+v", s)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"mount",
		"mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 " + mp,
		"umount " + mp,
	}
	if calls := f.Calls(); !slices.Equal(calls, wantCalls) {
		t.Errorf("calls\n%q\nwant\n%q", calls, wantCalls)
	}
}

// Close unmounts even once the run's been stopped, and says when it
// can't, trying again on the next Close.
func TestClose(t *testing.T) {
	mp := mountPoint(t)
	f := runnertest.New(t)
	f.On("mount").Prints(otherMountsOn)
	disk(f, "disk7s2", latestOnDisk, mp, map[string][]string{dataFolder: {"someone"}})
	s, err := timemachine.Open(context.Background(), stoppable{f}, opened, opened.Snapshots[0], "someone", mp)
	if err != nil {
		t.Fatal(err)
	}
	f.On("umount", mp).Exits(16).PrintsToStderr("umount: unmount(" + mp + "): Resource busy").Then()
	stopped, stop := context.WithCancel(context.Background())
	stop()
	want := "unmount the backup of 9 Nov 2026 17:57: umount " + mp + " exited 16: umount: unmount(" + mp + "): Resource busy"
	if err := s.Close(stopped); err == nil || err.Error() != want {
		t.Errorf("Close = %v\nwant %q", err, want)
	}
	if err := s.Close(stopped); err != nil {
		t.Errorf("Close again = %v", err)
	}
	if n := strings.Count(strings.Join(f.Calls(), "\n"), "umount "); n != 2 {
		t.Errorf("umount ran %d times, want 2", n)
	}
}

// stoppable runs commands as a real runner does in a context that's done:
// it doesn't.
type stoppable struct{ *runnertest.Fake }

func (r stoppable) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	if err := ctx.Err(); err != nil {
		return runner.Result{ExitCode: -1}, fmt.Errorf("%s: %w", cmd, err)
	}
	return r.Fake.Run(ctx, cmd)
}

// Whatever fails once the snapshot's mounted, it's unmounted before the
// error's returned; a mount that failed isn't unmounted.
func TestOpenCleansUp(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mounted func(f *runnertest.Fake, mp string)
		want    string
		unmount bool
	}{
		{"no such home folder", func(f *runnertest.Fake, mp string) {
			disk(f, "disk7s2", latestOnDisk, mp, map[string][]string{dataFolder: {"another"}})
		}, "the backup of 9 Nov 2026 17:57 on Time Machine holds no /Users/someone", true},
		{"no folder of the backup's date", func(f *runnertest.Fake, mp string) {
			disk(f, "disk7s2", latestOnDisk, mp, nil)
			f.On("mount_apfs", mountArgs("disk7s2", latestOnDisk, mp)...).Does(func() {
				showing(mp, "com.apple.TimeMachine.2026-11-09-175758.inprogress", map[string][]string{dataFolder: {"someone"}})
			})
		}, "read the backup of 9 Nov 2026 17:57: open MP/2026-11-09-175758.backup: no such file or directory", true},
		{"the home folder unreadable", func(f *runnertest.Fake, mp string) {
			disk(f, "disk7s2", latestOnDisk, mp, nil)
			f.On("mount_apfs", mountArgs("disk7s2", latestOnDisk, mp)...).Does(func() {
				showing(mp, latestOnDisk, map[string][]string{dataFolder: {"someone"}})
				if err := os.Chmod(filepath.Join(mp, "2026-11-09-175758.backup", dataFolder, "Users", "someone"), 0o000); err != nil {
					panic(err)
				}
			})
		}, "read MP/2026-11-09-175758.backup/" + dataFolder + "/Users/someone in the backup: open MP/2026-11-09-175758.backup/" + dataFolder + "/Users/someone: permission denied", true},
		{"the mount fails", func(f *runnertest.Fake, mp string) {
			disk(f, "disk7s2", latestOnDisk, mp, nil)
			f.On("mount_apfs", mountArgs("disk7s2", latestOnDisk, mp)...).Exits(1).PrintsToStderr("mount_apfs: volume could not be mounted")
		}, "mount the backup of 9 Nov 2026 17:57 from Time Machine: mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP exited 1: mount_apfs: volume could not be mounted", false},
		{"the unmount fails too", func(f *runnertest.Fake, mp string) {
			disk(f, "disk7s2", latestOnDisk, mp, map[string][]string{dataFolder: {"another"}})
			f.On("umount", mp).Exits(16).PrintsToStderr("umount: Resource busy")
		}, "the backup of 9 Nov 2026 17:57 on Time Machine holds no /Users/someone\nunmount the backup of 9 Nov 2026 17:57: umount MP exited 16: umount: Resource busy", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mp := mountPoint(t)
			f := runnertest.New(t)
			f.On("mount").Prints(otherMountsOn)
			tt.mounted(f, mp)
			t.Cleanup(func() { emptyFolder(mp) })
			s, err := timemachine.Open(context.Background(), f, opened, opened.Snapshots[0], "someone", mp)
			want := strings.ReplaceAll(tt.want, "MP", mp)
			if s != nil || err == nil || err.Error() != want {
				t.Errorf("Open = %v, %v\nwant error %q", s, err, want)
			}
			unmounted := slices.Contains(f.Calls(), "umount "+mp)
			if unmounted != tt.unmount {
				t.Errorf("unmounted = %v, want %v; calls %q", unmounted, tt.unmount, f.Calls())
			}
		})
	}
}

// What's at the mount point is read before mounting: the same snapshot,
// left by an earlier run, is used as it is; another of Time Machine's is
// unmounted first; anything else, mounted or files, is never mounted over.
func TestOpenOverWhatsThere(t *testing.T) {
	for _, tt := range []struct {
		name  string
		there func(mp string) string
		calls []string
		err   string
	}{
		{"the same snapshot", func(mp string) string {
			showing(mp, latestOnDisk, map[string][]string{dataFolder: {"someone"}})
			return latestOnDisk + "@/dev/disk7s2 on " + mp + " (apfs, local, nodev, nosuid, read-only, journaled, nobrowse, mounted by someone)\n"
		}, []string{"mount"}, ""},
		{"another snapshot", func(mp string) string {
			showing(mp, "com.apple.TimeMachine.2026-11-01-090000.backup", map[string][]string{dataFolder: {"someone"}})
			return "com.apple.TimeMachine.2026-11-01-090000.backup@/dev/disk7s2 on " + mp + " (apfs, local, read-only, nobrowse)\n"
		}, []string{"mount", "umount MP", "mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP"}, ""},
		{"the same snapshot from another device", func(mp string) string {
			showing(mp, latestOnDisk, map[string][]string{dataFolder: {"someone"}})
			return latestOnDisk + "@/dev/disk9s2 on " + mp + " (apfs, local, read-only, nobrowse)\n"
		}, []string{"mount", "umount MP", "mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP"}, ""},
		{"a folder below the mount point", func(mp string) string {
			return latestOnDisk + "@/dev/disk7s2 on " + mp + "/inner (apfs, local, read-only, nobrowse)\n"
		}, []string{"mount", "mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP"}, ""},
		{"something else mounted", func(mp string) string {
			return "/dev/disk8s1 on " + mp + " (apfs, local, nodev, nosuid)\n"
		}, []string{"mount"}, "/dev/disk8s1 is mounted at MP, where kit mounts Time Machine's backups"},
		{"files there", func(mp string) string {
			if err := os.WriteFile(filepath.Join(mp, "notes"), nil, 0o600); err != nil {
				panic(err)
			}
			return ""
		}, []string{"mount"}, "MP isn't empty, so Time Machine's backup can't be mounted there"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mp := mountPoint(t)
			if err := os.MkdirAll(mp, 0o700); err != nil {
				t.Fatal(err)
			}
			f := runnertest.New(t)
			f.On("mount").Prints(otherMountsOn + tt.there(mp))
			disk(f, "disk7s2", latestOnDisk, mp, map[string][]string{dataFolder: {"someone"}})
			s, err := timemachine.Open(context.Background(), f, opened, opened.Snapshots[0], "someone", mp)
			if tt.err != "" {
				if want := strings.ReplaceAll(tt.err, "MP", mp); s != nil || err == nil || err.Error() != want {
					t.Errorf("Open = %v, %v\nwant error %q", s, err, want)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if want := filepath.Join(mp, "2026-11-09-175758.backup", dataFolder, "Users", "someone"); s.Home != want {
				t.Errorf("Home = %s, want %s", s.Home, want)
			}
			var want []string
			for _, c := range tt.calls {
				want = append(want, strings.ReplaceAll(c, "MP", mp))
			}
			if calls := f.Calls(); !slices.Equal(calls, want) {
				t.Errorf("calls\n%q\nwant\n%q", calls, want)
			}
		})
	}
}

// What's mounted can't be read: nothing is mounted.
func TestOpenWithoutTheMountTable(t *testing.T) {
	mp := mountPoint(t)
	f := runnertest.New(t)
	f.On("mount").Exits(1).PrintsToStderr("mount: failed")
	_, err := timemachine.Open(context.Background(), f, opened, opened.Snapshots[0], "someone", mp)
	if want := "read what's mounted: mount exited 1: mount: failed"; err == nil || err.Error() != want {
		t.Errorf("Open's error = %v\nwant %q", err, want)
	}
	if calls := f.Calls(); !slices.Equal(calls, []string{"mount"}) {
		t.Errorf("calls %q", calls)
	}
}
