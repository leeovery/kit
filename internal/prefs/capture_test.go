package prefs_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// world is a Mac for prefs: a made-up home, kit's state, and the store's
// clone, every program faked.
type world struct {
	home, state, clone, apps string
	fake                     *runnertest.Fake
	p                        *prefs.Prefs
}

const remote = "git@github.com:someone/prefs.git"

func newWorld(t *testing.T, lists prefs.Lists) *world {
	t.Helper()
	w := &world{home: t.TempDir(), state: t.TempDir(), apps: t.TempDir(), fake: runnertest.New(t)}
	w.clone = filepath.Join(t.TempDir(), "prefs")
	if err := os.MkdirAll(filepath.Join(w.clone, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC) }
	w.p = prefs.New(w.fake, w.home, w.state, w.clone, remote, "laptop", lists, now)
	w.p.Probes = nil
	w.p.AppDirs = []string{w.apps}
	w.p.Sleep = func(time.Duration) {}
	for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
		w.fake.On("git", "-C", w.clone, "config", kv[0], kv[1])
	}
	w.fake.On("git", "-C", w.clone, "pull", "--rebase", "--autostash", "--quiet")
	w.fake.On("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Prints(`  "IOPlatformUUID" = "HW-THIS"` + "\n")
	w.fake.On("sysctl", "-n", "hw.model").Prints("Mac99,1\n")
	return w
}

// pushes scripts a private remote taking the commit made with message.
func (w *world) pushes(message string) {
	w.fake.On("git", "-C", w.clone, "status", "--porcelain", "--", "laptop").Prints(" M laptop\n")
	w.fake.On("git", "-C", w.clone, "add", "--", "laptop")
	w.fake.On("git", "-C", w.clone, "commit", "--quiet", "-m", message, "--", "laptop")
	w.fake.On("git", "-C", w.clone, "remote", "get-url", "origin").Prints(remote + "\n")
	w.fake.On("gh", "repo", "view", "someone/prefs", "--json", "visibility", "--jq", ".visibility").Prints("PRIVATE\n")
	w.fake.On("git", "-C", w.clone, "remote").Prints("origin\n")
	w.fake.On("git", "-C", w.clone, "push", "--quiet")
}

func (w *world) write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (w *world) read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// exports scripts defaults export of domain printing settings.
func (w *world) exports(t *testing.T, domain string, settings map[string]any) {
	t.Helper()
	data, err := plist.Encode(settings)
	if err != nil {
		t.Fatal(err)
	}
	w.fake.On("defaults", "export", domain, "-").Prints(string(data))
}

