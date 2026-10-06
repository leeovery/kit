package prefs_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	if report.Waiting["com.example.running"] != "com.example.running is running" || report.Waiting["com.example.missing"] != "not installed" || len(report.Failed) > 0 {
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
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.loose"}) || !strings.Contains(report.Failed["com.example.stubborn"], "reads back different") {
		t.Errorf("Restore() = %+v, %v", report, err)
	}
	if rec, _ := w.p.Load(); rec.On != "" {
		t.Error("restoring named domains switched capture on: only a full restore does")
	}
	if _, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.absent"}}); err == nil || !strings.Contains(err.Error(), "not in the store: com.example.absent") {
		t.Errorf("Restore(absent) = %v", err)
	}
}

// A sandboxed app's container is made first: launched hidden once, then
// quit.
func TestRestoreMakesASandboxedAppsContainer(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "domains", "com.example.boxed.plist"), stored(t, map[string]any{"A": true}))
	app := w.app(t, w.apps, "Boxed", "com.example.boxed")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints("<key>com.apple.security.app-sandbox</key><true/>")
	w.fake.On("open", "-g", "-j", "-b", "com.example.boxed").Does(func() {
		_ = os.MkdirAll(filepath.Join(w.home, "Library", "Containers", "com.example.boxed"), 0o700)
	})
	w.fake.On("osascript", "-e", `tell application id "com.example.boxed" to quit`)
	w.closed("com.example.boxed")
	w.imports(t, "com.example.boxed", "laptop", map[string]any{}, map[string]any{"A": true})
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.boxed"}) {
		t.Errorf("Restore() = %+v, %v", report, err)
	}
	calls := strings.Join(w.fake.Calls(), "\n")
	if strings.Index(calls, "open -g -j -b com.example.boxed") > strings.Index(calls, "defaults import com.example.boxed") {
		t.Errorf("ran\n%s\nwant the container made before the import", calls)
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
	format := []string{"-C", w.clone, "log", "-n5", "--date=format:%Y-%m-%d %H:%M", "--format=%h  %ad  %s"}
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
