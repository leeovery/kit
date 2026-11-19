package prefs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// world is a Mac for prefs: a made-up home, kit's state, kit-config's
// clone, and the store's clone, every program faked.
type world struct {
	home, state, config, clone, apps string
	fake                             *runnertest.Fake
	p                                *prefs.Prefs
}

// remote is where kit-config's clone fetches from, and so the store's.
const remote = "git@github.com:someone/kit-config.git"

func newWorld(t *testing.T, lists prefs.Lists) *world {
	t.Helper()
	w := &world{home: t.TempDir(), state: t.TempDir(), config: t.TempDir(), apps: t.TempDir(), fake: runnertest.New(t)}
	w.clone = filepath.Join(t.TempDir(), "prefs")
	if err := os.MkdirAll(filepath.Join(w.clone, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC) }
	w.p = prefs.New(w.fake, w.home, w.state, w.clone, w.config, "laptop", lists, now)
	w.p.Probes = nil
	w.p.AppDirs = []string{w.apps}
	w.p.Sleep = func(time.Duration) {}
	onTheBranch(w.fake, w.config, w.clone)
	w.pulls()
	w.fake.On("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Prints(`  "IOPlatformUUID" = "HW-THIS"` + "\n")
	w.fake.On("sysctl", "-n", "hw.model").Prints("Mac99,1\n")
	w.fake.On("scutil", "--get", "ComputerName").Prints("Laptop\n")
	return w
}

// onTheBranch scripts, on fake, kit-config's clone, at config, fetching
// from remote alone, its branch, main, on the remote, and the store's
// clone, at clone, a clone of its prefs branch, with the clone's own git
// settings.
func onTheBranch(fake *runnertest.Fake, config, clone string) {
	fake.On("git", "-C", config, "remote").Prints("origin\n")
	fake.On("git", "-C", config, "symbolic-ref", "-q", "HEAD").Prints("refs/heads/main\n")
	fake.On("git", "-C", config, "for-each-ref", "--format=%(upstream) %(upstream:remoteref)", "refs/heads/main").Prints("refs/remotes/origin/main refs/heads/main\n")
	fake.On("git", "-C", config, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main").Prints(head + "\n")
	fake.On("git", "-C", config, "remote", "get-url", "origin").Prints(remote + "\n")
	fake.On("git", "-C", config, "config", "--get", "remote.origin.url").Prints(remote + "\n")
	fake.On("git", "-C", config, "remote", "get-url", "--push", "--all", "origin").Prints(remote + "\n")
	fake.On("git", carries(config)...).Exits(1)
	fake.On("git", "-C", clone, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads").Prints("refs/heads/prefs\n")
	fake.On("git", "-C", clone, "remote", "get-url", "origin").Prints(remote + "\n")
	fake.On("git", "-C", clone, "remote", "get-url", "--push", "--all", "origin").Prints(remote + "\n")
	fake.On("git", "-C", clone, "symbolic-ref", "-q", "HEAD").Prints("refs/heads/prefs\n")
	tracks(fake, clone, "origin", "refs/heads/prefs")
	for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
		fake.On("git", "-C", clone, "config", kv[0], kv[1])
	}
}

// tracks scripts, on fake, the store's prefs branch in its clone, at
// clone, set to track merge, a branch of remote: each "" for not set, as
// git says by exiting 1. Its scripts answer each read in turn.
func tracks(fake *runnertest.Fake, clone, remote, merge string) (remoteOf, mergeOf *runnertest.Script) {
	read := func(key, value string) *runnertest.Script {
		script := fake.On("git", "-C", clone, "config", "--get", key)
		if value == "" {
			return script.Exits(1)
		}
		return script.Prints(value + "\n")
	}
	return read("branch.prefs.remote", remote), read("branch.prefs.merge", merge)
}

// carries is git reading the settings the store takes from kit-config, at
// config: its own credential and push URL settings, exiting 1 with none,
// as real git does.
func carries(config string) []string {
	return []string{"-C", config, "config", "--local", "--get-regexp", "-z", `^(credential\..*|remote\..*\.pushurl)$`}
}

// pulls scripts a pull of the store's, as real git answers, its one
// remote, origin, with nothing new, and nothing changed in the clone; its
// branch prefs, origin's prefs its upstream.
func (w *world) pulls() {
	w.fake.On("git", "-C", w.clone, "remote").Prints("origin\n")
	w.fake.On("git", "-C", w.clone, "rev-parse", "--absolute-git-dir").Prints(filepath.Join(w.clone, ".git") + "\n")
	w.fake.On("git", "-C", w.clone, "--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")
	w.fake.On("git", "-C", w.clone, "symbolic-ref", "-q", "HEAD").Prints("refs/heads/prefs\n")
	w.fake.On("git", "-C", w.clone, "for-each-ref", "--format=%(upstream) %(upstream:remoteref)", "refs/heads/prefs").Prints("refs/remotes/origin/prefs refs/heads/prefs\n")
	w.fake.On("git", "-C", w.clone, "fetch", "--prune", "--quiet", "origin")
	w.fake.On("git", "-C", w.clone, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/prefs").Prints(head + "\n")
	w.fake.On("git", "-C", w.clone, "rev-list", "--count", "HEAD..refs/remotes/origin/prefs").Prints("0\n")
}

// pushes scripts the commit of the Mac's folder with message, git's
// status of it being status (-z), as real git prints it: files committed
// whole, those of them new to git, added, entered in its index first; then
// a private remote taking it, with nothing new of its own, nothing else
// changed in the clone. Any other commit, or a push, fails the test, as an
// unscripted command.
func (w *world) pushes(t *testing.T, message, status string, files, added []string) {
	t.Helper()
	git := func(args ...string) []string { return append([]string{"-C", w.clone}, args...) }
	paths := func(args []string, paths []string) []string {
		return git(slices.Concat([]string{"--literal-pathspecs"}, args, []string{"--"}, paths)...)
	}
	w.fake.On("git", paths([]string{"status", "--porcelain", "-z", "--untracked-files=all", "--no-renames"}, []string{"laptop"})...).Prints(status)
	w.fake.On("git", git("rev-parse", "--absolute-git-dir")...).Prints(filepath.Join(w.clone, ".git") + "\n")
	w.fake.On("git", git("config", "--get", "--type=bool", "commit.gpgsign")...).Prints("false\n")
	if len(added) > 0 {
		w.fake.On("git", paths([]string{"add", "--intent-to-add"}, added)...)
	}
	w.fake.On("git", git("rev-parse", "--verify", "--quiet", "HEAD")...).Prints(head + "\n")
	w.fake.On("git", paths([]string{"diff", "--quiet", "--no-ext-diff", "HEAD"}, files)...).Exits(1)
	w.fake.On("git", paths([]string{"-c", "core.hooksPath=/dev/null", "commit", "--quiet", "--only", "-m", message}, files)...)
	w.fake.On("git", git("remote", "get-url", "--push", "--all", "origin")...).Prints(remote + "\n")
	w.fake.On("gh", "repo", "view", "https://github.com/someone/kit-config", "--json", "visibility", "--jq", ".visibility").Prints("PRIVATE\n")
	w.pulls()
	w.fake.On("git", git("push", "--quiet", "origin", "HEAD:refs/heads/prefs")...)
}

// newFiles are the files a first capture of one domain makes, each new to
// git.
var newFiles = []string{"laptop/.gitignore", "laptop/domains/com.example.app.plist", "laptop/owner"}

// head is the commit HEAD names in the store's clone.
const head = "0e0bf17a399109574ffb00e33300280a33f81ab7"

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
		Files: []string{"~/.config/tool/settings.json", "~/.config/tool/held.json", "~/.config/tool/absent.json", "~/.config/folder", "~/Library/Tool/*20[0-9][0-9].[0-9]/options", "~/missing.json"},
		Deny:  []string{"net.chatty.*"},
	})
	if err := w.p.StartFresh(); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(w.clone, "laptop")
	// The record says a domain and two files wait to be restored, one of
	// them not on the Mac, and a domain's kept till its app is installed:
	// capture leaves them, counting the kept one apart.
	writeRecord(t, w.state, `{"on": "started fresh", "pending": {"from": "laptop", "domains": ["com.example.kept", "com.example.pending"], "files": ["~/.config/tool/absent.json", "~/.config/tool/held.json"],
		"why": {"com.example.kept": "kept till its app is installed"}}}`)
	w.write(t, filepath.Join(store, "domains", "com.example.kept.plist"), "kept as stored")
	w.write(t, filepath.Join(store, "files", ".config", "tool", "absent.json"), "kept though absent")
	w.write(t, filepath.Join(store, "domains", "com.example.pending.plist"), "kept as stored")
	w.write(t, filepath.Join(store, "files", ".config", "tool", "held.json"), "kept as stored")
	w.write(t, filepath.Join(w.home, ".config", "tool", "held.json"), "the Mac's")
	w.write(t, filepath.Join(store, "domains", "com.example.gone.plist"), "an app since removed")
	w.write(t, filepath.Join(store, "domains", "com.example.empty.plist"), "settings since emptied")
	w.write(t, filepath.Join(store, "domains", "com.example.broken.plist"), "unreadable this time")
	w.write(t, filepath.Join(store, "files", ".config", "dropped.json"), "no longer listed")
	w.write(t, filepath.Join(store, "files", ".config", ".hidden"), "no longer listed, a dot-file")
	w.write(t, filepath.Join(store, "files", ".config", "folder", ".gone.txt.tmp"), "capture's, half written")
	w.write(t, filepath.Join(w.home, ".config", "tool", "settings.json"), `{"a": 1}`)
	w.write(t, filepath.Join(w.home, ".config", "folder", "one.txt"), "1")
	w.write(t, filepath.Join(w.home, ".config", "folder", "deeper", "two.txt"), "2")
	w.write(t, filepath.Join(w.home, ".config", "folder", ".DS_Store"), "Finder's")
	w.write(t, filepath.Join(w.home, ".config", "folder", ".editor.tmp"), "an app's, half written")
	w.write(t, filepath.Join(w.home, "Library", "Tool", "App2026.2", "options", "ui.xml"), "<ui/>")
	w.write(t, filepath.Join(w.home, "Library", "Tool", "App2026.1", "options", "ui.xml"), "<old/>")
	w.write(t, filepath.Join(w.home, "Library", "Tool", "Cache", "options", "ui.xml"), "not a version")

	w.fake.On("defaults", "domains").Prints("com.apple.finder, com.example.app, com.example.empty, com.example.broken, net.chatty.App, com.example.pending, loginwindow, com.example.same\n")
	w.exports(t, "com.example.app", map[string]any{"Theme": "dark", "NSWindow Frame Main": "0 0 100 100", "SULastCheckTime": time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)})
	w.exports(t, "com.example.empty", map[string]any{"NSWindow Frame Main": "0 0 1 1"})
	w.fake.On("defaults", "export", "com.example.broken", "-").Exits(1).PrintsToStderr("couldn't read it")
	w.exports(t, "com.example.same", map[string]any{"Size": int64(3)})
	w.write(t, filepath.Join(store, "domains", "com.example.same.plist"), stored(t, map[string]any{"Size": int64(3)}))
	message := `Capture 2026-10-06 03:00: 6 new, 4 removed

new: com.example.app
new: ~/.config/tool/settings.json
new: ~/.config/folder/one.txt
new: ~/.config/folder/deeper/two.txt
new: ~/Library/Tool/App2026.1/options/ui.xml
new: ~/Library/Tool/App2026.2/options/ui.xml
removed: domains/com.example.empty.plist
removed: domains/com.example.gone.plist
removed: files/.config/.hidden
removed: files/.config/dropped.json
claimed: the folder, for this Mac's hardware`
	// The store's files from before were committed then.
	w.pushes(t, message,
		" D laptop/domains/com.example.empty.plist\x00 D laptop/domains/com.example.gone.plist\x00 D laptop/files/.config/.hidden\x00 D laptop/files/.config/dropped.json\x00"+
			"?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/files/.config/folder/deeper/two.txt\x00?? laptop/files/.config/folder/one.txt\x00"+
			"?? laptop/files/.config/tool/settings.json\x00?? laptop/files/Library/Tool/App2026.1/options/ui.xml\x00?? laptop/files/Library/Tool/App2026.2/options/ui.xml\x00?? laptop/owner\x00",
		[]string{
			"laptop/domains/com.example.empty.plist", "laptop/domains/com.example.gone.plist", "laptop/files/.config/.hidden", "laptop/files/.config/dropped.json",
			"laptop/.gitignore", "laptop/domains/com.example.app.plist", "laptop/files/.config/folder/deeper/two.txt", "laptop/files/.config/folder/one.txt",
			"laptop/files/.config/tool/settings.json", "laptop/files/Library/Tool/App2026.1/options/ui.xml", "laptop/files/Library/Tool/App2026.2/options/ui.xml", "laptop/owner",
		},
		[]string{
			"laptop/.gitignore", "laptop/domains/com.example.app.plist", "laptop/files/.config/folder/deeper/two.txt", "laptop/files/.config/folder/one.txt",
			"laptop/files/.config/tool/settings.json", "laptop/files/Library/Tool/App2026.1/options/ui.xml", "laptop/files/Library/Tool/App2026.2/options/ui.xml", "laptop/owner",
		})

	report, err := w.p.Capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Committed {
		t.Error("the capture wasn't committed")
	}
	if got, want := report.Summary(), "2 domains, 1 changed (1 new); 5 files, 5 changed (5 new); 4 removed; 3 pending restore; 1 kept for apps not installed; 1 errors"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	if got := w.read(t, filepath.Join(store, "domains", "com.example.app.plist")); got != stored(t, map[string]any{"Theme": "dark"}) {
		t.Errorf("com.example.app stored as\n%s\nwant the keys that change by themselves stripped", got)
	}
	for path, want := range map[string]string{
		"domains/com.example.pending.plist":           "kept as stored",
		"domains/com.example.kept.plist":              "kept as stored",
		"files/.config/tool/held.json":                "kept as stored",
		"files/.config/tool/absent.json":              "kept though absent",
		"domains/com.example.broken.plist":            "unreadable this time",
		"files/.config/tool/settings.json":            `{"a": 1}`,
		"files/.config/folder/deeper/two.txt":         "2",
		"files/Library/Tool/App2026.2/options/ui.xml": "<ui/>",
		"files/.config/folder/.gone.txt.tmp":          "capture's, half written",
		"owner":                                       "hardware HW-THIS\nmodel Mac99,1\nname Laptop\nsince 2026-10-06 03:00\n",
		".gitignore":                                  ".DS_Store\n.*.tmp\n",
	} {
		if got := w.read(t, filepath.Join(store, path)); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	for _, gone := range []string{"domains/com.example.gone.plist", "domains/com.example.empty.plist", "files/.config/dropped.json", "files/.config/.hidden", "files/.config/folder/.DS_Store", "files/.config/folder/.editor.tmp", "files/Library/Tool/Cache"} {
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
	t.Run("into a folder another Mac took over, as the pull brings in", func(t *testing.T) {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "restored from laptop"}`)
		w.write(t, filepath.Join(w.clone, "laptop", "owner"), "hardware HW-THIS\nmodel Mac99,1\nsince 2026-09-28 17:00\n")
		w.fake.On("git", "-C", w.clone, "rev-list", "--count", "HEAD..refs/remotes/origin/prefs").Prints("1\n")
		w.fake.On("git", "-C", w.clone, "diff", "--name-only", "-z", "--no-renames", "HEAD...refs/remotes/origin/prefs").Prints("laptop/owner\x00")
		w.fake.On("git", "-C", w.clone, "stash", "create", "kit: your changes before pulling")
		w.fake.On("git", "-C", w.clone, "rebase", "--no-fork-point", "--quiet", "refs/remotes/origin/prefs").Does(func() {
			w.write(t, filepath.Join(w.clone, "laptop", "owner"), "hardware HW-NEW\nmodel Mac100,1\nsince 2026-10-05 09:00\n")
		})
		var notOwner prefs.NotOwnerError
		if _, err := w.p.Capture(t.Context()); !asNotOwner(err, &notOwner) || notOwner.Owner.Hardware != "HW-NEW" {
			t.Errorf("Capture() = %v, want it refused, the folder the new Mac's", err)
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

// A capture whose commit failed pushes nothing: refused while git's part
// way through a merge in the clone, which pulling or pushing would set
// aside, so nothing's brought in either; or given up as another git held
// the index throughout. The record says why.
func TestCaptureHoldsBackThePushWhenTheCommitFailed(t *testing.T) {
	const message = "Capture 2026-10-06 03:00: 1 new\n\nnew: com.example.app\nclaimed: the folder, for this Mac's hardware"
	for _, c := range []struct{ name, want string }{
		{"a merge under way", "not pushed: a merge is under way in the repository (MERGE_HEAD)"},
		{"the index held", "not pushed: another git holds the repository's index, so kit's change waits till it's done"},
	} {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "started fresh"}`)
		w.fake.On("defaults", "domains").Prints("com.example.app\n")
		w.exports(t, "com.example.app", map[string]any{"Theme": "dark"})
		w.pushes(t, message, "?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/owner\x00", newFiles, newFiles)
		if c.name == "a merge under way" {
			w.write(t, filepath.Join(w.clone, ".git", "MERGE_HEAD"), head+"\n")
		} else {
			w.fake.On("git", slices.Concat([]string{"-C", w.clone, "--literal-pathspecs", "add", "--intent-to-add", "--"}, newFiles)...).Exits(128).
				PrintsToStderr("fatal: Unable to create '" + filepath.Join(w.clone, ".git", "index.lock") + "': File exists.\n")
		}
		report, err := w.p.Capture(t.Context())
		if err != nil || report.Committed || !strings.HasPrefix(report.PushError, c.want) {
			t.Errorf("%s: Capture() = %+v, %v; want %q", c.name, report, err, c.want)
		}
		for _, call := range w.fake.Calls() {
			if strings.Contains(call, " push ") || strings.Contains(call, " stash ") || strings.Contains(call, " fetch ") && c.name == "a merge under way" {
				t.Errorf("%s: ran %s, want nothing pushed or set aside", c.name, call)
			}
		}
		if rec, _ := w.p.Load(); rec.Unpushed.IsZero() || !strings.HasPrefix(rec.PushError, c.want) {
			t.Errorf("%s: record = %+v, want the push held back, and why", c.name, rec)
		}
	}
}

