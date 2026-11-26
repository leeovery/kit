package prefs_test

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/prefs"
)

// A [prefs keys] line is a domain, the keys kept, then --restart and a
// process, if any; one that doesn't read so is refused, saying where and
// why.
func TestParseKeys(t *testing.T) {
	for _, c := range []struct {
		name, domain, value string
		want                prefs.Keys
		err                 string
	}{
		{name: "keys and a process", domain: "com.apple.dock", value: "persistent-apps persistent-others --restart Dock",
			want: prefs.Keys{Domain: "com.apple.dock", Keys: []string{"persistent-apps", "persistent-others"}, Restart: "Dock"}},
		{name: "--restart first", domain: "com.apple.dock", value: "--restart Dock persistent-apps",
			want: prefs.Keys{Domain: "com.apple.dock", Keys: []string{"persistent-apps"}, Restart: "Dock"}},
		{name: "no process", domain: "com.example.app", value: "Mode",
			want: prefs.Keys{Domain: "com.example.app", Keys: []string{"Mode"}}},
		{name: "a key with a space", domain: "com.example.app", value: `"Some Key"`,
			want: prefs.Keys{Domain: "com.example.app", Keys: []string{"Some Key"}}},
		{name: "no keys", domain: "com.apple.dock", value: "--restart Dock", err: "com.apple.dock names no keys: the keys kept follow the domain"},
		{name: "nothing after the domain", domain: "com.apple.dock", err: "com.apple.dock names no keys"},
		{name: "--restart with no process", domain: "com.apple.dock", value: "persistent-apps --restart", err: "--restart needs a process's name after it, as in --restart Dock"},
		{name: "--restart before an option", domain: "com.apple.dock", value: "persistent-apps --restart --now", err: "--restart needs a process's name after it"},
		{name: "--restart twice", domain: "com.apple.dock", value: "persistent-apps --restart Dock --restart Finder", err: "--restart is there twice: a line restarts one process"},
		{name: "an option kit doesn't know", domain: "com.apple.dock", value: "persistent-apps --quit Dock", err: `"--quit" isn't one of [prefs keys]'s options: --restart`},
		{name: "a key twice", domain: "com.apple.dock", value: "persistent-apps persistent-apps", err: "persistent-apps is named twice"},
		{name: "a quote left open", domain: "com.apple.dock", value: `"persistent-apps`, err: "a quote isn't closed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := prefs.ParseKeys(config.Entry{Name: c.domain, Value: c.value, Scope: "shared", Line: 7})
			if c.err != "" {
				if err == nil || !strings.HasPrefix(err.Error(), "shared/declarations:7: ") || !strings.Contains(err.Error(), c.err) {
					t.Errorf("ParseKeys() = %+v, %v; want it refused at shared/declarations:7, saying %q", got, err, c.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("ParseKeys() = %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

// tile is a Dock tile pointing at url, as the Dock saves one.
func tile(url string) map[string]any {
	return map[string]any{
		"tile-data": map[string]any{
			"file-data":  map[string]any{"_CFURLString": url, "_CFURLStringType": int64(15)},
			"file-label": "a tile",
		},
		"tile-type": "file-tile",
	}
}

// fileURL is path's file: URL, escaped as CoreFoundation writes it.
func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: path + "/"}).String()
}

// An array's items pointing at a file not on this Mac are left out: a
// file: URL or a path, as CoreFoundation saves either, found in the item
// at any depth. Items pointing at nothing, or at a web page, stay, as does
// any value not an array.
func TestOnThisMac(t *testing.T) {
	dir := t.TempDir()
	here := filepath.Join(dir, "Some App.app")
	if err := os.Mkdir(here, 0o700); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "Gone.app")
	spacer := map[string]any{"tile-data": map[string]any{}, "tile-type": "spacer-tile"}
	web := map[string]any{"tile-data": map[string]any{"url": map[string]any{"_CFURLString": "https://example.com/", "_CFURLStringType": int64(15)}}, "tile-type": "url-tile"}
	asPath := map[string]any{"_CFURLString": gone, "_CFURLStringType": int64(0)}
	for _, c := range []struct {
		name    string
		value   any
		want    any
		leftOut int
	}{
		{"an app here, its URL escaped", []any{tile(fileURL(here))}, []any{tile(fileURL(here))}, 0},
		{"an app gone", []any{tile(fileURL(here)), tile(fileURL(gone))}, []any{tile(fileURL(here))}, 1},
		{"a path gone", []any{asPath, tile(fileURL(here))}, []any{tile(fileURL(here))}, 1},
		{"a spacer and a web page", []any{spacer, web, tile(fileURL(gone))}, []any{spacer, web}, 1},
		{"not an array", "left", "left", 0},
		{"an empty array", []any{}, []any{}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, n := prefs.OnThisMac(c.value)
			if !plist.Equal(got, c.want) || n != c.leftOut {
				t.Errorf("OnThisMac() = %v, %d; want %v, %d", got, n, c.want, c.leftOut)
			}
		})
	}
}

// dock is the Dock's declared keys, as Lee's line declares them.
var dock = prefs.Keys{Domain: "com.apple.dock", Keys: []string{"persistent-apps", "persistent-others"}, Restart: "Dock"}

// Capture saves only the keys declared of a domain, Apple's included, in
// the store's keys folder, never the domain's other keys; a domain
// declared so is never saved whole, its whole copy from before removed;
// one no longer declared leaves the store; and Apple's other domains are
// still skipped.
func TestCaptureKeepsOnlyTheDeclaredKeys(t *testing.T) {
	w := newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock, {Domain: "com.example.keyed", Keys: []string{"Mode"}}}})
	writeRecord(t, w.state, `{"on": "started fresh"}`)
	store := filepath.Join(w.clone, "laptop")
	w.write(t, filepath.Join(store, "domains", "com.example.keyed.plist"), "saved whole, before")
	w.write(t, filepath.Join(store, "keys", "com.example.gone.plist"), "declared no more")
	apps := []any{tile("file:///Applications/Example.app/")}
	w.fake.On("defaults", "domains").Prints("com.apple.dock, com.apple.finder, com.example.keyed\n")
	w.exports(t, "com.apple.dock", map[string]any{"persistent-apps": apps, "persistent-others": []any{}, "tilesize": int64(48), "mod-count": int64(12)})
	w.exports(t, "com.example.keyed", map[string]any{"Mode": "fast", "Other": "left out"})
	status := " D laptop/domains/com.example.keyed.plist\x00 D laptop/keys/com.example.gone.plist\x00" +
		"?? laptop/.gitignore\x00?? laptop/keys/com.apple.dock.plist\x00?? laptop/keys/com.example.keyed.plist\x00?? laptop/owner\x00"
	added := []string{"laptop/.gitignore", "laptop/keys/com.apple.dock.plist", "laptop/keys/com.example.keyed.plist", "laptop/owner"}
	w.pushes(t, "Capture 2026-10-06 03:00: 2 new, 2 removed\n\nnew: com.apple.dock\nnew: com.example.keyed\nremoved: domains/com.example.keyed.plist\nremoved: keys/com.example.gone.plist\nclaimed: the folder, for this Mac's hardware",
		status, append([]string{"laptop/domains/com.example.keyed.plist", "laptop/keys/com.example.gone.plist"}, added...), added)

	report, err := w.p.Capture(t.Context())
	if err != nil || len(report.Errors) > 0 {
		t.Fatalf("Capture() = %+v, %v", report, err)
	}
	if got, want := report.Summary(), "2 domains, 2 changed (2 new); 0 files, 0 changed; 2 removed"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	for path, want := range map[string]string{
		"keys/com.apple.dock.plist":    stored(t, map[string]any{"persistent-apps": apps, "persistent-others": []any{}}),
		"keys/com.example.keyed.plist": stored(t, map[string]any{"Mode": "fast"}),
	} {
		if got := w.read(t, filepath.Join(store, path)); got != want {
			t.Errorf("%s =\n%s\nwant\n%s", path, got, want)
		}
	}
	for _, gone := range []string{"domains/com.example.keyed.plist", "domains/com.apple.dock.plist", "keys/com.example.gone.plist"} {
		if _, err := os.Stat(filepath.Join(store, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is there, want it gone", gone)
		}
	}
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "com.apple.finder") {
			t.Errorf("ran %s: Apple's domains not declared are left alone", c)
		}
	}
}

