package timemachine_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

var now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }

// tmPrefs is Time Machine's settings, as defaults exports them: automatic
// backups as given, and one destination whose backups are dates.
func tmPrefs(auto string, dates ...time.Time) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AutoBackup</key>
	` + auto + `
	<key>AutoBackupInterval</key>
	<integer>3600</integer>
	<key>Destinations</key>
	<array>
		<dict>
			<key>BackupAlias</key>
			<data>
			AAAAAAEcAAIAAA==
			</data>
			<key>LastKnownVolumeName</key>
			<string>Backups</string>
			<key>SnapshotDates</key>
			<array>
`)
	for _, d := range dates {
		b.WriteString("\t\t\t\t<date>" + d.UTC().Format(time.RFC3339) + "</date>\n")
	}
	b.WriteString(`			</array>
		</dict>
	</array>
	<key>SkipPaths</key>
	<array/>
</dict>
</plist>
`)
	return b.String()
}

const (
	tmIdle      = "Backup session status:\n{\n    ClientID = \"com.apple.backupd\";\n    Percent = \"-1\";\n    Running = 0;\n}\n"
	tmConnected = "====================================================\nName          : Backups\nKind          : Local\nMount Point   : /Volumes/Backups\nID            : 0000\n"
	tmAway      = "====================================================\nName          : Backups\nKind          : Local\nID            : 0000\n"
)

func tmStuck(since time.Time) string {
	return "Backup session status:\n{\n    BackupPhase = Copying;\n    DateOfStateChange = \"" + since.Format("2006-01-02 15:04:05 -0700") + "\";\n    Running = 1;\n}\n"
}

var prefs = []string{"export", timemachine.Prefs, "-"}

// Time Machine's step, as kit checks it through its driver: its state,
// summary, glance and its problems' ids, as kit status and the record say
// them.
func TestTimeMachine(t *testing.T) {
	recent := now().Add(-40 * time.Minute)
	old := now().Add(-5 * time.Hour)
	for _, tt := range []struct {
		name   string
		script func(*runnertest.Fake)
		state  check.State
		text   string
		glance string
		ids    []string
	}{
		{"backing up", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<true/>", old, recent))
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.OK, "last backup 11:20", "Time Machine 11:20", nil},
		{"automatic backups off, as an integer", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<integer>0</integer>", recent))
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.Attention, "last backup 11:20", "", []string{"time-machine:off"}},
		{"stale, the disk away", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<integer>1</integer>", old))
			f.On("tmutil", "destinationinfo").Prints(tmAway)
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.Attention, "last backup 07:00", "", []string{"time-machine:stale"}},
		{"stuck", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<true/>", recent))
			f.On("tmutil", "status").Prints(tmStuck(now().Add(-4 * time.Hour)))
		}, check.Attention, "last backup 11:20", "", []string{"time-machine:stuck"}},
		{"never set up", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Exits(1).PrintsToStderr("file does not exist")
		}, check.Attention, "not set up", "", []string{"time-machine:none"}},
		{"a disk just added", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<true/>"))
			f.On("tmutil", "destinationinfo").Prints(tmConnected)
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.OK, "first backup soon", "Time Machine's first backup soon", nil},
		{"its first backup under way", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<true/>"))
			f.On("tmutil", "status").Prints(tmStuck(now().Add(-10 * time.Minute)))
		}, check.OK, "first backup under way", "Time Machine's first backup under way", nil},
		{"no backup yet, one tried", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(strings.Replace(tmPrefs("<true/>"), "<key>SnapshotDates</key>", "<key>AttemptDates</key>\n\t\t\t<array><date>"+recent.UTC().Format(time.RFC3339)+"</date></array>\n\t\t\t<key>SnapshotDates</key>", 1))
			f.On("tmutil", "destinationinfo").Prints(tmConnected)
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.Attention, "no backup yet", "", []string{"time-machine:never"}},
		{"no backup yet, the disk away", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<true/>"))
			f.On("tmutil", "destinationinfo").Prints(tmAway)
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.Attention, "no backup yet", "", []string{"time-machine:never"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			tt.script(fake)
			res := steps.Backup(fake, timemachine.Driver{}, now).Check(t.Context())
			var ids []string
			for _, it := range res.Items {
				ids = append(ids, it.ID)
			}
			if res.State != tt.state || res.Summary != tt.text || res.Glance != tt.glance || !slices.Equal(ids, tt.ids) {
				t.Errorf("= %s %q %q %q; want %s %q %q %q", res.State, res.Summary, res.Glance, ids, tt.state, tt.text, tt.glance, tt.ids)
			}
			if res.State == check.Attention && !res.ToDo() {
				t.Errorf("%+v, want it the person's to do", res)
			}
		})
	}
	// A stale backup with the disk connected says nothing of the disk.
	fake := runnertest.New(t)
	fake.On("defaults", prefs...).Prints(tmPrefs("<true/>", old))
	fake.On("tmutil", "destinationinfo").Prints(tmConnected)
	fake.On("tmutil", "status").Prints(tmIdle)
	res := steps.Backup(fake, timemachine.Driver{}, now).Check(t.Context())
	if len(res.Items) != 1 || res.Items[0].Name != "last backup 5 hours ago" {
		t.Errorf("stale, the disk connected: %+v", res.Items)
	}
}

// Time Machine with no backup disk is the person's to set up: its to-do,
// in its words, one wording wherever it shows.
func TestTimeMachineToDo(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("defaults", prefs...).Exits(1).PrintsToStderr("file does not exist")
	res := steps.Backup(fake, timemachine.Driver{}, now).Check(t.Context())
	want := check.Item{
		ID: "time-machine:none", Name: "Add a Time Machine disk", State: steps.Problem,
		Detail: "Plug in your backup disk, then System Settings › General › Time Machine › Add Backup Disk",
		ToDo: &check.ToDo{
			Title: "Add a Time Machine disk",
			Why:   "Time Machine has nowhere to back up to.",
			Steps: []string{"Plug in your backup disk", "System Settings › General › Time Machine › Add Backup Disk, and choose it"},
			Watch: "Time Machine has a disk",
			Group: "Backups",
		}}
	if !res.ToDo() || len(res.Items) != 1 || !reflect.DeepEqual(res.Items[0], want) {
		t.Errorf("items = %+v\nwant %+v, %+v", res.Items, want, want.ToDo)
	}
}

// The driver's own side of the contract: what it says of its backups, as
// data; settings that won't read are an error, not a status.
func TestTimeMachineLooks(t *testing.T) {
	fake := runnertest.New(t)
	recent := now().Add(-40 * time.Minute)
	fake.On("defaults", prefs...).Prints(tmPrefs("<true/>", recent))
	fake.On("tmutil", "status").Prints(tmStuck(now().Add(-10 * time.Minute)))
	st, err := timemachine.Driver{}.Look(t.Context(), fake, now())
	if err != nil || st.NotSetUp || !st.Running || len(st.Backups) != 1 || !st.Backups[0].Last.Equal(recent) || st.Backups[0].Plan != "" || st.Summary != "" || len(st.Problems) != 0 {
		t.Errorf("Look() = %+v, %v; want its one backup's last, one running, its words left to kit", st, err)
	}
	fake = runnertest.New(t)
	fake.On("defaults", prefs...).Prints("not a plist")
	if _, err := (timemachine.Driver{}).Look(t.Context(), fake, now()); err == nil || err.Error() != "couldn't read Time Machine's settings" {
		t.Errorf("Look() error = %v, want the settings unread", err)
	}
	about := timemachine.Driver{}.About()
	if about.ID != timemachine.ID || about.Name != "Time Machine" || about.Follows != "" || (timemachine.Driver{}).Excluder() == nil {
		t.Errorf("About() = %+v; want Time Machine keeping its own exclusions", about)
	}
}

// Time Machine keeps exclusions as its own: fixed-path ones in its
// settings, kept and taken out through sudo; one where a folder is now with
// no password, read back with tmutil isexcluded.
func TestTimeMachineExcludes(t *testing.T) {
	ex := timemachine.Driver{}.Excluder()
	fake := runnertest.New(t)
	fake.On("defaults", prefs...).Prints("<plist><dict><key>SkipPaths</key><array><string>/Users/someone/.cache</string><string>/Users/someone/Movies</string></array></dict></plist>")
	fake.On("tmutil", "isexcluded", "/Users/someone/one", "/Users/someone/two").Prints("[Included]  /Users/someone/one\n[Excluded]  /Users/someone/two\n")
	got, err := ex.Excluded(t.Context(), fake, []string{"/Users/someone/.cache", "/Users/someone/one", "/Users/someone/two"})
	if want := []string{"/Users/someone/.cache", "/Users/someone/Movies", "/Users/someone/two"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Excluded() = %q, %v; want %q: the fixed ones, and two, excluded where it is, the fixed one not asked of again", got, err, want)
	}
	fake.On("tmutil", "addexclusion", "/Users/someone/one")
	fake.On("sudo", "-n", "tmutil", "addexclusion", "-p", "/Applications")
	fake.On("sudo", "-n", "tmutil", "removeexclusion", "-p", "/Users/someone/Movies")
	if err := ex.Exclude(t.Context(), fake, "/Users/someone/one", false); err != nil {
		t.Errorf("Exclude() where it is = %v", err)
	}
	if err := ex.Exclude(t.Context(), fake, "/Applications", true); err != nil {
		t.Errorf("Exclude() fixed = %v", err)
	}
	if err := ex.Include(t.Context(), fake, "/Users/someone/Movies"); err != nil {
		t.Errorf("Include() = %v", err)
	}
	// Never set up, Time Machine has no settings, so no exclusions yet.
	never := runnertest.New(t)
	never.On("defaults", prefs...).Exits(1).PrintsToStderr("file does not exist")
	if got, err := ex.Excluded(t.Context(), never, nil); err != nil || len(got) != 0 {
		t.Errorf("Excluded(), never set up = %q, %v; want none", got, err)
	}
	if !ex.NeedsAdmin(true) || ex.NeedsAdmin(false) || ex.Program() != "tmutil" {
		t.Errorf("NeedsAdmin(fixed) = %v, NeedsAdmin(where it is) = %v, Program() = %q; want the password for fixed alone, through tmutil", ex.NeedsAdmin(true), ex.NeedsAdmin(false), ex.Program())
	}
}