// The store waits for kit-config's own branch on the remote, as when kit
// setup's first push to a new repository didn't go: GitHub makes the first
// branch pushed the one a new Mac's boot clones. The commit waits, and the
// record says since when.
func TestCaptureWaitsForKitConfigsBranch(t *testing.T) {
	w := firstCapture(t)
	w.fake.On("git", "-C", w.config, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main").Exits(1)
	w.heldBack(t, "kit-config's branch not on its remote", "kit-config's own branch isn't on its remote yet")
}

// firstCapture is a Mac's first capture, of one domain, scripted to its
// push (pushes).
func firstCapture(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "started fresh"}`)
	w.fake.On("defaults", "domains").Prints("com.example.app\n")
	w.exports(t, "com.example.app", map[string]any{"Theme": "dark"})
	w.pushes(t, "Capture 2026-10-06 03:00: 1 new\n\nnew: com.example.app\nclaimed: the folder, for this Mac's hardware",
		"?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/owner\x00", newFiles, newFiles)
	return w
}

// heldBack checks a capture saves, pushing nothing, the push held saying
// says, and the record saying since when.
func (w *world) heldBack(t *testing.T, name, says string) {
	t.Helper()
	report, err := w.p.Capture(t.Context())
	if err != nil || !strings.HasPrefix(report.PushError, says) {
		t.Errorf("%s: Capture() = %+v, %v; want the push held, saying %q", name, report, err, says)
	}
	if slices.ContainsFunc(w.fake.Calls(), func(call string) bool { return strings.Contains(call, " push ") }) {
		t.Errorf("%s: pushed apps' settings", name)
	}
	if rec, _ := w.p.Load(); rec.Unpushed.IsZero() {
		t.Errorf("%s: record = %+v, want the push waiting", name, rec)
	}
}

// A push held back by kit-config's own state, not the store's, says so:
// HEAD on no branch, or git failing to tell whether kit-config's branch is
// on its remote.
func TestCaptureNamesKitConfigHoldingThePush(t *testing.T) {
	w := firstCapture(t)
	w.fake.On("git", "-C", w.config, "symbolic-ref", "-q", "HEAD").Exits(1)
	w.heldBack(t, "kit-config on no branch", "kit-config: HEAD isn't on a branch, so kit pushes nothing")

	w = firstCapture(t)
	w.fake.On("git", "-C", w.config, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main").Exits(128).PrintsToStderr("fatal: bad object\n")
	w.heldBack(t, "kit-config's branch unreadable", "kit-config: ")
}

// The store's branch is pushed only to the remote's branch of the same
// name. One set to track another, kit-config's main, since the clone was
// checked, is never pushed to it. One set to track nothing is pushed to
// the branch, which it then tracks, as real git does.
func TestCapturePushesOnlyToThePrefsBranch(t *testing.T) {
	w := firstCapture(t)
	_, merge := tracks(w.fake, w.clone, "origin", "refs/heads/prefs")
	merge.Then().Prints("refs/heads/main\n")
	w.heldBack(t, "tracking kit-config's main", w.clone+" tracks main on origin, not kit-config's prefs branch: ")

	w = firstCapture(t)
	tracks(w.fake, w.clone, "", "")
	w.fake.On("git", "-C", w.clone, "for-each-ref", "--format=%(upstream) %(upstream:remoteref)", "refs/heads/prefs").Prints(" \n")
	w.fake.On("git", "-C", w.clone, "push", "--quiet", "-u", "origin", "HEAD")
	report, err := w.p.Capture(t.Context())
	if err != nil || report.PushError != "" {
		t.Errorf("tracking nothing: Capture() = %+v, %v; want it pushed", report, err)
	}
	pushed := slices.DeleteFunc(w.fake.Calls(), func(call string) bool { return !strings.Contains(call, " push ") })
	if want := "git -C " + w.clone + " push --quiet -u origin HEAD"; !slices.Equal(pushed, []string{want}) {
		t.Errorf("tracking nothing: pushed %q; want %q alone", pushed, want)
	}
}

// A store that isn't proven private is never pushed to: the commit waits,
// and the record says since when. It's where git push sends it that's
// looked at, kit-config's remote, the store's: a repository on GitHub that
// isn't private; another host, or a path on this Mac, which kit doesn't
// prove private; and a clone with more than one remote, which kit pushes
// to none of. A push URL that isn't kit-config's never gets this far: the
// clone's refused (TestACloneElsewhereIsRefused).
func TestCaptureNeverPushesToAPublicStore(t *testing.T) {
	const notGitHub = "kit proves private only a repository on github.com, at https://github.com/<owner>/<name>, git@github.com:<owner>/<name> or ssh://git@github.com/<owner>/<name>"
	for _, c := range []struct{ name, remotes, url, public, says string }{
		{"the store public", "origin\n", remote, "someone/kit-config", "someone/kit-config is public on GitHub: kit pushes apps' settings only to a private repository"},
		{"another host", "origin\n", "https://gitlab.com/someone/kit-config.git", "", "kit pushes apps' settings only to a repository it can prove private, and can't for https://gitlab.com/someone/kit-config.git: " + notGitHub},
		{"a path on this Mac", "origin\n", "/Volumes/Backup/kit-config.git", "", "kit pushes apps' settings only to a repository it can prove private, and can't for /Volumes/Backup/kit-config.git: " + notGitHub},
		{"two remotes", "origin\nbackup\n", remote, "", "the repository has 2 remotes, so kit pushes nothing: kit pushes to one alone"},
	} {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "started fresh"}`)
		w.fake.On("defaults", "domains").Prints("com.example.app\n")
		w.exports(t, "com.example.app", map[string]any{"Theme": "dark"})
		w.pushes(t, "Capture 2026-10-06 03:00: 1 new\n\nnew: com.example.app\nclaimed: the folder, for this Mac's hardware",
			"?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/owner\x00", newFiles, newFiles)
		w.fake.On("git", "-C", w.config, "remote", "get-url", "origin").Prints(c.url + "\n")
		w.fake.On("git", "-C", w.config, "config", "--get", "remote.origin.url").Prints(c.url + "\n")
		w.fake.On("git", "-C", w.config, "remote", "get-url", "--push", "--all", "origin").Prints(c.url + "\n")
		w.fake.On("git", "-C", w.clone, "remote", "get-url", "origin").Prints(c.url + "\n")
		w.fake.On("git", "-C", w.clone, "remote").Prints(c.remotes)
		w.fake.On("git", "-C", w.clone, "remote", "get-url", "--push", "--all", "origin").Prints(c.url + "\n")
		w.fake.On("gh", "repo", "view", "https://github.com/"+c.public, "--json", "visibility", "--jq", ".visibility").Prints("PUBLIC\n")
		report, err := w.p.Capture(t.Context())
		if err != nil || !strings.HasPrefix(report.PushError, c.says) {
			t.Errorf("%s: Capture() = %+v, %v; want the push held, saying %q", c.name, report, err, c.says)
		}
		if slices.ContainsFunc(w.fake.Calls(), func(call string) bool { return strings.Contains(call, " push ") }) {
			t.Errorf("%s: pushed to a store not proven private", c.name)
		}
		if rec, _ := w.p.Load(); rec.Unpushed.IsZero() || rec.PushError == "" {
			t.Errorf("%s: record = %+v, want the push waiting", c.name, rec)
		}
	}
}

