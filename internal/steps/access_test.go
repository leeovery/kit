package steps_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/steps"
)

// probes are the folders the probe reads, which only Full Disk Access
// shows.
var probes = []string{"Library/Safari", "Library/Mail", "Library/Messages"}

// protected is a home whose folders only Full Disk Access shows are there,
// readable or not as readable says.
func protected(t *testing.T, readable bool) string {
	t.Helper()
	home := t.TempDir()
	for _, p := range probes {
		dir := filepath.Join(home, p)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if !readable {
			if err := os.Chmod(dir, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		}
	}
	return home
}

// env is a getenv answering with vars.
func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// ssh is an SSH session's environment.
var ssh = map[string]string{"TERM_PROGRAM": "ghostty", "SSH_CONNECTION": "192.0.2.1 50000 192.0.2.2 22"}

// Each place kit runs from has its own way to give Full Disk Access: a
// terminal, named as TERM_PROGRAM says when kit knows it, is given it in
// System Settings and reopened; an SSH session, by Remote Login's switch;
// kit.app, run on a schedule, by the step by hand kit-app-access raises,
// what waits for it saying it's kit.app's.
func TestGrantingFor(t *testing.T) {
	for _, tt := range []struct {
		name      string
		vars      map[string]string
		scheduled bool
		step      string
		wait      string
		title     string
		first     string
	}{
		{"Ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, false, steps.FullDiskAccessName, "needs Full Disk Access",
			"Give Ghostty Full Disk Access, then reopen it", "System Settings › Privacy & Security › Full Disk Access, turn on Ghostty"},
		{"Terminal", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, false, steps.FullDiskAccessName, "needs Full Disk Access",
			"Give Terminal Full Disk Access, then reopen it", "System Settings › Privacy & Security › Full Disk Access, turn on Terminal"},
		{"iTerm", map[string]string{"TERM_PROGRAM": "iTerm.app"}, false, steps.FullDiskAccessName, "needs Full Disk Access",
			"Give iTerm Full Disk Access, then reopen it", "System Settings › Privacy & Security › Full Disk Access, turn on iTerm"},
		{"a terminal kit doesn't know", map[string]string{"TERM_PROGRAM": "vscode"}, false, steps.FullDiskAccessName, "needs Full Disk Access",
			"Give the terminal kit runs in Full Disk Access, then reopen it", "System Settings › Privacy & Security › Full Disk Access, turn on the terminal kit runs in"},
		{"over SSH", ssh, false, steps.FullDiskAccessName, "needs Full Disk Access",
			"Allow Full Disk Access for remote users", "System Settings › General › Sharing › Remote Login › ⓘ"},
		{"from kit.app", ssh, true, steps.AppAccessName, "needs kit.app's Full Disk Access", "Give kit.app Full Disk Access", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := steps.GrantingFor(env(tt.vars), tt.scheduled)
			first := ""
			if len(g.Steps) > 0 {
				first = g.Steps[0]
			}
			if g.Step != tt.step || g.Wait != tt.wait || g.Title != tt.title || first != tt.first || g.Do == "" {
				t.Errorf("GrantingFor() = %+v; want step %s, %q, %q, first %q, and a line saying what to do", g, tt.step, tt.wait, tt.title, tt.first)
			}
		})
	}
}

