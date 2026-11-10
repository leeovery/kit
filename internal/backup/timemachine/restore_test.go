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

// mac is a Mac's disks as the fake runs them: its mount table shows what
// mount_apfs mounted at mp, until umount takes it away.
type mac struct {
	*runnertest.Fake
	mp string
	// mounted is the mount table's line for what's mounted at mp: "" for
	// nothing.
	mounted string
}

func newMac(t *testing.T) *mac {
	m := &mac{Fake: runnertest.New(t), mp: mountPoint(t)}
	m.On("mount").Filters(func(string) (string, error) { return otherMountsOn + m.mounted, nil })
	return m
}

// disk scripts mounting snapshot of device at mp, as mount_apfs does,
// showing the backup it holds (showing), and unmounting it.
func (m *mac) disk(device, snapshot string, homes map[string][]string) {
	m.On("mount_apfs", mountArgs(device, snapshot, m.mp)...).Does(func() { m.mount(device, snapshot, homes) })
	m.On("umount", m.mp).Does(m.unmount)
}

// mount mounts snapshot of device at mp, as the mount table has it.
func (m *mac) mount(device, snapshot string, homes map[string][]string) {
	showing(m.mp, snapshot, homes)
	m.mounted = snapshot + "@/dev/" + device + " on " + m.mp + " (apfs, local, nodev, nosuid, read-only, journaled, nobrowse, mounted by someone)\n"
}

// unmount empties mp, as an unmount does.
func (m *mac) unmount() {
	emptyFolder(m.mp)
	m.mounted = ""
}

// leftNothing fails the test when anything's still mounted at mp.
func (m *mac) leftNothing(t *testing.T) {
	t.Helper()
	if m.mounted != "" {
		t.Errorf("left mounted: %s", m.mounted)
	}
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

// emptyFolder empties dir, whatever's unreadable in it.
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

// snap is a snapshot as diskutil apfs listSnapshots -plist has one: its
// name and transaction id, which orders snapshots as they were made.
type snap struct {
	name string
	xid  int
}

// made are snapshots named names, made in that order.
func made(names ...string) []snap {
	var out []snap
	for i, n := range names {
		out = append(out, snap{n, 1000 + i})
	}
	return out
}

// snapshotsOf is diskutil apfs listSnapshots -plist listing snaps.
func snapshotsOf(snaps ...snap) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Snapshots</key>
	<array>
`)
	for _, s := range snaps {
		fmt.Fprintf(&b, "\t\t<dict>\n\t\t\t<key>SnapshotName</key>\n\t\t\t<string>%s</string>\n\t\t\t<key>SnapshotXID</key>\n\t\t\t<integer>%d</integer>\n\t\t</dict>\n", s.name, s.xid)
	}
	b.WriteString("\t</array>\n</dict>\n</plist>\n")
	return b.String()
}

// diskList is diskutil list -plist with a partition mounted at each of
// mounted, by its volume's name.
func diskList(mounted map[string]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>AllDisksAndPartitions</key>
	<array>
		<dict>
			<key>Content</key>
			<string>GUID_partition_scheme</string>
			<key>Partitions</key>
			<array>
				<dict>
					<key>Content</key>
					<string>Apple_APFS</string>
				</dict>
`)
	for name, at := range mounted {
		fmt.Fprintf(&b, "\t\t\t\t<dict>\n\t\t\t\t\t<key>Content</key>\n\t\t\t\t\t<string>Apple_HFS</string>\n\t\t\t\t\t<key>MountPoint</key>\n\t\t\t\t\t<string>%s</string>\n\t\t\t\t\t<key>VolumeName</key>\n\t\t\t\t\t<string>%s</string>\n\t\t\t\t</dict>\n", at, name)
	}
	b.WriteString("\t\t\t</array>\n\t\t</dict>\n\t</array>\n</dict>\n</plist>\n")
	return b.String()
}

