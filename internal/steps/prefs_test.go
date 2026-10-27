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
	"github.com/leeovery/kit/internal/engine"
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
			res := steps.Prefs(p, "", now).Check(t.Context())
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

// settingsStore is a Mac, laptop, whose apps' settings aren't switched
// on, with no clone of the store, cloning it making one with a folder for
// each machine ID of saves, an app's settings in it, its last save on the
// day given; and the settings step on it, laptop started as a copy of
// from, its paused to-do as its check finds it.
func settingsStore(t *testing.T, saves map[string]string, from string) (fake *runnertest.Fake, p *prefs.Prefs, step engine.Step, paused check.Item) {
	t.Helper()
	state := t.TempDir()
	clone := filepath.Join(t.TempDir(), "prefs")
	if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fake = runnertest.New(t)
	configures(fake, clone)
	fake.On("git", "clone", "--quiet", "git@github.com:someone/prefs.git", clone+".cloning").Does(func() {
		if err := os.MkdirAll(filepath.Join(clone+".cloning", ".git"), 0o700); err != nil {
			t.Error(err)
		}
		for mac := range saves {
			storeDomain(t, clone+".cloning", mac, "com.example.app")
		}
	})
	for mac, day := range saves {
		fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", mac).Prints(day + "T03:00:00Z\n")
	}
	fake.On("git", "-C", clone, "remote")
	fake.On("git", "-C", clone, "rev-parse", "HEAD").Prints("abc123\n")
	p = prefs.New(fake, t.TempDir(), state, clone, "git@github.com:someone/prefs.git", "laptop", prefs.Lists{}, now)
	p.Probes, p.AppDirs = nil, []string{t.TempDir()}
	step = steps.Prefs(p, from, now)
	return fake, p, step, step.Check(t.Context()).Items[0]
}

