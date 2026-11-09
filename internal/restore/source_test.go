package restore

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// snapshot names a Time Machine snapshot taken at the local time given.
func snapshot(year int, month time.Month, day, hour, minute int) string {
	return fmt.Sprintf("com.apple.TimeMachine.%04d-%02d-%02d-%02d%02d00.backup", year, month, day, hour, minute)
}

// tmMac is a Mac with Time Machine disks attached, as the fake runs
// diskutil, mount, mount_apfs and umount: each backup volume holding
// someone's home folder in each of its snapshots; what's mounted at mp in
// its mount table till it's unmounted.
type tmMac struct {
	*runnertest.Fake
	mp      string
	mounted string
}

// newTMMac is a Mac with the backup volumes given, by name, each its
// snapshots, oldest first.
func newTMMac(t *testing.T, volumes map[string][]string) *tmMac {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &tmMac{Fake: runnertest.New(t), mp: filepath.Join(dir, "mnt")}
	m.On("mount").Filters(func(string) (string, error) { return m.mounted, nil })
	m.On("umount", m.mp).Does(func() {
		entries, _ := os.ReadDir(m.mp)
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(m.mp, e.Name()))
		}
		m.mounted = ""
	})
	var list strings.Builder
	list.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Containers</key><array>`)
	for i, name := range slices.Sorted(maps.Keys(volumes)) {
		device := fmt.Sprintf("disk%ds2", 7+i)
		fmt.Fprintf(&list, `<dict><key>Volumes</key><array><dict><key>DeviceIdentifier</key><string>%s</string><key>Locked</key><false/><key>Name</key><string>%s</string><key>Roles</key><array><string>Backup</string></array></dict></array></dict>`, device, name)
		var snaps strings.Builder
		snaps.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Snapshots</key><array>`)
		for j, s := range volumes[name] {
			fmt.Fprintf(&snaps, `<dict><key>SnapshotName</key><string>%s</string><key>SnapshotXID</key><integer>%d</integer></dict>`, s, 100+j)
			m.On("mount_apfs", "-o", "rdonly,nobrowse", "-s", s, "/dev/"+device, m.mp).Does(func() {
				home := filepath.Join(m.mp, strings.TrimPrefix(s, "com.apple.TimeMachine."), "Macintosh HD - Data", "Users", "someone")
				if err := os.MkdirAll(home, 0o755); err != nil {
					panic(err)
				}
				m.mounted = s + "@/dev/" + device + " on " + m.mp + " (apfs, local, read-only, nobrowse)\n"
			})
		}
		snaps.WriteString(`</array></dict></plist>`)
		m.On("diskutil", "apfs", "listSnapshots", "-plist", device).Prints(snaps.String())
	}
	list.WriteString(`</array></dict></plist>`)
	m.On("diskutil", "apfs", "list", "-plist").Prints(list.String())
	return m
}

// home is someone's home folder in snapshot s, mounted at m's mount point.
func (m *tmMac) home(s string) string {
	return filepath.Join(m.mp, strings.TrimPrefix(s, "com.apple.TimeMachine."), "Macintosh HD - Data", "Users", "someone")
}

// Time Machine's backup is opened at the mount point: the latest by
// default; the latest at or before At; the snapshot a restore began from,
// resuming, by name, kit apply's flags naming Time Machine or not; At
// winning over it. That snapshot gone, with kit apply's flags naming Time
// Machine, the latest, or the latest at or before At, stands in for it,
// saying so. Opened, it's the old home folder in the snapshot, recorded by
// the snapshot's name, and closing it unmounts it.
func TestTimeMachineOpens(t *testing.T) {
	here := time.Now().Location()
	gone, first, second, third := snapshot(2026, 10, 1, 9, 0), snapshot(2026, 11, 1, 9, 0), snapshot(2026, 11, 7, 9, 0), snapshot(2026, 11, 8, 14, 19)
	byTheSeventh := time.Date(2026, 11, 7, 23, 59, 0, 0, here)
	for _, tt := range []struct {
		name     string
		at       time.Time
		snapshot string
		chosen   bool
		want     string
		replaces string
	}{
		{"the latest", time.Time{}, "", false, third, ""},
		{"at a time", byTheSeventh, "", true, second, ""},
		{"resumed", time.Time{}, first, false, first, ""},
		{"resumed, named again", time.Time{}, first, true, first, ""},
		{"at a time, resuming", byTheSeventh, first, true, second, ""},
		{"gone, named again", time.Time{}, gone, true, third, gone},
		{"gone, named again at a time", byTheSeventh, gone, true, second, gone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newTMMac(t, map[string][]string{"Backups of laptop": {first, second, third}})
			src, err := Sources{Run: m, User: "someone", MountPoint: m.mp}.For(Origin{Kind: FromTimeMachine, Detail: tt.snapshot}, tt.at, tt.chosen).Open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if src.Home != m.home(tt.want) || src.Origin != (Origin{Kind: FromTimeMachine, Detail: tt.want}) || src.Live || src.Host != "" {
				t.Errorf("opened %+v; want %s's home, by its name, not live", src, tt.want)
			}
			if want := (Origin{Kind: FromTimeMachine, Detail: tt.replaces}); tt.replaces == "" && src.Replaces != (Origin{}) || tt.replaces != "" && src.Replaces != want {
				t.Errorf("opened %+v, standing in for %+v; want it standing in for %q", src.Origin, src.Replaces, tt.replaces)
			}
			if got := src.Source("~/Code").Path; got != filepath.Join(m.home(tt.want), "Code") {
				t.Errorf("~/Code is at %s in the backup", got)
			}
			if err := src.Close(t.Context()); err != nil || m.mounted != "" {
				t.Errorf("Close() = %v, leaving %q mounted", err, m.mounted)
			}
		})
	}
}