// The real disk's listings: its one backup volume found, the internal
// disk's volumes passed over, its snapshots read, oldest first, each at
// the local time its name says; the latest opened to check the home
// folder's there, and closed again.
func TestFindOnTheRealListing(t *testing.T) {
	m := newMac(t)
	m.On("diskutil", apfsList...).Prints(fixture(t, "apfs-list.plist"))
	m.On("diskutil", listSnapshots("disk7s2")...).Prints(fixture(t, "snapshots.plist"))
	m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"someone"}})

	found, err := timemachine.Find(context.Background(), m, "someone", m.mp)
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
		"mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 " + m.mp,
		"umount " + m.mp,
	}
	if calls := m.Calls(); !slices.Equal(calls, wantCalls) {
		t.Errorf("calls\n%q\nwant\n%q", calls, wantCalls)
	}
	m.leftNothing(t)
}

// Several backup volumes, as two Macs backing up to one disk: every one
// holding the home folder is a candidate, in the order diskutil lists
// them. One that can't be read, or doesn't hold the home folder in its
// latest backup, is passed over: locked, with no backups, its snapshots
// not listed, its mount failing, its backup's folder missing, another
// user's. Snapshots that aren't Time Machine's are left out, and the rest
// put in the order they were made.
func TestFindSeveral(t *testing.T) {
	m := newMac(t)
	m.On("diskutil", apfsList...).Prints(apfsListOf(
		backupVolume{name: "Backups", device: "disk7s2"},
		backupVolume{name: "Locked", device: "disk7s3", locked: true},
		backupVolume{name: "Empty", device: "disk7s4"},
		backupVolume{name: "Another's", device: "disk7s5"},
		backupVolume{name: "Unlisted", device: "disk7s6"},
		backupVolume{name: "Unmountable", device: "disk7s7"},
		backupVolume{name: "Half", device: "disk7s8"},
		backupVolume{name: "Backups of laptop", device: "disk9s1"},
	))
	m.On("diskutil", listSnapshots("disk7s2")...).Prints(snapshotsOf(
		snap{"com.apple.TimeMachine.2026-11-02-090000.backup", 20},
		snap{"com.apple.TimeMachine.2026-11-01-090000.backup", 10},
		snap{"com.apple.os.update-0000", 5},
	))
	m.On("diskutil", listSnapshots("disk7s4")...).Prints(snapshotsOf(made("com.apple.os.update-0000")...))
	for _, device := range []string{"disk7s5", "disk7s7", "disk7s8"} {
		m.On("diskutil", listSnapshots(device)...).Prints(snapshotsOf(made(latestOnDisk)...))
	}
	m.On("diskutil", listSnapshots("disk7s6")...).Exits(1).PrintsToStderr("Could not find disk for disk7s6")
	m.On("diskutil", listSnapshots("disk9s1")...).Prints(snapshotsOf(made("com.apple.TimeMachine.2026-10-30-210000.backup")...))
	m.disk("disk7s2", "com.apple.TimeMachine.2026-11-02-090000.backup", map[string][]string{dataFolder: {"someone"}})
	m.disk("disk7s5", latestOnDisk, map[string][]string{dataFolder: {"another"}})
	m.On("mount_apfs", mountArgs("disk7s7", latestOnDisk, m.mp)...).Exits(77).PrintsToStderr("mount_apfs: Operation not permitted")
	m.disk("disk7s8", latestOnDisk, nil)
	m.On("mount_apfs", mountArgs("disk7s8", latestOnDisk, m.mp)...).Does(func() {
		m.mount("disk7s8", "com.apple.TimeMachine.2026-11-09-175758.inprogress", map[string][]string{dataFolder: {"someone"}})
		m.mounted = strings.Replace(m.mounted, ".inprogress", ".backup", 1)
	})
	m.disk("disk9s1", "com.apple.TimeMachine.2026-10-30-210000.backup", map[string][]string{"Studio HD": {"another", "someone"}})

	found, err := timemachine.Find(context.Background(), m, "someone", m.mp)
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
	for _, c := range m.Calls() {
		if strings.Contains(c, "disk7s3") {
			t.Errorf("the locked volume was used: %s", c)
		}
	}
	m.leftNothing(t)
}

