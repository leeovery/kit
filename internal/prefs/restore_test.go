package prefs_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/prefs"
)

// app makes an app bundle in dir whose Info.plist names id, with helper
// apps inside it.
func (w *world) app(t *testing.T, dir, name, id string, helpers ...[2]string) string {
	t.Helper()
	path := filepath.Join(dir, name+".app")
	w.write(t, filepath.Join(path, "Contents", "Info.plist"), stored(t, map[string]any{"CFBundleIdentifier": id}))
	for _, h := range helpers {
		w.write(t, filepath.Join(path, "Contents", "Library", "LoginItems", h[0]+".app", "Contents", "Info.plist"), stored(t, map[string]any{"CFBundleIdentifier": h[1]}))
	}
	return path
}

// sandboxedApp is codesign's entitlements of a sandboxed app, as it prints
// them.
const sandboxedApp = `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>com.apple.security.app-sandbox</key><true/></dict></plist>`

// closed scripts each app of ids as not running.
func (w *world) closed(ids ...string) {
	for _, id := range ids {
		w.fake.On("osascript", "-e", `application id "`+id+`" is running`).Prints("false\n")
	}
}

// imports scripts restoring domain: its live settings, the delete and the
// import of the stored copy, and the settings read back.
func (w *world) imports(t *testing.T, domain, from string, live, after map[string]any) {
	t.Helper()
	file := filepath.Join(w.clone, from, "domains", domain+".plist")
	w.exports(t, domain, live)
	w.fake.On("defaults", "delete", domain)
	w.fake.On("defaults", "import", domain, file).Does(func() { w.exports(t, domain, after) })
}

func TestRestoreFindsEachAppAndWaitsForTheRest(t *testing.T) {
	w := newWorld(t, prefs.Lists{
		Apps:         [][2]string{{"com.vendor.updater", "com.vendor.app"}},
		MachineBound: []string{"io.tailnet.*", "~/.config/tool/local.json"},
	})
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "owner"), "hardware HW-THIS\n")
	theme := map[string]any{"Theme": "dark"}
	for _, d := range []string{"com.example.app", "com.example.app.helper", "com.vendor.updater", "group.com.shared", "ABCDE12345.com.example.app", "com.example.running", "com.example.missing", "io.tailnet.client"} {
		w.write(t, filepath.Join(store, "domains", d+".plist"), stored(t, theme))
	}
	w.write(t, filepath.Join(store, "files", ".config", "tool", "settings.json"), "new settings")
	w.write(t, filepath.Join(store, "files", ".config", "tool", "same.json"), "same")
	w.write(t, filepath.Join(store, "files", ".config", "tool", "local.json"), "this Mac's")
	w.write(t, filepath.Join(w.home, ".config", "tool", "settings.json"), "old settings")
	w.write(t, filepath.Join(w.home, ".config", "tool", "same.json"), "same")
	w.write(t, filepath.Join(w.home, ".config", "tool", "local.json"), "this Mac's")

	w.app(t, w.apps, "Example", "com.example.app")
	w.app(t, w.apps, "Vendor", "com.vendor.app")
	w.app(t, w.apps, "Shared One", "com.shared.one")
	w.app(t, w.apps, "Shared Two", "com.shared.two")
	w.app(t, w.apps, "Running", "com.example.running")
	w.app(t, w.apps, "Tailnet", "io.tailnet.client")
	w.closed("com.example.app", "com.vendor.app", "com.shared.one", "com.shared.two", "io.tailnet.client")
	w.fake.On("osascript", "-e", `application id "com.example.running" is running`).Prints("true\n")
	for _, id := range []string{"com.example.app", "com.vendor.app", "com.shared.one", "com.shared.two", "io.tailnet.client"} {
		w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", w.p.AppDirs[0]+"/"+map[string]string{"com.example.app": "Example", "com.vendor.app": "Vendor", "com.shared.one": "Shared One", "com.shared.two": "Shared Two", "io.tailnet.client": "Tailnet"}[id]+".app").Prints("<plist><dict/></plist>")
	}
	for _, d := range []string{"com.example.app", "com.example.app.helper", "com.vendor.updater", "group.com.shared", "ABCDE12345.com.example.app", "io.tailnet.client"} {
		w.imports(t, d, "laptop", map[string]any{"Theme": "light"}, theme)
	}
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(report.Restored)
	if want := []string{"ABCDE12345.com.example.app", "com.example.app", "com.example.app.helper", "com.vendor.updater", "group.com.shared", "io.tailnet.client"}; !slices.Equal(report.Restored, want) {
		t.Errorf("restored %q, want %q (this Mac's own folder, so its machine-bound settings too)", report.Restored, want)
	}
	if report.Waiting["com.example.running"] != "Running is running" || report.Waiting["com.example.missing"] != "not installed" || len(report.Failed) > 0 {
		t.Errorf("waiting %v, failed %v", report.Waiting, report.Failed)
	}
	if report.Files != 1 || w.read(t, filepath.Join(w.home, ".config", "tool", "settings.json")) != "new settings" {
		t.Errorf("files %d; settings.json = %q", report.Files, w.read(t, filepath.Join(w.home, ".config", "tool", "settings.json")))
	}
	if got := w.read(t, filepath.Join(report.Saved, "files", ".config", "tool", "settings.json")); got != "old settings" {
		t.Errorf("the live file saved as %q", got)
	}
	if got := w.read(t, filepath.Join(report.Saved, "com.example.app.plist")); got != stored(t, map[string]any{"Theme": "light"}) {
		t.Errorf("the live settings saved as %q", got)
	}
	rec, _ := w.p.Load()
	if rec.On == "" || rec.Restored == nil || rec.Restored.Commit != "abc123" || rec.Pending == nil || !slices.Equal(rec.Pending.Domains, []string{"com.example.missing", "com.example.running"}) {
		t.Errorf("record = %+v, pending %+v", rec, rec.Pending)
	}
	if got, want := report.Summary(), "restored 6 domains and 1 file from laptop; 2 pending (not installed or running); previous settings saved in "+report.Saved; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}

