package restore

import (
	"context"
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
	var vs []tmVolume
	for _, name := range slices.Sorted(maps.Keys(volumes)) {
		vs = append(vs, tmVolume{name: name, snapshots: volumes[name]})
	}
	return newTMDisks(t, vs...)
}

// tmVolume is a backup volume on a disk attached: its name, its snapshots,
// oldest first, and whether it's locked.
type tmVolume struct {
	name      string
	snapshots []string
	locked    bool
}

// newTMDisks is a Mac with the backup volumes given, in order, on devices
// disk7s2, disk8s2 and on.
func newTMDisks(t *testing.T, volumes ...tmVolume) *tmMac {
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
	for i, v := range volumes {
		device := fmt.Sprintf("disk%ds2", 7+i)
		fmt.Fprintf(&list, `<dict><key>Volumes</key><array><dict><key>DeviceIdentifier</key><string>%s</string><key>Locked</key><%t/><key>Name</key><string>%s</string><key>Roles</key><array><string>Backup</string></array></dict></array></dict>`, device, v.locked, v.name)
		var snaps strings.Builder
		snaps.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Snapshots</key><array>`)
		for j, s := range v.snapshots {
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
// Machine, the latest, or the latest at or before At, stands in for it.
// Opened, it's the old home folder in the snapshot, recorded by
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
	}{
		{"the latest", time.Time{}, "", false, third},
		{"at a time", byTheSeventh, "", true, second},
		{"resumed", time.Time{}, first, false, first},
		{"resumed, named again", time.Time{}, first, true, first},
		{"at a time, resuming", byTheSeventh, first, true, second},
		{"gone, named again", time.Time{}, gone, true, third},
		{"gone, named again at a time", byTheSeventh, gone, true, second},
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
			"Time Machine's backup of 1 Oct 2026 09:00, which the restore began from, isn't on Backups of laptop any more"},
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

// Resuming, the disk holding the snapshot the restore began from is the
// one opened, whatever other disks hold the person's backups; the disk the
// person chose, by its device, is the one opened, its latest backup, and
// one no longer attached is an error saying so.
func TestTimeMachineOpensTheDiskChosen(t *testing.T) {
	first, second, third := snapshot(2026, 11, 1, 9, 0), snapshot(2026, 11, 7, 9, 0), snapshot(2026, 11, 8, 14, 19)
	disks := func(t *testing.T) *tmMac {
		return newTMDisks(t, tmVolume{name: "Backups of laptop", snapshots: []string{first, second}}, tmVolume{name: "Backups of studio", snapshots: []string{third}})
	}
	m := disks(t)
	src, err := TimeMachine{Run: m, User: "someone", MountPoint: m.mp, Snapshot: first}.Open(t.Context())
	if err != nil || src.Origin.Detail != first || src.Home != m.home(first) {
		t.Errorf("resuming from %s, opened %+v, %v", first, src, err)
	} else if err := src.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	m = disks(t)
	src, err = TimeMachine{Run: m, User: "someone", MountPoint: m.mp, Device: "disk8s2"}.Open(t.Context())
	if err != nil || src.Origin.Detail != third {
		t.Errorf("with disk8s2 chosen, opened %+v, %v; want its latest, %s", src, err, third)
	} else if err := src.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	m = disks(t)
	_, err = TimeMachine{Run: m, User: "someone", MountPoint: m.mp, Device: "disk9s2"}.Open(t.Context())
	if want := "the Time Machine disk chosen, disk9s2, no longer holds backups of /Users/someone: is it still attached?"; err == nil || err.Error() != want {
		t.Errorf("with disk9s2 chosen, Open() = %v; want %q", err, want)
	}
}

// The disks the restore can fall back on are those holding the person's
// backups, each by its name, two of one name told apart by their devices,
// each opening its latest backup; and those locked, by name, which can't
// be read till they're unlocked. Those holding none of theirs aren't
// among them.
func TestSourcesDisks(t *testing.T) {
	latest, other := snapshot(2026, 11, 8, 14, 19), snapshot(2026, 11, 2, 9, 0)
	m := newTMDisks(t,
		tmVolume{name: "Backups", snapshots: []string{latest}},
		tmVolume{name: "Vault", locked: true},
		tmVolume{name: "Backups", snapshots: []string{other}},
		tmVolume{name: "Empty"},
	)
	disks, err := Sources{Run: m, User: "someone", MountPoint: m.mp}.Disks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range disks.Found {
		names = append(names, d.Name)
	}
	if want := []string{"Backups (disk7s2)", "Backups (disk9s2)"}; !slices.Equal(names, want) || !slices.Equal(disks.Locked, []string{"Vault"}) {
		t.Errorf("Disks() found %q, locked %q; want %q, and Vault locked", names, disks.Locked, want)
	}
	src, err := disks.Found[1].Open(t.Context())
	if err != nil || src.Origin.Detail != other {
		t.Errorf("opening the second, %+v, %v; want its latest, %s", src, err, other)
	} else if err := src.Close(t.Context()); err != nil {
		t.Fatal(err)
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

// openerOf is a source that opens as opened says.
type openerOf func() (*Opened, error)

func (o openerOf) Open(context.Context) (*Opened, error) { return o() }

// The old Mac is opened as Sources' Mac has it, by its name; Arq's restore
// is the restore's card's, by hand, never opened here.
func TestSourcesForTheOldMacAndArq(t *testing.T) {
	var asked []string
	laptop := &Opened{Host: "192.0.2.1", Origin: Origin{Kind: FromMac, Detail: "laptop"}}
	s := Sources{Run: runnertest.New(t), User: "someone", MountPoint: t.TempDir(), Mac: func(from string) Opener {
		asked = append(asked, from)
		return openerOf(func() (*Opened, error) { return laptop, nil })
	}}
	if got, err := s.For(Origin{Kind: FromMac, Detail: "laptop"}, time.Time{}, false).Open(t.Context()); err != nil || got != laptop || !slices.Equal(asked, []string{"laptop"}) {
		t.Errorf("opening the old Mac = %+v, %v, asking for %q; want laptop", got, err, asked)
	}
	if _, err := s.For(Origin{Kind: FromArq}, time.Time{}, true).Open(t.Context()); err == nil || err.Error() != "Arq's restore is by hand, through the restore's card" {
		t.Errorf("opening Arq's restore: %v; want it refused", err)
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