// storeDomain puts a domain in the store's folder of mac.
func storeDomain(t *testing.T, clone, mac, domain string) {
	t.Helper()
	stored := filepath.Join(clone, mac, "domains", domain+".plist")
	if err := os.MkdirAll(filepath.Dir(stored), 0o700); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile(stored, []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict/></plist>`), 0o600); err != nil {
		t.Error(err)
	}
}

// The settings to-do is a choice of one source and f. As it comes up, the
// store cloned first when it isn't, r restores this machine ID's last save,
// its date said; else, with none, the save of the Mac it started as a copy
// of, named; with neither, f alone. Other machine IDs' saves are never
// offered. With no store to look at, the check's keys stand as they are.
func TestAppsSettingsKeys(t *testing.T) {
	afresh := check.Key{Key: "f", Does: "starts afresh: nothing restored, saving from now"}
	for _, tt := range []struct {
		name, from string
		saves      map[string]string
		want       []check.Key
	}{
		{"its own", "studio", map[string]string{"laptop": "2026-10-04", "studio": "2026-10-25", "desk": "2026-10-26"}, []check.Key{
			{Key: "r", Does: "restores this machine ID's last save (4 Oct)"}, afresh,
		}},
		{"the Mac it started from", "studio", map[string]string{"studio": "2026-10-25", "desk": "2026-10-26"}, []check.Key{
			{Key: "r", Does: "restores studio's last save (25 Oct) onto this Mac; studio keeps its own"}, afresh,
		}},
		{"others' alone", "", map[string]string{"studio": "2026-10-25"}, []check.Key{afresh}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, step, paused := settingsStore(t, tt.saves, tt.from)
			if got := step.Choice.Ready(t.Context(), paused).ToDo.Keys; !slices.Equal(got, tt.want) {
				t.Errorf("readied, the keys are %v\nwant %v", got, tt.want)
			}
		})
	}
	noStore := steps.Prefs(prefs.New(runnertest.New(t), t.TempDir(), t.TempDir(), t.TempDir(), "", "laptop", prefs.Lists{}, now), "", now)
	paused := noStore.Check(t.Context()).Items[0]
	if ready := noStore.Choice.Ready(t.Context(), paused); !slices.Equal(ready.ToDo.Keys, paused.ToDo.Keys) || paused.ToDo.Keys[0].Does != "restores the last save" {
		t.Errorf("with no store to look at, the keys are %v; want the check's, %v", ready.ToDo.Keys, paused.ToDo.Keys)
	}
}

// r restores this machine ID's last save, as kit prefs restore does,
// saying how many and from when, and how many wait for their apps; else
// the Mac's it started from, as kit prefs restore --from does, the log
// naming it; with neither, it says why, and capture stays off. A digit is
// no key. f starts afresh, as kit prefs start-fresh does. Each switches
// capture on, so it's done.
func TestAppsSettingsChoice(t *testing.T) {
	_, _, step, paused := settingsStore(t, map[string]string{"studio": "2026-10-25"}, "")
	if _, err := step.Choice.Answer(t.Context(), paused, "r"); err == nil || err.Error() != "no settings stored for laptop" {
		t.Errorf("with nothing saved, r = %v; want it to say so", err)
	}
	_, _, step, paused = settingsStore(t, map[string]string{"studio": "2026-10-25"}, "desk")
	if _, err := step.Choice.Answer(t.Context(), paused, "r"); err == nil || err.Error() != "no settings stored for laptop or desk" {
		t.Errorf("with nothing saved for either, r = %v; want it to name both", err)
	}
	if _, err := step.Choice.Answer(t.Context(), paused, "1"); err == nil || err.Error() != "1 isn't one of its keys" {
		t.Errorf("1 = %v; want it no key", err)
	}
	if step.Check(t.Context()).Items[0].ID != "prefs:paused" {
		t.Error("r failing, capture was switched on")
	}

	_, p, step, paused := settingsStore(t, map[string]string{"studio": "2026-10-25"}, "studio")
	var logged []string
	ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l.Line) })
	said, err := step.Choice.Answer(ctx, paused, "r")
	if err != nil || said != "no apps' settings, from 25 Oct; 1 waits for its app" {
		t.Errorf("r = %q, %v; want how many, from when, and how many wait", said, err)
	}
	if want := []string{"Restoring application settings from studio's last save"}; !slices.Equal(logged, want) {
		t.Errorf("the log says %q, want %q", logged, want)
	}
	if rec, err := p.Load(); err != nil || rec.Restored == nil || rec.Restored.From != "studio" {
		t.Errorf("the record = %+v, %v; want restored from studio", rec, err)
	}
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "saving from tonight" {
		t.Errorf("restored, the check says %+v; want capture switched on, fine for now", res)
	}

	_, _, step, paused = settingsStore(t, map[string]string{"laptop": "2026-10-04", "studio": "2026-10-25"}, "studio")
	if said, err := step.Choice.Answer(t.Context(), paused, "r"); err != nil || said != "no apps' settings, from 4 Oct; 1 waits for its app" {
		t.Errorf("r = %q, %v; want how many, from when, and how many wait", said, err)
	}

	_, _, step, paused = settingsStore(t, nil, "")
	if said, err := step.Choice.Answer(t.Context(), paused, "f"); err != nil || said != "saving from now, nothing restored" {
		t.Errorf("f = %q, %v", said, err)
	}
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "saving from tonight" {
		t.Errorf("started afresh, the check says %+v; want capture switched on, fine for now", res)
	}
}

// Applied, the settings step starts afresh by itself when the store holds
// no source, raising no card, the log saying so; with a source, or a store it can't read, it
// leaves the card be.
func TestAppsSettingsWithNoSource(t *testing.T) {
	for _, tt := range []struct {
		name, from string
		saves      map[string]string
		afresh     bool
	}{
		{"no source", "", map[string]string{"studio": "2026-10-25"}, true},
		{"its own", "", map[string]string{"laptop": "2026-10-04"}, false},
		{"the Mac it started from", "studio", map[string]string{"studio": "2026-10-25"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, step, _ := settingsStore(t, tt.saves, tt.from)
			var logged []string
			ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l.Line) })
			if err := step.Apply(ctx, step.Check(t.Context())); err != nil {
				t.Fatalf("apply = %v", err)
			}
			if said := []string{"No saved application settings to restore: saving from now, nothing restored"}; slices.Equal(logged, said) != tt.afresh {
				t.Errorf("the log says %q; want %q only when started afresh", logged, said)
			}
			res := step.Check(t.Context())
			if afresh := res.State == check.OK && res.Summary == "saving from tonight" && !res.ToDo(); afresh != tt.afresh {
				t.Errorf("applied, the check says %+v; want started afresh %v", res, tt.afresh)
			}
		})
	}
	fake := runnertest.New(t)
	clone := filepath.Join(t.TempDir(), "prefs")
	fake.On("git", "clone", "--quiet", "git@github.com:someone/prefs.git", clone+".cloning").Exits(128)
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	step := steps.Prefs(prefs.New(fake, t.TempDir(), state, clone, "git@github.com:someone/prefs.git", "laptop", prefs.Lists{}, now), "", now)
	if err := step.Apply(t.Context(), step.Check(t.Context())); err != nil {
		t.Fatalf("apply with a store it can't read = %v", err)
	}
	if res := step.Check(t.Context()); len(res.Items) != 1 || res.Items[0].ID != "prefs:paused" {
		t.Errorf("with a store it can't read, the check says %+v; want the card still", res)
	}
}

// Other hardware owning this machine ID's folder, the settings to-do says
// once that this Mac takes it over from that Mac, by its name; a key then
// writes this Mac's owner file in its place, after what the key does: r
// restoring this machine ID's save, or f starting afresh. A key that fails,
// r with nothing saved for this machine ID, leaves the owner file be.
func TestAppsSettingsTakeOver(t *testing.T) {
	old := "hardware HW-OLD\nmodel Mac1,1\nname Studio\n"
	for _, tt := range []struct {
		name, key string
		saves     map[string]string
		fails     bool
	}{
		{"r", "r", map[string]string{"laptop": "2026-10-04"}, false},
		{"f", "f", map[string]string{"laptop": "2026-10-04"}, false},
		{"r with nothing saved", "r", map[string]string{"studio": "2026-10-25"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, p, step, paused := settingsStore(t, tt.saves, "")
			clone := p.Clone
			fake.On("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Prints(`  "IOPlatformUUID" = "HW-THIS"` + "\n")
			fake.On("sysctl", "-n", "hw.model").Prints("Mac99,1\n")
			fake.On("scutil", "--get", "ComputerName").Prints("Laptop\n")
			if tt.fails {
				// Its folder there for the owner file alone, never committed.
				fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", "laptop")
			}
			ready := step.Choice.Ready(t.Context(), paused)
			if strings.Contains(ready.ToDo.Why, "takes over") {
				t.Errorf("with no owner, the why says %q; want nothing taken over", ready.ToDo.Why)
			}
			owner := filepath.Join(clone, "laptop", "owner")
			if err := os.MkdirAll(filepath.Dir(owner), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(owner, []byte(old), 0o600); err != nil {
				t.Fatal(err)
			}
			ready = step.Choice.Ready(t.Context(), paused)
			if want := "This Mac takes over laptop from Studio, which stops saving to it. First, so"; !strings.HasPrefix(ready.ToDo.Why, want) || strings.Count(ready.ToDo.Why, "takes over") != 1 {
				t.Errorf("owned by other hardware, the why says %q; want it to start %q, once", ready.ToDo.Why, want)
			}
			_, err := step.Choice.Answer(t.Context(), paused, tt.key)
			want := "hardware HW-THIS\nmodel Mac99,1\nname Laptop\nsince 2026-10-05 12:00\n"
			if tt.fails {
				want = old
			}
			if got, _ := os.ReadFile(owner); (err != nil) != tt.fails || string(got) != want {
				t.Errorf("%s pressed, it said %v, and the owner file says %q; want %q", tt.key, err, got, want)
			}
		})
	}
}

// r names, in the log, each app whose settings failed otherwise, with why,
// and says how many apps are to be opened once and how many are held for
// kit prefs restore --pending, opening none; saving switches on all the
// same. Each sandboxed app to open is then a to-do, by name, the person
// saying when it's done: kit opens the app as its card goes up, once a
// run, however often it goes up; space,
// while it's running or has no settings folder, says so and restores
// nothing; once it's quit with its folder, space restores its settings,
// leaving what failed otherwise alone, which the check goes on saying is
// held, and the to-do's gone.
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
	fake.On("codesign", "-d", "--entitlements", "-", "--xml", apps["com.example.boxed"]).Prints(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>com.apple.security.app-sandbox</key><true/></dict></plist>`)
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
	step := steps.Prefs(p, "", now)
	paused := step.Check(t.Context()).Items[0]

	var logged []event.Logged
	ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l) })
	said, err := step.Choice.Answer(ctx, paused, "r")
	if err != nil || said != "1 app's settings, from 4 Oct; 1 app to open once; 1 held for kit prefs restore --pending" {
		t.Errorf("r = %q, %v; want how many came back, how many apps to open, and how many held", said, err)
	}
	want := []event.Logged{
		{Line: "Restoring application settings from this machine ID's last save"},
		{Line: "Stubborn's settings didn't come back: macOS didn't take them", Alert: true},
	}
	if !slices.Equal(logged, want) {
		t.Errorf("the log says %+v\nwant %+v", logged, want)
	}

	res := step.Check(t.Context())
	open := check.ToDo{
		Title: "Open Boxed App and quit it", Why: "Its settings go in its own folder, which macOS makes the first time it opens: quit it, then press space.",
		Group: "Apps", SaysDone: true, First: true,
	}
	held := check.Item{ID: "prefs:held", Name: "1 app's settings didn't come back", State: steps.Problem, Detail: "kit prefs restore --pending tries again; kit prefs pending says why"}
	if res.State != check.Attention || res.Summary != "1 app to open once; 1 held for kit prefs restore --pending" || len(res.Items) != 2 ||
		res.Items[0].ID != "prefs:open:com.example.boxed" || !reflect.DeepEqual(*res.Items[0].ToDo, open) || !reflect.DeepEqual(res.Items[1], held) {
		t.Errorf("with an app to open, the check says %+v; want its to-do, the person saying when it's done, and what's held", res)
	}
	item := res.Items[0]
	if strings.Contains(strings.Join(fake.Calls(), "\n"), "open ") {
		t.Errorf("restoring ran\n%s\nwant no app opened", strings.Join(fake.Calls(), "\n"))
	}
	fake.On("open", "-b", "com.example.boxed")
	step.Choice.Open(t.Context(), item)
	step.Choice.Open(t.Context(), item)
	step.Choice.Open(t.Context(), paused)
	if opened := slices.DeleteFunc(fake.Calls(), func(c string) bool { return !strings.HasPrefix(c, "open ") }); !slices.Equal(opened, []string{"open -b com.example.boxed"}) {
		t.Errorf("its card going up twice, and the settings card once, ran %q; want the app opened in view once, the settings card opening nothing", opened)
	}

	fake.On("osascript", "-e", `application id "com.example.boxed" is running`).Prints("true\n").Then().Prints("false\n")
	if _, err := step.Choice.Answer(t.Context(), item, "done"); err == nil || err.Error() != "Boxed App is still running" {
		t.Errorf("space while it's running = %v; want it to say so", err)
	}
	if _, err := step.Choice.Answer(t.Context(), item, "done"); err == nil || err.Error() != "Boxed App has no settings folder yet" {
		t.Errorf("space with no settings folder = %v; want it to say so", err)
	}
	if res := step.Check(t.Context()); len(res.Items) != 2 {
		t.Errorf("not restored, the check says %+v; want the to-do still there", res)
	}
	write(filepath.Join(home, "Library", "Containers", "com.example.boxed", ".made"), "")
	if said, err := step.Choice.Answer(t.Context(), item, "done"); err != nil || said != "1 app's settings" {
		t.Errorf("space once it's quit = %q, %v; want its settings restored", said, err)
	}
	if res := step.Check(t.Context()); res.Summary != "1 held for kit prefs restore --pending" || len(res.Items) != 1 || res.Items[0].ID != "prefs:held" {
		t.Errorf("restored, the check says %+v; want the to-do gone, what's held still said", res)
	}
	if calls := strings.Join(fake.Calls(), "\n"); strings.Count(calls, "defaults import com.example.stubborn") != 1 {
		t.Errorf("space ran\n%s\nwant what failed otherwise left alone", calls)
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
	step := steps.Prefs(prefs.New(runnertest.New(t), t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{}, now), "", now)
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
	fake := runnertest.New(t)
	fake.On("codesign", "-d", "--entitlements", "-", "--xml", filepath.Join(appDir, "Boxed App.app")).Prints(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>com.apple.security.app-sandbox</key><true/></dict></plist>`)
	p := prefs.New(fake, t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{}, now)
	p.AppDirs = []string{appDir}
	res := steps.Prefs(p, "", now).Check(t.Context())
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
}