// Onto other hardware, as a replacement Mac given the same name, or from
// another Mac's folder, settings bound to one Mac stay behind.
func TestRestoreLeavesMachineBoundSettingsOnOtherHardware(t *testing.T) {
	for _, c := range []struct{ name, from, owner string }{
		{"a replacement Mac with the same name", "laptop", "HW-OLD"},
		{"another Mac's folder", "studio", "HW-THIS"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, prefs.Lists{MachineBound: []string{"io.tailnet.*", "~/.config/tool/local.json"}})
			store := filepath.Join(w.clone, c.from)
			w.write(t, filepath.Join(store, "owner"), "hardware "+c.owner+"\n")
			w.write(t, filepath.Join(store, "domains", "io.tailnet.client.plist"), stored(t, map[string]any{"Node": "old"}))
			w.write(t, filepath.Join(store, "files", ".config", "tool", "local.json"), "the old Mac's")
			w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
			report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{From: c.from})
			if err != nil || len(report.Restored) != 0 || report.Files != 0 {
				t.Errorf("Restore() = %+v, %v; want Tailscale's identity and the local file left", report, err)
			}
			if _, err := os.Stat(filepath.Join(w.home, ".config", "tool", "local.json")); !os.IsNotExist(err) {
				t.Error("the machine-bound file was restored")
			}
		})
	}
}

// A domain named is restored even without its app; one that reads back
// different fails, its live settings saved.
func TestRestoreNamedDomains(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "domains", "com.example.loose.plist"), stored(t, map[string]any{"A": int64(1)}))
	w.write(t, filepath.Join(store, "domains", "com.example.stubborn.plist"), stored(t, map[string]any{"A": int64(1)}))
	w.imports(t, "com.example.loose", "laptop", map[string]any{}, map[string]any{"A": int64(1)})
	w.imports(t, "com.example.stubborn", "laptop", map[string]any{"A": int64(2)}, map[string]any{"A": int64(2)})
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.loose", "com.example.stubborn"}})
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.loose"}) || report.Failed["com.example.stubborn"] != "macOS took them, but they read back different" {
		t.Errorf("Restore() = %+v, %v", report, err)
	}
	if rec, _ := w.p.Load(); rec.On != "" {
		t.Error("restoring named domains switched capture on: only a full restore does")
	}
	if _, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.absent"}}); err == nil || !strings.Contains(err.Error(), "not in the store: com.example.absent") {
		t.Errorf("Restore(absent) = %v", err)
	}
}