// The live config's wildcard patterns, JetBrains' versioned folders and
// Claude's memory, match the names they meet as they did before a glob was
// read rsync's way.
func TestLivePatternsMatchAsBefore(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	jetbrains, projects := "Library/Application Support/JetBrains", ".claude/projects"
	for _, path := range []string{
		jetbrains + "/PhpStorm2026.2/options/ide.general.xml",
		jetbrains + "/WebStorm2025.3/options/editor.xml",
		jetbrains + "/PhpStorm2026.2-backup/options/editor.xml",
		jetbrains + "/Toolbox/options/settings.xml",
		jetbrains + "/consentOptions/accepted",
		projects + "/-Users-someone-Code-site/memory/MEMORY.md",
		projects + "/-Users-someone--config/memory/notes.md",
		projects + "/-Users-someone-Code-site--claude-worktrees-fix/memory/MEMORY.md",
		projects + "/-Users-someone-Code-site/session.jsonl",
	} {
		w.write(t, filepath.Join(w.home, path), "x")
	}
	for pattern, matched := range map[string][]string{
		jetbrains + "/*20[0-9][0-9].[0-9]/options": {jetbrains + "/PhpStorm2026.2/options", jetbrains + "/WebStorm2025.3/options"},
		projects + "/*/memory":                     {projects + "/-Users-someone--config/memory", projects + "/-Users-someone-Code-site--claude-worktrees-fix/memory", projects + "/-Users-someone-Code-site/memory"},
	} {
		want := make([]string, len(matched))
		for i, m := range matched {
			want[i] = filepath.Join(w.home, m)
		}
		slices.Sort(want)
		if got := prefs.Glob(filepath.Join(w.home, pattern)); !slices.Equal(got, want) {
			t.Errorf("Glob(~/%s) = %q\nwant %q", pattern, got, want)
		}
	}
}

