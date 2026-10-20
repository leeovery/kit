package steps_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/backup/tool"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// A [backups] line names a tool kit knows, then what its check comes
// after and before, as a step's line does; a tool kit doesn't know is
// refused, naming those it has.
func TestParseBackup(t *testing.T) {
	for _, tc := range []struct {
		name, value, wrong string
		after, before      []string
	}{
		{name: "time-machine"},
		{name: "arq", value: "--after cask:arq --before nightly --after brew", after: []string{"cask:arq", "brew"}, before: []string{"nightly"}},
		{name: "time-capsule", wrong: "laptop/declarations:4: kit doesn't know the backup tool time-capsule: one of arq, time-machine"},
		{name: "arq", value: "--sideways", wrong: `laptop/declarations:4: "--sideways" isn't one of a backup tool's options: --after and --before`},
		{name: "arq", value: "--after", wrong: "laptop/declarations:4: --after needs a step's name after it"},
		{name: "arq", value: "--before cask:arq", wrong: "--before a step"},
		{name: "arq", value: `--after "cask:arq`, wrong: "a quote isn't closed"},
	} {
		got, err := steps.ParseBackup(config.Entry{Name: tc.name, Value: tc.value, Scope: "laptop", Line: 4})
		switch {
		case tc.wrong != "":
			if err == nil || !strings.Contains(err.Error(), tc.wrong) {
				t.Errorf("%s %s: err = %v, want %q", tc.name, tc.value, err, tc.wrong)
			}
			continue
		case err != nil:
			t.Errorf("%s %s: %v", tc.name, tc.value, err)
			continue
		}
		if got.Driver.About().ID != tc.name || !slices.Equal(got.After, tc.after) || !slices.Equal(got.Before, tc.before) || got.Pos != "laptop/declarations:4" {
			t.Errorf("%s %s = %+v", tc.name, tc.value, got)
		}
	}
}

// fakeTool is a backup tool whose backups stand as status says, or that
// can't be looked at, err.
type fakeTool struct {
	status tool.Status
	err    error
}

func (f fakeTool) About() tool.About {
	return tool.About{ID: "vault", Name: "Vault", SetUp: tool.SetUp{
		Title: "Set up Vault", Why: "Vault has nowhere to back up to.", Steps: []string{"Open Vault", "Choose a disk"},
		Watch: "Vault has a disk", Do: "Open Vault, and choose a disk",
	}}
}

func (f fakeTool) Look(context.Context, runner.Runner, time.Time) (tool.Status, error) {
	return f.status, f.err
}

func (f fakeTool) Excluder() tool.Excluder { return nil }

// A backup tool's step says what its driver does, in kit's words where the
// driver leaves them to kit: its summary from when its backups last
// finished, a plan named, or, none finished, the first under way or none
// yet; its glance its name and those times, or its summary; its problems
// keyed by the tool; and setting it up the person's to-do, in the Backups
// group.
func TestBackupStep(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 5, h, m, 0, 0, time.UTC) }
	for _, tt := range []struct {
		name   string
		status tool.Status
		state  check.State
		text   string
		glance string
		ids    []string
	}{
		{"one backup", tool.Status{Backups: []tool.Backup{{Last: at(11, 20)}}}, check.OK, "last backup 11:20", "Vault 11:20", nil},
		{"a day before", tool.Status{Backups: []tool.Backup{{Last: at(11, 20).Add(-24 * time.Hour)}}}, check.OK, "last backup 4 Oct 11:20", "Vault 4 Oct 11:20", nil},
		{"plans, named and sorted", tool.Status{Backups: []tool.Backup{{Plan: "Work", Last: at(9, 0)}, {Plan: "Home", Last: at(10, 0)}}}, check.OK, "last backup Home 10:00, Work 09:00", "Vault 09:00, 10:00", nil},
		{"the first under way", tool.Status{Backups: []tool.Backup{{}}, Running: true}, check.OK, "first backup under way", "Vault's first backup under way", nil},
		{"none finished, its words left to kit", tool.Status{Backups: []tool.Backup{{}}}, check.OK, "no backup yet", "Vault's no backup yet", nil},
		{"none finished, in its words", tool.Status{Backups: []tool.Backup{{}}, Summary: "first backup soon"}, check.OK, "first backup soon", "Vault's first backup soon", nil},
		{"its own words", tool.Status{Summary: "resting", Backups: []tool.Backup{{Last: at(11, 20)}}}, check.OK, "resting", "Vault 11:20", nil},
		{"problems", tool.Status{Backups: []tool.Backup{{Last: at(11, 20)}}, Problems: []tool.Problem{{Key: "off", What: "it's off", Do: "turn it on"}, {Key: "stale:home", What: "Home: old", Do: "look"}}}, check.Attention, "last backup 11:20", "", []string{"vault:off", "vault:stale:home"}},
		{"not set up", tool.Status{NotSetUp: true, Summary: "ignored"}, check.Attention, "not set up", "", []string{"vault:none"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			step := steps.Backup(runnertest.New(t), fakeTool{status: tt.status}, now)
			res := step.Check(t.Context())
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
			if step.Name != "vault" || step.Title != "Vault" || step.Area != steps.AreaBackups || step.Row != "Backups" {
				t.Errorf("step = %s %q %s %s, want the tool's, in Backups", step.Name, step.Title, step.Area, step.Row)
			}
		})
	}
	res := steps.Backup(runnertest.New(t), fakeTool{status: tool.Status{NotSetUp: true}}, now).Check(t.Context())
	want := check.Item{ID: "vault:none", Name: "Set up Vault", State: steps.Problem, Detail: "Open Vault, and choose a disk", ToDo: &check.ToDo{
		Title: "Set up Vault", Why: "Vault has nowhere to back up to.", Steps: []string{"Open Vault", "Choose a disk"}, Watch: "Vault has a disk", Group: "Backups",
	}}
	if len(res.Items) != 1 || !reflect.DeepEqual(res.Items[0], want) {
		t.Errorf("not set up: items = %+v\nwant %+v, %+v", res.Items, want, want.ToDo)
	}
	res = steps.Backup(runnertest.New(t), fakeTool{err: errors.New("couldn't read Vault's settings")}, now).Check(t.Context())
	if res.State != check.Failed || res.Reason != "couldn't read Vault's settings" {
		t.Errorf("unread: %+v, want failed, saying why", res)
	}
}