// keysStore writes the Dock's stored keys into the store's folder of mac,
// this hardware's: one app on this Mac, one gone, and a folder.
func (w *world) keysStore(t *testing.T, mac string) (here, gone, folder map[string]any) {
	t.Helper()
	app := filepath.Join(w.apps, "Example.app")
	docs := filepath.Join(w.home, "Documents")
	for _, dir := range []string{app, docs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	here, gone, folder = tile(fileURL(app)), tile(fileURL(filepath.Join(w.apps, "Gone.app"))), tile(fileURL(docs))
	store := filepath.Join(w.clone, mac)
	w.write(t, filepath.Join(store, "owner"), "hardware HW-THIS\n")
	w.write(t, filepath.Join(store, "keys", "com.apple.dock.plist"), stored(t, map[string]any{
		"persistent-apps": []any{here, gone}, "persistent-others": []any{folder}, "tilesize": int64(64),
	}))
	return here, gone, folder
}

// importsWhole scripts defaults import of domain from its standard input,
// noting what it was given in into, the domain read back as imported.
func (w *world) importsWhole(t *testing.T, domain string, into *map[string]any) {
	t.Helper()
	w.fake.On("defaults", "import", domain, "-").Filters(func(input string) (string, error) {
		v, err := plist.Decode([]byte(input))
		if err != nil {
			t.Errorf("imported %q: %v", input, err)
		}
		*into, _ = v.(map[string]any)
		w.fake.On("defaults", "export", domain, "-").Prints(input)
		return "", nil
	})
}

// A restore merges the declared keys into the domain as it stands, its
// other keys left as they are, and a stored key no longer declared left
// out; a tile whose file isn't on this Mac is left out; the process named
// is restarted once they're back, and the live settings saved first. A
// named restore merges them the same, never deleting the domain.
func TestRestoreMergesTheDeclaredKeys(t *testing.T) {
	for _, c := range []struct {
		name string
		opts prefs.RestoreOptions
	}{
		{"a full restore", prefs.RestoreOptions{}},
		{"the domain named", prefs.RestoreOptions{Domains: []string{"com.apple.dock"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock}})
			here, _, folder := w.keysStore(t, "laptop")
			live := map[string]any{"persistent-apps": []any{}, "tilesize": int64(36), "orientation": "left"}
			w.exports(t, "com.apple.dock", live)
			var imported map[string]any
			w.importsWhole(t, "com.apple.dock", &imported)
			w.fake.On("killall", "Dock")
			w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")

			report, err := w.p.Restore(t.Context(), c.opts)
			if err != nil || !slices.Equal(report.Restored, []string{"com.apple.dock"}) || len(report.Failed) > 0 || report.LeftOut != 1 {
				t.Fatalf("Restore() = %+v, %v", report, err)
			}
			want := map[string]any{"persistent-apps": []any{here}, "persistent-others": []any{folder}, "tilesize": int64(36), "orientation": "left"}
			if !plist.Equal(imported, want) {
				t.Errorf("imported %v\nwant %v: the Dock's other keys as they were, the app not here left out", imported, want)
			}
			calls := w.fake.Calls()
			at := func(prefix string) int {
				return slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
			}
			if imp, kill := at("defaults import com.apple.dock"), at("killall Dock"); kill < 0 || kill < imp {
				t.Errorf("calls %q: want the Dock restarted after the import", calls)
			}
			if got := w.read(t, filepath.Join(report.Saved, "com.apple.dock.plist")); got != stored(t, live) {
				t.Errorf("the live settings saved as %q", got)
			}
			if !strings.Contains(report.Summary(), "restored 1 domain and 0 files from laptop; 1 item left out till it's on this Mac; previous") {
				t.Errorf("Summary() = %q", report.Summary())
			}
		})
	}
}

