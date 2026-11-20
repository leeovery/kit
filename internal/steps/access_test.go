package steps_test

import (
	"os"
	"path/filepath"
	"slices"
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

// Each place kit runs from has its own way to give Full Disk Access: a
// terminal, named as TERM_PROGRAM says when kit knows it, is given it in
// System Settings and reopened, a to-do of the full-disk-access step's own;
// an SSH session, by Remote Login's switch, which the config's own step by
// hand covers, so kit names it alone; kit.app, run on a schedule, by the
// step by hand kit-app-access raises.
func TestGrantingFor(t *testing.T) {
	for _, tt := range []struct {
		name      string
		vars      map[string]string
		scheduled bool
		step      string
		title     string
		first     string
		raised    bool
	}{
		{"Ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, false, steps.FullDiskAccessName,
			"Give Ghostty Full Disk Access, then reopen it", "System Settings › Privacy & Security › Full Disk Access, turn on Ghostty", true},
		{"Terminal", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, false, steps.FullDiskAccessName,
			"Give Terminal Full Disk Access, then reopen it", "System Settings › Privacy & Security › Full Disk Access, turn on Terminal", true},
		{"iTerm", map[string]string{"TERM_PROGRAM": "iTerm.app"}, false, steps.FullDiskAccessName,
			"Give iTerm Full Disk Access, then reopen it", "System Settings › Privacy & Security › Full Disk Access, turn on iTerm", true},
		{"a terminal kit doesn't know", map[string]string{"TERM_PROGRAM": "vscode"}, false, steps.FullDiskAccessName,
			"Give the terminal kit runs in Full Disk Access, then reopen it", "System Settings › Privacy & Security › Full Disk Access, turn on the terminal kit runs in", true},
		{"over SSH", map[string]string{"TERM_PROGRAM": "ghostty", "SSH_CONNECTION": "192.0.2.1 50000 192.0.2.2 22"}, false, steps.FullDiskAccessName,
			"Allow Full Disk Access for remote users", "System Settings › General › Sharing › Remote Login › ⓘ", false},
		{"from kit.app", map[string]string{"TERM_PROGRAM": "ghostty", "SSH_CONNECTION": "192.0.2.1 50000 192.0.2.2 22"}, true, steps.AppAccessName,
			"Give kit.app Full Disk Access", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := steps.GrantingFor(env(tt.vars), tt.scheduled)
			first := ""
			if len(g.Steps) > 0 {
				first = g.Steps[0]
			}
			if g.Step != tt.step || g.Title != tt.title || first != tt.first || g.Raised != tt.raised || g.Do == "" {
				t.Errorf("GrantingFor() = %+v; want step %s, %q, first %q, raised %v, and a line saying what to do", g, tt.step, tt.title, tt.first, tt.raised)
			}
		})
	}
}

// The to-do giving Full Disk Access ends by applying the steps waiting for
// it, when there are any; kit-app-access's, with no steps of its own here,
// is left as it is.
func TestGrantingThen(t *testing.T) {
	terminal := steps.GrantingFor(env(map[string]string{"TERM_PROGRAM": "ghostty"}), false)
	if got, want := terminal.Then([]string{"backup-exclusion"}).Steps, []string{
		"System Settings › Privacy & Security › Full Disk Access, turn on Ghostty",
		"Quit Ghostty and open it again",
		"Run kit apply backup-exclusion",
	}; !slices.Equal(got, want) {
		t.Errorf("Then() = %q; want %q", got, want)
	}
	if got := terminal.Then(nil).Steps; len(got) != 2 {
		t.Errorf("Then(none) = %q; want the steps as they were", got)
	}
	if got := steps.GrantingFor(env(nil), true).Then([]string{"backup-exclusion"}).Steps; len(got) != 0 {
		t.Errorf("kit.app's Then() = %q; want none", got)
	}
}

// The step finds what kit runs in has Full Disk Access when what only that
// shows reads, or there's none of it to read, and no program kit ran has
// refused for want of it; else not, the item the to-do giving it, raised
// at a terminal, named over SSH.
func TestFullDiskAccess(t *testing.T) {
	terminal := steps.GrantingFor(env(map[string]string{"TERM_PROGRAM": "ghostty"}), false).Then([]string{"backup-exclusion"})
	remote := steps.GrantingFor(env(map[string]string{"SSH_CONNECTION": "192.0.2.1 50000 192.0.2.2 22"}), false)
	refused := &steps.Refusal{}
	refused.Note("tmutil")
	for _, tt := range []struct {
		name    string
		home    string
		g       steps.Granting
		refused *steps.Refusal
		state   check.State
		summary string
		raised  bool
	}{
		{"readable", protected(t, true), terminal, nil, check.OK, "granted", false},
		{"nothing to read", t.TempDir(), terminal, nil, check.OK, "nothing protected to read", false},
		{"denied at a terminal", protected(t, false), terminal, nil, check.Attention, "not granted", true},
		{"denied over SSH", protected(t, false), remote, nil, check.Attention, "not granted", false},
		{"readable, tmutil refused", protected(t, true), terminal, refused, check.Attention, "not granted: tmutil needs it", true},
		{"nothing to read, tmutil refused", t.TempDir(), terminal, refused, check.Attention, "not granted: tmutil needs it", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state, summary, ids := checked(t, steps.FullDiskAccess(tt.home, tt.g, tt.refused))
			if state != tt.state || summary != tt.summary {
				t.Fatalf("check = %s %q; want %s %q", state, summary, tt.state, tt.summary)
			}
			if state == check.OK {
				return
			}
			res := steps.FullDiskAccess(tt.home, tt.g, tt.refused).Check(t.Context())
			it := res.Items[0]
			if !slices.Equal(ids, []string{"full-disk-access:denied"}) || it.Name != tt.g.Title || it.Detail != tt.g.Do {
				t.Errorf("items = %+v; want the one, titled %q, saying %q", res.Items, tt.g.Title, tt.g.Do)
			}
			if raised := it.ToDo != nil; raised != tt.raised {
				t.Fatalf("to-do = %+v; want one raised %v", it.ToDo, tt.raised)
			}
			if tt.raised && (it.ToDo.Title != tt.g.Title || !slices.Equal(it.ToDo.Steps, tt.g.Steps) || it.ToDo.Why == "") {
				t.Errorf("to-do = %+v; want %q, its why, then its steps %q", it.ToDo, tt.g.Title, tt.g.Steps)
			}
		})
	}
}