// A domain's pattern matches as rsync's does, [!...] a class's complement.
// A file's glob matches each part as rsync does, as a data restore reads
// it: a wildcard matches a hidden name too, and [^...] is a class's
// complement.
func TestPatterns(t *testing.T) {
	w := newWorld(t, prefs.Lists{Files: []string{"~/Tool/*20[0-9][0-9].[0-9]/opts", "~/Other/[^x]*/conf"}, Deny: []string{"jetbrains.*.csat", "a.[!b].c"}})
	writeRecord(t, w.state, `{"on": "started fresh"}`)
	for _, dir := range []string{"Tool/App2026.2/opts/x", "Tool/.App2026.3/opts/x", "Tool/App26.2/opts/x", "Other/abc/conf", "Other/xyz/conf"} {
		w.write(t, filepath.Join(w.home, dir), "x")
	}
	w.fake.On("defaults", "domains").Prints("jetbrains.ps.262.csat, jetbrains.csat, a.x.c, a.b.c\n")
	w.exports(t, "jetbrains.csat", map[string]any{"a": true})
	w.exports(t, "a.b.c", map[string]any{"a": true})
	files := []string{"laptop/files/Other/abc/conf", "laptop/files/Tool/.App2026.3/opts/x", "laptop/files/Tool/App2026.2/opts/x"}
	paths := slices.Concat([]string{"laptop/.gitignore", "laptop/domains/a.b.c.plist", "laptop/domains/jetbrains.csat.plist"}, files, []string{"laptop/owner"})
	w.pushes(t, "Capture 2026-10-06 03:00: 5 new\n\nnew: a.b.c\nnew: jetbrains.csat\nnew: ~/Tool/.App2026.3/opts/x\nnew: ~/Tool/App2026.2/opts/x\nnew: ~/Other/abc/conf\nclaimed: the folder, for this Mac's hardware",
		"?? "+strings.Join(paths, "\x00?? ")+"\x00", paths, paths)
	if report, err := w.p.Capture(t.Context()); err != nil || report.Domains != 2 || report.Files != 3 {
		t.Errorf("Capture() = %+v, %v", report, err)
	}
}