// Look says what Find does, and what it passed over, saying why, each
// locked one said so; none found is no error, nor no disk at all.
func TestLookSaysWhatItPassedOver(t *testing.T) {
	m := newMac(t)
	m.On("diskutil", apfsList...).Prints(apfsListOf(
		backupVolume{name: "Backups", device: "disk7s2"},
		backupVolume{name: "Vault", device: "disk7s3", locked: true},
		backupVolume{name: "Empty", device: "disk7s4"},
	))
	m.On("diskutil", listSnapshots("disk7s2")...).Prints(snapshotsOf(made(latestOnDisk)...))
	m.On("diskutil", listSnapshots("disk7s4")...).Prints(snapshotsOf())
	m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"someone"}})

	found, passed, err := timemachine.Look(context.Background(), m, "someone", m.mp)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Volume != "Backups" {
		t.Errorf("found %+v; want Backups", found)
	}
	want := []timemachine.Passed{
		{Volume: "Vault", Why: "locked: unlock it with its password in Disk Utility", Locked: true},
		{Volume: "Empty", Why: "no backups"},
	}
	if !reflect.DeepEqual(passed, want) {
		t.Errorf("passed over\n%+v\nwant\n%+v", passed, want)
	}
	m.leftNothing(t)

	none := newMac(t)
	none.On("diskutil", apfsList...).Prints(apfsListOf())
	if found, passed, err := timemachine.Look(context.Background(), none, "someone", none.mp); err != nil || len(found)+len(passed) > 0 {
		t.Errorf("with no disk, Look() = %+v, %+v, %v; want nothing, no error", found, passed, err)
	}
}

// Snapshots are in the order they were made, not their names' times: when
// the clocks go back, an hour's names come again, and a later snapshot's
// name is earlier.
func TestFindOrdersAsMade(t *testing.T) {
	m := newMac(t)
	m.On("diskutil", apfsList...).Prints(apfsListOf(backupVolume{name: "Backups", device: "disk7s2"}))
	m.On("diskutil", listSnapshots("disk7s2")...).Prints(snapshotsOf(
		snap{"com.apple.TimeMachine.2026-10-25-020500.backup", 300},
		snap{"com.apple.TimeMachine.2026-10-25-014500.backup", 100},
		snap{"com.apple.TimeMachine.2026-10-25-011500.backup", 200},
	))
	m.disk("disk7s2", "com.apple.TimeMachine.2026-10-25-020500.backup", map[string][]string{dataFolder: {"someone"}})

	found, err := timemachine.Find(context.Background(), m, "someone", m.mp)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range found[0].Snapshots {
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(p.Name, "com.apple.TimeMachine."), ".backup"))
	}
	if want := []string{"2026-10-25-014500", "2026-10-25-011500", "2026-10-25-020500"}; !slices.Equal(names, want) {
		t.Errorf("snapshots %q, want %q", names, want)
	}
}

