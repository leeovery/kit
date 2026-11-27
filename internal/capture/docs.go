package capture

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
)

// docsAt is when the docs pages' stills are drawn.
var docsAt = time.Date(2026, 11, 20, 16, 30, 0, 0, time.UTC)

// docs are the docs pages' stills, each named for its page and what it
// shows there: what kit prints at a terminal, from the run the page's words
// came from, each a window as small as what it shows.
var docs = []Fixture{
	result("backups-add", "kit backup add arq: declared, kit-config committed and pushed", "backup add arq",
		said{"arq", []string{"declared in laptop"}}, committed("laptop/declarations")),
	sectionOf("backups-status", "kit status's BACKUPS", "BACKUPS", "Time Machine", 37, 6, func() []event.Event {
		return statusAt(docsAt, fineAt(docsAt, "time-machine", "last backup 02:35"), fineAt(docsAt, "arq", "last backup 02:10"))
	}),
	sectionOf("backups-todo", "kit apply's to-dos at its end, Arq's plan among them", "TO DO", "Set up Arq", 81, 12, backupsLeft),
	// The drift and packages pages' both.
	sectionOf("drift-status", "kit status's CONFIG, ffmpeg installed by hand", "CONFIG", "ffmpeg", 64, 10, ffmpegDrifted),
	{Name: "drift-reconcile", About: "kit reconcile's record: ffmpeg declared, kit-config committed and pushed", Width: 58, Height: 10,
		Shows: "1 settled", at: docsAt, draw: onFace(func(at time.Time) []event.Event {
			return reconciled(at, 6100*time.Millisecond, said{"brew:ffmpeg", []string{"already installed", "declared in laptop"}}, committed("laptop/declarations"))
		})},
	result("drift-temp", "kit brew add hello --temp: installed, not declared", "brew add hello --temp",
		said{"hello", []string{"installed, for now: not declared, quiet for 7 days, then kit reconcile asks"}}, said{"kit-config", []string{"nothing changed"}}),
	{Name: "first-config-done", About: "kit setup's end on a Mac in use: the Mac declared as it is, kit apply carrying on", Width: 82, Height: 37,
		Shows: "carries on now", at: docsAt, draw: setupDone},
	result("linked-files-add", "kit file add: the file moved into kit-config and linked back", "file add ~/.config/tool/conf",
		said{"~/.config/tool/conf", []string{"in kit-config, linked: laptop/home/.config/tool/conf"}}, committed("laptop/home/.config/tool/conf")),
	{Name: "linked-files-revert", About: "kit reconcile --revert: kit-config's file linked back, the Mac's in the Bin", Width: 79, Height: 11,
		Shows: "1 settled", at: docsAt, draw: onFace(func(at time.Time) []event.Event {
			return reconciled(at, 400*time.Millisecond,
				said{"file:~/.zshrc", []string{"linked to kit-config's", "the Mac's copy is in the Bin (~/.Trash/.zshrc)"}}, said{"kit-config", []string{"nothing changed"}})
		})},
	{Name: "new-mac-boot", About: "the boot's end in Terminal, Ghostty opening", Width: 63, Height: 34, Look: Terminal,
		Shows: "choose Open", at: docsAt, draw: bootEnds},
	{Name: "new-mac-record", About: "kit apply's record on a new Mac, two to-dos left", Width: 81, Height: 31,
		Shows: "brings them back", at: docsAt, draw: onFace(func(time.Time) []event.Event { return backupsLeft() })},
	result("packages-add", "kit brew add jq: installed, declared, kit-config committed and pushed", "brew add jq",
		said{"jq", []string{"installed", "declared in laptop"}}, committed("laptop/declarations")),
	result("running-by-itself-check", "kit check add db: declared, failing now", "check add db",
		said{"db", []string{"declared in laptop", "it fails now: 127.0.0.1:5432 - no response"}}, committed("laptop/declarations")),
	{Name: "secrets-places", About: "kit secret places: the secrets blocks this Mac reads", Width: 88, Height: 5,
		Shows: "references in full", at: docsAt, draw: secretPlaces},
	sectionOf("settings-status", "kit status's CONFIG, a setting removed by hand", "CONFIG", "autohide", 79, 10, func() []event.Event {
		return statusAt(docsAt, waiting(docsAt, "defaults", "99 declared, all set, 1 removed", 99,
			check.Item{ID: "defaults:com.apple.dock:autohide", Name: "com.apple.dock:autohide", State: kind.Removed, Since: docsAt.Add(-2 * 24 * time.Hour)}))
	}),
	result("steps-add", "kit step add: declared, its script a template", "step add tool",
		said{"tool", []string{"declared in laptop", "its script, laptop/steps/tool/run, is a template: write its check and apply"}},
		committed("laptop/declarations, laptop/steps/tool/run")),
	sectionOf("steps-status", "kit status's STEPS, each step with what its check prints", "STEPS", "16 installed", 34, 5, func() []event.Event {
		return statusAt(docsAt, fineAt(docsAt, "fonts", "16 installed"))
	}),
	sectionOf("troubleshooting-private", "kit status's CONFIG, kit-config public", "CONFIG", "must be private", 82, 13, func() []event.Event {
		return statusAt(docsAt, event.StepFinished{Time: docsAt, Step: "config-private", Result: check.Result{
			State: check.Attention, Summary: "public on GitHub (owner/kit-config): it must be private",
			Items: []check.Item{{ID: "config-private:owner/kit-config", Name: "owner/kit-config", State: "not-private",
				Detail: "make it private: gh repo edit owner/kit-config --visibility private --accept-visibility-change-consequences"}},
		}})
	}),
	sectionOf("troubleshooting-differs", "kit apply's record, a linked file waiting", "DONE", "a file of its own", 69, 18, fileDiffers),
	result("your-data-add", "kit restore add ~/Code: declared, nothing copied", "restore add ~/Code",
		said{"~/Code", []string{"declared in laptop", "nothing copied"}}, committed("laptop/declarations")),
}

