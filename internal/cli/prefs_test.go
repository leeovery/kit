package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// kit prefs: capture waits until it's switched on; start-fresh switches it
// on; kit status says how it stands once the feature is on: fine for now,
// saving from tonight.
func TestPrefsCommands(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "prefs\n")
	out, _, code := w.run(t, "prefs", "capture")
	if code != 0 || !strings.Contains(out, "prefs attention paused: capture isn't switched on for this Mac\n") {
		t.Errorf("kit prefs capture printed\n%s exit %d", out, code)
	}
	if out, _, _ := w.run(t, "prefs", "pending"); out != "Nothing is held to be restored.\n" {
		t.Errorf("kit prefs pending printed %q", out)
	}
	if out, _, _ := w.run(t, "prefs", "pending", "--json"); out != "{\n  \"from\": \"\",\n  \"domains\": []\n}\n" {
		t.Errorf("kit prefs pending --json printed %q", out)
	}
	if out, _, code := w.run(t, "prefs", "start-fresh", "--json"); code != 0 || compact(t, out) != `{"schema":1,"capture_on":true}` {
		t.Errorf("kit prefs start-fresh --json printed %q, exit %d", out, code)
	}
	if out, _, code := w.run(t, "prefs", "start-fresh"); code != 0 || !strings.Contains(out, "Capture is switched on for this Mac") {
		t.Errorf("kit prefs start-fresh printed %q, exit %d", out, code)
	}
	out, _, _ = w.run(t, "status")
	if !strings.Contains(out, "prefs ok saving from tonight\n") || !strings.Contains(out, "full-disk-access ok") {
		t.Errorf("kit status printed\n%s", out)
	}
}

// kit prefs pending lists what's held, domains then settings files, each
// with why where the record says: a domain whose app isn't installed kept
// till it is.
func TestPrefsPendingSaysWhy(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, ".local/state/kit/prefs.json", `{"pending": {"from": "laptop", "domains": ["com.example.app", "com.example.old"], "files": ["~/.config/tool/settings.json"],
		"why": {"com.example.app": "kept till its app is installed", "~/.config/tool/settings.json": "it couldn't be put in place"}}}`)
	want := "Held to be restored from laptop (kit prefs restore --pending):\n" +
		"  com.example.app: kept till its app is installed\n" +
		"  com.example.old\n" +
		"  ~/.config/tool/settings.json: it couldn't be put in place\n"
	if out, _, _ := w.run(t, "prefs", "pending"); out != want {
		t.Errorf("kit prefs pending printed\n%s\nwant\n%s", out, want)
	}
	if out, _, code := w.run(t, "prefs", "pending", "--clear", "--json"); code != 0 || compact(t, out) != `{"schema":1,"cleared":3}` {
		t.Errorf("kit prefs pending --clear --json printed %q, exit %d", out, code)
	}
}

// kit prefs history shows the captures, each with what it changed; --json
// lists them, each one's commit, date, message and what it changed;
// a domain with no history says so, and exits 0, as a lookup that finds
// nothing does.
func TestPrefsHistory(t *testing.T) {
	w := laptopWorld(t)
	clone := w.storeCloned()
	w.write(t, filepath.Join(".local", "share", "kit", "prefs", ".git", "HEAD"), "ref: refs/heads/prefs\n")
	format := []string{"-C", clone, "log", "--no-color", "-n20", "--format=%x1e%h%x00%aI%x00%s"}
	w.fake.On("git", append(format, "--shortstat", "--", "laptop")...).Prints("\x1eabc1234\x002026-10-06T03:00:00+01:00\x00Capture: 1 changed\n\n 1 file changed, 2 insertions(+)\n")
	w.fake.On("git", append(format, "-p", "--", "laptop/domains/com.example.none.plist")...).Prints("")
	if out, errOut, code := w.run(t, "prefs", "history"); out != "abc1234  2026-10-06 03:00  Capture: 1 changed\n\n1 file changed, 2 insertions(+)\n" || errOut != "" || code != 0 {
		t.Errorf("kit prefs history printed %q, %q, exit %d", out, errOut, code)
	}
	want := `{"schema":1,"history":[{"commit":"abc1234","date":"2026-10-06T03:00:00+01:00","message":"Capture: 1 changed","changed":"1 file changed, 2 insertions(+)"}]}`
	if out, errOut, code := w.run(t, "prefs", "history", "--json"); compact(t, out) != want || errOut != "" || code != 0 {
		t.Errorf("kit prefs history --json printed %q, %q, exit %d; want %s", out, errOut, code, want)
	}
	if out, errOut, code := w.run(t, "prefs", "history", "com.example.none"); out != "" || errOut != "kit: no history for laptop/domains/com.example.none.plist\n" || code != 0 {
		t.Errorf("kit prefs history com.example.none printed %q, %q, exit %d; want none found, exit 0", out, errOut, code)
	}
}

// compact is the JSON document doc, on one line.
func compact(t *testing.T, doc string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(doc)); err != nil {
		t.Fatalf("%q isn't JSON: %v", doc, err)
	}
	return b.String()
}

// kitConfigURL is where the world's kit-config fetches from.
const kitConfigURL = "git@github.com:someone/kit-config.git"

// kitConfigRemote scripts kit-config's one remote, where the store is.
func (w *world) kitConfigRemote() {
	config := filepath.Join(w.home, ".config", "kit")
	w.fake.On("git", "-C", config, "remote").Prints("origin\n")
	w.fake.On("git", "-C", config, "remote", "get-url", "origin").Prints(kitConfigURL + "\n")
	w.fake.On("git", "-C", config, "config", "--get", "remote.origin.url").Prints(kitConfigURL + "\n")
	w.fake.On("git", "-C", config, "remote", "get-url", "--push", "--all", "origin").Prints(kitConfigURL + "\n")
	w.fake.On("git", "-C", config, "config", "--local", "--get-regexp", "-z", `^(credential\..*|remote\..*\.pushurl)$`).Exits(1)
}

// storeCloned scripts the store's clone, in kit's data folder, as kit finds
// it: a clone of kit-config's prefs branch, set up for kit. It's the clone.
func (w *world) storeCloned() string {
	w.kitConfigRemote()
	clone := filepath.Join(w.home, ".local", "share", "kit", "prefs")
	w.fake.On("git", "-C", clone, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads").Prints("refs/heads/prefs\n")
	w.fake.On("git", "-C", clone, "remote", "get-url", "origin").Prints(kitConfigURL + "\n")
	w.fake.On("git", "-C", clone, "remote", "get-url", "--push", "--all", "origin").Prints(kitConfigURL + "\n")
	w.fake.On("git", "-C", clone, "symbolic-ref", "-q", "HEAD").Prints("refs/heads/prefs\n")
	for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
		w.fake.On("git", "-C", clone, "config", kv[0], kv[1])
	}
	return clone
}

// storeOutOfReach scripts kit-config's remote out of reach, over ssh with
// no network, as kit first looks for the store: there's no clone of it.
func (w *world) storeOutOfReach() {
	w.kitConfigRemote()
	w.fake.On("git", "ls-remote", "--quiet", "--exit-code", kitConfigURL, "refs/heads/prefs").Exits(128).
		PrintsToStderr("ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n")
}
