package steps_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

func TestPrefs(t *testing.T) {
	recent, old := now().Add(-2*time.Hour).Format(time.RFC3339), now().Add(-40*time.Hour).Format(time.RFC3339)
	for _, tt := range []struct {
		name, record string
		state        check.State
		text, id     string
	}{
		{"captured", `{"on": "started fresh", "captured": "` + recent + `", "summary": "57 domains, 2 changed; 1056 files, 0 changed"}`, check.OK, "saved 10:00", ""},
		{"never switched on", `{}`, check.Attention, "paused: capture isn't switched on for this Mac", "prefs:paused"},
		{"just switched on", `{"on": "started fresh", "on_at": "` + recent + `"}`, check.OK, "saving from tonight", ""},
		{"not captured yet", `{"on": "started fresh", "on_at": "` + old + `"}`, check.Attention, "not captured yet", "prefs:never"},
		{"not captured yet, from before kit kept when", `{"on": "started fresh"}`, check.Attention, "not captured yet", "prefs:never"},
		{"stale", `{"on": "started fresh", "captured": "` + old + `"}`, check.Attention, "last capture", "prefs:stale"},
		{"errors", `{"on": "started fresh", "captured": "` + recent + `", "summary": "57 domains; 1 errors", "errors": 1}`, check.Attention, "57 domains; 1 errors", "prefs:errors"},
		{"unpushed for a day", `{"on": "started fresh", "captured": "` + recent + `", "unpushed": "` + old + `", "push_error": "offline"}`, check.Attention, "not pushed since", "prefs:unpushed"},
		{"another Mac's folder", `{"on": "restored", "not_owner": "the store's laptop folder belongs to another Mac"}`, check.Attention, "another Mac owns this Mac's folder", "prefs:not-owner"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := t.TempDir()
			if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(tt.record), 0o600); err != nil {
				t.Fatal(err)
			}
			p := prefs.New(runnertest.New(t), t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{}, now)
			res := steps.Prefs(p, now).Check(t.Context())
			if res.State != tt.state || !strings.Contains(res.Summary, tt.text) || (tt.id != "" && (len(res.Items) != 1 || res.Items[0].ID != tt.id)) {
				t.Errorf("result = %+v", res)
			}
			if res.State == check.Attention && (!res.ToDo() || res.Items[0].Detail == "") {
				t.Errorf("result = %+v, want it the person's to do, saying what to do", res)
			}
		})
	}
}

// Apps' settings are yet to come to this Mac till capture's switched on, by
// a restore or by starting fresh, as the step checking them says it's
// paused.
func TestSettingsToCome(t *testing.T) {
	for record, want := range map[string]bool{`{}`: true, `{"on": "restored from studio"}`: false, `{"on": "started fresh"}`: false} {
		state := t.TempDir()
		if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(record), 0o600); err != nil {
			t.Fatal(err)
		}
		p := prefs.New(runnertest.New(t), t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{}, now)
		if got := steps.SettingsToCome(p)(); got != want {
			t.Errorf("%s: SettingsToCome() = %v, want %v", record, got, want)
		}
	}
}

// Apps' settings' to-do is a choice. As it comes up, r says the date of
// this Mac's last save, the store cloned first when it isn't, and nothing
// when there's none to say. r brings them back, as kit prefs restore does,
// saying how many and from when, and how many wait for their apps; f starts
// afresh, as kit prefs start-fresh does; either switches capture on, so
// it's done. Nothing to bring back, r says why, and it isn't.
func TestAppsSettingsChoice(t *testing.T) {
	state, clone := t.TempDir(), filepath.Join(t.TempDir(), "prefs")
	if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
		fake.On("git", "-C", clone, "config", kv[0], kv[1])
	}
	fake.On("git", "clone", "--quiet", "git@github.com:someone/prefs.git", clone).Does(func() {
		if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o700); err != nil {
			t.Error(err)
		}
	})
	fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", "laptop").Prints("2026-10-04T03:00:00Z\n")
	fake.On("git", "-C", clone, "pull", "--rebase", "--autostash", "--quiet")
	fake.On("git", "-C", clone, "rev-parse", "HEAD").Prints("abc123\n")
	p := prefs.New(fake, t.TempDir(), state, clone, "git@github.com:someone/prefs.git", "laptop", prefs.Lists{}, now)
	p.Probes, p.AppDirs = nil, []string{t.TempDir()}
	step := steps.Prefs(p, now)
	paused := step.Check(t.Context()).Items[0]

	ready := step.Choice.Ready(t.Context(), paused)
	if got := ready.ToDo.Keys[0].Does; got != "brings them back from this Mac's last save, 4 Oct" || paused.ToDo.Keys[0].Does != "brings them back from this Mac's last save" {
		t.Errorf("readied, r says %q, and the check's to-do %q; want the date added to a copy", got, paused.ToDo.Keys[0].Does)
	}
	if _, err := step.Choice.Answer(t.Context(), paused, "r"); err == nil || err.Error() != "no settings stored for laptop" {
		t.Errorf("with nothing saved, r = %v; want it to say so", err)
	}
	if step.Check(t.Context()).Items[0].ID != "prefs:paused" {
		t.Error("r failing, capture was switched on")
	}
	stored := filepath.Join(clone, "laptop", "domains", "com.example.app.plist")
	if err := os.MkdirAll(filepath.Dir(stored), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stored, []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict/></plist>`), 0o600); err != nil {
		t.Fatal(err)
	}
	said, err := step.Choice.Answer(t.Context(), paused, "r")
	if err != nil || said != "0 apps' settings, from 4 Oct; 1 wait for their apps" {
		t.Errorf("r = %q, %v; want how many, from when, and how many wait", said, err)
	}
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "saving from tonight" {
		t.Errorf("brought back, the check says %+v; want capture switched on, fine for now", res)
	}

	fresh := t.TempDir()
	if err := os.WriteFile(filepath.Join(fresh, "prefs.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	afresh := steps.Prefs(prefs.New(runnertest.New(t), t.TempDir(), fresh, t.TempDir(), "", "laptop", prefs.Lists{}, now), now)
	if said, err := afresh.Choice.Answer(t.Context(), paused, "f"); err != nil || said != "started afresh, saving from now" {
		t.Errorf("f = %q, %v", said, err)
	}
	if res := afresh.Check(t.Context()); res.State != check.OK || res.Summary != "saving from tonight" {
		t.Errorf("started afresh, the check says %+v; want capture switched on, fine for now", res)
	}
	if ready := afresh.Choice.Ready(t.Context(), paused); ready.ToDo.Keys[0].Does != "brings them back from this Mac's last save" {
		t.Errorf("with no store to look at, r says %q; want no date", ready.ToDo.Keys[0].Does)
	}
}