// holdLock takes the lock in the file at path, as another kit's capture
// would: closing what it returns lets it go.
func holdLock(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// lockFree reports whether no one holds the lock in the file at path.
func lockFree(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// Two captures never write the store at once, as the hourly launch's and
// one by hand: one waits while another holds the store's lock, running
// nothing, and holds it itself throughout its run.
func TestCapturesTakeTurns(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "restored from laptop"}`)
	w.write(t, filepath.Join(w.clone, "laptop", "owner"), "hardware HW-OLD\nmodel Mac1,1\nsince 2026-09-28 17:00\n")
	lock := w.clone + ".capture.lock"
	other := holdLock(t, lock)
	waited := 0
	w.p.Sleep = func(time.Duration) {
		waited++
		if waited == 1 {
			if calls := w.fake.Calls(); len(calls) > 0 {
				t.Errorf("ran %q while another capture held the store", calls)
			}
			_ = other.Close()
		}
	}
	held := false
	w.fake.On("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Prints(`  "IOPlatformUUID" = "HW-THIS"` + "\n").Does(func() { held = !lockFree(lock) })

	var notOwner prefs.NotOwnerError
	if _, err := w.p.Capture(t.Context()); !asNotOwner(err, &notOwner) {
		t.Fatalf("Capture() = %v, want it to run once the other's done", err)
	}
	if waited == 0 || !held {
		t.Errorf("waited %d times, held the lock while it ran: %v; want it to wait, then hold it", waited, held)
	}
	if !lockFree(lock) {
		t.Error("the store's lock is still held once the capture's done")
	}
}

// A capture stopped while it waits for another runs nothing.
func TestACaptureStoppedWhileItWaits(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "started fresh"}`)
	holdLock(t, w.clone+".capture.lock")
	ctx, cancel := context.WithCancel(t.Context())
	w.p.Sleep = func(time.Duration) { cancel() }
	if _, err := w.p.Capture(ctx); !errors.Is(err, context.Canceled) || len(w.fake.Calls()) > 0 {
		t.Errorf("Capture() = %v, ran %q; want it stopped, nothing run", err, w.fake.Calls())
	}
}