// Backups on more than one disk can't be told apart: which is the old
// Mac's is the person's to say, by attaching it alone. A snapshot a
// restore began from that's gone, kit apply's flags not naming Time
// Machine, is an error naming it, and saying how to carry on from another;
// a time before every backup is an error too.
func TestTimeMachineRefuses(t *testing.T) {
	here := time.Now().Location()
	first := snapshot(2026, 11, 1, 9, 0)
	for _, tt := range []struct {
		name     string
		volumes  map[string][]string
		at       time.Time
		snapshot string
		want     string
	}{
		{"two disks", map[string][]string{"Backups of laptop": {first}, "Backups of studio": {first}}, time.Time{}, "",
			"backups of /Users/someone are on more than one Time Machine disk, Backups of laptop (disk7s2) and Backups of studio (disk8s2): attach the old Mac's alone"},
		{"the snapshot gone", map[string][]string{"Backups of laptop": {first}}, time.Time{}, snapshot(2026, 10, 1, 9, 0),
			"Time Machine's backup of 1 Oct 2026 09:00, which the restore began from, isn't on Backups of laptop any more: kit apply --restore-from time-machine carries on from the latest backup there, or the one --at picks"},
		{"before them all", map[string][]string{"Backups of laptop": {first}}, time.Date(2026, 10, 1, 0, 0, 0, 0, here), "",
			"Backups of laptop holds no backup from 1 Oct 2026 00:00 or before: its earliest is 1 Nov 2026 09:00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newTMMac(t, tt.volumes)
			src, err := Sources{Run: m, User: "someone", MountPoint: m.mp}.For(Origin{Kind: FromTimeMachine, Detail: tt.snapshot}, tt.at, !tt.at.IsZero()).Open(t.Context())
			if err == nil || err.Error() != tt.want {
				t.Errorf("Open() = %+v, %v; want %q", src, err, tt.want)
			}
			if m.mounted != "" {
				t.Errorf("left %q mounted", m.mounted)
			}
		})
	}
}

// With no backup to read, Find's own words say why: a disk to attach, or
// one to unlock in Disk Utility.
func TestTimeMachineSaysWhyThereIsNone(t *testing.T) {
	m := runnertest.New(t)
	m.On("diskutil", "apfs", "list", "-plist").Prints(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Containers</key><array><dict><key>Volumes</key><array><dict><key>DeviceIdentifier</key><string>disk7s2</string><key>Locked</key><true/><key>Name</key><string>Backups of laptop</string><key>Roles</key><array><string>Backup</string></array></dict></array></dict></array></dict></plist>`)
	_, err := Sources{Run: m, User: "someone", MountPoint: t.TempDir()}.For(Origin{Kind: FromTimeMachine}, time.Time{}, false).Open(t.Context())
	if !errors.Is(err, timemachine.ErrNoBackup) || !strings.Contains(err.Error(), "unlock it with its password in Disk Utility") {
		t.Errorf("Open() = %v; want no backup, saying to unlock it in Disk Utility", err)
	}
}

// Arq's restore, and the old Mac over SSH, aren't built yet: opening
// either says so, the old Mac's pointing at its Time Machine disk.
func TestSourcesNotBuilt(t *testing.T) {
	s := Sources{Run: runnertest.New(t), User: "someone", MountPoint: t.TempDir()}
	for _, tt := range []struct {
		origin Origin
		want   string
	}{
		{Origin{Kind: FromArq}, "restoring through Arq isn't built yet"},
		{Origin{Kind: FromMac, Detail: "laptop"}, "copying from laptop over SSH isn't built yet: kit apply --restore-from time-machine restores from its Time Machine disk"},
	} {
		if _, err := s.For(tt.origin, time.Time{}, true).Open(t.Context()); err == nil || err.Error() != tt.want {
			t.Errorf("opening %+v: %v; want %q", tt.origin, err, tt.want)
		}
	}
}

// An origin says where it is in kit's words: Time Machine's backup by when
// it was taken; Arq's restore by its folder; the old Mac by its name.
func TestOriginSays(t *testing.T) {
	for _, tt := range []struct {
		origin Origin
		want   string
	}{
		{Origin{Kind: FromTimeMachine, Detail: snapshot(2026, 11, 8, 14, 19)}, "Time Machine's backup of 8 Nov 2026 14:19"},
		{Origin{Kind: FromTimeMachine}, "Time Machine's backup"},
		{Origin{Kind: FromArq, Detail: "/Volumes/Scratch/arq"}, "Arq's restore in /Volumes/Scratch/arq"},
		{Origin{Kind: FromArq}, "Arq's restore"},
		{Origin{Kind: FromMac, Detail: "laptop"}, "laptop"},
		{Origin{Kind: FromMac}, "the old Mac"},
	} {
		if got := tt.origin.Says(); got != tt.want {
			t.Errorf("%+v says %q, want %q", tt.origin, got, tt.want)
		}
	}
}

// The restore's own folders are kit's state's: the mount point; a line's
// partial files and rules, by its path from ~.
func TestRestoreFolders(t *testing.T) {
	state := "/Users/someone/.local/state/kit"
	l := Line{Path: "~/Library/Application Support/Claude/claude-code-sessions"}
	for got, want := range map[string]string{
		MountPoint(state):   state + "/restore/mount",
		l.Partial(state):    state + "/restore/partial/Library/Application Support/Claude/claude-code-sessions",
		l.FilterFile(state): state + "/restore/filters/Library/Application Support/Claude/claude-code-sessions",
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}