// The declared keys already as stored, nothing's imported and nothing's
// restarted.
func TestRestoreLeavesKeysAlreadyThere(t *testing.T) {
	w := newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock}})
	here, _, folder := w.keysStore(t, "laptop")
	w.exports(t, "com.apple.dock", map[string]any{"persistent-apps": []any{here}, "persistent-others": []any{folder}, "tilesize": int64(36)})
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || !slices.Equal(report.Restored, []string{"com.apple.dock"}) {
		t.Errorf("Restore() = %+v, %v", report, err)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "defaults import") || strings.HasPrefix(c, "killall") {
			t.Errorf("ran %s, with nothing to change", c)
		}
	}
}

// Without a declaration, keys stored before are never restored, and a
// dry run changes nothing.
func TestRestoreKeysOnlyAsDeclared(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.keysStore(t, "laptop")
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	if report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{}); err != nil || len(report.Restored) > 0 {
		t.Errorf("Restore() undeclared = %+v, %v; want nothing restored", report, err)
	}

	w = newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock}})
	w.keysStore(t, "laptop")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{DryRun: true})
	if err != nil || !slices.Equal(report.Restored, []string{"com.apple.dock"}) {
		t.Errorf("Restore(--dry-run) = %+v, %v", report, err)
	}
	if calls := w.fake.Calls(); slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "defaults") || strings.HasPrefix(c, "killall") }) {
		t.Errorf("--dry-run ran %q", calls)
	}
}

