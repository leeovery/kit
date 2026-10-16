package render_test

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/look"
)

// The to-dos at the run's end: kit's own (apps' settings first, the
// backups last) and the config's lines, in kit's order, grouped.
var toDoList = []event.ToDo{
	{Step: "prefs", ID: "prefs:paused", Title: "Bring back apps' settings", Group: "Apps", How: []string{"kit prefs restore, or kit prefs start-fresh"}},
	{Step: "manual", ID: "manual:karabiner", Title: "Open Karabiner-Elements", Group: "Apps", How: []string{"Open Karabiner-Elements", "Allow what it asks for"}},
	{Step: "manual", ID: "manual:hammerspoon", Title: "Open Hammerspoon", Group: "Apps", How: []string{"Open Hammerspoon", "Allow what it asks for"}},
	{Step: "manual", ID: "manual:sharing", Title: "Allow Full Disk Access for remote users", Group: "System Settings", How: []string{"System Settings › General › Sharing › Remote Login › ⓘ", "Turn on Allow full disk access for remote users"}},
	{Step: "time-machine", ID: "time-machine:none", Title: "Add a Time Machine disk", Group: "Backups", How: []string{"Plug in your backup disk", "System Settings › General › Time Machine › Add Backup Disk, and choose it"}},
	{Step: "arq", ID: "arq:none", Title: "Set up Arq's backup plan", Group: "Backups", How: []string{"Open Arq", "Sign in to your Arq account", "Adopt this Mac's plan, and let it run"}},
}

// toDoCards are the to-dos' cards, as each comes up: a choice; a config
// line kit tells about; one it can't, and one with a subheading; kit's own,
// watched, each saying what for.
var toDoCards = []event.NeedsYou{
	{Item: "prefs:paused", Title: "Bring back apps' settings", Group: "Apps",
		Why:  "First, so the apps you open next start with yours. This Mac saves its own once they're back. Claude Code's come too, sending its temporary files to Scratch.",
		Keys: []check.Key{{Key: "r", Does: "brings them back from this Mac's last save, 15 Oct"}, {Key: "f", Does: "starts afresh: nothing back, saving from now"}}},
	{Item: "karabiner", Title: "Open Karabiner-Elements", Group: "Apps", Tells: true,
		Why: "Its key remapping needs its driver and Input Monitoring, which only you can allow. Opened, it asks for both.",
		Steps: []string{"Open Karabiner-Elements", "Allow what it asks for", "If it doesn't ask, in System Settings:",
			"Privacy & Security: allow its driver (the Allow button, near the bottom)", "Privacy & Security › Input Monitoring: turn on Karabiner-Elements"}},
	{Item: "hammerspoon", Title: "Open Hammerspoon", Group: "Apps",
		Why:   "Your window moves and shortcuts need Accessibility, which only you can allow. Opened, it asks.",
		Steps: []string{"Open Hammerspoon", "Allow what it asks for"}},
	{Item: "sharing", Title: "Allow Full Disk Access for remote users", Group: "System Settings",
		Why:   "So an SSH session from another Mac can reach everything, which Remote Login alone can't.",
		Steps: []string{"System Settings › General › Sharing › Remote Login › ⓘ", "Turn on Allow full disk access for remote users"}},
	{Item: "time-machine:none", Title: "Add a Time Machine disk", Group: "Backups", Tells: true, Watch: "Time Machine has a disk",
		Why:   "Time Machine has nowhere to back up to.",
		Steps: []string{"Plug in your backup disk", "System Settings › General › Time Machine › Add Backup Disk, and choose it"}},
	{Item: "arq:none", Title: "Set up Arq's backup plan", Group: "Backups", Tells: true, Watch: "Arq has a plan",
		Why:   "Arq is in, but has nothing to back up to yet.",
		Steps: []string{"Open Arq", "Sign in to your Arq account", "Adopt this Mac's plan, and let it run"}},
}

// toDoSaw are what kit saw as each to-do was done.
var toDoSaw = []string{"214 apps' settings, from 15 Oct", "", "", "", "no backup yet", "no recent backup"}