// A settings file that can't be put back fails alone, named with why: the
// rest of the restore goes on, capture's switched on, the restore recorded,
// and the file held aside with the domains waiting, each with why, till
// --pending puts it back.
func TestRestoreHoldsAFailedFileAside(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "domains", "com.example.missing.plist"), stored(t, map[string]any{"A": true}))
	w.write(t, filepath.Join(store, "files", ".config", "tool", "settings.json"), "saved settings")
	w.write(t, filepath.Join(store, "files", ".config", "other.json"), "other settings")
	// A file where its folder should be: the settings file can't go there.
	w.write(t, filepath.Join(w.home, ".config", "tool"), "in the way")
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || report.Files != 1 || report.FilesFailed["~/.config/tool/settings.json"] != "it couldn't be put in place" {
		t.Errorf("Restore() = %+v, %v; want the other file back, this one failed, with why", report, err)
	}
	if got := report.Summary(); !strings.HasSuffix(got, "; 1 pending (not installed or running); 1 failed") {
		t.Errorf("Summary() = %q, want the failed file counted", got)
	}
	rec, _ := w.p.Load()
	want := &prefs.Pending{From: "laptop", Domains: []string{"com.example.missing"}, Files: []string{"~/.config/tool/settings.json"},
		Why: map[string]string{"com.example.missing": "not installed", "~/.config/tool/settings.json": "it couldn't be put in place"}}
	if rec.On == "" || rec.Restored == nil || !reflect.DeepEqual(rec.Pending, want) {
		t.Errorf("record = %+v, pending %+v; want capture on, the restore recorded, and pending %+v", rec, rec.Pending, want)
	}

	if err := os.Remove(filepath.Join(w.home, ".config", "tool")); err != nil {
		t.Fatal(err)
	}
	report, err = w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true})
	if err != nil || report.Files != 1 || len(report.FilesFailed) != 0 || w.read(t, filepath.Join(w.home, ".config", "tool", "settings.json")) != "saved settings" {
		t.Errorf("Restore(--pending) = %+v, %v; want the held file put back", report, err)
	}
	if rec, _ := w.p.Load(); rec.Pending == nil || len(rec.Pending.Files) != 0 || !reflect.DeepEqual(rec.Pending.Why, map[string]string{"com.example.missing": "not installed"}) {
		t.Errorf("pending = %+v, want the file off it, the domain left with why", rec.Pending)
	}
}

// A sandboxed app's container is made first: launched hidden once, then
// quit, the run's log naming it as it opens.
func TestRestoreMakesASandboxedAppsContainer(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "domains", "com.example.boxed.plist"), stored(t, map[string]any{"A": true}))
	app := w.app(t, w.apps, "Boxed", "com.example.boxed")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints(sandboxedApp)
	w.fake.On("open", "-g", "-j", "-b", "com.example.boxed").Does(func() {
		_ = os.MkdirAll(filepath.Join(w.home, "Library", "Containers", "com.example.boxed"), 0o700)
	})
	w.fake.On("osascript", "-e", `tell application id "com.example.boxed" to quit`)
	w.closed("com.example.boxed")
	w.imports(t, "com.example.boxed", "laptop", map[string]any{}, map[string]any{"A": true})
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	var logged []string
	ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l.Line) })
	report, err := w.p.Restore(ctx, prefs.RestoreOptions{})
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.boxed"}) {
		t.Errorf("Restore() = %+v, %v", report, err)
	}
	calls := strings.Join(w.fake.Calls(), "\n")
	if strings.Index(calls, "open -g -j -b com.example.boxed") > strings.Index(calls, "defaults import com.example.boxed") {
		t.Errorf("ran\n%s\nwant the container made before the import", calls)
	}
	if want := []string{"Opening Boxed once, for its settings folder"}; !slices.Equal(logged, want) {
		t.Errorf("the log says %q, want %q", logged, want)
	}
}

