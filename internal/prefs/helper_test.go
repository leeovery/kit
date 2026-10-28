package prefs_test

import (
	"fmt"
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
// LSBackgroundOnly, to value; its settings stored, and kit's waits timed,
// not slept: the helper's path, and how long kit would have waited.
func (w *world) windowless(t *testing.T, key string, value any) (string, *time.Duration) {
	t.Helper()
	w.write(t, filepath.Join(w.clone, "laptop", "domains", helperID+".plist"), stored(t, map[string]any{"A": true}))
	app := w.app(t, w.apps, "Studio", "com.example.studio")
	helper := filepath.Join(app, "Contents", "Library", "LoginItems", "Prism.app")
	w.write(t, filepath.Join(helper, "Contents", "Info.plist"), stored(t, map[string]any{"CFBundleIdentifier": helperID, key: value}))
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	slept := new(time.Duration)
	w.p.Sleep = func(d time.Duration) { *slept += d }
	return helper, slept
}

// running scripts the app of id as running.
func (w *world) running(id string) {
	w.fake.On("osascript", "-e", `application id "`+id+`" is running`).Prints("true\n")
}

// quitting is the calls of w's fake that quit an app.
func (w *world) quitting() []string {
	var out []string
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "quit app id") {
			out = append(out, c)
		}
	}
	return out
}

// quits scripts quitting the app of id, which then isn't running.
func (w *world) quits(id string) {
	w.fake.On("osascript", "-e", `quit app id "`+id+`"`).Does(func() { w.closed(id) })
}

// A windowless helper that's running, the person unable to quit it, kit
// quits itself, Info.plist's flag written as any Info.plist writes it, and
// its settings come back; then it starts it again, out of view, as the
// login item it was. One whose flag is off is an app of its own: waiting,
// never quit.
func TestRestoreQuitsARunningWindowlessHelper(t *testing.T) {
	for _, tt := range []struct {
		key   string
		value any
		on    bool
	}{
		{"LSUIElement", true, true},
		{"LSBackgroundOnly", true, true},
		{"LSUIElement", "YES", true},
		{"LSUIElement", "1", true},
		{"LSBackgroundOnly", "True", true},
		{"LSUIElement", int64(1), true},
		{"LSUIElement", false, false},
		{"LSUIElement", "NO", false},
		{"LSUIElement", int64(0), false},
	} {
		t.Run(fmt.Sprintf("%s %T %v", tt.key, tt.value, tt.value), func(t *testing.T) {
			w := newWorld(t, prefs.Lists{})
			w.windowless(t, tt.key, tt.value)
			w.write(t, filepath.Join(w.home, "Library", "Containers", helperID, ".made"), "")
			w.running(helperID)
			w.quits(helperID)
			w.fake.On("open", "-g", "-b", helperID)
			w.imports(t, helperID, "laptop", map[string]any{}, map[string]any{"A": true})

			report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
			calls := w.fake.Calls()
			imported := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "defaults import") })
			started := slices.Index(calls, "open -g -b "+helperID)
			switch {
			case err != nil:
				t.Fatalf("Restore() error = %v", err)
			case tt.on && (!slices.Equal(report.Restored, []string{helperID}) || len(w.quitting()) != 1 || len(w.opened()) != 1 || started < imported):
				t.Errorf("Restore() = %+v, ran %q; want it quit once, restored, then started again", report, calls)
			case !tt.on && (report.Waiting[helperID] != "Prism is running" || len(w.quitting())+len(w.opened()) != 0):
				t.Errorf("Restore() = %+v, ran %q; want it waiting, neither quit nor opened", report, calls)
			}
		})
	}
}

// A sandboxed windowless helper with no settings folder kit opens out of
// view till macOS makes it one, then quits, and its settings come back. It
// wasn't running, so it stays quit.
func TestRestoreOpensAWindowlessHelperForItsFolder(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	helper, _ := w.windowless(t, "LSUIElement", true)
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", helper).Prints(sandboxedApp)
	w.closed(helperID)
	w.fake.On("open", "-g", "-b", helperID).Does(func() {
		w.write(t, filepath.Join(w.home, "Library", "Containers", helperID, ".made"), "")
		w.running(helperID)
	})
	w.quits(helperID)
	w.imports(t, helperID, "laptop", map[string]any{}, map[string]any{"A": true})

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || !slices.Equal(report.Restored, []string{helperID}) || len(w.quitting()) != 1 || !slices.Equal(w.opened(), []string{"open -g -b " + helperID}) {
		t.Errorf("Restore() = %+v, %v, ran %q; want it opened out of view once, quit and restored", report, err, w.fake.Calls())
	}
	if rec, _ := w.p.Load(); rec.Pending != nil {
		t.Errorf("pending = %+v, want nothing held", rec.Pending)
	}
}

