package cli_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/nightly"
)

// tmutilNoAccess is what tmutil prints on its standard error refusing for
// want of Full Disk Access, exiting 80.
const tmutilNoAccess = `tmutil: addexclusion requires Full Disk Access privileges.
To allow this operation, select Full Disk Access in the Privacy
tab of the Security & Privacy preference pane, and add Terminal
to the list of applications which are allowed Full Disk Access.
`

// exclusionsWorld is the laptop keeping two backup exclusions, a glob's
// match and a plain path, out of Time Machine's backups, neither kept out
// yet, the password at hand.
func exclusionsWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.write(t, "Library/Caches/one/.keep", "")
	w.writeSection(t, "laptop", "backup exclusions", "~/Library/Caches/*\n/Applications\n")
	w.writeSection(t, "laptop", "backups", "time-machine\n")
	w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Prints("<plist><dict><key>SkipPaths</key><array/></dict></plist>")
	match := filepath.Join(w.home, "Library/Caches/one")
	w.fake.On("tmutil", "isexcluded", match).Prints("[Included]  " + match + "\n")
	w.fake.On("sudo", "-n", "true")
	w.write(t, filepath.Join(".local", "state", "kit", "drift.json"), `{"first_seen": {"backup-exclusion:/Applications": "2025-12-31T00:00:00Z", "backup-exclusion:~/Library/Caches/one": "2025-12-31T00:00:00Z"}}`)
	return w
}

// unreadable has the folders only Full Disk Access shows there in w's home,
// and unreadable, as they are to a terminal without it.
func (w *world) unreadable(t *testing.T) {
	t.Helper()
	for _, p := range []string{"Library/Safari", "Library/Mail", "Library/Messages"} {
		dir := filepath.Join(w.home, p)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}
}

// lastResult is how the step called step last stood in w's last run, as
// its log says.
func lastResult(t *testing.T, w *world, step string) check.Result {
	t.Helper()
	var res *check.Result
	for _, r := range w.lastLog(t) {
		if r.Event == "step_finished" && r.Step == step {
			res = r.Result
		}
	}
	if res == nil {
		t.Fatalf("the run's log has no %s finished", step)
	}
	return *res
}

// excluded are the exclusions kit ran tmutil to add.
func excluded(w *world) []string {
	var out []string
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "tmutil addexclusion") {
			out = append(out, c)
		}
	}
	return out
}

// Without Full Disk Access for what kit runs in, the backup exclusions are
// held behind the step that checks it, nothing asked of tmutil, and that
// step says what to do where kit runs: at a terminal, give it the access
// and reopen it, named as TERM_PROGRAM says; over SSH, Remote Login's
// switch.
func TestBackupExclusionsWaitForFullDiskAccess(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  map[string]string
		item string
	}{
		{"in Ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, "Give Ghostty Full Disk Access, then reopen it"},
		{"in iTerm", map[string]string{"TERM_PROGRAM": "iTerm.app"}, "Give iTerm Full Disk Access, then reopen it"},
		{"in a terminal kit doesn't know", nil, "Give the terminal kit runs in Full Disk Access, then reopen it"},
		{"over SSH", map[string]string{"SSH_CONNECTION": "192.0.2.1 50000 192.0.2.2 22"}, "Allow Full Disk Access for remote users"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := exclusionsWorld(t)
			w.unreadable(t)
			maps.Copy(w.env, tt.env)
			out, errOut, _ := w.run(t, "apply")
			for _, want := range []string{"full-disk-access attention not granted\n", "full-disk-access problem " + tt.item, "backup-exclusion deferred needs Full Disk Access\n"} {
				if !strings.Contains(out, want) {
					t.Errorf("kit apply printed\n%s%s\nwant %q", out, errOut, want)
				}
			}
			if got := excluded(w); len(got) != 0 {
				t.Errorf("ran %q; want tmutil asked nothing", got)
			}
		})
	}
}

