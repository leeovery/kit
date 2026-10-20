package steps_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
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

// configures scripts kit setting up git in the store's clone.
func configures(fake *runnertest.Fake, clone string) {
	for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
		fake.On("git", "-C", clone, "config", kv[0], kv[1])
	}
}

// Apps' settings' to-do is a choice. As it comes up, r says the date of
// this Mac's last save, the store cloned first when it isn't, and nothing
// when there's none to say. r brings them back, as kit prefs restore does,
// saying how many and from when, and how many wait for their apps; f starts
// afresh, as kit prefs start-fresh does, saying nothing came back; either
// switches capture on, so it's done. Nothing to bring back, r says why, and
// it isn't.
func TestAppsSettingsChoice(t *testing.T) {
	state, clone := t.TempDir(), filepath.Join(t.TempDir(), "prefs")
	if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	configures(fake, clone)
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
	if err != nil || said != "0 apps' settings, from 4 Oct; 1 waits for its app" {
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
	if said, err := afresh.Choice.Answer(t.Context(), paused, "f"); err != nil || said != "saving from now, nothing brought back" {
		t.Errorf("f = %q, %v", said, err)
	}
	if res := afresh.Check(t.Context()); res.State != check.OK || res.Summary != "saving from tonight" {
		t.Errorf("started afresh, the check says %+v; want capture switched on, fine for now", res)
	}
	if ready := afresh.Choice.Ready(t.Context(), paused); ready.ToDo.Keys[0].Does != "brings them back from this Mac's last save" {
		t.Errorf("with no store to look at, r says %q; want no date", ready.ToDo.Keys[0].Does)
	}
}

// Apps' settings' to-do says up front that bringing them back opens some
// apps, and what may follow; and, only when the config keeps settings
// files beside the apps' settings, that those come back too. It names no
// app.
func TestAppsSettingsWhy(t *testing.T) {
	opens := "Bringing them back opens some apps once: their windows, notifications and permission prompts may follow."
	for _, tt := range []struct {
		name  string
		files []string
		too   bool
	}{{"no settings files", nil, false}, {"settings files", []string{"~/.config/tool/settings.json"}, true}} {
		t.Run(tt.name, func(t *testing.T) {
			state := t.TempDir()
			if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			p := prefs.New(runnertest.New(t), t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{Files: tt.files}, now)
			why := steps.Prefs(p, now).Check(t.Context()).Items[0].ToDo.Why
			if !strings.HasPrefix(why, "First, so the apps you open next start with yours. "+opens) {
				t.Errorf("why = %q; want it to say first that bringing them back opens some apps", why)
			}
			if got := strings.HasSuffix(why, " Settings files come back too."); got != tt.too {
				t.Errorf("why = %q; want settings files said only when the config keeps some", why)
			}
		})
	}
}

// An app that couldn't be brought back is named in the log, with why, and
// counted in what r says; saving switches on all the same, so the to-do's
// done, the app held aside to be tried again. f, after, says saving's on
// and how many still wait, not that the restore was thrown away.
func TestAppsSettingsFailedApp(t *testing.T) {
	state, clone, home, appDir := t.TempDir(), filepath.Join(t.TempDir(), "prefs"), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	empty := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict/></plist>`
	write(filepath.Join(clone, ".git", "HEAD"), "")
	write(filepath.Join(clone, "laptop", "domains", "com.example.boxed.plist"), empty)
	app := filepath.Join(appDir, "Boxed App.app")
	write(filepath.Join(app, "Contents", "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.boxed</string></dict></plist>`)
	fake := runnertest.New(t)
	configures(fake, clone)
	fake.On("git", "-C", clone, "pull", "--rebase", "--autostash", "--quiet")
	fake.On("git", "-C", clone, "rev-parse", "HEAD").Prints("abc123\n")
	fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", "laptop").Prints("2026-10-04T03:00:00Z\n")
	fake.On("osascript", "-e", `application id "com.example.boxed" is running`).Prints("false\n")
	fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints("<key>com.apple.security.app-sandbox</key><true/>")
	fake.On("open", "-g", "-j", "-b", "com.example.boxed")
	fake.On("osascript", "-e", `tell application id "com.example.boxed" to quit`)
	p := prefs.New(fake, home, state, clone, "git@github.com:someone/prefs.git", "laptop", prefs.Lists{}, now)
	p.Probes, p.AppDirs, p.Sleep = nil, []string{appDir}, func(time.Duration) {}
	step := steps.Prefs(p, now)
	paused := step.Check(t.Context()).Items[0]

	var logged []event.Logged
	ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l) })
	said, err := step.Choice.Answer(ctx, paused, "r")
	if err != nil || said != "0 apps' settings, from 4 Oct; 1 didn't come back, tried again hourly" {
		t.Errorf("r = %q, %v; want how many didn't come back, and that they're tried again", said, err)
	}
	want := []event.Logged{
		{Line: "Bringing back apps' settings from this Mac's last save"},
		{Line: "Opening Boxed App once, for its settings folder"},
		{Line: "Boxed App's settings didn't come back: opened once, it still has no settings folder", Alert: true},
	}
	if !slices.Equal(logged, want) {
		t.Errorf("the log says %+v\nwant %+v", logged, want)
	}
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "saving from tonight" {
		t.Errorf("with an app failed, the check says %+v; want saving switched on all the same", res)
	}
	if said, err := step.Choice.Answer(t.Context(), paused, "f"); err != nil || said != "saving from now; 1 still waits to come back" {
		t.Errorf("f after r = %q, %v; want saving on, and how many still wait", said, err)
	}
}
