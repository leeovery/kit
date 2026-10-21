package prefs_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
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
	w.pulls()
	w.fake.On("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Prints(`  "IOPlatformUUID" = "HW-THIS"` + "\n")
	w.fake.On("sysctl", "-n", "hw.model").Prints("Mac99,1\n")
	return w
}

// pulls scripts a pull of the store's, as real git answers, its remote
// with nothing new, and nothing changed in the clone; its branch main,
// origin's main its upstream, and no other remote set to push to.
func (w *world) pulls() {
	w.fake.On("git", "-C", w.clone, "remote").Prints("origin\n")
	w.fake.On("git", "-C", w.clone, "rev-parse", "--absolute-git-dir").Prints(filepath.Join(w.clone, ".git") + "\n")
	w.fake.On("git", "-C", w.clone, "--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")
	w.fake.On("git", "-C", w.clone, "symbolic-ref", "-q", "HEAD").Prints("refs/heads/main\n")
	w.fake.On("git", "-C", w.clone, "for-each-ref", "--format=%(upstream)%00%(upstream:remotename)", "refs/heads/main").Prints("refs/remotes/origin/main\x00origin\n")
	w.fake.On("git", "-C", w.clone, "config", "--get", "branch.main.pushRemote").Exits(1)
	w.fake.On("git", "-C", w.clone, "config", "--get", "remote.pushDefault").Exits(1)
	w.fake.On("git", "-C", w.clone, "fetch", "--prune", "--quiet", "origin")
	w.fake.On("git", "-C", w.clone, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main").Prints(head + "\n")
	w.fake.On("git", "-C", w.clone, "rev-list", "--count", "HEAD..refs/remotes/origin/main").Prints("0\n")
}

// pushes scripts the commit of the Mac's folder with message, git's
// status of it being status (-z), each file of which kit stages as it is on
// disk in an index of its own (update), what's staged there being staged
// (-z), the files committed, which the user's index then takes as
// committed, as real git prints them; and a private remote taking it, with
// nothing new of its own, nothing else changed in the clone.
func (w *world) pushes(t *testing.T, message, status string, update []string, staged string) {
	t.Helper()
	reads := func(want string) func(string) (string, error) {
		return func(in string) (string, error) {
			if in != want {
				t.Errorf("git read %q, want %q", in, want)
			}
			return "", nil
		}
	}
	gitDir := filepath.Join(w.clone, ".git")
	w.fake.On("git", "-C", w.clone, "--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--", "laptop").Prints(status)
	w.fake.On("git", "-C", w.clone, "rev-parse", "--absolute-git-dir").Prints(gitDir + "\n")
	w.fake.On("git", "-C", w.clone, "rev-parse", "--verify", "--quiet", "HEAD").Prints(head + "\n")
	w.fake.On("git", "-C", w.clone, "read-tree", head)
	w.fake.On("git", "-C", w.clone, "update-index", "--add", "--remove", "--replace", "-z", "--stdin").Filters(reads(strings.Join(update, "\x00") + "\x00"))
	w.fake.On("git", "-C", w.clone, "diff", "--cached", "--name-only", "-z", "--no-renames").Prints(staged)
	w.fake.On("git", "-C", w.clone, "write-tree").Prints(tree + "\n")
	w.fake.On("git", "-C", w.clone, "config", "--get", "--type=bool", "commit.gpgsign").Prints("false\n")
	w.fake.On("git", "-C", w.clone, "commit-tree", tree, "-p", head, "-m", message).Prints(made + "\n")
	subject, _, _ := strings.Cut(message, "\n")
	w.fake.On("git", "-C", w.clone, "update-ref", "-m", "kit: "+subject, "HEAD", made, head)
	w.fake.On("git", "-C", w.clone, "--literal-pathspecs", "reset", "--quiet", made, "--pathspec-from-file=-", "--pathspec-file-nul").Filters(reads(staged)).Does(func() {
		w.write(t, filepath.Join(gitDir, "kit-index.next"), "the index, as committed")
	})
	w.fake.On("git", "-C", w.clone, "remote", "get-url", "--push", "--all", "origin").Prints(remote + "\n")
	w.fake.On("ssh", "-G", "github.com").Prints("user git\nhostname github.com\nport 22\n")
	w.fake.On("gh", "repo", "view", "someone/prefs", "--json", "visibility", "--jq", ".visibility").Prints("PRIVATE\n")
	w.pulls()
	w.fake.On("git", "-C", w.clone, "push", "--quiet")
}

// head is the commit HEAD names in the store's clone, tree the tree git
// writes of kit's own index of it, and made the commit a capture makes of
// that onto it.
const (
	head = "0e0bf17a399109574ffb00e33300280a33f81ab7"
	tree = "af9055821923c351c404eaaff97d82a5552a1ad5"
	made = "2f8f9a0d6c1b4e5a7d3c2b1a0f9e8d7c6b5a4f3e"
)

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
	// them not on the Mac: capture leaves them.
	writeRecord(t, w.state, `{"on": "started fresh", "pending": {"from": "laptop", "domains": ["com.example.pending"], "files": ["~/.config/tool/absent.json", "~/.config/tool/held.json"]}}`)
	w.write(t, filepath.Join(store, "files", ".config", "tool", "absent.json"), "kept though absent")
	w.write(t, filepath.Join(store, "domains", "com.example.pending.plist"), "kept as stored")
	w.write(t, filepath.Join(store, "files", ".config", "tool", "held.json"), "kept as stored")
	w.write(t, filepath.Join(w.home, ".config", "tool", "held.json"), "the Mac's")
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
	// The store's files from before were committed then.
	w.pushes(t, message,
		" D laptop/domains/com.example.empty.plist\x00 D laptop/domains/com.example.gone.plist\x00 D laptop/files/.config/dropped.json\x00"+
			"?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/files/.config/folder/deeper/two.txt\x00?? laptop/files/.config/folder/one.txt\x00"+
			"?? laptop/files/.config/tool/settings.json\x00?? laptop/files/Library/Tool/App2026.1/options/ui.xml\x00?? laptop/files/Library/Tool/App2026.2/options/ui.xml\x00?? laptop/owner\x00",
		[]string{
			"laptop/domains/com.example.empty.plist", "laptop/domains/com.example.gone.plist", "laptop/files/.config/dropped.json",
			"laptop/.gitignore", "laptop/domains/com.example.app.plist", "laptop/files/.config/folder/deeper/two.txt", "laptop/files/.config/folder/one.txt",
			"laptop/files/.config/tool/settings.json", "laptop/files/Library/Tool/App2026.1/options/ui.xml", "laptop/files/Library/Tool/App2026.2/options/ui.xml", "laptop/owner",
		},
		"laptop/.gitignore\x00laptop/domains/com.example.app.plist\x00laptop/domains/com.example.empty.plist\x00laptop/domains/com.example.gone.plist\x00laptop/files/.config/dropped.json\x00"+
			"laptop/files/.config/folder/deeper/two.txt\x00laptop/files/.config/folder/one.txt\x00laptop/files/.config/tool/settings.json\x00"+
			"laptop/files/Library/Tool/App2026.1/options/ui.xml\x00laptop/files/Library/Tool/App2026.2/options/ui.xml\x00laptop/owner\x00")

	report, err := w.p.Capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Committed {
		t.Error("the capture wasn't committed")
	}
	if got, want := report.Summary(), "2 domains, 1 changed (1 new); 5 files, 5 changed (5 new); 3 removed; 3 pending restore; 1 errors"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	if got := w.read(t, filepath.Join(store, "domains", "com.example.app.plist")); got != stored(t, map[string]any{"Theme": "dark"}) {
		t.Errorf("com.example.app stored as\n%s\nwant the keys that change by themselves stripped", got)
	}
	for path, want := range map[string]string{
		"domains/com.example.pending.plist":           "kept as stored",
		"files/.config/tool/held.json":                "kept as stored",
		"files/.config/tool/absent.json":              "kept though absent",
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
	t.Run("into a folder another Mac took over, as the pull brings in", func(t *testing.T) {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "restored from laptop"}`)
		w.write(t, filepath.Join(w.clone, "laptop", "owner"), "hardware HW-THIS\nmodel Mac99,1\nsince 2026-09-28 17:00\n")
		w.fake.On("git", "-C", w.clone, "rev-list", "--count", "HEAD..refs/remotes/origin/main").Prints("1\n")
		w.fake.On("git", "-C", w.clone, "diff", "--name-only", "-z", "--no-renames", "HEAD...refs/remotes/origin/main").Prints("laptop/owner\x00")
		w.fake.On("git", "-C", w.clone, "config", "--get", "--type=bool", "core.ignorecase").Prints("true\n")
		w.fake.On("git", "-C", w.clone, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
		w.fake.On("git", "-C", w.clone, "rebase", "--no-fork-point", "--quiet", "refs/remotes/origin/main").Does(func() {
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
// aside, so nothing's brought in either; or given up as other gits kept
// moving HEAD. The record says why.
func TestCaptureHoldsBackThePushWhenTheCommitFailed(t *testing.T) {
	const message = "Capture 2026-10-06 03:00: 1 new\n\nnew: com.example.app\nclaimed: the folder, for this Mac's hardware"
	for _, c := range []struct{ name, want string }{
		{"a merge under way", "not pushed: a merge is under way in the repository (MERGE_HEAD)"},
		{"HEAD kept moving", "not pushed: HEAD kept moving while kit committed, so nothing was committed"},
	} {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "started fresh"}`)
		w.fake.On("defaults", "domains").Prints("com.example.app\n")
		w.exports(t, "com.example.app", map[string]any{"Theme": "dark"})
		w.pushes(t, message,
			"?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/owner\x00",
			[]string{"laptop/.gitignore", "laptop/domains/com.example.app.plist", "laptop/owner"},
			"laptop/.gitignore\x00laptop/domains/com.example.app.plist\x00laptop/owner\x00")
		if c.name == "a merge under way" {
			w.write(t, filepath.Join(w.clone, ".git", "MERGE_HEAD"), head+"\n")
		} else {
			w.movingHEAD(message)
		}
		report, err := w.p.Capture(t.Context())
		if err != nil || report.Committed || !strings.HasPrefix(report.PushError, c.want) {
			t.Errorf("%s: Capture() = %+v, %v; want %q", c.name, report, err, c.want)
		}
		for _, call := range w.fake.Calls() {
			if strings.Contains(call, " push --quiet") || strings.Contains(call, " stash ") || strings.Contains(call, " fetch ") && c.name == "a merge under way" {
				t.Errorf("%s: ran %s, want nothing pushed or set aside", c.name, call)
			}
		}
		if rec, _ := w.p.Load(); rec.Unpushed.IsZero() || !strings.HasPrefix(rec.PushError, c.want) {
			t.Errorf("%s: record = %+v, want the push held back, and why", c.name, rec)
		}
	}
}