// None found is ErrNoBackup, saying what was looked for and why each
// volume looked at doesn't hold it; a disk of Time Machine's older backups
// is named as one kit doesn't read.
func TestFindNone(t *testing.T) {
	older := t.TempDir()
	if err := os.Mkdir(filepath.Join(older, "Backups.backupdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		script func(m *mac)
		want   string
	}{
		{"no Time Machine disk", func(m *mac) {
			m.On("diskutil", apfsList...).Prints(apfsListOf())
			m.On("diskutil", "list", "-plist").Prints(diskList(map[string]string{"Photos": t.TempDir()}))
		}, "no Time Machine backup found: no Time Machine disk is attached"},
		{"the internal disk alone", func(m *mac) {
			m.On("diskutil", apfsList...).Prints(strings.Replace(fixture(t, "apfs-list.plist"), "<string>Backup</string>", "<string>Data</string>", 1))
			m.On("diskutil", "list", "-plist").Prints(diskList(nil))
		}, "no Time Machine backup found: no Time Machine disk is attached"},
		{"backups of before APFS", func(m *mac) {
			m.On("diskutil", apfsList...).Prints(apfsListOf())
			m.On("diskutil", "list", "-plist").Prints(diskList(map[string]string{"Old Backups": older}))
		}, "no Time Machine backup found: Old Backups holds Time Machine's backups of before macOS 11 (Backups.backupdb), and only APFS backups are read"},
		{"none holding it", func(m *mac) {
			m.On("diskutil", apfsList...).Prints(apfsListOf(
				backupVolume{name: "Backups", device: "disk7s2"},
				backupVolume{name: "Locked", device: "disk7s3", locked: true},
				backupVolume{name: "Empty", device: "disk7s4"},
				backupVolume{name: "Unlisted", device: "disk7s5"},
				backupVolume{name: "Unmountable", device: "disk7s6"},
			))
			m.On("diskutil", listSnapshots("disk7s2")...).Prints(snapshotsOf(made(latestOnDisk)...))
			m.On("diskutil", listSnapshots("disk7s4")...).Prints(snapshotsOf())
			m.On("diskutil", listSnapshots("disk7s5")...).Exits(1).PrintsToStderr("Could not find disk for disk7s5")
			m.On("diskutil", listSnapshots("disk7s6")...).Prints(snapshotsOf(made(latestOnDisk)...))
			m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"another"}})
			m.On("mount_apfs", mountArgs("disk7s6", latestOnDisk, m.mp)...).Exits(77).PrintsToStderr("mount_apfs: Operation not permitted")
		}, "no Time Machine backup found for /Users/someone: " +
			"Backups, not in its latest backup, 9 Nov 2026 17:57; " +
			"Locked, locked: unlock it with its password in Disk Utility; " +
			"Empty, no backups; " +
			"Unlisted, diskutil apfs listSnapshots -plist disk7s5 exited 1: Could not find disk for disk7s5; " +
			"Unmountable, mount the backup of 9 Nov 2026 17:57 from Unmountable: mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s6 MP exited 77: mount_apfs: Operation not permitted"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t)
			tt.script(m)
			found, err := timemachine.Find(context.Background(), m, "someone", m.mp)
			if want := strings.ReplaceAll(tt.want, "MP", m.mp); !errors.Is(err, timemachine.ErrNoBackup) || err.Error() != want {
				t.Errorf("Find = %v, %v\nwant error %q", found, err, want)
			}
			m.leftNothing(t)
		})
	}
}

