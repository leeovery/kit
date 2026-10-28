package prefs_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/prefs"
)

// helperID is the bundle id of the windowless helper the tests make.
const helperID = "com.example.studio.prism"

// windowless makes an app in w's apps folder with a helper inside it, as a
// login item is, whose Info.plist sets key, LSUIElement or
// LSBackgroundOnly; its settings stored, and kit's waits timed, not slept:
// the helper's path, and how long kit would have waited.
func (w *world) windowless(t *testing.T, key string) (string, *time.Duration) {
	t.Helper()
	w.write(t, filepath.Join(w.clone, "laptop", "domains", helperID+".plist"), stored(t, map[string]any{"A": true}))
	app := w.app(t, w.apps, "Studio", "com.example.studio")
	helper := filepath.Join(app, "Contents", "Library", "LoginItems", "Prism.app")
	w.write(t, filepath.Join(helper, "Contents", "Info.plist"), stored(t, map[string]any{"CFBundleIdentifier": helperID, key: true}))
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	slept := new(time.Duration)
	w.p.Sleep = func(d time.Duration) { *slept += d }
	return helper, slept
}

// running scripts the app of id as running.
func (w *world) running(id string) {
	w.fake.On("osascript", "-e", `application id "`+id+`" is running`).Prints("true\n")
}

// quits scripts quitting the app of id, which then isn't running; and
// counts the quits.
func (w *world) quits(id string) func() int {
	w.fake.On("osascript", "-e", `quit app id "`+id+`"`).Does(func() { w.closed(id) })
	return func() int {
		n := 0
		for _, c := range w.fake.Calls() {
			if strings.Contains(c, "quit app id") {
				n++
			}
		}
		return n
	}
}

// A windowless helper that's running, the person unable to quit it, kit
// quits itself, and its settings come back, nothing opened.
func TestRestoreQuitsARunningWindowlessHelper(t *testing.T) {
	for _, key := range []string{"LSUIElement", "LSBackgroundOnly"} {
		t.Run(key, func(t *testing.T) {
			w := newWorld(t, prefs.Lists{})
			w.windowless(t, key)
			w.write(t, filepath.Join(w.home, "Library", "Containers", helperID, ".made"), "")
			w.running(helperID)
			quits := w.quits(helperID)
			w.imports(t, helperID, "laptop", map[string]any{}, map[string]any{"A": true})

			report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
			if err != nil || !slices.Equal(report.Restored, []string{helperID}) || quits() != 1 || len(w.opened()) != 0 {
				t.Errorf("Restore() = %+v, %v, %d quits, opened %q; want it quit once and restored, nothing opened", report, err, quits(), w.opened())
			}
		})
	}
}

// A sandboxed windowless helper with no settings folder kit opens out of
// view till macOS makes it one, then quits, and its settings come back.
func TestRestoreOpensAWindowlessHelperForItsFolder(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	helper, _ := w.windowless(t, "LSUIElement")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", helper).Prints(sandboxedApp)
	w.closed(helperID)
	w.fake.On("open", "-g", "-b", helperID).Does(func() {
		w.write(t, filepath.Join(w.home, "Library", "Containers", helperID, ".made"), "")
		w.running(helperID)
	})
	quits := w.quits(helperID)
	w.imports(t, helperID, "laptop", map[string]any{}, map[string]any{"A": true})

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || !slices.Equal(report.Restored, []string{helperID}) || quits() != 1 || !slices.Equal(w.opened(), []string{"open -g -b " + helperID}) {
		t.Errorf("Restore() = %+v, %v, %d quits, opened %q; want it opened out of view, quit and restored", report, err, quits(), w.opened())
	}
	if rec, _ := w.p.Load(); rec.Pending != nil {
		t.Errorf("pending = %+v, want nothing held", rec.Pending)
	}
}

// A windowless helper whose folder doesn't come in time stays held as an
// app to open, quit again, kit waiting no longer than it allows; but it's
// never the person's to open: no to-do asks for it, and the next pass
// opens it again.
func TestAWindowlessHelperWithNoFolderStaysHeldWithNoToDo(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	helper, slept := w.windowless(t, "LSUIElement")
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", helper).Prints(sandboxedApp)
	w.closed(helperID)
	w.fake.On("open", "-g", "-b", helperID).Does(func() { w.running(helperID) })
	quits := w.quits(helperID)

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || len(report.Restored) != 0 || report.ToOpen[helperID] != "it has no settings folder till it's opened once" || quits() != 1 {
		t.Errorf("Restore() = %+v, %v, %d quits; want it held to open, quit", report, err, quits())
	}
	if *slept != 15*time.Second {
		t.Errorf("waited %v for its folder, want 15s", *slept)
	}
	rec, _ := w.p.Load()
	if rec.Pending == nil || rec.Pending.Open[helperID].ID != helperID {
		t.Errorf("pending = %+v, want it held to open", rec.Pending)
	}
	if got := w.p.ToOpen(t.Context(), rec); len(got) != 0 {
		t.Errorf("ToOpen() = %+v, want no to-do for a windowless helper", got)
	}
	if _, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true}); err != nil || len(w.opened()) != 2 {
		t.Errorf("the hourly pass = %v, opened %q; want it opened again", err, w.opened())
	}
}

// A windowless helper that won't quit waits as a running app does, kit
// waiting no longer than it allows.
func TestAWindowlessHelperThatWontQuitWaits(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	_, slept := w.windowless(t, "LSBackgroundOnly")
	w.write(t, filepath.Join(w.home, "Library", "Containers", helperID, ".made"), "")
	w.running(helperID)
	w.fake.On("osascript", "-e", `quit app id "`+helperID+`"`)

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || report.Waiting[helperID] != "Prism is running" {
		t.Errorf("Restore() = %+v, %v; want it waiting, running", report, err)
	}
	if *slept != 5*time.Second {
		t.Errorf("waited %v for it to quit, want 5s", *slept)
	}
}

// An app of its own that has no window, as a menu-bar app, is the person's
// to quit and to open: kit does neither, and its to-do asks for it.
func TestAMenuBarAppIsLeftToThePerson(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.write(t, filepath.Join(w.clone, "laptop", "domains", "com.example.bar.plist"), stored(t, map[string]any{"A": true}))
	app := filepath.Join(w.apps, "Bar.app")
	w.write(t, filepath.Join(app, "Contents", "Info.plist"), stored(t, map[string]any{"CFBundleIdentifier": "com.example.bar", "LSUIElement": true}))
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", app).Prints(sandboxedApp)
	w.running("com.example.bar")
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	if report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{}); err != nil || report.Waiting["com.example.bar"] != "Bar is running" {
		t.Errorf("Restore(), running = %+v, %v; want it waiting", report, err)
	}

	w.closed("com.example.bar")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true})
	bar := prefs.App{ID: "com.example.bar", Name: "Bar"}
	if err != nil || report.ToOpen["com.example.bar"] == "" {
		t.Errorf("Restore(), closed = %+v, %v; want it to open", report, err)
	}
	if rec, _ := w.p.Load(); !slices.Equal(w.p.ToOpen(t.Context(), rec), []prefs.App{bar}) {
		t.Errorf("ToOpen() = %+v, want Bar", w.p.ToOpen(t.Context(), rec))
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "open ") || strings.Contains(c, "quit app id") {
			t.Errorf("kit ran %s; want it to open and quit nothing", c)
		}
	}
}