// A merge macOS refuses is held, failed, for kit prefs restore --pending,
// which merges it, never restoring it whole, then holds it for what it left
// out; the hourly pass leaves it.
func TestAFailedMergeWaitsForPendingByHand(t *testing.T) {
	w := newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock}})
	w.keysStore(t, "laptop")
	w.exports(t, "com.apple.dock", map[string]any{"tilesize": int64(36)})
	w.fake.On("defaults", "import", "com.apple.dock", "-").Exits(1)
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{})
	if err != nil || report.Failed["com.apple.dock"] != "macOS didn't take them" {
		t.Fatalf("Restore() = %+v, %v", report, err)
	}
	if rec, _ := w.p.Load(); rec.Pending == nil || !slices.Equal(rec.Pending.Failed, []string{"com.apple.dock"}) {
		t.Fatalf("pending = %+v, want the Dock held, failed", rec.Pending)
	}

	if report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true}); err != nil || len(report.Restored) > 0 {
		t.Errorf("the hourly pass = %+v, %v; want the failed merge left", report, err)
	}

	var imported map[string]any
	w.importsWhole(t, "com.apple.dock", &imported)
	w.fake.On("killall", "Dock")
	report, err = w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true})
	if err != nil || !slices.Equal(report.Restored, []string{"com.apple.dock"}) || imported["tilesize"] != int64(36) {
		t.Errorf("Restore(--pending) = %+v, %v; imported %v", report, err, imported)
	}
	if rec, _ := w.p.Load(); rec.Pending == nil || rec.Pending.Why["com.apple.dock"] != prefs.LeftOutWhy || len(rec.Pending.Failed) > 0 {
		t.Errorf("pending = %+v, want the Dock held for the app left out, failed no more", rec.Pending)
	}
}

// A domain kept by its keys has its history as any domain does, from its
// stored copy in the keys folder.
func TestHistoryOfDeclaredKeys(t *testing.T) {
	w := newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock}})
	w.fake.On("git", "-C", w.clone, "log", "--no-color", "-n5", "--format=%x1e%h%x00%aI%x00%s", "-p", "--", "laptop/keys/com.apple.dock.plist").
		Prints("\x1eabc1234\x002026-10-06T03:00:00+01:00\x00Capture\n\ndiff --git a/x b/x\n+an app\n")
	changes, err := w.p.History(t.Context(), "", "com.apple.dock", 5)
	if err != nil || len(changes) != 1 || changes[0].Changed != "diff --git a/x b/x\n+an app" {
		t.Errorf("History(com.apple.dock) = %+v, %v", changes, err)
	}
}

