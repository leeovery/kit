package steps_test

import (
	"os"
	"path/filepath"
	"reflect"
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
	fake.On("git", "clone", "--quiet", "git@github.com:someone/prefs.git", clone+".cloning").Does(func() {
		if err := os.MkdirAll(filepath.Join(clone+".cloning", ".git"), 0o700); err != nil {
			t.Error(err)
		}
	})
	fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", "laptop").Prints("2026-10-04T03:00:00Z\n")
	fake.On("git", "-C", clone, "remote")
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
	if err != nil || said != "no apps' settings, from 4 Oct; 1 waits for its app" {
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

// r names, in the log, each app whose settings failed otherwise, with why,
// and says how many apps are to be opened once and how many are held for
// kit prefs restore --pending; saving switches on all the same. Each app to
// open is then a to-do, by name, watched: once it has its settings folder
// and is quit, it's done, and settling it brings its settings back, opening
// nothing, leaving what failed otherwise alone, which the check goes on
// saying is held.
func TestAppsSettingsAppsToOpen(t *testing.T) {
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
	write(filepath.Join(clone, ".git", "HEAD"), "")
	theme := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Theme</key><string>dark</string></dict></plist>`
	fake := runnertest.New(t)
	apps := map[string]string{}
	for _, a := range [][2]string{{"Boxed App", "com.example.boxed"}, {"Plain", "com.example.plain"}, {"Stubborn", "com.example.stubborn"}} {
		apps[a[1]] = filepath.Join(appDir, a[0]+".app")
		write(filepath.Join(clone, "laptop", "domains", a[1]+".plist"), theme)
		write(filepath.Join(apps[a[1]], "Contents", "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>`+a[1]+`</string></dict></plist>`)
		fake.On("osascript", "-e", `application id "`+a[1]+`" is running`).Prints("false\n")
		fake.On("defaults", "delete", a[1])
	}
	configures(fake, clone)
	fake.On("git", "-C", clone, "remote")
	fake.On("git", "-C", clone, "rev-parse", "HEAD").Prints("abc123\n")
	fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", "laptop").Prints("2026-10-04T03:00:00Z\n")
	fake.On("codesign", "-d", "--entitlements", "-", "--xml", apps["com.example.boxed"]).Prints("<key>com.apple.security.app-sandbox</key><true/>")
	fake.On("open", "-g", "-j", "-b", "com.example.boxed")
	fake.On("osascript", "-e", `tell application id "com.example.boxed" to quit`)
	for _, id := range []string{"com.example.boxed", "com.example.plain"} {
		fake.On("defaults", "export", id, "-").Exits(1).Then().Prints(theme)
		fake.On("defaults", "import", id, filepath.Join(clone, "laptop", "domains", id+".plist"))
	}
	fake.On("codesign", "-d", "--entitlements", "-", "--xml", apps["com.example.plain"])
	fake.On("codesign", "-d", "--entitlements", "-", "--xml", apps["com.example.stubborn"])
	fake.On("defaults", "export", "com.example.stubborn", "-").Exits(1)
	fake.On("defaults", "import", "com.example.stubborn", filepath.Join(clone, "laptop", "domains", "com.example.stubborn.plist")).Exits(1)
	p := prefs.New(fake, home, state, clone, "git@github.com:someone/prefs.git", "laptop", prefs.Lists{}, now)
	p.Probes, p.AppDirs, p.Sleep = nil, []string{appDir}, func(time.Duration) {}
	step := steps.Prefs(p, now)
	paused := step.Check(t.Context()).Items[0]

	var logged []event.Logged
	ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l) })
	said, err := step.Choice.Answer(ctx, paused, "r")
	if err != nil || said != "1 app's settings, from 4 Oct; 1 app to open once; 1 held for kit prefs restore --pending" {
		t.Errorf("r = %q, %v; want how many came back, how many apps to open, and how many held", said, err)
	}
	want := []event.Logged{
		{Line: "Bringing back apps' settings from this Mac's last save"},
		{Line: "Opening Boxed App once, for its settings folder"},
		{Line: "Stubborn's settings didn't come back: macOS didn't take them", Alert: true},
	}
	if !slices.Equal(logged, want) {
		t.Errorf("the log says %+v\nwant %+v", logged, want)
	}

	res := step.Check(t.Context())
	open := check.ToDo{
		Title: "Open Boxed App once and quit it", Why: "Its settings come back after: macOS makes its settings folder only once it's opened.",
		Steps: []string{"Open Boxed App", "Quit it"}, Watch: "Boxed App has its settings folder and is quit", Group: "Apps", First: true,
	}
	held := check.Item{ID: "prefs:held", Name: "1 app's settings didn't come back", State: steps.Problem, Detail: "kit prefs restore --pending tries again; kit prefs pending says why"}
	if res.State != check.Attention || res.Summary != "1 app to open once; 1 held for kit prefs restore --pending" || len(res.Items) != 2 ||
		res.Items[0].ID != "prefs:open:com.example.boxed" || !reflect.DeepEqual(*res.Items[0].ToDo, open) || !reflect.DeepEqual(res.Items[1], held) {
		t.Errorf("with an app to open, the check says %+v; want its to-do, watched, kit not offering keys, and what's held", res)
	}
	item := res.Items[0]
	if said := step.Choice.Settle(t.Context(), paused); said != "" {
		t.Errorf("settling another to-do said %q, want nothing done", said)
	}

	write(filepath.Join(home, "Library", "Containers", "com.example.boxed", ".made"), "")
	fake.On("osascript", "-e", `application id "com.example.boxed" is running`).Prints("true\n").Then().Prints("false\n")
	if res := step.Check(t.Context()); len(res.Items) != 2 {
		t.Errorf("its folder there, still open, the check says %+v; want the to-do till it's quit", res)
	}
	if res := step.Check(t.Context()); res.Summary != "1 held for kit prefs restore --pending" || len(res.Items) != 1 || res.Items[0].ID != "prefs:held" {
		t.Errorf("its folder there, quit, the check says %+v; want the to-do gone, what's held still said", res)
	}
	opened := strings.Count(strings.Join(fake.Calls(), "\n"), "open -g -j -b")
	if said := step.Choice.Settle(t.Context(), item); said != "1 app's settings" {
		t.Errorf("settled, it said %q; want the app's settings back", said)
	}
	calls := strings.Join(fake.Calls(), "\n")
	if strings.Count(calls, "open -g -j -b") != opened || strings.Count(calls, "defaults import com.example.stubborn") != 1 {
		t.Errorf("settling ran\n%s\nwant nothing opened, and what failed otherwise left alone", calls)
	}
}