// said is a step of a command's run and what it said.
type said struct {
	step string
	says []string
}

// committed is kit-config's step, having committed and pushed what.
func committed(what string) said { return said{"kit-config", []string{"committed and pushed " + what}} }

// result is the still name of what kit command prints at a terminal once
// done, a row a step, kit-config's last: as wide as its longest; or, with a
// row too long for a line, as wide as kit draws, that row stacked, as it
// wraps under its name.
func result(name, about, command string, steps ...said) Fixture {
	width, height := 0, 2
	for _, s := range steps {
		says := strings.Join(s.says, " · ")
		if ansi.StringWidth("  ● "+s.step+"  "+says) <= look.Width {
			width, height = max(width, ansi.StringWidth(s.step+strings.Join(s.says, "; "))+12), height+1
			continue
		}
		width, height = look.Width+2, height+1+len(look.Wrap(says, look.Width-4))
	}
	return Fixture{Name: name, About: about, Width: min(width, look.Width+2), Height: height, Shows: "kit-config",
		at: docsAt, draw: onFace(func(at time.Time) []event.Event { return changeRun(at, command, steps) })}
}

// changeRun is a command's run that changes things a name at a time, as
// kit runs one: a step for each name, then kit-config's, committing and
// pushing.
func changeRun(at time.Time, command string, steps []said) []event.Event {
	infos := make([]event.Step, len(steps))
	events := []event.Event{event.RunStarted{Time: at, Command: command, Machine: "laptop", Steps: infos}}
	for i, s := range steps {
		infos[i] = event.Step{Name: s.step, Title: s.step}
		events = append(events, event.StepFinished{Time: at, Step: s.step, Result: check.Result{State: check.OK}.Saying(s.says...)})
	}
	return append(events, event.RunFinished{Time: at, Counts: map[check.State]int{check.OK: len(steps)}})
}