// heldForGone restores the Dock's keys, the app Gone not on this Mac, so
// the Dock is held for it, the live Dock what the restore wrote; and
// returns the tiles stored.
func (w *world) heldForGone(t *testing.T) (here, gone, folder map[string]any) {
	t.Helper()
	here, gone, folder = w.keysStore(t, "laptop")
	w.exports(t, "com.apple.dock", map[string]any{"persistent-apps": []any{}, "tilesize": int64(36)})
	var imported map[string]any
	w.importsWhole(t, "com.apple.dock", &imported)
	w.fake.On("killall", "Dock")
	w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")
	if _, err := w.p.Restore(t.Context(), prefs.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	rec, _ := w.p.Load()
	if rec.Pending == nil || !slices.Equal(rec.Pending.Domains, []string{"com.apple.dock"}) || rec.Pending.Why["com.apple.dock"] != prefs.LeftOutWhy || rec.Pending.Wrote["com.apple.dock"] == "" {
		t.Fatalf("pending = %+v, want the Dock held for the app left out, with what kit wrote", rec.Pending)
	}
	return here, gone, folder
}

// count is how many of the fake's calls start with prefix.
func (w *world) count(prefix string) int {
	n := 0
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// A Dock restored with a tile left out is held: the passes leave it while
// the app's still not on this Mac, and merge it again once it is, the
// Dock as kit wrote it, then let it go.
func TestALeftOutTileComesBackOnceItsAppIs(t *testing.T) {
	w := newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock}})
	here, gone, folder := w.heldForGone(t)

	if report, err := w.p.RestoreInstalled(t.Context()); err != nil || len(report.Restored) > 0 {
		t.Errorf("RestoreInstalled() = %+v, %v; want nothing, the app still not here", report, err)
	}
	if report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true}); err != nil || len(report.Restored) > 0 {
		t.Errorf("the hourly pass = %+v, %v; want nothing, the app still not here", report, err)
	}
	if n := w.count("defaults import"); n != 1 {
		t.Errorf("imported %d times, want once: nothing more till the app's here", n)
	}

	if err := os.MkdirAll(filepath.Join(w.apps, "Gone.app"), 0o700); err != nil {
		t.Fatal(err)
	}
	var imported map[string]any
	w.importsWhole(t, "com.apple.dock", &imported)
	report, err := w.p.RestoreInstalled(t.Context())
	if err != nil || !slices.Equal(report.Restored, []string{"com.apple.dock"}) || report.LeftOut != 0 {
		t.Fatalf("RestoreInstalled() = %+v, %v; want the Dock merged again", report, err)
	}
	want := map[string]any{"persistent-apps": []any{here, gone}, "persistent-others": []any{folder}, "tilesize": int64(36)}
	if !plist.Equal(imported, want) {
		t.Errorf("imported %v\nwant %v", imported, want)
	}
	if n := w.count("killall Dock"); n != 2 {
		t.Errorf("restarted the Dock %d times, want twice", n)
	}
	if rec, _ := w.p.Load(); rec.Pending != nil {
		t.Errorf("pending = %+v, want nothing held, nothing left out", rec.Pending)
	}
}

// A Dock rearranged since kit restored it is left as it is once the app
// left out arrives, and held no more, the summary saying so.
func TestARearrangedDockIsLeftAlone(t *testing.T) {
	w := newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock}})
	_, _, folder := w.heldForGone(t)
	w.exports(t, "com.apple.dock", map[string]any{"persistent-apps": []any{}, "persistent-others": []any{folder}, "tilesize": int64(36)})
	if err := os.MkdirAll(filepath.Join(w.apps, "Gone.app"), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Pending: true, Unattended: true})
	if err != nil || len(report.Restored) > 0 || !slices.Equal(report.Rearranged, []string{"com.apple.dock"}) {
		t.Fatalf("the hourly pass = %+v, %v; want the rearranged Dock left", report, err)
	}
	if !strings.Contains(report.Summary(), "1 changed since kit restored it, left as it is") {
		t.Errorf("Summary() = %q", report.Summary())
	}
	if n := w.count("defaults import"); n != 1 {
		t.Errorf("imported %d times, want once: the rearranged Dock left alone", n)
	}
	if rec, _ := w.p.Load(); rec.Pending != nil {
		t.Errorf("pending = %+v, want the hold dropped", rec.Pending)
	}
}

// While the Dock's held for what its restore left out, from this Mac's own
// folder, capture leaves the stored copy, the whole list, as it is.
func TestCaptureLeavesAHeldDockAsStored(t *testing.T) {
	w := newWorld(t, prefs.Lists{Keys: []prefs.Keys{dock}})
	w.heldForGone(t)
	store := filepath.Join(w.clone, "laptop")
	before := w.read(t, filepath.Join(store, "keys", "com.apple.dock.plist"))
	w.write(t, filepath.Join(store, ".gitignore"), ".DS_Store\n.*.tmp\n")
	w.fake.On("defaults", "domains").Prints("com.apple.dock\n")
	w.pushes(t, "Capture 2026-10-06 03:00: no changes", "", nil, nil)
	if _, err := w.p.Capture(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.read(t, filepath.Join(store, "keys", "com.apple.dock.plist")); got != before {
		t.Errorf("the held Dock stored as\n%s\nwant it left as\n%s", got, before)
	}
}
