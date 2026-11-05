package prefs_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/prefs"
)

// The domains saved for an app are those of this Mac's folder whose app it
// is, as a restore finds it: its bundle id, a sub-domain of it, or one
// [prefs apps] says is its; never another app's.
func TestSavedFor(t *testing.T) {
	w := newWorld(t, prefs.Lists{Apps: [][2]string{{"com.vendor.updater", "com.example.app"}}})
	for _, d := range []string{"com.example.app", "com.example.app.helper", "com.vendor.updater", "com.other.app"} {
		w.write(t, filepath.Join(w.clone, "laptop", "domains", d+".plist"), stored(t, map[string]any{"A": true}))
	}
	w.write(t, filepath.Join(w.clone, "studio", "domains", "com.example.app.extra.plist"), stored(t, map[string]any{"A": true}))
	w.app(t, w.apps, "Example", "com.example.app")
	w.app(t, w.apps, "Other", "com.other.app")
	if got, want := w.p.SavedFor("com.example.app"), []string{"com.example.app", "com.example.app.helper", "com.vendor.updater"}; !slices.Equal(got, want) {
		t.Errorf("SavedFor() = %q, want %q", got, want)
	}
	if got := w.p.SavedFor("com.absent.app"); len(got) != 0 {
		t.Errorf("SavedFor(an app with nothing saved) = %q, want none", got)
	}
}

// Forgetting deletes each domain through cfprefsd, and takes those held to
// be restored off the list, so capture drops them; the rest stay held.
func TestForget(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.app", "com.other.app"],
		"why": {"com.example.app": "kept till its app is installed", "com.other.app": "kept till its app is installed"}}}`)
	w.fake.On("defaults", "delete", "com.example.app")
	w.fake.On("defaults", "delete", "com.example.app.helper").Exits(1).PrintsToStderr("Domain (com.example.app.helper) not found.")
	if err := w.p.Forget(t.Context(), []string{"com.example.app", "com.example.app.helper"}); err != nil {
		t.Fatal(err)
	}
	if calls := w.fake.Calls(); !slices.Equal(calls, []string{"defaults delete com.example.app", "defaults delete com.example.app.helper"}) {
		t.Errorf("ran %q, want each domain deleted", calls)
	}
	want := &prefs.Pending{From: "laptop", Domains: []string{"com.other.app"}, Why: map[string]string{"com.other.app": "kept till its app is installed"}}
	if rec, _ := w.p.Load(); !reflect.DeepEqual(rec.Pending, want) {
		t.Errorf("pending = %+v, want %+v", rec.Pending, want)
	}
}

// Forgetting what isn't held leaves kit's record alone: none is made.
func TestForgetWithNothingHeld(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.fake.On("defaults", "delete", "com.example.app")
	if err := w.p.Forget(t.Context(), []string{"com.example.app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.state, "prefs.json")); !os.IsNotExist(err) {
		t.Errorf("the record's there (%v), want none made", err)
	}
}

// A record from before kit kept the settings of apps not installed says
// they're "not installed": they're read as kept, never as failed.
func TestARecordFromBeforeReadsAsKept(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["com.example.gone"], "why": {"com.example.gone": "not installed"}}}`)
	rec, err := w.p.Load()
	if err != nil || rec.Pending == nil || rec.Pending.Why["com.example.gone"] != "kept till its app is installed" {
		t.Errorf("Load() = %+v, %v; want it kept", rec.Pending, err)
	}
	if domains, files := w.p.Failures(rec); domains+files != 0 {
		t.Errorf("Failures() = %d, %d; want none", domains, files)
	}
}

// Straight after kit installs apps, the settings kept for those now
// installed come back, as the hourly pass would bring them: nothing's run
// at all while capture's off, or while none kept has its app; one held as
// its app runs is no reason to run.
func TestRestoreInstalled(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	store := filepath.Join(w.clone, "laptop")
	for _, d := range []string{"com.example.app", "com.example.running"} {
		w.write(t, filepath.Join(store, "domains", d+".plist"), stored(t, map[string]any{"A": true}))
	}
	held := `"pending": {"from": "laptop", "domains": ["com.example.app", "com.example.running"],
		"why": {"com.example.app": "kept till its app is installed", "com.example.running": "Running is running"}}`
	w.app(t, w.apps, "Running", "com.example.running")

	writeRecord(t, w.state, `{`+held+`}`)
	w.app(t, w.apps, "Example", "com.example.app")
	if report, err := w.p.RestoreInstalled(t.Context()); err != nil || len(report.Restored) != 0 || len(w.fake.Calls()) != 0 {
		t.Errorf("RestoreInstalled(capture off) = %+v, %v, ran %q; want nothing", report, err, w.fake.Calls())
	}
	if err := os.RemoveAll(filepath.Join(w.apps, "Example.app")); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, w.state, `{"on": "restored", `+held+`}`)
	if report, err := w.p.RestoreInstalled(t.Context()); err != nil || len(report.Restored) != 0 || len(w.fake.Calls()) != 0 {
		t.Errorf("RestoreInstalled(no app kept for installed) = %+v, %v, ran %q; want nothing", report, err, w.fake.Calls())
	}

	app := w.app(t, w.apps, "Example", "com.example.app")
	w.closed("com.example.app")
	w.fake.On("osascript", "-e", `application id "com.example.running" is running`).Prints("true\n")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints("<plist><dict/></plist>")
	w.imports(t, "com.example.app", "laptop", map[string]any{}, map[string]any{"A": true})
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	report, err := w.p.RestoreInstalled(t.Context())
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.app"}) {
		t.Errorf("RestoreInstalled() = %+v, %v; want the app installed now restored", report, err)
	}
	if rec, _ := w.p.Load(); rec.Pending == nil || !slices.Equal(rec.Pending.Domains, []string{"com.example.running"}) {
		t.Errorf("pending = %+v, want the running app's left", rec.Pending)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "defaults import com.example.running") {
			t.Errorf("ran %s, while its app runs", c)
		}
	}
}