// entitled is codesign's entitlements of an app declaring sandbox, as it
// prints them: the app sandbox's key set to it, or none, for "".
func entitled(sandbox string) string {
	key := ""
	if sandbox != "" {
		key = "<key>com.apple.security.app-sandbox</key><" + sandbox + "/>"
	}
	return `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict>` +
		key + `<key>com.apple.security.network.client</key><true/></dict></plist>`
}

// An app is sandboxed only when its entitlements turn the app sandbox on:
// one declaring it off, as some do, or not at all, or unsigned, has no
// container to make, and its settings are restored straight to its domain,
// no app opened, none to open.
func TestRestoreTakesAnAppAsSandboxedOnlyWhenItsEntitlementIsOn(t *testing.T) {
	for _, tt := range []struct {
		name, entitlements string
		sandboxed          bool
	}{
		{"on", entitled("true"), true},
		{"off", entitled("false"), false},
		{"not declared", entitled(""), false},
		{"unsigned", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t, prefs.Lists{})
			w.write(t, filepath.Join(w.clone, "laptop", "domains", "com.example.monitor.plist"), stored(t, map[string]any{"A": true}))
			app := w.app(t, w.apps, "Monitor", "com.example.monitor")
			w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints(tt.entitlements)
			w.fake.On("open", "-g", "-j", "-b", "com.example.monitor")
			w.fake.On("osascript", "-e", `tell application id "com.example.monitor" to quit`)
			w.closed("com.example.monitor")
			w.imports(t, "com.example.monitor", "laptop", map[string]any{}, map[string]any{"A": true})
			w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")

			report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
			rec, _ := w.p.Load()
			switch {
			case err != nil:
				t.Fatalf("Restore() error = %v", err)
			case tt.sandboxed && (len(w.opened()) != 1 || report.ToOpen["com.example.monitor"] == "" || len(w.p.ToOpen(t.Context(), rec)) != 1):
				t.Errorf("Restore() = %+v, opened %q; want the app opened once for its container, then held to open", report, w.opened())
			case !tt.sandboxed && (!slices.Equal(report.Restored, []string{"com.example.monitor"}) || len(w.opened()) != 0 || len(report.ToOpen) != 0 || rec.Pending != nil):
				t.Errorf("Restore() = %+v, opened %q, pending %+v; want its settings restored to its domain, no app opened, nothing held", report, w.opened(), rec.Pending)
			}
		})
	}
}