// movingHEAD scripts other gits moving HEAD each time a capture commits
// with message, as real git refuses the move: off head, then off each of
// theirs in turn.
func (w *world) movingHEAD(message string) {
	heads := []string{head, "154906a47f12246099602d1bee6b1c558d3409c9", "2c1d8e0f4b6a7c9d3e5f7a1b2c4d6e8f0a1b3c5d", "3d2e9f1a5c7b8d0e4f6a8b2c3d5e7f9a1b2c4d6e"}
	var moved atomic.Int32
	w.fake.On("git", "-C", w.clone, "rev-parse", "--verify", "--quiet", "HEAD").Filters(func(string) (string, error) {
		return heads[moved.Load()] + "\n", nil
	})
	subject, _, _ := strings.Cut(message, "\n")
	for i, base := range heads[:len(heads)-1] {
		w.fake.On("git", "-C", w.clone, "read-tree", base)
		w.fake.On("git", "-C", w.clone, "commit-tree", tree, "-p", base, "-m", message).Prints(made + "\n")
		w.fake.On("git", "-C", w.clone, "update-ref", "-m", "kit: "+subject, "HEAD", made, base).Exits(128).
			PrintsToStderr("fatal: update_ref failed for ref 'HEAD': cannot lock ref 'HEAD': is at " + heads[i+1] + " but expected " + base + "\n").
			Does(func() { moved.Add(1) })
	}
}

