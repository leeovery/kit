package cli_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/login"
	"github.com/leeovery/kit/internal/logs"
)

// timeMachinePrefs are Time Machine's settings, as defaults exports them:
// automatic backups on, and a disk backed up to half an hour before the
// world's now.
const timeMachinePrefs = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>AutoBackup</key><true/>
<key>Destinations</key><array><dict>
<key>SnapshotDates</key><array><date>2026-01-02T02:34:05Z</date></array>
</dict></array>
</dict></plist>`

// toDoWorld is a Mac at a terminal 160 by 64, using Time Machine: no backup disk as its step's checked, one there each time it's
// looked at after, unless never.
func toDoWorld(t *testing.T, never bool) (*world, *keyboard) {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "backups", "time-machine\n")
	tm := w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Exits(1).PrintsToStderr("file does not exist")
	if !never {
		tm.Then().Prints(timeMachinePrefs)
	}
	w.fake.On("tmutil", "status").Prints("Backup session status:\n{\n    Running = 0;\n}\n")
	// osascript there, the login items are checked too: there are none.
	w.fake.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints("[]")
	w.terminal, w.size = true, [2]int{160, 64}
	// Reconciling what's drifted is offered after: not now.
	w.choose = func(string, []string) (int, error) { return 1, nil }
	kb := &keyboard{}
	w.listen = kb.listen
	return w, kb
}

// notified scripts the notification of the to-do titled title, saying
// body, to press keys as it's posted.
func notified(t *testing.T, w *world, kb *keyboard, title, body string, keys ...ask.Key) {
	w.fake.On("osascript", "-e", `display notification "`+body+`" with title "kit" subtitle "`+title+`"`).Does(func() { kb.press(t, keys...) })
}

// toDoLists are the to-do lists records says the run worked through, in
// order.
func toDoLists(records []logs.Record) []logs.Record {
	var lists []logs.Record
	for _, r := range records {
		if r.Event == "to_dos" {
			lists = append(lists, r)
		}
	}
	return lists
}

// finishedAs are how each time the step called step finished, as records
// say, stood.
func finishedAs(records []logs.Record, step string) []check.State {
	var states []check.State
	for _, r := range records {
		if r.Event == "step_finished" && r.Step == step {
			states = append(states, r.Result.State)
		}
	}
	return states
}

// titles are the to-dos' titles in list, in order.
func titles(list logs.Record) []string {
	var out []string
	for _, t := range list.ToDos {
		out = append(out, t.Title)
	}
	return out
}

// At a terminal, the run ends working through what's left for the person,
// one at a time, in kit's order: apps' settings first, a choice, f starting
// afresh as kit prefs start-fresh does; the config's line, space saying
// it's done; Time Machine's disk last, kit seeing it there. After the last,
// the steps they held are checked again, their rows and the record as they
// stand now.
func TestApplyWorksThroughTheToDos(t *testing.T) {
	w, kb := toDoWorld(t, false)
	w.writeSection(t, "laptop", "features", "prefs\n")
	w.writeSection(t, "laptop", "manual", `sharing "Allow remote users Full Disk Access" --group "System Settings"`+"\n  System Settings › General › Sharing\n")
	notified(t, w, kb, "Bring back apps' settings", "kit waits for you to do this", ask.Key{Text: "f"})
	notified(t, w, kb, "Allow remote users Full Disk Access", "System Settings › General › Sharing", ask.Key{Text: " "})

	if _, errOut, code := w.run(t, "apply"); code == 2 {
		t.Fatalf("kit apply exit 2: %s", errOut)
	}
	records := w.lastLog(t)
	lists := toDoLists(records)
	if len(lists) != 4 {
		t.Fatalf("the to-dos were listed %d times, want once as each came up and once over: %+v", len(lists), lists)
	}
	want := []string{"Bring back apps' settings", "Allow remote users Full Disk Access", "Add a Time Machine disk"}
	if got := titles(lists[0]); !slices.Equal(got, want) || *lists[0].Up != 0 {
		t.Errorf("the to-dos came up as %q, %d up; want %q, the first up", got, *lists[0].Up, want)
	}
	last := lists[len(lists)-1]
	for i, saw := range []string{"started afresh, saving from now", "", "last backup 02:34"} {
		if td := last.ToDos[i]; !td.Done || td.Saw != saw {
			t.Errorf("%s ended %+v; want it done, kit having seen %q", td.Title, td, saw)
		}
	}
	if *last.Up != -1 {
		t.Errorf("the last list has %d up, want none", *last.Up)
	}
	if got := len(cards(records)); got != 2 {
		t.Errorf("%d cards went up, want apps' settings' and the config's line: Time Machine's disk was there by its turn", got)
	}
	if got := finishedAs(records, "time-machine"); !slices.Equal(got, []check.State{check.Attention, check.OK}) {
		t.Errorf("Time Machine finished %q; want left to do, then, checked again, ok", got)
	}
	if got := finishedAs(records, "prefs"); len(got) != 2 {
		t.Errorf("apps' settings finished %q; want it checked again once they started afresh", got)
	}
	var rec struct {
		On string `json:"on"`
	}
	if err := json.Unmarshal([]byte(w.read(t, filepath.Join(".local", "state", "kit", "prefs.json"))), &rec); err != nil || !strings.HasPrefix(rec.On, "started fresh") {
		t.Errorf("prefs.json says %+v, %v: want capture switched on, started afresh", rec, err)
	}
}

// q's Stop at the to-dos stops there: the run ends as it stands, not
// stopped, the record's To do listing what's left in kit's order, each by
// its title, its steps as how.
func TestStoppingAtTheToDosLeavesTheRestToDo(t *testing.T) {
	w, kb := toDoWorld(t, true)
	w.writeSection(t, "laptop", "manual", `first "Do the first"`+"\n"+`second "Do the second"`+"\n  Open the second\n")
	notified(t, w, kb, "Do the first", "kit waits for you to do this", ask.Key{Text: "q"}, ask.Key{Name: "down"}, ask.Key{Name: "enter"})

	out, errOut, code := w.run(t, "apply")
	if code != 1 {
		t.Fatalf("kit apply, stopped at the to-dos, exit %d (%s), want 1: they're left to do", code, errOut)
	}
	records := w.lastLog(t)
	lists := toDoLists(records)
	if last := lists[len(lists)-1]; *last.Up != -1 || slices.ContainsFunc(last.ToDos, func(td event.ToDo) bool { return td.Done }) {
		t.Errorf("stopped, the list ended %+v; want none up, none done", last)
	}
	if got := finishedAs(records, "manual"); !slices.Equal(got, []check.State{check.Attention}) {
		t.Errorf("the steps by hand finished %q, want them left to do, not stopped", got)
	}
	record := ansi.Strip(out[max(strings.LastIndex(out, "█  ▄▀"), 0):])
	want := "\n  TO DO  3 · kit apply brings them back\n" +
		"  △ 1  Do the first\n" +
		"       → then: kit manual done first\n" +
		"  △ 2  Do the second\n" +
		"       → Open the second\n" +
		"  △ 3  Add a Time Machine disk\n" +
		"       → Plug in your backup disk\n"
	if !strings.Contains(record, want) || strings.Contains(record, "stopped") || !strings.Contains(record, "│  3 to do · ") {
		t.Errorf("the record is\n%s\nwant it to hold\n%s\nand the run not stopped", record, want)
	}
}