// A restore never works on the store's clone while a capture does: it
// waits while the capture holds the store's lock, running nothing, then
// holds it itself throughout, the pull and the settings put back alike.
func TestARestoreWaitsForACapture(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.write(t, filepath.Join(w.clone, "laptop", "domains", "com.example.app.plist"), stored(t, map[string]any{"A": true}))
	lock := w.clone + ".capture.lock"
	capture := holdLock(t, lock)
	waited := 0
	w.p.Sleep = func(time.Duration) {
		waited++
		if waited == 1 {
			if calls := w.fake.Calls(); len(calls) > 0 {
				t.Errorf("ran %q while a capture held the store", calls)
			}
			_ = capture.Close()
		}
	}
	pulling, applying := false, false
	w.fake.On("git", "-C", w.clone, "remote").Prints("origin\n").Does(func() { pulling = !lockFree(lock) })
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	w.exports(t, "com.example.app", map[string]any{})
	w.fake.On("defaults", "delete", "com.example.app")
	w.fake.On("defaults", "import", "com.example.app", filepath.Join(w.clone, "laptop", "domains", "com.example.app.plist")).Does(func() {
		applying = !lockFree(lock)
		w.exports(t, "com.example.app", map[string]any{"A": true})
	})

	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.app"}})
	if err != nil || !slices.Equal(report.Restored, []string{"com.example.app"}) {
		t.Fatalf("Restore() = %+v, %v; want it restored once the capture's done", report, err)
	}
	if waited == 0 || !pulling || !applying {
		t.Errorf("waited %d times, held the lock pulling: %v, importing: %v; want it to wait, then hold it throughout", waited, pulling, applying)
	}
	if !lockFree(lock) {
		t.Error("the store's lock is still held once the restore's done")
	}
}

