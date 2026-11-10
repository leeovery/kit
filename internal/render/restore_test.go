package render_test

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
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

// laptopSilent is the card the data restore raises in its row when the Mac
// it copies from, laptop, doesn't answer: why, a disk locked, and where
// else to restore from, a key each.
var laptopSilent = event.NeedsYou{Time: after(70), Step: "restore", Item: "restore:source", Title: "laptop doesn't answer over SSH",
	Why: "Someone’s MacBook Pro refused this Mac's key, which the old Mac's ~/.ssh/authorized_keys must hold. The Time Machine disk Vault is locked: unlock it in Disk Utility, then try again.",
	Keys: []check.Key{
		{Key: "t", Does: "restores from the Time Machine disk Backups of laptop"},
		{Key: "a", Does: "restores through Arq, by hand"},
		{Key: "enter", Does: "tries laptop again"},
	}}

// askingWhereFrom is the run with the data restore asking where to restore
// from, laptop not answering: Homebrew's formulae done, the restore's card
// up in its row.
func askingWhereFrom() []event.Event {
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
		event.Doing{Time: after(61), Step: "restore", Says: "looking for laptop"},
		event.Doing{Time: after(65), Step: "restore", Says: "looking for Time Machine disks"},
		laptopSilent,
	)
}

// The data restore's card, up mid-run in its row, offers its choices as
// the run's end does a to-do's: each key, what it does, a line each, never
// "space when it's done". In a small window, its card says a bigger window
// shows the keys.
func TestApplyingShowsTheRestoresChoices(t *testing.T) {
	took := 80 * time.Second
	for _, size := range [][2]int{{100, 30}, {160, 64}} {
		name := fmt.Sprintf("apply-screen-restore-card-%dx%d.golden", size[0], size[1])
		t.Run(name, func(t *testing.T) {
			face, out, _ := applyAt(size[0], size[1], took, askingWhereFrom())
			defer func() { _ = face.Close() }()
			golden(t, name, screen(out.String())+"\n")
		})
	}
	t.Run("apply-restore-card-small.golden", func(t *testing.T) {
		face, out, _ := applyAt(99, 40, took, askingWhereFrom())
		got := turned(screen(out.String()))
		if err := face.Close(); err != nil {
			t.Fatal(err)
		}
		golden(t, "apply-restore-card-small.golden", got+"\n")
	})
}

// The card saying what of Arq's restore isn't in its holding folders, once
// the person says it's done, lists each, numbered, then its choices, a line
// each.
func TestApplyingShowsWhatArqsRestoreLacks(t *testing.T) {
	lacking := event.NeedsYou{Time: after(75), Step: "restore", Item: "restore:arq-lacking", Title: "Arq's restore isn't all there",
		Why:   "Should Arq still be restoring, let it finish. Should a holding folder hold something else, move it into its own, or empty it. Then look again.",
		Steps: []string{"~/Code: …/arq/1 holds .claude, not Code", "~/.claude: …/arq/2 holds nothing yet"},
		Keys:  []check.Key{{Key: "enter", Does: "looks again"}, {Key: "n", Does: "says the backup hasn't those holding nothing, restoring the rest"}},
	}
	face, out, _ := applyAt(100, 30, 80*time.Second, append(askingWhereFrom(), lacking))
	defer func() { _ = face.Close() }()
	golden(t, "apply-screen-restore-lacking-100x30.golden", screen(out.String())+"\n")
}

// A mid-run card's choice being answered says what its key does, turning,
// in place of its keys.
func TestApplyingShowsTheRestoresChoiceAnswered(t *testing.T) {
	card := laptopSilent
	card.Time, card.Answering = after(75), "t"
	face, out, _ := applyAt(160, 64, 80*time.Second, append(askingWhereFrom(), card))
	defer func() { _ = face.Close() }()
	_, right := panes(screen(out.String()), 160)
	if !slices.Contains(right, "      ◐ restores from the Time Machine disk Backups of laptop") || slices.Contains(right, "      a restores through Arq, by hand") {
		t.Errorf("answered with t, the panel is\n%s\nwant what t does, turning, alone", strings.Join(right[:14], "\n"))
	}
}

// A mid-run card's keys answer it, passing each to its step; space, not
// one of them, answers nothing, as kit can't take it as done; tab, with no
// card waiting after it, chooses a row.
func TestApplyingAnswersTheRestoresChoices(t *testing.T) {
	face, _, kb := applyAt(160, 64, 80*time.Second, askingWhereFrom())
	defer func() { _ = face.Close() }()
	var mu sync.Mutex
	var got []hand.Answer
	face.Hands(func(step string, a hand.Answer) bool {
		mu.Lock()
		defer mu.Unlock()
		if step == "restore" {
			got = append(got, a)
		}
		return true
	})
	kb.press(t, ask.Key{Name: "tab"}, ask.Key{Text: " "}, ask.Key{Text: "t"}, ask.Key{Text: "a"}, ask.Key{Name: "enter"})
	mu.Lock()
	defer mu.Unlock()
	if want := []hand.Answer{"t", "a", "enter"}; !slices.Equal(got, want) {
		t.Errorf("tab, space, t, a and enter answered %q; want %q", got, want)
	}
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