func stored(t *testing.T, settings map[string]any) string {
	t.Helper()
	data, err := plist.Encode(settings)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCaptureSavesWhatChanged(t *testing.T) {
	w := newWorld(t, prefs.Lists{
		Files: []string{"~/.config/tool/settings.json", "~/.config/folder", "~/Library/Tool/*20[0-9][0-9].[0-9]/options", "~/missing.json"},
		Deny:  []string{"net.chatty.*"},
	})
	if err := w.p.StartFresh(); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(w.clone, "laptop")
	// The record says a domain waits to be restored: capture leaves it.
	writeRecord(t, w.state, `{"on": "started fresh", "pending": {"from": "laptop", "domains": ["com.example.pending"]}}`)
	w.write(t, filepath.Join(store, "domains", "com.example.pending.plist"), "kept as stored")
	w.write(t, filepath.Join(store, "domains", "com.example.gone.plist"), "an app since removed")
	w.write(t, filepath.Join(store, "domains", "com.example.empty.plist"), "settings since emptied")
	w.write(t, filepath.Join(store, "domains", "com.example.broken.plist"), "unreadable this time")
	w.write(t, filepath.Join(store, "files", ".config", "dropped.json"), "no longer listed")
	w.write(t, filepath.Join(store, "files", ".config", ".hidden"), "left alone")
	w.write(t, filepath.Join(w.home, ".config", "tool", "settings.json"), `{"a": 1}`)
	w.write(t, filepath.Join(w.home, ".config", "folder", "one.txt"), "1")
	w.write(t, filepath.Join(w.home, ".config", "folder", "deeper", "two.txt"), "2")
	w.write(t, filepath.Join(w.home, ".config", "folder", ".DS_Store"), "Finder's")
	w.write(t, filepath.Join(w.home, "Library", "Tool", "App2026.2", "options", "ui.xml"), "<ui/>")
	w.write(t, filepath.Join(w.home, "Library", "Tool", "App2026.1", "options", "ui.xml"), "<old/>")
	w.write(t, filepath.Join(w.home, "Library", "Tool", "Cache", "options", "ui.xml"), "not a version")

	w.fake.On("defaults", "domains").Prints("com.apple.finder, com.example.app, com.example.empty, com.example.broken, net.chatty.App, com.example.pending, loginwindow, com.example.same\n")
	w.exports(t, "com.example.app", map[string]any{"Theme": "dark", "NSWindow Frame Main": "0 0 100 100", "SULastCheckTime": time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)})
	w.exports(t, "com.example.empty", map[string]any{"NSWindow Frame Main": "0 0 1 1"})
	w.fake.On("defaults", "export", "com.example.broken", "-").Exits(1).PrintsToStderr("couldn't read it")
	w.exports(t, "com.example.same", map[string]any{"Size": int64(3)})
	w.write(t, filepath.Join(store, "domains", "com.example.same.plist"), stored(t, map[string]any{"Size": int64(3)}))
	message := `Capture 2026-10-06 03:00: 6 new, 3 removed

new: com.example.app
new: ~/.config/tool/settings.json
new: ~/.config/folder/one.txt
new: ~/.config/folder/deeper/two.txt
new: ~/Library/Tool/App2026.1/options/ui.xml
new: ~/Library/Tool/App2026.2/options/ui.xml
removed: domains/com.example.empty.plist
removed: domains/com.example.gone.plist
removed: files/.config/dropped.json
claimed: the folder, for this Mac's hardware`
	w.pushes(message)

	report, err := w.p.Capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := report.Summary(), "2 domains, 1 changed (1 new); 5 files, 5 changed (5 new); 3 removed; 1 pending restore; 1 errors"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	if got := w.read(t, filepath.Join(store, "domains", "com.example.app.plist")); got != stored(t, map[string]any{"Theme": "dark"}) {
		t.Errorf("com.example.app stored as\n%s\nwant the keys that change by themselves stripped", got)
	}
	for path, want := range map[string]string{
		"domains/com.example.pending.plist":           "kept as stored",
		"domains/com.example.broken.plist":            "unreadable this time",
		"files/.config/tool/settings.json":            `{"a": 1}`,
		"files/.config/folder/deeper/two.txt":         "2",
		"files/Library/Tool/App2026.2/options/ui.xml": "<ui/>",
		"files/.config/.hidden":                       "left alone",
		"owner":                                       "hardware HW-THIS\nmodel Mac99,1\nsince 2026-10-06 03:00\n",
		".gitignore":                                  ".DS_Store\n.*.tmp\n",
	} {
		if got := w.read(t, filepath.Join(store, path)); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	for _, gone := range []string{"domains/com.example.gone.plist", "domains/com.example.empty.plist", "files/.config/dropped.json", "files/.config/folder/.DS_Store", "files/Library/Tool/Cache"} {
		if _, err := os.Stat(filepath.Join(store, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is there, want it gone", gone)
		}
	}
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "com.apple.finder") || strings.Contains(c, "net.chatty") || strings.Contains(c, "loginwindow") || strings.Contains(c, "export com.example.pending") {
			t.Errorf("ran %s: Apple's, the system's, denied and pending domains are left alone", c)
		}
	}
	rec, _ := w.p.Load()
	if rec.Summary != report.Summary() || !rec.Captured.Equal(time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)) || rec.Errors != 1 || rec.PushError != "" {
		t.Errorf("record = %+v", rec)
	}
}