// What isn't one volume's own fails the find: reading the disks, the mount
// table or the mount point, and a backup that can't be unmounted, which is
// said, never passed over and left mounted.
func TestFindFails(t *testing.T) {
	oneVolume := func(m *mac) {
		m.On("diskutil", apfsList...).Prints(apfsListOf(backupVolume{name: "Backups", device: "disk7s2"}, backupVolume{name: "Backups 2", device: "disk7s3"}))
		m.On("diskutil", listSnapshots("disk7s2")...).Prints(snapshotsOf(made(latestOnDisk)...))
		m.On("diskutil", listSnapshots("disk7s3")...).Prints(snapshotsOf(made(latestOnDisk)...))
		m.disk("disk7s3", latestOnDisk, map[string][]string{dataFolder: {"someone"}})
	}
	for _, tt := range []struct {
		name    string
		script  func(m *mac)
		want    string
		mounted bool
	}{
		{"diskutil fails", func(m *mac) {
			m.On("diskutil", apfsList...).Exits(1).PrintsToStderr("diskutil failed")
		}, "diskutil apfs list -plist exited 1: diskutil failed", false},
		{"diskutil prints what isn't a list", func(m *mac) {
			m.On("diskutil", apfsList...).Prints("Could not find disk")
		}, "couldn't read what diskutil apfs list -plist printed", false},
		{"the disks can't be listed", func(m *mac) {
			m.On("diskutil", apfsList...).Prints(apfsListOf())
			m.On("diskutil", "list", "-plist").Exits(1).PrintsToStderr("diskutil failed")
		}, "diskutil list -plist exited 1: diskutil failed", false},
		{"the unmount fails", func(m *mac) {
			oneVolume(m)
			m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"another"}})
			m.On("umount", m.mp).Exits(16).PrintsToStderr("umount: Resource busy")
		}, "unmount the backup of 9 Nov 2026 17:57: umount MP exited 16: umount: Resource busy", true},
		{"the mount table can't be read", func(m *mac) {
			oneVolume(m)
			m.On("mount").Exits(1).PrintsToStderr("mount: failed")
		}, "read what's mounted: mount exited 1: mount: failed", false},
		{"something else at the mount point", func(m *mac) {
			oneVolume(m)
			m.mounted = "/dev/disk8s1 on " + m.mp + " (apfs, local)\n"
		}, "/dev/disk8s1 is mounted at MP, where kit mounts Time Machine's backups", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t)
			tt.script(m)
			_, err := timemachine.Find(context.Background(), m, "someone", m.mp)
			want := strings.ReplaceAll(tt.want, "MP", m.mp)
			if err == nil || err.Error() != want || errors.Is(err, timemachine.ErrNoBackup) {
				t.Errorf("Find's error = %v\nwant %q", err, want)
			}
			if !tt.mounted {
				m.leftNothing(t)
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
// made when missing; Close unmounts it, once.
func TestOpen(t *testing.T) {
	m := newMac(t)
	m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"someone"}})

	s, err := timemachine.Open(context.Background(), m, opened, opened.Snapshots[0], "someone", m.mp)
	if err != nil {
		t.Fatal(err)
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
		"mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 " + m.mp,
		"umount " + m.mp,
	}
	if calls := m.Calls(); !slices.Equal(calls, wantCalls) {
		t.Errorf("calls\n%q\nwant\n%q", calls, wantCalls)
	}
	m.leftNothing(t)
}

// The home folder is in the folder of the backup's date and of the volume
// it backed up, whatever that's named; where several volumes' folders hold
// one, a Data volume's alone is the Mac's, and otherwise which is can't be
// told.
func TestOpenFindsTheHome(t *testing.T) {
	for _, tt := range []struct {
		name  string
		homes map[string][]string
		want  string
		err   string
	}{
		{"the old Mac's volume, named its own way", map[string][]string{"Old Mac HD": {"someone"}, "External": {"another"}}, "Old Mac HD", ""},
		{"a clone's too", map[string][]string{"Clone": {"someone"}, dataFolder: {"someone"}}, dataFolder, ""},
		{"two volumes, neither a Data volume", map[string][]string{"Clone": {"someone"}, "Old Mac HD": {"someone"}}, "",
			"the backup of 9 Nov 2026 17:57 on Time Machine holds /Users/someone in more than one volume's folder, which is the Mac's can't be told: Clone, Old Mac HD"},
		{"two Data volumes", map[string][]string{"Clone - Data": {"someone"}, dataFolder: {"someone"}}, "",
			"the backup of 9 Nov 2026 17:57 on Time Machine holds /Users/someone in more than one volume's folder, which is the Mac's can't be told: Clone - Data, " + dataFolder},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t)
			m.disk("disk7s2", latestOnDisk, tt.homes)
			s, err := timemachine.Open(context.Background(), m, opened, opened.Snapshots[0], "someone", m.mp)
			if tt.err != "" {
				if s != nil || err == nil || err.Error() != tt.err {
					t.Errorf("Open = %v, %v\nwant error %q", s, err, tt.err)
				}
				m.leftNothing(t)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(m.mp, "2026-11-09-175758.backup", tt.want, "Users", "someone"); s.Home != want {
				t.Errorf("Home = %s, want %s", s.Home, want)
			}
		})
	}
}