// Full Disk Access found, or nothing there to tell, tmutil refusing for
// want of it all the same is said once, as the step checking it not
// granted, what to do with it, and the exclusions held behind it: tmutil's
// asked once, never once a path. From kit.app, on the hour, that's kit.app
// without it, recorded as kit-app-access looks.
func TestBackupExclusionsRefusedFullDiskAccess(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		step    string
		summary string
		item    string
		waits   string
	}{
		{"kit apply", []string{"apply"}, "full-disk-access", "not granted: tmutil needs it", "Give Ghostty Full Disk Access, then reopen it", "needs Full Disk Access"},
		{"the hourly run", []string{"nightly", "--scheduled"}, "kit-app-access", "not granted: the hourly run at 03:04 couldn't read what it protects", "Give kit.app Full Disk Access", "needs kit.app's Full Disk Access"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := exclusionsWorld(t)
			w.env["TERM_PROGRAM"] = "ghostty"
			w.fake.OnMatch("tmutil", func(args []string) bool { return args[0] == "addexclusion" }).Exits(80).PrintsToStderr(tmutilNoAccess)
			w.fake.OnMatch("sudo", func(args []string) bool { return len(args) > 2 && args[2] == "addexclusion" }).Exits(80).PrintsToStderr(tmutilNoAccess)
			out, errOut, _ := w.run(t, tt.args...)
			if want := "backup-exclusion deferred " + tt.waits + "\n"; !strings.Contains(out, want) {
				t.Errorf("kit %s printed\n%s%s\nwant %q", strings.Join(tt.args, " "), out, errOut, want)
			}
			res := lastResult(t, w, tt.step)
			if res.State != check.Attention || res.Summary != tt.summary || len(res.Items) != 1 || res.Items[0].Name != tt.item {
				t.Errorf("%s stands %+v; want %q, its one item %q", tt.step, res, tt.summary, tt.item)
			}
			if got := excluded(w); len(got) != 1 {
				t.Errorf("ran %q; want tmutil asked once", got)
			}
		})
	}
}

// From kit.app, on the hour, the exclusions wait for kit.app's Full Disk
// Access, as this run, its child, finds it: not granted, tmutil's asked
// nothing; nothing there to tell, and nothing recorded, they go ahead, as
// tmutil would say otherwise.
func TestTheHourlyRunsExclusionsWaitForKitApp(t *testing.T) {
	for _, tt := range []struct {
		name     string
		denied   bool
		excluded int
	}{
		{"not granted", true, 0},
		{"nothing to tell", false, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := exclusionsWorld(t)
			if tt.denied {
				w.unreadable(t)
			}
			if err := nightly.Update(w.stateDir(), func(r *nightly.Record) { r.Access = nightly.Access{} }); err != nil {
				t.Fatal(err)
			}
			w.fake.On("tmutil", "addexclusion", filepath.Join(w.home, "Library/Caches/one"))
			out, errOut, _ := w.run(t, "nightly", "--scheduled")
			if held := strings.Contains(out, "backup-exclusion deferred needs kit.app's Full Disk Access\n"); held != tt.denied {
				t.Errorf("kit nightly --scheduled printed\n%s%s\nwant the exclusions held %v", out, errOut, tt.denied)
			}
			if got := excluded(w); len(got) != tt.excluded {
				t.Errorf("ran %q; want tmutil asked %d times", got, tt.excluded)
			}
		})
	}
}

// Without Full Disk Access, the exclusions' check still looks, as reading
// them needs none: kit status says what's missing, or that they're all
// in place.
func TestBackupExclusionsCheckedWithoutFullDiskAccess(t *testing.T) {
	for _, tt := range []struct {
		name  string
		skip  string
		match string
		want  string
	}{
		{"missing", "", "[Included]", "backup-exclusion attention 2 declared, 0 excluded\n"},
		{"all in place", "<string>/Applications</string>", "[Excluded]", "backup-exclusion ok 2 declared, all excluded\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := exclusionsWorld(t)
			w.unreadable(t)
			match := filepath.Join(w.home, "Library/Caches/one")
			w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Prints("<plist><dict><key>SkipPaths</key><array>" + tt.skip + "</array></dict></plist>")
			w.fake.On("tmutil", "isexcluded", match).Prints(tt.match + "  " + match + "\n")
			if out, errOut, _ := w.run(t, "status"); !strings.Contains(out, tt.want) {
				t.Errorf("kit status printed\n%s%s\nwant %q", out, errOut, tt.want)
			}
		})
	}
}