// An app held to open that isn't sandboxed, as kit once took an app
// declaring its sandbox off to be, is no app to open: it never gets a
// settings folder. The next pass restores its settings to its domain.
func TestAnAppHeldToOpenThatIsntSandboxedIsRestored(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.monitor"],
		"open": {"com.example.monitor": {"id": "com.example.monitor", "name": "Monitor"}},
		"why": {"com.example.monitor": "opened once, it still has no settings folder"}}}`)
	w.write(t, filepath.Join(w.clone, "laptop", "domains", "com.example.monitor.plist"), stored(t, map[string]any{"A": true}))
	app := w.app(t, w.apps, "Monitor", "com.example.monitor")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints(entitled("false"))
	w.closed("com.example.monitor")
	rec, _ := w.p.Load()
	if got := w.p.ToOpen(t.Context(), rec); len(got) != 0 {
		t.Errorf("ToOpen() = %+v; want no app to open", got)
	}
	w.imports(t, "com.example.monitor", "laptop", map[string]any{}, map[string]any{"A": true})
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true})
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.monitor"}) {
		t.Errorf("the next pass = %+v, %v; want its settings restored", report, err)
	}
	if rec, _ := w.p.Load(); rec.Pending != nil {
		t.Errorf("pending = %+v, want nothing held", rec.Pending)
	}
}

// opened is the calls of w's fake that opened an app.
func (w *world) opened() []string {
	var out []string
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "open ") {
			out = append(out, c)
		}
	}
	return out
}

// A sandboxed app whose settings folder didn't appear when kit opened it,
// once for all its domains, is held for the person to open once, named,
// with why; a full restore switches capture on all the same. The hourly pass opens nothing: with no
// folder yet it leaves the app held, and once the folder's there and the
// app's closed, it brings its settings back.
func TestRestoreHoldsAnAppToOpenTillItsFolderIsThere(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	for _, d := range []string{"com.example.boxed", "com.example.boxed.extra", "com.example.later"} {
		w.write(t, filepath.Join(store, "domains", d+".plist"), stored(t, map[string]any{"A": true}))
	}
	app := w.app(t, w.apps, "Boxed App", "com.example.boxed")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints(sandboxedApp)
	w.fake.On("open", "-g", "-j", "-b", "com.example.boxed")
	w.fake.On("osascript", "-e", `tell application id "com.example.boxed" to quit`)
	w.closed("com.example.boxed")
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	boxed := prefs.App{ID: "com.example.boxed", Name: "Boxed App"}
	if err != nil || len(report.Restored) != 0 || report.ToOpen["com.example.boxed"] != "opened once, it still has no settings folder" || report.Apps["com.example.boxed"] != boxed {
		t.Errorf("Restore() = %+v, %v; want the app to open, named, with why", report, err)
	}
	rec, _ := w.p.Load()
	if rec.On == "" || rec.Pending == nil || !slices.Equal(rec.Pending.Domains, []string{"com.example.boxed", "com.example.boxed.extra", "com.example.later"}) || rec.Pending.Open["com.example.boxed"] != boxed || rec.Pending.Why["com.example.boxed"] != "opened once, it still has no settings folder" {
		t.Errorf("record = %+v, pending %+v; want capture on, the app held to open with the one waiting, with why", rec, rec.Pending)
	}
	if got := w.opened(); len(got) != 1 {
		t.Errorf("the restore ran %q; want the app opened once for both its domains", got)
	}
	if got := w.p.ToOpen(t.Context(), rec); !slices.Equal(got, []prefs.App{boxed}) {
		t.Errorf("ToOpen() = %+v, want the app", got)
	}
	opens := len(w.opened())

	hourly := prefs.RestoreOptions{Pending: true, Unattended: true}
	if report, err := w.p.Restore(t.Context(), hourly); err != nil || len(report.Restored) != 0 || len(report.ToOpen) != 2 {
		t.Errorf("the hourly pass, no folder yet = %+v, %v; want the app left to open", report, err)
	}
	if rec, _ := w.p.Load(); rec.Pending.Open["com.example.boxed"] != boxed || rec.Pending.Why["com.example.boxed"] != "opened once, it still has no settings folder" {
		t.Errorf("pending = %+v, want the app still held to open", rec.Pending)
	}

	w.write(t, filepath.Join(w.home, "Library", "Containers", "com.example.boxed", ".made"), "")
	w.fake.On("osascript", "-e", `application id "com.example.boxed" is running`).Prints("true\n").Then().Prints("false\n")
	if got := w.p.ToOpen(t.Context(), rec); !slices.Equal(got, []prefs.App{boxed}) {
		t.Errorf("its folder there, still running, ToOpen() = %+v; want the app till it's quit", got)
	}
	w.imports(t, "com.example.boxed", "laptop", map[string]any{}, map[string]any{"A": true})
	w.imports(t, "com.example.boxed.extra", "laptop", map[string]any{}, map[string]any{"A": true})
	if report, err := w.p.Restore(t.Context(), hourly); err != nil || !slices.Equal(report.Restored, []string{"com.example.boxed", "com.example.boxed.extra"}) {
		t.Errorf("the hourly pass, its folder there = %+v, %v; want its settings back", report, err)
	}
	rec, _ = w.p.Load()
	if rec.Pending == nil || !slices.Equal(rec.Pending.Domains, []string{"com.example.later"}) || len(rec.Pending.Open) != 0 || len(w.p.ToOpen(t.Context(), rec)) != 0 {
		t.Errorf("pending = %+v, want com.example.later left", rec.Pending)
	}
	if got := w.opened(); len(got) != opens {
		t.Errorf("the hourly pass ran %q; want it to open nothing", got[opens:])
	}
}

// A record from before kit sorted what it held has its failed domains among
// those waiting: one whose why isn't a wait's is taken as failed, so the
// hourly pass, with nothing else to try, does nothing at all.
func TestAnOlderRecordsFailuresWaitForPendingByHand(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.app", "com.example.boxed", "com.example.gone"],
		"why": {"com.example.app": "import: exit status 1", "com.example.boxed": "opened once, it still has no settings folder", "com.example.gone": "not installed"}}}`)
	rec, _ := w.p.Load()
	if domains, files := w.p.Failures(rec); domains != 1 || files != 0 {
		t.Errorf("Failures() = %d, %d; want the one whose why isn't a wait's, nor an app's to open", domains, files)
	}
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.app"], "why": {"com.example.app": "import: exit status 1"}}}`)
	if _, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true}); err != nil || len(w.fake.Calls()) != 0 {
		t.Errorf("the hourly pass = %v, ran %q; want nothing done", err, w.fake.Calls())
	}
}

