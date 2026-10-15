package steps_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

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
)

func tmStuck(since time.Time) string {
	return "Backup session status:\n{\n    BackupPhase = Copying;\n    DateOfStateChange = \"" + since.Format("2006-01-02 15:04:05 -0700") + "\";\n    Running = 1;\n}\n"
}

func TestTimeMachine(t *testing.T) {
	prefs := []string{"export", "/Library/Preferences/com.apple.TimeMachine", "-"}
	recent := now().Add(-40 * time.Minute)
	old := now().Add(-5 * time.Hour)
	for _, tt := range []struct {
		name   string
		script func(*runnertest.Fake)
		state  check.State
		text   string
		ids    []string
	}{
		{"backing up", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<true/>", old, recent))
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.OK, "last backup 11:20", nil},
		{"automatic backups off, as an integer", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<integer>0</integer>", recent))
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.Attention, "last backup 11:20", []string{"time-machine:off"}},
		{"stale, the disk away", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<integer>1</integer>", old))
			f.On("tmutil", "destinationinfo").Prints("====================================================\nName          : Backups\nKind          : Local\nID            : 0000\n")
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.Attention, "last backup 07:00", []string{"time-machine:stale"}},
		{"stuck", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<true/>", recent))
			f.On("tmutil", "status").Prints(tmStuck(now().Add(-4 * time.Hour)))
		}, check.Attention, "last backup 11:20", []string{"time-machine:stuck"}},
		{"never set up", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Exits(1).PrintsToStderr("file does not exist")
		}, check.Attention, "not set up", []string{"time-machine:none"}},
		{"no backup yet", func(f *runnertest.Fake) {
			f.On("defaults", prefs...).Prints(tmPrefs("<true/>"))
			f.On("tmutil", "status").Prints(tmIdle)
		}, check.Attention, "no backup yet", []string{"time-machine:never"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			tt.script(fake)
			state, text, ids := checked(t, steps.TimeMachine(fake, now))
			if state != tt.state || text != tt.text || !slices.Equal(ids, tt.ids) {
				t.Errorf("= %s %q %q; want %s %q %q", state, text, ids, tt.state, tt.text, tt.ids)
			}
		})
	}
	// A stale backup with the disk connected says nothing of the disk.
	fake := runnertest.New(t)
	fake.On("defaults", prefs...).Prints(tmPrefs("<true/>", old))
	fake.On("tmutil", "destinationinfo").Prints(tmConnected)
	fake.On("tmutil", "status").Prints(tmIdle)
	res := steps.TimeMachine(fake, now).Check(t.Context())
	if len(res.Items) != 1 || res.Items[0].Name != "last backup 5 hours ago" {
		t.Errorf("stale, the disk connected: %+v", res.Items)
	}
}

func TestArq(t *testing.T) {
	stats := func(plans string) string { return `{"arqVersion": "7.0", "backupPlans": [` + plans + `]}` }
	plan := func(name, schedule string, last time.Time) string {
		return `{"name": "` + name + `", "schedule": {"type": "` + schedule + `"}, "lastBackedUp": "` + last.UTC().Format("2006-01-02T15:04:05.000Z") + `"}`
	}
	for _, tt := range []struct {
		name  string
		out   string
		state check.State
		text  string
		ids   []string
	}{
		{"backed up, a plan with no schedule left out", stats(plan("Old Mac", "Manual", now().Add(-30*24*time.Hour)) + "," + plan("User data", "Daily", now().Add(-11*time.Hour))), check.OK, "last backup 01:00", nil},
		{"stale", stats(plan("User data", "Daily", now().Add(-50*time.Hour))), check.Attention, "no recent backup", []string{"arq:stale:user-data"}},
		{"never", stats(`{"name": "User data", "schedule": {"type": "Daily"}}`), check.Attention, "no recent backup", []string{"arq:never:user-data"}},
		{"none scheduled", stats(plan("User data", "Manual", now())), check.Attention, "no plan scheduled", []string{"arq:no-plan"}},
		{"unreadable", "Error: the agent isn't running", check.Attention, "status unreadable", []string{"arq:unreadable"}},
		{"no plan made", stats(""), check.Attention, "not set up", []string{"arq:none"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On(steps.Arqc, "stats").Prints(tt.out)
			state, text, ids := checked(t, steps.Arq(fake, now))
			if state != tt.state || text != tt.text || !slices.Equal(ids, tt.ids) {
				t.Errorf("= %s %q %q; want %s %q %q", state, text, ids, tt.state, tt.text, tt.ids)
			}
		})
	}
	fake := runnertest.New(t)
	fake.On(steps.Arqc, "stats").Fails(runner.ErrNotFound)
	if state, _, ids := checked(t, steps.Arq(fake, now)); state != check.Attention || !slices.Equal(ids, []string{"arq:missing"}) {
		t.Errorf("not installed = %s %q", state, ids)
	}
}

// Arq just installed, never opened, asks for its licence agreement to be
// accepted: it isn't set up, as Time Machine without a backup disk isn't,
// saying how to set it up, not that its agent doesn't answer. Failing
// otherwise, its status is unreadable.
func TestArqNeverSetUp(t *testing.T) {
	for _, tt := range []struct {
		name, stderr string
		text         string
		item         check.Item
	}{
		{"licence not accepted", "Please review the license agreement at https://example.com/license.html and run 'arqc acceptLicenseAgreement' to accept it\n", "not set up",
			check.Item{ID: "arq:none", Name: "no backup plan is chosen yet", State: steps.Problem, Detail: "open Arq, sign in, then choose a backup plan"}},
		{"agent not answering", "Error: couldn't connect to the agent\n", "status unreadable",
			check.Item{ID: "arq:unreadable", Name: "couldn't read Arq's status", State: steps.Problem, Detail: "open Arq: is its agent running? If it complains, restart the Mac"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On(steps.Arqc, "stats").Exits(1).PrintsToStderr(tt.stderr)
			res := steps.Arq(fake, now).Check(t.Context())
			if res.State != check.Attention || res.Summary != tt.text || len(res.Items) != 1 || res.Items[0] != tt.item {
				t.Errorf("= %+v\nwant %s, %+v", res, tt.text, tt.item)
			}
		})
	}
}