// yours is step's result left for the person: its to-do, t.
func yours(t event.ToDo) check.Result {
	return check.Result{State: check.Attention, Summary: "not set up", Items: []check.Item{{ID: t.ID, Name: t.Title, State: check.Problem, ToDo: &check.ToDo{Title: t.Title, Group: t.Group}}}}
}

// toDosBegin is a new Mac's run as its to-dos begin, half an hour in:
// every other step finished, Time Machine, Arq and apps' settings left to
// do, the steps by hand at the end applying.
func toDosBegin() []event.Event {
	events := []event.Event{event.RunStarted{Time: at, Command: "bootstrap", Machine: "laptop", Steps: newMac}}
	left := map[string]event.ToDo{"prefs": toDoList[0], "time-machine": toDoList[4], "arq": toDoList[5]}
	for i, s := range newMac {
		switch t, ok := left[s.Name]; {
		case s.Name == "manual":
		case ok:
			events = append(events, finished(1700+i, s.Name, yours(t)))
		default:
			events = append(events, fine(1700+i, s.Name, "fine"))
		}
	}
	return append(events, event.StepStarted{Time: after(1790), Step: "manual", Doing: "applying"})
}

// toDosUpTo is toDosBegin, the to-dos worked through to the one at up, a
// minute or so each: each before it done, what kit saw said, and up's card
// up; with up past the last, all done, the steps they held checked again,
// as kit carries on.
func toDosUpTo(up int) []event.Event {
	events := toDosBegin()
	list := slices.Clone(toDoList)
	sec := 1800
	for i := range min(up+1, len(list)) {
		events = append(events, event.ToDos{Time: after(sec), Step: "manual", List: slices.Clone(list), Up: i})
		if i == up {
			card := toDoCards[i]
			card.Time, card.Step, card.More = after(sec), "manual", len(list)-1-i
			return append(events, event.Doing{Time: after(sec), Step: "manual", Says: event.Queued}, card)
		}
		card := toDoCards[i]
		card.Time, card.Step, card.More = after(sec), "manual", len(list)-1-i
		events = append(events, event.Doing{Time: after(sec), Step: "manual", Says: event.Queued}, card)
		sec += 40 + 13*i
		list[i].Done, list[i].Saw = true, toDoSaw[i]
	}
	events = append(events, event.ToDos{Time: after(sec), Step: "manual", List: list, Up: -1},
		event.Doing{Time: after(sec + 1), Step: "manual", Says: "checking once more"})
	for _, s := range []struct{ name, says string }{{"time-machine", "no backup yet"}, {"arq", "no recent backup"}} {
		events = append(events, event.StepStarted{Time: after(sec + 1), Step: s.name, Doing: "checking"},
			finished(sec+2, s.name, check.Result{State: check.OK, Summary: s.says}))
	}
	return events
}

// stoppedAtTheToDos is toDosUpTo the Time Machine disk, then the person
// stopping there: the list over with the last two not done, the steps by
// hand at the end done with, the run finished.
func stoppedAtTheToDos() []event.Event {
	events := toDosUpTo(4)
	list := slices.Clone(toDoList)
	for i := range 4 {
		list[i].Done, list[i].Saw = true, toDoSaw[i]
	}
	return append(events,
		event.ToDos{Time: after(2100), Step: "manual", List: list, Up: -1},
		finished(2101, "manual", check.Result{State: check.OK, Summary: "all 3 done"}),
		event.RunFinished{Time: after(2102), Duration: 35*time.Minute + 2*time.Second, Counts: map[check.State]int{check.OK: 27, check.Attention: 2}})
}

// since is how long the run's taken by a few seconds after the last of
// events, as a face shows it then.
func since(events []event.Event) time.Duration {
	return events[len(events)-1].At().Sub(at) + 12*time.Second
}