// reconciled is kit reconcile's run, having done what each step says, in
// took: each a thing, by its id, called by its name.
func reconciled(at time.Time, took time.Duration, steps ...said) []event.Event {
	infos := make([]event.Step, len(steps))
	events := []event.Event{event.RunStarted{Time: at, Command: "reconcile", Machine: "laptop", Steps: infos}}
	for i, s := range steps {
		_, name, _ := strings.Cut(s.step, ":")
		infos[i] = event.Step{Name: s.step, Title: name}
		if name == "" {
			infos[i].Title = s.step
		}
		events = append(events, event.StepFinished{Time: at, Step: s.step, Result: check.Result{State: check.OK}.Saying(s.says...)})
	}
	return append(events, event.RunFinished{Time: at, Duration: took, Counts: map[check.State]int{check.OK: len(steps)}})
}

// onFace draws a run's events on kit's face as it prints them, as wide as
// the window, then holds.
func onFace(run func(at time.Time) []event.Event) func(*stage) error {
	return func(s *stage) error {
		if err := draw(s.term.Out, s.term.Size, run(s.now())); err != nil {
			return err
		}
		s.hold()
		return nil
	}
}

// draw prints events on kit's face at out, a terminal as wide as size says.
func draw(out io.Writer, size func() (int, int), events []event.Event) error {
	width, _ := size()
	face := render.NewPretty(out, width, false)
	for _, e := range events {
		face.Emit(e)
	}
	return face.Close()
}

// holdLines writes lines, after a blank one, then holds.
func (s *stage) holdLines(lines []string) error {
	if _, err := io.WriteString(s.term.Out, "\r\n"+strings.Join(lines, "\r\n")+"\r\n"); err != nil {
		return err
	}
	s.hold()
	return nil
}

// sectionOf is the still name of one section of what kit prints for run,
// at kit's widest, the one headed title, alone, showing shows, in a window
// columns by rows.
func sectionOf(name, about, title, shows string, columns, rows int, run func() []event.Event) Fixture {
	return Fixture{Name: name, About: about, Width: columns, Height: rows, Shows: shows, at: docsAt, draw: func(s *stage) error {
		var all bytes.Buffer
		if err := draw(&all, func() (int, int) { return look.Width, rows }, run()); err != nil {
			return err
		}
		return s.holdLines(section(all.String(), title))
	}}
}

// section is the lines of printed, in its colours, from the one headed
// title to the blank line ending it.
func section(printed, title string) []string {
	lines := strings.Split(strings.ReplaceAll(printed, "\r\n", "\n"), "\n")
	plain := func(l string) string { return strings.TrimRight(ansi.Strip(l), " ") }
	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(plain(l), "  "+strings.ToUpper(title)) })
	if start < 0 {
		return nil
	}
	end := slices.IndexFunc(lines[start:], func(l string) bool { return plain(l) == "" })
	if end < 0 {
		return lines[start:]
	}
	return lines[start : start+end]
}

// fineAt is step's result at at, fine, saying summary.
func fineAt(at time.Time, step, summary string) event.StepFinished {
	return event.StepFinished{Time: at, Step: step, Result: check.Result{State: check.OK, Summary: summary}}
}

// ffmpegDrifted is kit status on a Mac where ffmpeg was installed by hand
// two days ago.
func ffmpegDrifted() []event.Event {
	return statusAt(docsAt, waiting(docsAt, "brew", "64 declared, all installed, 1 not declared", 64,
		check.Item{ID: "brew:ffmpeg", Name: "ffmpeg", State: kind.Extra, Since: docsAt.Add(-2 * 24 * time.Hour)}))
}

// backupsLeft is a new Mac's run to its end, the backup disk and Arq's plan
// left for later.
func backupsLeft() []event.Event {
	tm, plan := setUp(timemachine.Driver{}.About()), setUp(arq.Driver{}.About())
	steps := rendertest.NewMac()
	events := []event.Event{event.RunStarted{Time: docsAt, Command: "bootstrap", Machine: "laptop", Steps: steps}}
	for _, st := range steps {
		switch st.Name {
		case "time-machine":
			events = append(events, toDo(tm, "not set up"))
		case "arq":
			events = append(events, toDo(plan, "not set up"))
		default:
			events = append(events, fine(st.Name, "fine"))
		}
	}
	return append(events,
		event.ToDos{Time: docsAt, Step: "manual", List: []event.ToDo{tm, plan}, Up: -1},
		event.RunFinished{Time: docsAt, Duration: 31*time.Minute + 7*time.Second, Counts: map[check.State]int{check.OK: len(steps) - 2, check.Attention: 2}})
}

