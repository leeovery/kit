package arq_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

var now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }

func stats(plans string) string { return `{"arqVersion": "7.0", "backupPlans": [` + plans + `]}` }

func plan(name, schedule string, last time.Time) string {
	return `{"name": "` + name + `", "schedule": {"type": "` + schedule + `"}, "lastBackedUp": "` + last.UTC().Format("2006-01-02T15:04:05.000Z") + `"}`
}

// Arq's step, as kit checks it through its driver: its state, summary,
// glance and its problems' ids, as kit status and the record say them. One
// plan scheduled is Arq's alone, so goes unnamed; several are named.
func TestArq(t *testing.T) {
	for _, tt := range []struct {
		name   string
		out    string
		state  check.State
		text   string
		glance string
		ids    []string
	}{
		{"backed up, a plan with no schedule left out", stats(plan("Old Mac", "Manual", now().Add(-30*24*time.Hour)) + "," + plan("User data", "Daily", now().Add(-11*time.Hour))), check.OK, "last backup 01:00", "Arq 01:00", nil},
		{"several plans, named", stats(plan("User data", "Daily", now().Add(-11*time.Hour)) + "," + plan("Photos", "Daily", now().Add(-9*time.Hour))), check.OK, "last backup Photos 03:00, User data 01:00", "Arq 01:00, 03:00", nil},
		{"several plans, one stale, the other named", stats(plan("User data", "Daily", now().Add(-50*time.Hour)) + "," + plan("Photos", "Daily", now().Add(-9*time.Hour))), check.Attention, "last backup Photos 03:00", "", []string{"arq:stale:user-data"}},
		{"stale", stats(plan("User data", "Daily", now().Add(-50*time.Hour))), check.Attention, "no recent backup", "", []string{"arq:stale:user-data"}},
		{"never", stats(`{"name": "User data", "schedule": {"type": "Daily"}}`), check.Attention, "no recent backup", "", []string{"arq:never:user-data"}},
		{"none scheduled", stats(plan("User data", "Manual", now())), check.Attention, "no plan scheduled", "", []string{"arq:no-plan"}},
		{"unreadable", "Error: the agent isn't running", check.Attention, "status unreadable", "", []string{"arq:unreadable"}},
		{"no plan made", stats(""), check.Attention, "not set up", "", []string{"arq:none"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On(arq.Arqc, "stats").Prints(tt.out)
			res := steps.Backup(fake, arq.Driver{}, now).Check(t.Context())
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
	fake := runnertest.New(t)
	fake.On(arq.Arqc, "stats").Fails(runner.ErrNotFound)
	res := steps.Backup(fake, arq.Driver{}, now).Check(t.Context())
	if res.State != check.Attention || res.Summary != "not installed" || len(res.Items) != 1 || res.Items[0].ID != "arq:missing" {
		t.Errorf("not installed = %+v", res)
	}
}

// arqPlan is Arq's to-do, never set up, in kit's words.
var arqPlan = check.ToDo{
	Title: "Set up Arq's backup plan",
	Why:   "Arq is in, but has nothing to back up to yet.",
	Steps: []string{"Open Arq", "Sign in to your Arq account", "Adopt this Mac's plan, and let it run"},
	Watch: "Arq has a plan",
	Group: "Backups",
}

// Arq just installed, never opened, asks for its licence agreement to be
// accepted: it isn't set up, as Time Machine without a backup disk isn't,
// saying how to set it up, not that its agent doesn't answer; nor is it
// with no plan made. Failing otherwise, its status is unreadable.
func TestArqNeverSetUp(t *testing.T) {
	setUp := check.Item{ID: "arq:none", Name: "Set up Arq's backup plan", State: steps.Problem, Detail: "Open Arq, sign in to your Arq account, and adopt this Mac's plan", ToDo: &arqPlan}
	for _, tt := range []struct {
		name   string
		script func(*runnertest.Fake)
		text   string
		item   check.Item
	}{
		{"licence not accepted", func(f *runnertest.Fake) {
			f.On(arq.Arqc, "stats").Exits(1).PrintsToStderr("Please review the license agreement at https://example.com/license.html and run 'arqc acceptLicenseAgreement' to accept it\n")
		}, "not set up", setUp},
		{"no plan made", func(f *runnertest.Fake) {
			f.On(arq.Arqc, "stats").Prints(`{"backupPlans": []}`)
		}, "not set up", setUp},
		{"agent not answering", func(f *runnertest.Fake) {
			f.On(arq.Arqc, "stats").Exits(1).PrintsToStderr("Error: couldn't connect to the agent\n")
		}, "status unreadable", check.Item{ID: "arq:unreadable", Name: "couldn't read Arq's status", State: steps.Problem, Detail: "open Arq: is its agent running? If it complains, restart the Mac"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			tt.script(fake)
			res := steps.Backup(fake, arq.Driver{}, now).Check(t.Context())
			if res.State != check.Attention || res.Summary != tt.text || len(res.Items) != 1 || !reflect.DeepEqual(res.Items[0], tt.item) {
				t.Errorf("= %+v\nwant %s, %+v", res, tt.text, tt.item)
			}
		})
	}
}

// The driver's own side of the contract: what it says of its plans, as
// data, the stale ones left to its problems; and that it keeps no
// exclusions, following Time Machine's list instead.
func TestArqLooks(t *testing.T) {
	fake := runnertest.New(t)
	fresh := now().Add(-9 * time.Hour)
	fake.On(arq.Arqc, "stats").Prints(stats(plan("User data", "Daily", now().Add(-50*time.Hour)) + "," + plan("Photos", "Daily", fresh)))
	st, err := arq.Driver{}.Look(t.Context(), fake, now())
	want := []struct {
		plan string
		last time.Time
	}{{"Photos", fresh}}
	if err != nil || st.NotSetUp || st.Running || len(st.Backups) != len(want) || st.Backups[0].Plan != want[0].plan || !st.Backups[0].Last.Equal(want[0].last) || len(st.Problems) != 1 || st.Problems[0].Key != "stale:user-data" {
		t.Errorf("Look() = %+v, %v; want Photos' backup, User data's a problem", st, err)
	}
	about := arq.Driver{}.About()
	if about.ID != arq.ID || about.Name != "Arq" || about.Follows != timemachine.ID || (arq.Driver{}).Excluder() != nil {
		t.Errorf("About() = %+v, Excluder() = %v; want Arq keeping no exclusions, following Time Machine's", about, arq.Driver{}.Excluder())
	}
}
