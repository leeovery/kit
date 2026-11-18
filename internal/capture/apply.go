package capture

import (
	"fmt"
	"slices"
	"time"

	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/backup/tool"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
)

// beat is what happens in a run a pause after the beat before.
type beat struct {
	pause  time.Duration
	events []event.Event
}

// play emits beats' events on face, each beat after its pause.
func (s *stage) play(face *render.Pretty, beats []beat) {
	for _, b := range beats {
		s.pause(b.pause)
		for _, e := range b.events {
			face.Emit(e)
		}
	}
}

// fine is step finished well, saying summary.
func fine(step, summary string) event.Event {
	return event.StepFinished{Step: step, Result: check.Result{State: check.OK, Summary: summary}}
}

// toDo is step finished with t left for the person to do.
func toDo(t event.ToDo, summary string) event.Event {
	return event.StepFinished{Step: t.Step, Result: check.Result{State: check.Attention, Summary: summary, Items: []check.Item{
		{ID: t.ID, Name: t.Title, State: check.Problem, ToDo: &check.ToDo{Title: t.Title, Group: t.Group}},
	}}}
}

// work is step checked, then applying.
func work(step string) []event.Event {
	return []event.Event{event.StepStarted{Step: step, Doing: "checking"}, event.StepStarted{Step: step, Doing: "applying"}}
}

// What a new Mac's Homebrew installs, in the order the scenario has it.
var (
	casks = []string{
		"1password", "1password-cli", "raycast", "slack", "arc", "obsidian", "figma", "visual-studio-code",
		"orbstack", "spotify", "zoom", "notion", "tableplus", "cleanshot",
	}
	formulae = []string{
		"git", "gh", "jq", "ripgrep", "fzf", "ffmpeg", "bat", "eza", "fd", "zoxide", "starship", "tmux",
		"neovim", "lazygit", "go", "node", "python@3.13", "wget",
	}
)

// Of all Homebrew installs on the new Mac, as its rows count them.
const (
	allCasks    = 42
	allFormulae = 129
)

// installing is a Homebrew step having installed names[i], and at work on
// the next, done of of all it installs.
func installing(step string, names []string, i, done, of int) []event.Event {
	events := []event.Event{event.Logged{Step: step, Line: "Installed " + names[i]}}
	if i+1 < len(names) {
		events = append(events, event.Doing{Step: step, Says: "installing " + names[i+1], Done: done, Of: of})
	}
	return events
}

// toTheCard is a new Mac's run as kit apply has it, its first seconds: the
// boot's steps done, what's quick done, Homebrew installing formulae and
// casks, and 1Password's sign-in coming up once its casks are in.
func toTheCard() []beat {
	beats := []beat{
		{300 * time.Millisecond, []event.Event{
			fine("boot-github", githubSaid), fine("boot-mac", newMac),
			fine("boot-homebrew", homebrewSaid), fine("boot-kit-config", kitConfigSaid),
		}},
		{400 * time.Millisecond, []event.Event{
			fine("homebrew", "/opt/homebrew"), fine("file", "54 linked"), fine("path", "15 directories"),
			fine("disk", "93% free"), fine("load", "load 2.4 on 14 cores"),
		}},
		{300 * time.Millisecond, slices.Concat(work("cask"), work("brew"), work("claude-code"))},
	}
	for i := range 3 {
		beats = append(beats,
			beat{280 * time.Millisecond, installing("cask", casks, i, i+1, allCasks)},
			beat{280 * time.Millisecond, installing("brew", formulae, i, i+1, allFormulae)},
		)
		switch i {
		case 0:
			beats = append(beats, beat{0, []event.Event{fine("touch-id-sudo", "turned on"), fine("fonts", "16 installed")}})
		case 1:
			beats = append(beats, beat{300 * time.Millisecond, []event.Event{
				fine("defaults", "99 set"), fine("power", "6 set"), toDo(timeMachineDisk, "no backup disk"),
			}})
		}
	}
	return append(beats, beat{200 * time.Millisecond, []event.Event{
		event.StepStarted{Step: "1password", Doing: "applying"}, rendertest.SignIn(time.Time{}),
	}})
}

// toTheEnd is the run from 1Password's card to its end, flying through
// what a new Mac takes half an hour over: to the to-dos, then the to-dos
// worked through.
func toTheEnd() []beat { return slices.Concat(toTheToDos(), workingThrough()) }

// toTheToDos is the run from 1Password's card to the to-dos at its end:
// signed in, the secrets and git set, Homebrew counting through its
// installs, the rest done, and the steps by hand at the end applying.
func toTheToDos() []beat {
	beats := []beat{
		{350 * time.Millisecond, installing("cask", casks, 3, 4, allCasks)},
		{350 * time.Millisecond, installing("brew", formulae, 3, 4, allFormulae)},
		{300 * time.Millisecond, []event.Event{fine("claude-code", "set up · 3 plugins")}},
		{500 * time.Millisecond, []event.Event{fine("1password", "signed in")}},
		{200 * time.Millisecond, work("secret")},
		{400 * time.Millisecond, []event.Event{fine("secret", "12 in place")}},
		{200 * time.Millisecond, slices.Concat(work("git-config"), work("mas"), work("npm"), work("mise"))},
	}
	beats = append(beats, counting(4)...)
	return append(beats,
		beat{150 * time.Millisecond, []event.Event{
			fine("brew", fmt.Sprintf("%d installed", allFormulae)), fine("cask", fmt.Sprintf("%d installed", allCasks)),
			fine("git-config", "8 set"), fine("mas", "6 installed"),
		}},
		beat{250 * time.Millisecond, slices.Concat(work("composer"), work("login-item"), work("arq"), work("prefs"))},
		beat{300 * time.Millisecond, []event.Event{
			fine("npm", "4 installed"), fine("mise", "3 tools"), fine("composer", "2 installed"),
			fine("login-item", "5 added"), fine("arq", "first backup soon"), fine("prefs", "214 apps' settings restored"),
			fine("config", "nothing to commit"), fine("config-sync", "pushed"),
		}},
		beat{300 * time.Millisecond, []event.Event{event.StepStarted{Step: "manual", Doing: "applying"}}},
	)
}