// A restore that can't take the store's lock fails with why, or stops,
// stopped while it waits, having run nothing.
func TestARestoreThatCantLockTheStore(t *testing.T) {
	for _, tt := range []struct {
		name string
		lock func(w *world) context.Context
		want func(err error) bool
	}{
		{"stopped while it waits", func(w *world) context.Context {
			holdLock(t, w.clone+".capture.lock")
			ctx, cancel := context.WithCancel(t.Context())
			w.p.Sleep = func(time.Duration) { cancel() }
			return ctx
		}, func(err error) bool { return errors.Is(err, context.Canceled) }},
		{"its lock unreadable", func(w *world) context.Context {
			if err := os.Mkdir(w.clone+".capture.lock", 0o700); err != nil {
				t.Fatal(err)
			}
			return t.Context()
		}, func(err error) bool { return err != nil && strings.HasPrefix(err.Error(), "lock the store: ") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t, prefs.Lists{})
			w.write(t, filepath.Join(w.clone, "laptop", "domains", "com.example.app.plist"), stored(t, map[string]any{"A": true}))
			if _, err := w.p.Restore(tt.lock(w), prefs.RestoreOptions{Domains: []string{"com.example.app"}}); !tt.want(err) || len(w.fake.Calls()) > 0 {
				t.Errorf("Restore() = %v, ran %q; want the lock's failure, nothing run", err, w.fake.Calls())
			}
		})
	}
}

// Taking a Mac's folder over writes into the store's clone, so it waits
// while a capture holds the store's lock.
func TestTakingOverWaitsForACapture(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.write(t, filepath.Join(w.clone, "laptop", "owner"), "hardware HW-OLD\n")
	capture := holdLock(t, w.clone+".capture.lock")
	waited := false
	w.p.Sleep = func(time.Duration) {
		if calls := w.fake.Calls(); len(calls) > 0 {
			t.Errorf("ran %q while a capture held the store", calls)
		}
		waited = true
		_ = capture.Close()
	}
	if err := w.p.TakeOver(t.Context()); err != nil || !waited {
		t.Errorf("TakeOver() = %v, waited: %v; want it to wait, then take over", err, waited)
	}
	if got := w.read(t, filepath.Join(w.clone, "laptop", "owner")); !strings.HasPrefix(got, "hardware HW-THIS\n") {
		t.Errorf("the owner file says %q, want this Mac's", got)
	}
}