func writeRecord(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "prefs.json"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureRefuses(t *testing.T) {
	t.Run("before it's switched on", func(t *testing.T) {
		w := newWorld(t, prefs.Lists{})
		if _, err := w.p.Capture(t.Context()); err != prefs.ErrPaused {
			t.Errorf("Capture() = %v, want it paused", err)
		}
	})
	t.Run("without Full Disk Access", func(t *testing.T) {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "started fresh"}`)
		locked := filepath.Join(t.TempDir(), "Safari")
		if err := os.Mkdir(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		w.p.Probes = []string{locked, filepath.Join(t.TempDir(), "absent")}
		if _, err := w.p.Capture(t.Context()); err == nil || !strings.Contains(err.Error(), "no Full Disk Access") {
			t.Errorf("Capture() = %v", err)
		}
	})
	t.Run("into another Mac's folder", func(t *testing.T) {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "restored from laptop"}`)
		w.write(t, filepath.Join(w.clone, "laptop", "owner"), "hardware HW-OLD\nmodel Mac1,1\nsince 2026-09-28 17:00\n")
		_, err := w.p.Capture(t.Context())
		var notOwner prefs.NotOwnerError
		if !asNotOwner(err, &notOwner) || notOwner.Owner.Hardware != "HW-OLD" || !strings.Contains(err.Error(), "belongs to another Mac (hardware HW-OLD, a Mac1,1, since 2026-09-28 17:00)") {
			t.Errorf("Capture() = %v", err)
		}
		if rec, _ := w.p.Load(); rec.NotOwner == "" {
			t.Error("the record doesn't say why capture was refused")
		}
		for _, c := range w.fake.Calls() {
			if strings.HasPrefix(c, "defaults ") {
				t.Errorf("ran %s: nothing is captured into another Mac's folder", c)
			}
		}
	})
}

func asNotOwner(err error, into *prefs.NotOwnerError) bool {
	e, ok := err.(prefs.NotOwnerError)
	if ok {
		*into = e
	}
	return ok
}

// A store that isn't private is never pushed to: the commit waits, and the
// record says since when.
func TestCaptureNeverPushesToAPublicStore(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "started fresh"}`)
	w.fake.On("defaults", "domains").Prints("com.example.app\n")
	w.exports(t, "com.example.app", map[string]any{"Theme": "dark"})
	w.pushes("Capture 2026-10-06 03:00: 1 new\n\nnew: com.example.app\nclaimed: the folder, for this Mac's hardware")
	w.fake.On("gh", "repo", "view", "someone/prefs", "--json", "visibility", "--jq", ".visibility").Prints("PUBLIC\n")
	report, err := w.p.Capture(t.Context())
	if err != nil || !strings.Contains(report.PushError, "someone/prefs is public on GitHub: kit pushes apps' settings only to a private repository") {
		t.Errorf("Capture() = %+v, %v", report, err)
	}
	if slices.Contains(w.fake.Calls(), "git -C "+w.clone+" push --quiet") {
		t.Error("pushed to a public repository")
	}
	if rec, _ := w.p.Load(); rec.Unpushed.IsZero() || rec.PushError == "" {
		t.Errorf("record = %+v, want the push waiting", rec)
	}
}

// Python's fnmatch and glob, as prefsync matched: a wildcard matches a
// slash too in a pattern, and never a hidden name in a glob.
func TestPatterns(t *testing.T) {
	w := newWorld(t, prefs.Lists{Files: []string{"~/Tool/*20[0-9][0-9].[0-9]/opts"}, Deny: []string{"jetbrains.*.csat", "a.[!b].c"}})
	writeRecord(t, w.state, `{"on": "started fresh"}`)
	for _, dir := range []string{"Tool/App2026.2/opts/x", "Tool/.App2026.3/opts/x", "Tool/App26.2/opts/x"} {
		w.write(t, filepath.Join(w.home, dir), "x")
	}
	w.fake.On("defaults", "domains").Prints("jetbrains.ps.262.csat, jetbrains.csat, a.x.c, a.b.c\n")
	w.exports(t, "jetbrains.csat", map[string]any{"a": true})
	w.exports(t, "a.b.c", map[string]any{"a": true})
	w.pushes("Capture 2026-10-06 03:00: 3 new\n\nnew: a.b.c\nnew: jetbrains.csat\nnew: ~/Tool/App2026.2/opts/x\nclaimed: the folder, for this Mac's hardware")
	if report, err := w.p.Capture(t.Context()); err != nil || report.Domains != 2 || report.Files != 1 {
		t.Errorf("Capture() = %+v, %v", report, err)
	}
}