// A store that isn't private is never pushed to: the commit waits, and the
// record says since when. It's where git push sends it that's looked at: a
// push URL the remote has set, public, in its private URL's place, which
// git names alone, as it pushes there alone.
func TestCaptureNeverPushesToAPublicStore(t *testing.T) {
	for _, c := range []struct{ name, pushURLs, public string }{
		{"the store public", remote + "\n", "someone/prefs"},
		{"a push URL public", "git@github.com:someone/public-prefs.git\n", "someone/public-prefs"},
	} {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "started fresh"}`)
		w.fake.On("defaults", "domains").Prints("com.example.app\n")
		w.exports(t, "com.example.app", map[string]any{"Theme": "dark"})
		w.pushes(t, "Capture 2026-10-06 03:00: 1 new\n\nnew: com.example.app\nclaimed: the folder, for this Mac's hardware",
			"?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/owner\x00",
			[]string{"laptop/.gitignore", "laptop/domains/com.example.app.plist", "laptop/owner"},
			"laptop/.gitignore\x00laptop/domains/com.example.app.plist\x00laptop/owner\x00")
		w.fake.On("git", "-C", w.clone, "remote", "get-url", "--push", "--all", "origin").Prints(c.pushURLs)
		w.fake.On("gh", "repo", "view", c.public, "--json", "visibility", "--jq", ".visibility").Prints("PUBLIC\n")
		report, err := w.p.Capture(t.Context())
		if err != nil || !strings.Contains(report.PushError, c.public+" is public on GitHub: kit pushes apps' settings only to a private repository") {
			t.Errorf("%s: Capture() = %+v, %v", c.name, report, err)
		}
		if slices.Contains(w.fake.Calls(), "git -C "+w.clone+" push --quiet") {
			t.Errorf("%s: pushed to a public repository", c.name)
		}
		if rec, _ := w.p.Load(); rec.Unpushed.IsZero() || rec.PushError == "" {
			t.Errorf("%s: record = %+v, want the push waiting", c.name, rec)
		}
	}
}

// leavesIndex leaves in the clone's git folder git's lock holding the index
// a capture left, stopped once HEAD had moved onto its commit, head, and
// its mark: the commit HEAD was on, that one, and the lock by its hash and
// file, device and inode.
func (w *world) leavesIndex(t *testing.T) {
	t.Helper()
	const left, before = "the index a capture left", "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432"
	lock := filepath.Join(w.clone, ".git", "index.lock")
	w.write(t, lock, left)
	info, err := os.Stat(lock)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	sum := sha256.Sum256([]byte(left))
	w.write(t, filepath.Join(w.clone, ".git", "kit-mark"), fmt.Sprintf("%s %s %s %d:%d\n", before, head, hex.EncodeToString(sum[:]), st.Dev, st.Ino))
}

// The index a capture left in git's lock, stopped once HEAD had moved onto
// its commit, its mark naming that commit and the lock by its hash and
// file, is put in place before the next commits, and the summary says so.
func TestCaptureFinishesTheIndexAStoppedCaptureLeft(t *testing.T) {
	const message = "Capture 2026-10-06 03:00: 1 new\n\nnew: com.example.app\nclaimed: the folder, for this Mac's hardware"
	w := newWorld(t, prefs.Lists{})
	writeRecord(t, w.state, `{"on": "started fresh"}`)
	w.fake.On("defaults", "domains").Prints("com.example.app\n")
	w.exports(t, "com.example.app", map[string]any{"Theme": "dark"})
	w.pushes(t, message,
		"?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/owner\x00",
		[]string{"laptop/.gitignore", "laptop/domains/com.example.app.plist", "laptop/owner"},
		"laptop/.gitignore\x00laptop/domains/com.example.app.plist\x00laptop/owner\x00")
	w.leavesIndex(t)
	report, err := w.p.Capture(t.Context())
	if err != nil || !report.Committed || !slices.Equal(report.Notes, []string{"finished the last run's index"}) || !strings.HasSuffix(report.Summary(), "; finished the last run's index") {
		t.Errorf("Capture() = %+v, %v; want the index finished, then committed, and said", report, err)
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
	w.pushes(t, "Capture 2026-10-06 03:00: 3 new\n\nnew: a.b.c\nnew: jetbrains.csat\nnew: ~/Tool/App2026.2/opts/x\nclaimed: the folder, for this Mac's hardware",
		"?? laptop/.gitignore\x00?? laptop/domains/a.b.c.plist\x00?? laptop/domains/jetbrains.csat.plist\x00?? laptop/files/Tool/App2026.2/opts/x\x00?? laptop/owner\x00",
		[]string{"laptop/.gitignore", "laptop/domains/a.b.c.plist", "laptop/domains/jetbrains.csat.plist", "laptop/files/Tool/App2026.2/opts/x", "laptop/owner"},
		"laptop/.gitignore\x00laptop/domains/a.b.c.plist\x00laptop/domains/jetbrains.csat.plist\x00laptop/files/Tool/App2026.2/opts/x\x00laptop/owner\x00")
	if report, err := w.p.Capture(t.Context()); err != nil || report.Domains != 2 || report.Files != 1 {
		t.Errorf("Capture() = %+v, %v", report, err)
	}
}