// What holds apps' settings back, as a wait in [features --after] for what
// isn't a step, never holds back the step the exclusions wait for: Full
// Disk Access is checked all the same.
func TestPrefsWaitNeverHoldsTheExclusionsAccess(t *testing.T) {
	w := exclusionsWorld(t)
	w.writeSection(t, "laptop", "features --after nosuch", "prefs\n")
	out, errOut, _ := w.run(t, "status")
	if !strings.Contains(out, "full-disk-access ok nothing protected to read\n") {
		t.Errorf("kit status printed\n%s%s\nwant Full Disk Access checked, not held with prefs", out, errOut)
	}
}

// At a terminal, without Full Disk Access, the run ends raising one step
// by hand for it, Ghostty's, its steps ending with applying the exclusions
// again; kit can't see it given till Ghostty's reopened, so the person
// leaves it for later, and the record lists it, the exclusions held behind
// it, tmutil asked nothing.
func TestApplyRaisesFullDiskAccessAtATerminal(t *testing.T) {
	w, kb := toDoWorld(t, false)
	w.unreadable(t)
	w.env["TERM_PROGRAM"] = "ghostty"
	w.write(t, "Library/Caches/one/.keep", "")
	w.writeSection(t, "laptop", "backup exclusions", "~/Library/Caches/*\n/Applications\n")
	match := filepath.Join(w.home, "Library/Caches/one")
	w.fake.On("tmutil", "isexcluded", match).Prints("[Included]  " + match + "\n")
	w.expectPassword()
	title := "Give Ghostty Full Disk Access, then reopen it"
	notified(t, w, kb, title, "Only apps with Full Disk Access can keep folders out of backups.", ask.Key{Text: "s"})

	out, errOut, code := w.run(t, "apply")
	if code == 2 {
		t.Fatalf("kit apply exit 2: %s", errOut)
	}
	records := w.lastLog(t)
	if !slices.ContainsFunc(cards(records), func(c logs.Record) bool { return c.Title == title }) {
		t.Errorf("cards went up %+v; want %q", cards(records), title)
	}
	lists := toDoLists(records)
	if len(lists) == 0 {
		t.Fatal("no to-dos were listed")
	}
	steps := []string{"System Settings › Privacy & Security › Full Disk Access, turn on Ghostty", "Quit Ghostty and open it again", "Run kit apply backup-exclusion"}
	last := lists[len(lists)-1]
	if i := slices.IndexFunc(last.ToDos, func(td event.ToDo) bool { return td.Title == title }); i < 0 || !slices.Equal(last.ToDos[i].How, steps) || last.ToDos[i].Done || !last.ToDos[i].Skipped {
		t.Errorf("the to-dos ended %+v; want %q left for later, its steps %q", last.ToDos, title, steps)
	}
	if res := lastResult(t, w, "backup-exclusion"); res.State != check.Deferred || res.Reason != "needs Full Disk Access" {
		t.Errorf("the exclusions stand %+v; want them held behind Full Disk Access", res)
	}
	record := ansi.Strip(out[max(strings.LastIndex(out, "█  ▄▀"), 0):])
	want := "  △ 1  " + title + "\n" +
		"       → System Settings › Privacy & Security › Full Disk Access, turn on\n" +
		"         Ghostty\n" +
		"       → Quit Ghostty and open it again\n" +
		"       → Run kit apply backup-exclusion\n"
	if !strings.Contains(record, want) {
		t.Errorf("the record is\n%s\nwant it to hold\n%s", record, want)
	}
	if got := excluded(w); len(got) != 0 {
		t.Errorf("ran %q; want tmutil asked nothing", got)
	}
}