// An app held to open that's running still stays to open, with its why,
// till it's quit, though another's settings come back on the same pass, as
// when its to-do, or another app's, settles while it's open.
func TestAnAppToOpenStaysTillItsQuit(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.one", "com.example.two"],
		"open": {"com.example.one": {"id": "com.example.one", "name": "One"}, "com.example.two": {"id": "com.example.two", "name": "Two"}},
		"why": {"com.example.one": "opened once, it still has no settings folder", "com.example.two": "opened once, it still has no settings folder"}}}`)
	for _, a := range [][2]string{{"One", "com.example.one"}, {"Two", "com.example.two"}} {
		w.write(t, filepath.Join(store, "domains", a[1]+".plist"), stored(t, map[string]any{"A": true}))
		w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", w.app(t, w.apps, a[0], a[1])).Prints(sandboxedApp)
		w.write(t, filepath.Join(w.home, "Library", "Containers", a[1], ".made"), "")
	}
	w.closed("com.example.one")
	w.fake.On("osascript", "-e", `application id "com.example.two" is running`).Prints("true\n")
	w.imports(t, "com.example.one", "laptop", map[string]any{}, map[string]any{"A": true})
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true})
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.one"}) || report.Waiting["com.example.two"] != "Two is running" {
		t.Errorf("Restore(hourly) = %+v, %v; want One back, Two waiting to be quit", report, err)
	}
	rec, _ := w.p.Load()
	two := prefs.App{ID: "com.example.two", Name: "Two"}
	if rec.Pending == nil || rec.Pending.Open["com.example.two"] != two || rec.Pending.Why["com.example.two"] != "opened once, it still has no settings folder" {
		t.Errorf("pending = %+v; want Two still to open, with its why", rec.Pending)
	}
	if got := w.p.ToOpen(t.Context(), rec); !slices.Equal(got, []prefs.App{two}) {
		t.Errorf("ToOpen() = %+v, want Two till it's quit", got)
	}
}

// The hourly pass opens no app, for any reason: a sandboxed app installed
// since, with no settings folder yet, is held for the person to open.
func TestTheHourlyPassOpensNothing(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "domains", "com.example.boxed.plist"), stored(t, map[string]any{"A": true}))
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.boxed"], "why": {"com.example.boxed": "not installed"}}}`)
	app := w.app(t, w.apps, "Boxed App", "com.example.boxed")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints(sandboxedApp)
	w.closed("com.example.boxed")
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true})
	if err != nil || report.ToOpen["com.example.boxed"] != "it has no settings folder till it's opened once" {
		t.Errorf("Restore(hourly) = %+v, %v; want the app left for you to open", report, err)
	}
	if got := w.opened(); len(got) != 0 {
		t.Errorf("the hourly pass ran %q; want it to open nothing", got)
	}
	if rec, _ := w.p.Load(); rec.Pending == nil || rec.Pending.Open["com.example.boxed"] != (prefs.App{ID: "com.example.boxed", Name: "Boxed App"}) {
		t.Errorf("pending = %+v, want the app held to open", rec.Pending)
	}
}