// counting is Homebrew's installs counted through from the from'th, a few
// to a beat, each beat logging one of each.
func counting(from int) []beat {
	var beats []beat
	steps := len(casks) - from
	for i := range steps {
		cask, formula := from+i, from+i*(len(formulae)-from)/steps
		casksDone := from + 1 + (allCasks-from-1)*(i+1)/steps
		formulaeDone := from + 1 + (allFormulae-from-1)*(i+1)/steps
		beats = append(beats, beat{120 * time.Millisecond, slices.Concat(
			installing("cask", casks, cask, min(casksDone, allCasks-1), allCasks),
			installing("brew", formulae, formula, min(formulaeDone, allFormulae-1), allFormulae),
		)})
	}
	return beats
}

// The to-dos at a new Mac's run's end, in kit's order: the steps by hand
// that wait for the end, and the backup disk only the person can add.
var (
	timeMachineDisk = setUp(timemachine.Driver{}.About())
	toDos           = []event.ToDo{
		{Step: "manual", ID: "manual:karabiner", Title: "Allow Karabiner-Elements", Group: "Apps", How: []string{"Open Karabiner-Elements", "Allow what it asks for"}},
		{Step: "manual", ID: "manual:editor", Title: "Install the editor from its site", Group: "Apps", How: []string{"Download it from editor.example.com", "Drag it to Applications"}},
		{Step: "manual", ID: "manual:sharing", Title: "Allow remote users Full Disk Access", Group: "System Settings",
			How: []string{"System Settings › General › Sharing › Remote Login › ⓘ", "Turn on Allow full disk access for remote users"}},
		timeMachineDisk,
	}
	// toDoCards are the to-dos' cards, as each comes up.
	toDoCards = []event.NeedsYou{
		{Item: "manual:karabiner", Title: "Allow Karabiner-Elements", Tells: true,
			Why:   "Its key remapping needs its driver and Input Monitoring, which only you can allow. Opened, it asks for both.",
			Steps: []string{"Open Karabiner-Elements", "Allow what it asks for"}},
		{Item: "manual:editor", Title: "Install the editor from its site",
			Why:   "It isn't in Homebrew or the App Store, so only you can fetch it.",
			Steps: []string{"Download it from editor.example.com", "Drag it to Applications"}},
		{Item: "manual:sharing", Title: "Allow remote users Full Disk Access",
			Why:   "So an SSH session from another Mac can reach everything, which Remote Login alone can't.",
			Steps: []string{"System Settings › General › Sharing › Remote Login › ⓘ", "Turn on Allow full disk access for remote users"}},
		setUpCard(timemachine.Driver{}.About()),
	}
)

// setUp is the to-do a backup tool that isn't set up leaves, in its
// driver's words, as kit's backups step makes it.
func setUp(about tool.About) event.ToDo {
	return event.ToDo{Step: about.ID, ID: about.ID + ":none", Title: about.SetUp.Title, Group: "Backups", How: about.SetUp.Steps}
}

// setUpCard is the card of the to-do a backup tool that isn't set up
// leaves, in its driver's words: kit tells by itself once it's done.
func setUpCard(about tool.About) event.NeedsYou {
	s := about.SetUp
	return event.NeedsYou{Item: about.ID + ":none", Title: s.Title, Tells: true, Watch: s.Watch, Why: s.Why, Steps: s.Steps}
}

// toDoUp is the to-dos as they stand with the up'th's card up.
func toDoUp(list []event.ToDo, up int) []event.Event {
	card := toDoCards[up]
	card.Step, card.More = "manual", len(list)-1-up
	return []event.Event{
		event.ToDos{Step: "manual", List: slices.Clone(list), Up: up},
		event.Doing{Step: "manual", Says: event.Queued}, card,
	}
}

// workingThrough is the to-dos worked through, a card at a time: each done
// but the backup disk, left for later; then kit carrying on, checking the
// backup disk once more, and the steps by hand done.
func workingThrough() []beat {
	list := slices.Clone(toDos)
	beats := []beat{{400 * time.Millisecond, toDoUp(list, 0)}}
	for i := range list[:len(list)-1] {
		list[i].Done = true
		beats = append(beats, beat{700 * time.Millisecond, toDoUp(list, i+1)})
	}
	list[len(list)-1].Skipped = true
	return append(beats,
		beat{700 * time.Millisecond, []event.Event{
			event.ToDos{Step: "manual", List: slices.Clone(list), Up: -1},
			event.Doing{Step: "manual", Says: "checking once more"},
			event.StepStarted{Step: "time-machine", Doing: "checking"},
		}},
		beat{500 * time.Millisecond, []event.Event{toDo(timeMachineDisk, "no backup disk"), fine("manual", "all 3 done")}},
	)
}

// applied is how the new Mac's run ended, began at began: all but the
// backup disk done.
func applied(began, now time.Time) event.RunFinished {
	steps := len(rendertest.NewMac())
	return event.RunFinished{Time: now, Duration: now.Sub(began), Counts: map[check.State]int{check.OK: steps - 1, check.Attention: 1}}
}