// f on a record from before a restore switched saving on itself, the only
// way a restore can come before it now, says saving's on and how many
// still wait, not that the restore was thrown away.
func TestAppsSettingsAfreshAfterAnOlderRestore(t *testing.T) {
	state := t.TempDir()
	record := `{"restored": {"from": "laptop", "at": "2026-10-04T03:00:00Z"}, "pending": {"from": "laptop", "domains": ["com.example.app"], "files": ["~/.config/tool/settings.json"]}}`
	if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	step := steps.Prefs(prefs.New(runnertest.New(t), t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{}, now), now)
	paused := step.Check(t.Context()).Items[0]
	if said, err := step.Choice.Answer(t.Context(), paused, "f"); err != nil || said != "saving from now; 2 still wait to come back" {
		t.Errorf("f = %q, %v; want saving on, and how many still wait", said, err)
	}
}

// With apps to open and settings held after failing, what's wrong with
// capture itself still comes first: they're said beside it, never in its
// place.
func TestAppsSettingsHeldNeverHideCapture(t *testing.T) {
	state, appDir := t.TempDir(), t.TempDir()
	old := now().Add(-40 * time.Hour).Format(time.RFC3339)
	record := `{"on": "restored", "captured": "` + old + `", "pending": {"from": "laptop", "domains": ["com.example.boxed", "com.example.app"], "failed": ["com.example.app"], "files": ["~/.config/tool/settings.json"],
		"open": {"com.example.boxed": {"id": "com.example.boxed", "name": "Boxed App"}}}}`
	if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(appDir, "Boxed App.app", "Contents", "Info.plist")
	if err := os.MkdirAll(filepath.Dir(info), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(info, []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.boxed</string></dict></plist>`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := prefs.New(runnertest.New(t), t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{}, now)
	p.AppDirs = []string{appDir}
	res := steps.Prefs(p, now).Check(t.Context())
	var ids []string
	for _, it := range res.Items {
		ids = append(ids, it.ID)
	}
	if res.State != check.Attention || !strings.HasPrefix(res.Summary, "last capture") || !slices.Equal(ids, []string{"prefs:stale", "prefs:open:com.example.boxed", "prefs:held"}) {
		t.Errorf("the check says %q, items %q; want the stale capture first, the app to open and what's held beside it", res.Summary, ids)
	}
	if held := res.Items[2].Name; held != "1 app's settings and 1 settings file didn't come back" {
		t.Errorf("what's held is named %q; want what it is, apps' settings and files apart", held)
	}

	if err := os.RemoveAll(filepath.Join(appDir, "Boxed App.app")); err != nil {
		t.Fatal(err)
	}
	if said := steps.Prefs(p, now).Choice.Settle(t.Context(), res.Items[1]); said != "Boxed App was removed" {
		t.Errorf("its app removed while its to-do was up, settling it said %q; want that it was removed", said)
	}
}