// Close unmounts even once the run's been stopped, and says when it
// can't, trying again on the next Close.
func TestClose(t *testing.T) {
	m := newMac(t)
	m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"someone"}})
	s, err := timemachine.Open(context.Background(), stoppable{m.Fake}, opened, opened.Snapshots[0], "someone", m.mp)
	if err != nil {
		t.Fatal(err)
	}
	m.On("umount", m.mp).Exits(16).PrintsToStderr("umount: unmount(" + m.mp + "): Resource busy").Then().Does(m.unmount)
	stopped, stop := context.WithCancel(context.Background())
	stop()
	want := "unmount the backup of 9 Nov 2026 17:57: umount " + m.mp + " exited 16: umount: unmount(" + m.mp + "): Resource busy"
	if err := s.Close(stopped); err == nil || err.Error() != want {
		t.Errorf("Close = %v\nwant %q", err, want)
	}
	if err := s.Close(stopped); err != nil {
		t.Errorf("Close again = %v", err)
	}
	if n := strings.Count(strings.Join(m.Calls(), "\n"), "umount "); n != 2 {
		t.Errorf("umount ran %d times, want 2", n)
	}
	m.leftNothing(t)
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
// error's returned; a mount that failed isn't unmounted, and an unmount
// that fails is said.
func TestOpenCleansUp(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mounted func(m *mac)
		want    string
		unmount bool
	}{
		{"no such home folder", func(m *mac) {
			m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"another"}})
		}, "the backup of 9 Nov 2026 17:57 on Time Machine holds no /Users/someone", true},
		{"no folder of the backup's date", func(m *mac) {
			m.disk("disk7s2", latestOnDisk, nil)
			m.On("mount_apfs", mountArgs("disk7s2", latestOnDisk, m.mp)...).Does(func() {
				m.mount("disk7s2", "com.apple.TimeMachine.2026-11-09-175758.inprogress", map[string][]string{dataFolder: {"someone"}})
			})
		}, "read the backup of 9 Nov 2026 17:57: open MP/2026-11-09-175758.backup: no such file or directory", true},
		{"the home folder unreadable", func(m *mac) {
			m.disk("disk7s2", latestOnDisk, nil)
			m.On("mount_apfs", mountArgs("disk7s2", latestOnDisk, m.mp)...).Does(func() {
				m.mount("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"someone"}})
				if err := os.Chmod(filepath.Join(m.mp, "2026-11-09-175758.backup", dataFolder, "Users", "someone"), 0o000); err != nil {
					panic(err)
				}
			})
		}, "read MP/2026-11-09-175758.backup/" + dataFolder + "/Users/someone in the backup: open MP/2026-11-09-175758.backup/" + dataFolder + "/Users/someone: permission denied", true},
		{"the mount fails", func(m *mac) {
			m.disk("disk7s2", latestOnDisk, nil)
			m.On("mount_apfs", mountArgs("disk7s2", latestOnDisk, m.mp)...).Exits(1).PrintsToStderr("mount_apfs: volume could not be mounted")
		}, "mount the backup of 9 Nov 2026 17:57 from Time Machine: mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP exited 1: mount_apfs: volume could not be mounted", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t)
			tt.mounted(m)
			t.Cleanup(func() { emptyFolder(m.mp) })
			s, err := timemachine.Open(context.Background(), m, opened, opened.Snapshots[0], "someone", m.mp)
			want := strings.ReplaceAll(tt.want, "MP", m.mp)
			if s != nil || err == nil || err.Error() != want {
				t.Errorf("Open = %v, %v\nwant error %q", s, err, want)
			}
			if unmounted := slices.Contains(m.Calls(), "umount "+m.mp); unmounted != tt.unmount {
				t.Errorf("unmounted = %v, want %v; calls %q", unmounted, tt.unmount, m.Calls())
			}
			m.leftNothing(t)
		})
	}
}

