package render_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/render"
)

// restoringMac are a new Mac's steps restoring its data, in the words the
// data restore's step uses: Backups' row, with Time Machine and apps'
// settings; Claude's MCP servers waiting for it, as they come after it.
var restoringMac = []event.Step{
	{Name: "homebrew", Title: "Homebrew", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"},
	{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Waits: []string{"homebrew"}},
	{Name: "claude-mcp", Title: "Claude MCP", Area: "Drift", Row: "Developer tools", Waits: []string{"restore"}},
	{Name: "time-machine", Title: "Time Machine", Area: "Backups", Row: "Backups"},
	{Name: "prefs", Title: "App settings", Area: "Backups"},
	{Name: "restore", Title: "Data restore", Area: "Backups", Row: "Backups", Verb: "restored", Waits: []string{"brew"}},
}

// restoringData is the run with the data restore copying ~/Code, the
// second of its paths to copy: what's here left as it is, ~/.claude
// restored, the log saying so, and how far the copies have got.
func restoringData() []event.Event {
	events := []event.Event{event.RunStarted{Time: at, Command: "bootstrap", Machine: "studio", Steps: restoringMac},
		fine(2, "homebrew", "/opt/homebrew"),
		fine(5, "time-machine", "no backup disk yet"),
		fine(6, "prefs", "saving from tonight"),
	}
	events = append(events, applying(10, "brew")...)
	events = append(events, installed("brew", 11, 20, "rsync", "jq")...)
	events = append(events, fine(60, "brew", "2 declared, all installed"))
	events = append(events, applying(61, "restore")...)
	return append(events,
		event.Doing{Time: after(61), Step: "restore", Says: "opening Time Machine's backup"},
		event.Logged{Time: after(62), Step: "restore", Line: "Left ~/Documents as it is: it held files of its own"},
		event.Doing{Time: after(62), Step: "restore", Says: "measuring ~/.claude"},
		event.Doing{Time: after(90), Step: "restore", Says: "measuring ~/Code"},
		event.Doing{Time: after(140), Step: "restore", Says: "copying ~/.claude", Of: 132611},
		event.Doing{Time: after(1800), Step: "restore", Says: "comparing ~/.claude", Done: 80301, Of: 132611, Far: 0.42},
		event.Logged{Time: after(2100), Step: "restore", Line: "Restored ~/.claude: 80301 files, 36.0 GB"},
		event.Doing{Time: after(2101), Step: "restore", Says: "copying ~/Code", Done: 80301, Of: 132611, Far: 0.42},
		event.Doing{Time: after(2400), Step: "restore", Says: "copying ~/Code", Done: 101204, Of: 132611, Far: 0.61},
	)
}

// restoredData is restoringData to its end: the restore done, two paths
// restored and one here, and Claude's servers added after it.
func restoredData() []event.Event {
	events := append(restoringData(),
		event.Logged{Time: after(3300), Step: "restore", Line: "Restored ~/Code: 52310 files, 49.8 GB"},
		finished(3400, "restore", check.Result{State: check.OK, Summary: "3 declared, 2 restored, 1 here already",
			Counts: map[string]int{"declared": 3, "restored": 2, "here": 1},
			Done: []check.Item{
				{ID: "restore:~/.claude", Name: "~/.claude", State: "missing", Action: "restore"},
				{ID: "restore:~/Code", Name: "~/Code", State: "missing", Action: "restore"},
			}}),
		finished(3420, "claude-mcp", check.Result{State: check.OK, Summary: "4 declared, all added to Claude Code"}),
	)
	return append(events, event.RunFinished{Time: after(3421), Duration: 57 * time.Minute, Counts: map[check.State]int{check.OK: 6}})
}

// The data restore's row says how far its copies have got, as the step
// says it: what it's copying, the files copied of all those to copy, and,
// in the bar under the row, how far by their bytes; the log what it's
// restored. Done, the record says what it restored, and how its paths
// stand. In a small window, the row's the same, in the run's place.
func TestApplyingShowsTheDataRestore(t *testing.T) {
	took := 40 * time.Minute
	for _, size := range [][2]int{{100, 30}, {160, 64}} {
		name := fmt.Sprintf("apply-screen-restore-%dx%d.golden", size[0], size[1])
		t.Run(name, func(t *testing.T) {
			face, out, _ := applyAt(size[0], size[1], took, restoringData())
			defer func() { _ = face.Close() }()
			golden(t, name, screen(out.String())+"\n")
		})
	}
	t.Run("apply-restore-small.golden", func(t *testing.T) {
		face, out, _ := applyAt(99, 40, took, restoringData())
		got := turned(screen(out.String()))
		if err := face.Close(); err != nil {
			t.Fatal(err)
		}
		golden(t, "apply-restore-small.golden", got+"\n")
	})
	t.Run("apply-record-restore.golden", func(t *testing.T) {
		face, out, _ := applyAt(160, 64, time.Hour, restoredData())
		if err := face.Close(); err != nil {
			t.Fatal(err)
		}
		golden(t, "apply-record-restore.golden", screen(out.String())+"\n")
	})
}

// kit status shows the data restore with Backups: each path to restore,
// declared, not restored, what's to come after it, and what to run; once
// it's settled, fine, what would bring a path its source hadn't, or one
// listed since.
func TestStatusShowsTheDataRestore(t *testing.T) {
	backups := []event.Step{
		{Name: "time-machine", Title: "Time Machine", Area: "Backups", Row: "Backups"},
		{Name: "restore", Title: "Data restore", Area: "Backups", Row: "Backups", Verb: "restored"},
	}
	for _, tt := range []struct {
		golden string
		result check.Result
	}{
		{"status-restore.golden", check.Result{State: check.Attention, Summary: "5 declared, 1 here already, 4 to restore", Items: []check.Item{
			{ID: "restore:~/Code", Name: "~/Code", State: "missing", Detail: "part-way from Time Machine's backup of 8 Nov 2026 14:19", Action: "restore"},
			{ID: "restore:~/.claude", Name: "~/.claude", State: "missing", Action: "restore"},
			{ID: "restore:~/.zsh_history", Name: "~/.zsh_history", State: "missing", Detail: "set-up's copy set aside first", Action: "restore"},
			{ID: "restore:~/Documents", Name: "~/Documents", State: "missing", Detail: "2 files differed from Time Machine's backup of 8 Nov 2026 14:19, copied again; compared again next run", Action: "restore"},
		}}},
		{"status-restore-settled.golden", check.Result{State: check.OK, Summary: "5 declared, 3 restored, 2 not restored", Items: []check.Item{
			{ID: "restore:~/.config/tool", Name: "~/.config/tool", State: "not restored", Detail: "Time Machine's backup of 8 Nov 2026 14:19 hadn't it; kit apply --restore-from time-machine|arq brings it from another", Quiet: "not in its source"},
			{ID: "restore:~/.local/state/newtool", Name: "~/.local/state/newtool", State: "not restored", Detail: "kit apply --restore-from time-machine|arq brings it", Quiet: "listed since the restore settled"},
		}}},
	} {
		t.Run(tt.golden, func(t *testing.T) {
			var out bytes.Buffer
			show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
				event.RunStarted{Time: at, Command: "status", Machine: "studio", Steps: backups},
				fine(1, "time-machine", "latest backup 14:19"),
				finished(2, "restore", tt.result),
				event.RunFinished{Time: after(3), Duration: 3 * time.Second, Counts: map[check.State]int{check.OK: 1, tt.result.State: 1}},
			})
			golden(t, tt.golden, out.String())
		})
	}
}