// An import that failed otherwise is held with why: the hourly pass leaves
// it alone, and kit prefs restore --pending, by hand, tries it again.
func TestAFailedImportWaitsForPendingByHand(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "domains", "com.example.app.plist"), stored(t, map[string]any{"A": true}))
	app := w.app(t, w.apps, "Example", "com.example.app")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app)
	w.closed("com.example.app")
	w.exports(t, "com.example.app", map[string]any{})
	w.fake.On("defaults", "delete", "com.example.app")
	imported := w.fake.On("defaults", "import", "com.example.app", filepath.Join(store, "domains", "com.example.app.plist")).Exits(1)
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || report.Failed["com.example.app"] != "macOS didn't take them" || report.Apps["com.example.app"].Name != "Example" {
		t.Errorf("Restore() = %+v, %v; want it failed, named, with why", report, err)
	}
	if rec, _ := w.p.Load(); rec.Pending == nil || !slices.Equal(rec.Pending.Failed, []string{"com.example.app"}) || rec.Pending.Why["com.example.app"] != "macOS didn't take them" {
		t.Errorf("pending = %+v, want it held as failed, with why", rec.Pending)
	}
	imports := func() int {
		n := 0
		for _, c := range w.fake.Calls() {
			if strings.HasPrefix(c, "defaults import com.example.app") {
				n++
			}
		}
		return n
	}
	if report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true}); err != nil || imports() != 1 || report.Failures() != 0 {
		t.Errorf("the hourly pass = %+v, %v, %d imports; want it left alone", report, err, imports())
	}
	imported.Then().Does(func() { w.exports(t, "com.example.app", map[string]any{"A": true}) })
	if report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true}); err != nil || imports() != 2 || !slices.Equal(report.Restored, []string{"com.example.app"}) {
		t.Errorf("--pending by hand = %+v, %v, %d imports; want it tried again, and back", report, err, imports())
	}
	if rec, _ := w.p.Load(); rec.Pending != nil {
		t.Errorf("pending = %+v, want nothing left", rec.Pending)
	}
}

// --pending restores what waits, once installed and closed; --dry-run
// changes nothing; --as imports one domain into a scratch domain.
func TestRestorePendingDryRunAndScratch(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "domains", "com.example.app.plist"), stored(t, map[string]any{"A": true}))
	w.write(t, filepath.Join(store, "domains", "com.example.later.plist"), stored(t, map[string]any{"A": true}))
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.app", "com.example.later"]}}`)
	app := w.app(t, w.apps, "Example", "com.example.app")
	w.closed("com.example.app")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints("")

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, DryRun: true})
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.app"}) || report.Waiting["com.example.later"] != "not installed" {
		t.Errorf("Restore(--pending --dry-run) = %+v, %v", report, err)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "defaults import") {
			t.Errorf("--dry-run ran %s", c)
		}
	}

	w.imports(t, "com.example.app", "laptop", map[string]any{}, map[string]any{"A": true})
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	if _, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := w.p.Load(); rec.Pending == nil || !slices.Equal(rec.Pending.Domains, []string{"com.example.later"}) {
		t.Errorf("pending = %+v, want com.example.later left", rec.Pending)
	}

	w.fake.On("defaults", "export", "scratch.test", "-").Exits(1)
	w.fake.On("defaults", "delete", "scratch.test")
	w.fake.On("defaults", "import", "scratch.test", filepath.Join(store, "domains", "com.example.later.plist")).Does(func() {
		w.exports(t, "scratch.test", map[string]any{"A": true})
	})
	report, err = w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.later"}, As: "scratch.test"})
	if err != nil || report.Summary() != "imported com.example.later as scratch.test" {
		t.Errorf("Restore(--as) = %q, %v", report.Summary(), err)
	}

	// What a capture that stopped left, the pull deals with, and the
	// summary says.
	w.leavesIndex(t)
	w.fake.On("git", "-C", w.clone, "rev-parse", "--verify", "--quiet", "HEAD").Prints(head + "\n")
	report, err = w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.later"}, As: "scratch.test"})
	if err != nil || report.Summary() != "imported com.example.later as scratch.test; finished the last run's index" {
		t.Errorf("Restore(--as), a capture's index left = %q, %v", report.Summary(), err)
	}
}