// A windowless helper whose folder doesn't come in time, kit waiting no
// longer than it allows, is quit again and waits, as for its app, with
// why: nothing says it's to open, nor counts it failed, and the hourly
// pass opens it again.
func TestAWindowlessHelperWithNoFolderWaits(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	helper, slept := w.windowless(t, "LSUIElement", true)
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", helper).Prints(sandboxedApp)
	w.closed(helperID)
	w.fake.On("open", "-g", "-b", helperID).Does(func() { w.running(helperID) })
	w.quits(helperID)

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || len(report.Restored) != 0 || report.Waiting[helperID] != "its settings folder hasn't come yet" || len(report.ToOpen) != 0 || len(w.quitting()) != 1 {
		t.Errorf("Restore() = %+v, %v, ran %q; want it waiting, quit", report, err, w.fake.Calls())
	}
	if *slept != 15*time.Second {
		t.Errorf("waited %v for its folder, want 15s", *slept)
	}
	if got := report.Summary(); strings.Contains(got, "open") || !strings.Contains(got, "1 pending") {
		t.Errorf("Summary() = %q, want it pending, nothing to open", got)
	}
	rec, _ := w.p.Load()
	if rec.Pending == nil || len(rec.Pending.Open) != 0 || rec.Pending.Why[helperID] != "its settings folder hasn't come yet" {
		t.Errorf("pending = %+v, want it waiting, not to open", rec.Pending)
	}
	if domains, _ := w.p.Failures(rec); domains != 0 || len(w.p.ToOpen(t.Context(), rec)) != 0 {
		t.Errorf("Failures() = %d, ToOpen() = %+v; want neither", domains, w.p.ToOpen(t.Context(), rec))
	}
	if _, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true}); err != nil || len(w.opened()) != 2 {
		t.Errorf("the hourly pass = %v, opened %q; want it opened again", err, w.opened())
	}
}

// A windowless helper held to open, as kit held one before it opened them
// itself, gets no to-do.
func TestAWindowlessHelperHeldToOpenGetsNoToDo(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	helper, _ := w.windowless(t, "LSUIElement", true)
	w.fake.On("codesign", "-d", "--entitlements", "-", "--xml", helper).Prints(sandboxedApp)
	writeRecord(t, w.state, `{"on": "restored", "pending": {"from": "laptop", "domains": ["`+helperID+`"],
		"open": {"`+helperID+`": {"id": "`+helperID+`", "name": "Prism"}}, "why": {"`+helperID+`": "it has no settings folder till it's opened once"}}}`)
	rec, _ := w.p.Load()
	if got := w.p.ToOpen(t.Context(), rec); len(got) != 0 {
		t.Errorf("ToOpen() = %+v, want no to-do for a windowless helper", got)
	}
}

// A windowless helper that won't quit, though kit asks it to, waits as a
// running app does, kit waiting no longer than it allows, and isn't
// started again.
func TestAWindowlessHelperThatWontQuitWaits(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	_, slept := w.windowless(t, "LSBackgroundOnly", true)
	w.write(t, filepath.Join(w.home, "Library", "Containers", helperID, ".made"), "")
	w.running(helperID)
	w.fake.On("osascript", "-e", `quit app id "`+helperID+`"`)

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || report.Waiting[helperID] != "Prism is running" || len(w.quitting()) != 1 || len(w.opened()) != 0 {
		t.Errorf("Restore() = %+v, %v, ran %q; want it asked to quit, waiting, not opened", report, err, w.fake.Calls())
	}
	if *slept != 5*time.Second {
		t.Errorf("waited %v for it to quit, want 5s", *slept)
	}
}

// A domain whose app of its own is running waits for it, its windowless
// helper left running: quitting it would be for nothing.
func TestAHelperIsLeftWhileItsAppRuns(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.write(t, filepath.Join(w.clone, "laptop", "domains", "com.example.suite.plist"), stored(t, map[string]any{"A": true}))
	app := w.app(t, w.apps, "Suite", "com.example.suite.main")
	w.write(t, filepath.Join(app, "Contents", "Library", "LoginItems", "Agent.app", "Contents", "Info.plist"), stored(t, map[string]any{"CFBundleIdentifier": "com.example.suite.agent", "LSUIElement": true}))
	w.running("com.example.suite.main")
	w.running("com.example.suite.agent")
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || report.Waiting["com.example.suite"] != "Suite is running" || len(w.quitting())+len(w.opened()) != 0 {
		t.Errorf("Restore() = %+v, %v, ran %q; want it waiting for Suite, its helper left be", report, err, w.fake.Calls())
	}
}

// A dry run neither quits nor opens a windowless helper: one running waits.
func TestADryRunLeavesAWindowlessHelperBe(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.windowless(t, "LSUIElement", true)
	w.running(helperID)
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{DryRun: true})
	if err != nil || report.Waiting[helperID] != "Prism is running" || len(w.quitting())+len(w.opened()) != 0 {
		t.Errorf("Restore(--dry-run) = %+v, %v, ran %q; want it waiting, neither quit nor opened", report, err, w.fake.Calls())
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
	if got := append(w.quitting(), w.opened()...); len(got) != 0 {
		t.Errorf("kit ran %q; want it to open and quit nothing", got)
	}
}