// fileDiffers is kit apply's run on a Mac in use, a file where a linked
// one belongs, different from kit-config's.
func fileDiffers() []event.Event {
	steps := rendertest.NewMac()
	events := []event.Event{event.RunStarted{Time: docsAt, Command: "apply", Machine: "laptop", Steps: steps}}
	for _, st := range steps {
		res := check.Result{State: check.OK, Summary: "fine"}
		if st.Name == "file" {
			res.Items = []check.Item{{ID: "file:~/.zshrc", Name: "~/.zshrc", State: "diverged", Quiet: "new", Detail: "a file of its own, different from shared/home/.zshrc"}}
		}
		events = append(events, event.StepFinished{Time: docsAt, Step: st.Name, Result: res})
	}
	return append(events, event.RunFinished{Time: docsAt, Duration: time.Minute, Counts: map[check.State]int{check.OK: len(steps)}})
}

// bootEnds is the boot in Terminal at its end: signed in, the boot order
// done but for Ghostty, opening, and what macOS asks as it does.
func bootEnds(s *stage) error {
	face := render.NewBootFace(s.term, true, s.now, nil)
	if err := face.PlaySelfTest(s.ctx, selfTest(s.now())); err != nil {
		return err
	}
	face.Start(rendertest.BootOrder())
	for _, step := range []struct {
		name, summary string
		took          time.Duration
	}{
		{"github", githubSaid, 3 * time.Second},
		{"mac", "laptop", 0},
		{"password", "Touch ID on for sudo", 12 * time.Second},
		{"homebrew", homebrewSaid, 6*time.Minute + 2*time.Second},
		{"apps", appsSaid, 98 * time.Second},
		{"kit-config", kitConfigSaid, 6 * time.Second},
		{"kit", kitSaid, 21 * time.Second},
		{"full-disk-access", "granted to Ghostty", 40 * time.Second},
	} {
		face.Emit(event.StepStarted{Time: s.now(), Step: step.name})
		face.Emit(event.StepFinished{Time: s.now(), Step: step.name, Result: check.Result{State: check.OK, Summary: step.summary}, Duration: step.took})
	}
	face.Emit(event.StepStarted{Time: s.now(), Step: "terminal"})
	face.Emit(event.Doing{Time: s.now(), Step: "terminal", Says: "opening", Todo: "macOS asks before Ghostty first opens: choose Open"})
	s.holdShown()
	return face.Close()
}

// secretPlaces is kit secret places on a Mac reading three blocks: one
// named, shared, one of references in full, and one of its own.
func secretPlaces(s *stage) error {
	width, _ := s.term.Size()
	return s.holdLines(render.Places(width, []render.Place{
		{Name: "tokens", Store: "1Password", Item: "op://vault/Item", Scope: config.Shared, Secrets: 2, Sections: []string{"Other", "Section"}},
		{Scope: "laptop", Secrets: 1},
		{Store: "1Password", Item: "op://vault/Other", Scope: "laptop", Secrets: 1, Sections: []string{"Section"}},
	}))
}

// setupDone is kit setup's screen as it ends on a Mac in use, kit-config
// made anew, kit apply carrying on: what it leaves on screen.
func setupDone(s *stage) error {
	face := render.NewSetupFace(s.term, true, s.now, nil, func() string { return "laptop" })
	face.Start()
	for _, e := range rendertest.SetupDone(s.now()) {
		face.Emit(e)
	}
	face.Finish("kit apply", "carries on now")
	if err := face.Close(); err != nil {
		return err
	}
	s.holdShown()
	return nil
}