// The to-dos at the run's end have applying's screen, as round 12 has it,
// at the size it drew and at the least the screen takes: opened with the
// choice first; kit watching for one of its own; one only the person can
// say is done; all done, kit carrying on.
func TestTheToDosHaveTheScreen(t *testing.T) {
	for _, tt := range []struct {
		name string
		up   int
	}{{"choice", 0}, {"watched", 4}, {"space", 3}, {"carrying-on", len(toDoList)}} {
		for _, size := range [][2]int{{160, 64}, {100, 30}} {
			name := fmt.Sprintf("apply-todo-%s-%dx%d.golden", tt.name, size[0], size[1])
			t.Run(name, func(t *testing.T) {
				events := toDosUpTo(tt.up)
				face, out, _ := applyAt(size[0], size[1], since(events), events)
				defer func() { _ = face.Close() }()
				got := screen(out.String())
				golden(t, name, got+"\n")
				ls := lines(got)
				if len(ls) != size[1] {
					t.Errorf("the screen is %d lines, want %d", len(ls), size[1])
				}
				for i, l := range ls {
					if w := ansi.StringWidth(l); w > size[0] {
						t.Errorf("line %d is %d wide, wider than the window, %d", i, w, size[0])
					}
				}
			})
		}
	}
}

// Stopped at the to-dos, the screen goes as the run ends, and the record's
// To do lists what's left in kit's order, each by its title, its steps as
// how; the run wasn't stopped, so the record's head says how it ended.
func TestTheRecordAfterStoppingAtTheToDos(t *testing.T) {
	for _, size := range [][2]int{{160, 64}, {100, 30}} {
		name := fmt.Sprintf("apply-todo-record-%dx%d.golden", size[0], size[1])
		t.Run(name, func(t *testing.T) {
			face, out, _ := applyAt(size[0], size[1], time.Minute, stoppedAtTheToDos())
			if err := face.Close(); err != nil {
				t.Fatal(err)
			}
			got := screen(out.String())
			golden(t, name, got+"\n")
			for _, want := range []string{
				"│  2 to do · ",
				"\n  TO DO  2 · kit apply brings them back\n" +
					"  △ 1  Add a Time Machine disk\n" +
					"       → Plug in your backup disk\n" +
					"       → System Settings › General › Time Machine › Add Backup Disk, and choose\n" +
					"         it\n" +
					"  △ 2  Set up Arq's backup plan\n" +
					"       → Open Arq\n",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the record is\n%s\nwant it to hold\n%s", got, want)
				}
			}
			if strings.Contains(got, "stopped") || strings.Contains(got, "DIDN'T RUN") {
				t.Errorf("the record is\n%s\nwant it to end as the run did, not stopped", got)
			}
		})
	}
}