// An unmount that fails after a failure is said with it, the mount left.
func TestOpenCantCleanUp(t *testing.T) {
	m := newMac(t)
	m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"another"}})
	m.On("umount", m.mp).Exits(16).PrintsToStderr("umount: Resource busy")
	t.Cleanup(func() { emptyFolder(m.mp) })
	s, err := timemachine.Open(context.Background(), m, opened, opened.Snapshots[0], "someone", m.mp)
	want := "the backup of 9 Nov 2026 17:57 on Time Machine holds no /Users/someone\nunmount the backup of 9 Nov 2026 17:57: umount " + m.mp + " exited 16: umount: Resource busy"
	if s != nil || err == nil || err.Error() != want {
		t.Errorf("Open = %v, %v\nwant error %q", s, err, want)
	}
}

// What's at the mount point is read before mounting: the same snapshot,
// left by an earlier run, is used as it is; another of Time Machine's is
// unmounted first; anything else, mounted or files, is never mounted over.
func TestOpenOverWhatsThere(t *testing.T) {
	for _, tt := range []struct {
		name  string
		there func(m *mac)
		calls []string
		err   string
	}{
		{"the same snapshot", func(m *mac) {
			m.mount("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"someone"}})
		}, []string{"mount"}, ""},
		{"another snapshot", func(m *mac) {
			m.mount("disk7s2", "com.apple.TimeMachine.2026-11-01-090000.backup", map[string][]string{dataFolder: {"someone"}})
		}, []string{"mount", "umount MP", "mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP"}, ""},
		{"the same snapshot from another device", func(m *mac) {
			m.mount("disk9s2", latestOnDisk, map[string][]string{dataFolder: {"someone"}})
		}, []string{"mount", "umount MP", "mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP"}, ""},
		{"a folder below the mount point", func(m *mac) {
			m.mounted = latestOnDisk + "@/dev/disk7s2 on " + m.mp + "/inner (apfs, local, read-only, nobrowse)\n"
		}, []string{"mount", "mount_apfs -o rdonly,nobrowse -s " + latestOnDisk + " /dev/disk7s2 MP"}, ""},
		{"something else mounted", func(m *mac) {
			m.mounted = "/dev/disk8s1 on " + m.mp + " (apfs, local, nodev, nosuid)\n"
		}, []string{"mount"}, "/dev/disk8s1 is mounted at MP, where kit mounts Time Machine's backups"},
		{"files there", func(m *mac) {
			if err := os.WriteFile(filepath.Join(m.mp, "notes"), nil, 0o600); err != nil {
				panic(err)
			}
		}, []string{"mount"}, "MP isn't empty, so Time Machine's backup can't be mounted there"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t)
			if err := os.MkdirAll(m.mp, 0o700); err != nil {
				t.Fatal(err)
			}
			tt.there(m)
			m.disk("disk7s2", latestOnDisk, map[string][]string{dataFolder: {"someone"}})
			s, err := timemachine.Open(context.Background(), m, opened, opened.Snapshots[0], "someone", m.mp)
			if tt.err != "" {
				if want := strings.ReplaceAll(tt.err, "MP", m.mp); s != nil || err == nil || err.Error() != want {
					t.Errorf("Open = %v, %v\nwant error %q", s, err, want)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if want := filepath.Join(m.mp, "2026-11-09-175758.backup", dataFolder, "Users", "someone"); s.Home != want {
				t.Errorf("Home = %s, want %s", s.Home, want)
			}
			var want []string
			for _, c := range tt.calls {
				want = append(want, strings.ReplaceAll(c, "MP", m.mp))
			}
			if calls := m.Calls(); !slices.Equal(calls, want) {
				t.Errorf("calls\n%q\nwant\n%q", calls, want)
			}
		})
	}
}