// --at restores the store as it was then, from its history.
func TestRestoreAt(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	old := stored(t, map[string]any{"Theme": "the old one"})
	for name, body := range map[string]string{"laptop/domains/com.example.app.plist": old} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	w.fake.On("git", "-C", w.clone, "rev-list", "-1", "--before=3 days ago", "HEAD").Prints("old123\n")
	w.fake.On("git", "-C", w.clone, "log", "-1", "--date=format:%Y-%m-%d %H:%M", "--format=%ad", "old123").Prints("2026-10-03 03:00\n")
	w.fake.On("git", "-C", w.clone, "archive", "--format=tar", "old123", "laptop").Prints(archive.String())
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.app"}, At: "3 days ago", DryRun: true})
	if err != nil || report.At != "2026-10-03 03:00" || report.Commit != "old123" || !slices.Equal(report.Restored, []string{"com.example.app"}) {
		t.Errorf("Restore(--at) = %+v, %v", report, err)
	}
	if got := report.Summary(); got != "would restore 1 domain and 0 files from laptop as it was at 2026-10-03 03:00" {
		t.Errorf("Summary() = %q", got)
	}
}

func TestHistory(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	format := []string{"-C", w.clone, "log", "--no-color", "-n5", "--date=format:%Y-%m-%d %H:%M", "--format=%h  %ad  %s"}
	w.fake.On("git", append(format, "--shortstat", "--", "laptop")...).Prints("abc1234  2026-10-06 03:00  Capture 2026-10-06 03:00: 1 changed\n")
	w.fake.On("git", append(format, "-p", "--", "laptop/domains/com.example.app.plist")...).Prints("a diff\n")
	w.fake.On("git", append(format, "-p", "--", "laptop/files/.config/tool/settings.json")...).Prints("")
	if out, err := w.p.History(t.Context(), "", "", 5); err != nil || !strings.HasPrefix(out, "abc1234") {
		t.Errorf("History() = %q, %v", out, err)
	}
	if out, err := w.p.History(t.Context(), "", "com.example.app", 5); err != nil || out != "a diff" {
		t.Errorf("History(domain) = %q, %v", out, err)
	}
	if _, err := w.p.History(t.Context(), "", "~/.config/tool/settings.json", 5); err == nil || !strings.Contains(err.Error(), "no history for laptop/files/.config/tool/settings.json") {
		t.Errorf("History(file) = %v", err)
	}
}

// The last save is when this Mac's folder in the store last changed, as a
// restore brings it back; with nothing saved for it, there's none.
func TestLastSave(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.fake.On("git", "-C", w.clone, "log", "-1", "--format=%cI", "--", "laptop").Prints("2026-10-15T03:00:12+01:00\n").Then().Prints("")
	last, err := w.p.LastSave(t.Context())
	if want := time.Date(2026, 10, 15, 2, 0, 12, 0, time.UTC); err != nil || !last.Equal(want) {
		t.Errorf("LastSave() = %v, %v; want %v", last, err, want)
	}
	if _, err := w.p.LastSave(t.Context()); err == nil || err.Error() != "no settings stored for laptop" {
		t.Errorf("with nothing saved, LastSave() = %v", err)
	}
}