// The left pane lists the to-dos, To do and how many, then each group
// where its first comes, its name muted after a blank line; done ones the
// done mark and muted, the one up a pill, the rest white; q at its foot.
// The state line counts what's left, then what's done once some are, and
// once they're over, that kit's carrying on. The log says each done, how
// long it took and what kit saw, and nothing of each coming up.
func TestTheToDosList(t *testing.T) {
	face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, toDosUpTo(3))
	defer func() { _ = face.Close() }()
	got := screen(uncoloured(out.String()))
	left, _ := panes(got, 160)
	want := []string{
		"", "  TO DO  6",
		"", "  Apps", "  ● Bring back apps' settings", "  ● Open Karabiner-Elements", "  ● Open Hammerspoon",
		"", "  System Settings", "  △ Allow Full Disk Access for remote users",
		"", "  Backups", "  △ Add a Time Machine disk", "  △ Set up Arq's backup plan",
	}
	if !slices.Equal(left[:len(want)], want) {
		t.Errorf("the left pane starts\n%s\nwant\n%s", strings.Join(left[:len(want)], "\n"), strings.Join(want, "\n"))
	}
	if foot := leftFoot(got, 160, 1); foot[0] != "  q stop here" {
		t.Errorf("the left pane's foot is %q, want q to stop there", foot)
	}
	if state := lines(got)[3]; !strings.HasSuffix(state, "│  △ 3 to do · ● 3 done") {
		t.Errorf("the state line is %q", state)
	}
	frame := strings.Join(lastFrame(out.String()), "\n")
	for _, styled := range []string{look.Muted("Apps"), look.Mark(look.Done) + " " + look.Muted("Open Hammerspoon"), look.Pill("Allow Full Disk Access for remote users", look.Done), look.White("Set up Arq's backup plan"), look.Pink("● 3 done")} {
		if !strings.Contains(frame, styled) {
			t.Errorf("the screen has no %q, styled as it should be", ansi.Strip(styled))
		}
	}
	log := strings.Join(logOf(got, 160), "\n")
	for _, want := range []string{
		"  03:34:45  ● Bring back apps' settings: done, in 40.0s · 214 apps' settings, from 15 Oct\n",
		"  03:35:38  ● Open Karabiner-Elements: done, in 53.0s\n",
		"  03:36:44  ● Open Hammerspoon: done, in 1m06s\n",
	} {
		if !strings.Contains(log+"\n", want) {
			t.Errorf("the log is\n%s\nwant it to hold %q", log, want)
		}
	}
	if strings.Contains(log, "waiting for you") {
		t.Errorf("the log is\n%s\nwant nothing said as each came up", log)
	}
	events := toDosUpTo(len(toDoList))
	done, out2, _ := applyAt(160, 64, since(events), events)
	defer func() { _ = done.Close() }()
	over := screen(out2.String())
	if state := lines(over)[3]; !strings.HasSuffix(state, "│  ◐ carrying on") {
		t.Errorf("all done, the state line is %q", state)
	}
	if log := logOf(over, 160); len(log) < 3 || log[len(log)-1] != "  03:41:34  ◐ Checking once more" || log[len(log)-3] != "  03:41:22  ● Time Machine: no backup yet" {
		t.Errorf("all done, the log is\n%s\nwant the steps they held checked again, kit checking at its foot", strings.Join(log, "\n"))
	}
}

// The to-do up is a panel over the log, ruled off: its title bold, where
// it is in the list at the right; why, muted and wrapped; its steps
// numbered, a subheading muted and unnumbered; then how it ends: a
// choice's keys, a line each; kit watching, and what for, or, for a line
// whose test tells, that it moves on by itself; else space.
func TestTheToDoPanel(t *testing.T) {
	for _, tt := range []struct {
		up   int
		want []string
	}{
		{0, []string{
			"",
			"  △ Bring back apps' settings" + strings.Repeat(" ", 62) + "1 of 6",
			"",
			"      First, so the apps you open next start with yours. This Mac saves its own once they're back.",
			"      Claude Code's come too, sending its temporary files to Scratch.",
			"",
			"      r brings them back from this Mac's last save, 15 Oct",
			"      f starts afresh: nothing back, saving from now",
			"",
			"───────────────────────────────────────────────────────────────────────────────────────────────────",
			"",
			"  LOG",
		}},
		{1, []string{
			"",
			"  △ Open Karabiner-Elements" + strings.Repeat(" ", 64) + "2 of 6",
			"",
			"      Its key remapping needs its driver and Input Monitoring, which only you can allow. Opened, it",
			"      asks for both.",
			"",
			"      1  Open Karabiner-Elements",
			"      2  Allow what it asks for",
			"",
			"      If it doesn't ask, in System Settings:",
			"      3  Privacy & Security: allow its driver (the Allow button, near the bottom)",
			"      4  Privacy & Security › Input Monitoring: turn on Karabiner-Elements",
			"",
			"      ◐ kit's watching: it moves on by itself once it's done",
			"",
			"───────────────────────────────────────────────────────────────────────────────────────────────────",
		}},
		{3, []string{"", "  △ Allow Full Disk Access for remote users" + strings.Repeat(" ", 48) + "4 of 6", "", "      So an SSH session from another Mac can reach everything, which Remote Login alone can't.", "",
			"      1  System Settings › General › Sharing › Remote Login › ⓘ", "      2  Turn on Allow full disk access for remote users", "", "      space when it's done", ""}},
		{4, []string{"", "  △ Add a Time Machine disk" + strings.Repeat(" ", 64) + "5 of 6", "", "      Time Machine has nowhere to back up to.", "",
			"      1  Plug in your backup disk", "      2  System Settings › General › Time Machine › Add Backup Disk, and choose it", "", "      ◐ kit's watching: it moves on once Time Machine has a disk", ""}},
	} {
		face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, toDosUpTo(tt.up))
		got := screen(uncoloured(out.String()))
		_, right := panes(got, 160)
		if !slices.Equal(right[:len(tt.want)], tt.want) {
			t.Errorf("with %s up, the right pane starts\n%s\nwant\n%s", toDoList[tt.up].Title, strings.Join(right[:len(tt.want)], "\n"), strings.Join(tt.want, "\n"))
		}
		frame := strings.Join(lastFrame(out.String()), "\n")
		for _, styled := range []string{look.Mark(look.ToDo) + " " + look.Strong(toDoList[tt.up].Title), look.Muted(fmt.Sprintf("%d of 6", tt.up+1))} {
			if !strings.Contains(frame, styled) {
				t.Errorf("with %s up, the screen has no %q, styled as it should be", toDoList[tt.up].Title, ansi.Strip(styled))
			}
		}
		_ = face.Close()
	}
	face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, toDosUpTo(0))
	defer func() { _ = face.Close() }()
	frame := strings.Join(lastFrame(out.String()), "\n")
	if !strings.Contains(frame, look.Urgent("r")+" "+look.White("brings them back from this Mac's last save, 15 Oct")) {
		t.Error("a choice's keys aren't styled as a card's keys are")
	}
}