// For what waits for it: with the backup exclusions waiting, the to-do ends
// by applying them, in its steps and its line, and at a terminal it's
// raised; with apps' settings alone, it's named, never raised; its why says
// what's declared. kit.app's is left as it is.
func TestGrantingForWhatWaits(t *testing.T) {
	terminal := steps.GrantingFor(env(map[string]string{"TERM_PROGRAM": "ghostty"}), false)
	remote := steps.GrantingFor(env(ssh), false)
	app := steps.GrantingFor(env(nil), true)
	exclusions := []string{"backup-exclusion"}
	for _, tt := range []struct {
		name   string
		g      steps.Granting
		again  []string
		prefs  bool
		raised bool
		last   string
		why    string
	}{
		{"exclusions, at a terminal", terminal, exclusions, false, true, "Run kit apply backup-exclusion", "Only apps with Full Disk Access can keep folders out of backups. Ghostty takes it up only once it's reopened."},
		{"both, at a terminal", terminal, exclusions, true, true, "Run kit apply backup-exclusion", "Only apps with Full Disk Access can keep folders out of backups or read apps' settings. Ghostty takes it up only once it's reopened."},
		{"apps' settings alone, at a terminal", terminal, nil, true, false, "Quit Ghostty and open it again", "Only apps with Full Disk Access can read apps' settings. Ghostty takes it up only once it's reopened."},
		{"exclusions, over SSH", remote, exclusions, false, false, "Run kit apply backup-exclusion", "SSH sessions here can't keep folders out of backups without Full Disk Access for remote users."},
		{"kit.app's", app, exclusions, true, false, "", app.Why},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.g.For(tt.again, tt.prefs)
			last := ""
			if len(g.Steps) > 0 {
				last = g.Steps[len(g.Steps)-1]
			}
			if g.Raised != tt.raised || last != tt.last || g.Why != tt.why {
				t.Errorf("For() = %+v; want raised %v, last step %q, why %q", g, tt.raised, tt.last, tt.why)
			}
			if again := strings.HasSuffix(g.Do, ", then run kit apply backup-exclusion"); again != (len(tt.again) > 0 && g.Step == steps.FullDiskAccessName) {
				t.Errorf("Do = %q; want it to end applying the exclusions only when they wait", g.Do)
			}
		})
	}
}

// The step finds what kit runs in has Full Disk Access when what only that
// shows reads, or there's none of it to read, and no program kit ran has
// refused for want of it; else not, the item the to-do giving it, raised
// as For has it once something's been held for it, else named, as with
// nothing to exclude nothing waits. Access is granted, or not, as the step
// finds it.
func TestFullDiskAccess(t *testing.T) {
	terminal := steps.GrantingFor(env(map[string]string{"TERM_PROGRAM": "ghostty"}), false)
	waited := terminal.For([]string{"backup-exclusion"}, false)
	prefsAlone := terminal.For(nil, true)
	remote := steps.GrantingFor(env(ssh), false).For([]string{"backup-exclusion"}, false)
	for _, tt := range []struct {
		name    string
		home    string
		g       steps.Granting
		refused bool
		held    bool
		state   check.State
		summary string
		raised  bool
	}{
		{"readable", protected(t, true), waited, false, false, check.OK, "granted", false},
		{"nothing to read", t.TempDir(), waited, false, false, check.OK, "nothing protected to read", false},
		{"denied, nothing held", protected(t, false), waited, false, false, check.Attention, "not granted", false},
		{"denied, an exclusion held", protected(t, false), waited, false, true, check.Attention, "not granted", true},
		{"denied, apps' settings alone", protected(t, false), prefsAlone, false, true, check.Attention, "not granted", false},
		{"denied over SSH", protected(t, false), remote, false, true, check.Attention, "not granted", false},
		{"readable, tmutil refused", protected(t, true), waited, true, true, check.Attention, "not granted: tmutil needs it", true},
		{"nothing to read, tmutil refused", t.TempDir(), waited, true, true, check.Attention, "not granted: tmutil needs it", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			access := &steps.Access{Home: tt.home}
			if tt.refused {
				access.Refused("tmutil")
			}
			if tt.held {
				access.Held()
			}
			step := steps.FullDiskAccess(tt.g, access)
			state, summary, ids := checked(t, step)
			if state != tt.state || summary != tt.summary || access.Granted() != (state == check.OK) {
				t.Fatalf("check = %s %q, granted %v; want %s %q", state, summary, access.Granted(), tt.state, tt.summary)
			}
			if state == check.OK {
				return
			}
			it := step.Check(t.Context()).Items[0]
			if !slices.Equal(ids, []string{"full-disk-access:denied"}) || it.Name != tt.g.Title || it.Detail != tt.g.Do {
				t.Errorf("item = %+v; want the one, titled %q, saying %q", it, tt.g.Title, tt.g.Do)
			}
			if raised := it.ToDo != nil; raised != tt.raised {
				t.Fatalf("to-do = %+v; want one raised %v", it.ToDo, tt.raised)
			}
			if tt.raised && (it.ToDo.Title != tt.g.Title || !slices.Equal(it.ToDo.Steps, tt.g.Steps) || it.ToDo.Why != tt.g.Why) {
				t.Errorf("to-do = %+v; want %q, its why, then its steps %q", it.ToDo, tt.g.Title, tt.g.Steps)
			}
		})
	}
}