// In a window too small for the screen, the to-do up is a card in place,
// as a step by hand's is, by its title: no list; a choice, that a bigger
// window shows its keys.
func TestTheToDosInASmallWindow(t *testing.T) {
	events := toDosUpTo(0)
	small, out, _ := applyAt(99, 40, since(events), events)
	got := turned(screen(out.String()))
	if err := small.Close(); err != nil {
		t.Fatal(err)
	}
	golden(t, "apply-todo-small.golden", got+"\n")
	if !strings.Contains(got, "Bring back apps' settings") || !strings.Contains(got, "a bigger window shows the keys to answer it") || strings.Contains(got, "TO DO") {
		t.Errorf("in a small window, the to-dos show\n%s\nwant the one up as a card, no list", got)
	}
}

// With a to-do up, a choice's keys answer it, and space doesn't; space
// says one kit can't tell about is done, and nothing of one it tells
// about; tab chooses no row, as the to-dos have the left pane; Stop stops
// there, the run carrying on, but stops the run once they're over.
func TestTheToDosKeys(t *testing.T) {
	for _, tt := range []struct {
		name  string
		up    int
		keys  []ask.Key
		want  []answered
		stops int
	}{
		{"a choice", 0, []ask.Key{keySpace, keyEnter, keyO, {Text: "r"}, {Text: "f"}}, []answered{{"manual", "r"}, {"manual", "f"}}, 0},
		{"kit tells", 4, []ask.Key{keySpace, keyEnter, {Text: "r"}}, nil, 0},
		{"kit can't tell", 3, []ask.Key{keySpace, keyEnter}, []answered{{"manual", hand.Done}, {"manual", hand.Done}}, 0},
		{"stop here", 3, []ask.Key{keyQ, keyDown, keyEnter}, []answered{{"manual", hand.Stop}}, 0},
		{"stop once they're over", len(toDoList), []ask.Key{keyQ, keyDown, keyEnter}, nil, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			face, out, kb := applyAt(160, 64, time.Minute, toDosUpTo(tt.up))
			defer func() { _ = face.Close() }()
			var mu sync.Mutex
			var got []answered
			face.Hands(func(step string, a hand.Answer) bool {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, answered{step, a})
				return true
			})
			kb.press(t, keyTab)
			if _, right := panes(screen(out.String()), 160); !slices.Contains(right, "  LOG") {
				t.Errorf("tab chose a row: the right pane is\n%s", strings.Join(right, "\n"))
			}
			kb.press(t, tt.keys...)
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(got, tt.want) || kb.stops() != tt.stops {
				t.Errorf("answered %v, stopped the run %d times; want %v, %d", got, kb.stops(), tt.want, tt.stops)
			}
		})
	}
}
