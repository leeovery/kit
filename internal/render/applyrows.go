package render

import (
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/steps"
)

// Applying's rows (kit-look, round 10): what a person recognises in place
// of kit's steps, each one of kit's kinds or its own areas, never an app.
const (
	rowThisMac  = "This Mac"
	rowFiles    = "Files and shell"
	rowSettings = "Mac settings"
	rowSecrets  = "Secrets"
	rowFormulae = "Homebrew formulae"
	rowCasks    = "Homebrew casks"
	rowAppStore = "App Store apps"
	rowTools    = "Developer tools"
	rowGit      = "Git"
	rowLogin    = "Login items"
	rowYours    = "Your steps"
	rowBackups  = "Backups"
)

// applyRows are the rows, in the order they show.
var applyRows = []string{rowThisMac, rowFiles, rowSettings, rowSecrets, rowFormulae, rowCasks,
	rowAppStore, rowTools, rowGit, rowLogin, rowYours, rowBackups}

// Rows are applying's rows, in the order they show: the screen's design,
// one of which each kind sits in (kind.Descriptor.Row).
func Rows() []string { return slices.Clone(applyRows) }

// rowSteps are kit's own steps that don't say their row, by name, in each
// row: a kind's step, and the steps it needs, say theirs (event.Step.Row).
var rowSteps = map[string][]string{
	rowThisMac: {"boot-github", "boot-mac", "boot-password", "boot-homebrew", "boot-apps", "boot-kit-config",
		"boot-full-disk-access", "boot-terminal", "disk", "memory", "file-events", "load", "full-disk-access"},
	rowFiles:    {"file", "path"},
	rowSettings: {"touch-id-sudo", "remote-login", "file-sharing"},
	rowGit:      {"config", "config-sync", "config-private"},
	rowBackups:  {"prefs", "nightly"},
}

// rowNamed is the row of each of kit's own steps, by its name.
var rowNamed = func() map[string]string {
	named := map[string]string{}
	for row, names := range rowSteps {
		for _, n := range names {
			named[n] = row
		}
	}
	return named
}()

// RowOf is the row of applying's screen the step s is in, and whether it
// has one of its own: a step of the user's own (a [steps] line, or one of
// their checks) is in Your steps whatever it's called, as one can share a
// name with one of kit's; one of kit's in the row it says, as a kind's
// does, or its own (rowSteps);
// a step by hand in none, as its lines go to the row of the step it holds
// up. Any other falls back on its area, known false: a new step of kit's
// is to have a row of its own.
func RowOf(s event.Step) (row string, known bool) {
	switch s.Area {
	case steps.AreaSteps, steps.AreaChecks:
		return rowYours, true
	case steps.AreaManual:
		return "", true
	}
	if s.Row != "" {
		return s.Row, true
	}
	if row, ok := rowNamed[s.Name]; ok {
		return row, true
	}
	switch s.Area {
	case steps.AreaBackups, "Jobs":
		return rowBackups, false
	case steps.AreaConfig:
		return rowGit, false
	}
	return rowThisMac, false
}

// applyRow is one of applying's rows: its name, and its steps, in the run's
// order.
type applyRow struct {
	name  string
	steps []event.Step
}

// runSteps are the run's steps, or, while it's getting ready, those it will
// run.
func (p *Pretty) runSteps() []event.Step {
	if p.start.Steps != nil {
		return p.start.Steps
	}
	return p.planned
}

// shownRows are the rows the run has a step in, in order, each with its
// steps.
func (p *Pretty) shownRows() []applyRow {
	in := map[string][]event.Step{}
	for _, s := range p.runSteps() {
		if row, _ := RowOf(s); row != "" {
			in[row] = append(in[row], s)
		}
	}
	var rows []applyRow
	for _, name := range applyRows {
		if len(in[name]) > 0 {
			rows = append(rows, applyRow{name: name, steps: in[name]})
		}
	}
	return rows
}

// rowOf is the row the step called name is in: a step by hand's is the row
// of the first step it holds up, one waiting for it, if any does.
func (p *Pretty) rowOf(name string) string {
	return p.heldRow(name, map[string]bool{})
}

// heldRow is rowOf, seen the steps by hand already followed, as one can
// hold up another.
func (p *Pretty) heldRow(name string, seen map[string]bool) string {
	s := p.step(name)
	if row, _ := RowOf(s); row != "" || s.Area != steps.AreaManual || seen[name] {
		return row
	}
	seen[name] = true
	for _, t := range p.runSteps() {
		if !slices.Contains(t.Waits, name) {
			continue
		}
		if row := p.heldRow(t.Name, seen); row != "" {
			return row
		}
	}
	return ""
}

// rowLook is a row as it stands, its steps taken together: a step failed,
// what went wrong, or how many did; a card up for a step by hand holding
// one of them up, or one of them needing you, orange; one at work, what
// it's doing; some waiting, what for; all finished, how many things
// they've left to do at the end, or that they didn't run, or, all well,
// what's done.
func (p *Pretty) rowLook(r applyRow) look.Row {
	row := look.Row{Name: r.name}
	var failed, unwell, skipped []event.Step
	finished, toDo := 0, 0
	for _, s := range r.steps {
		f, ok := p.results[s.Name]
		if !ok {
			continue
		}
		finished++
		switch p.ended(f) {
		case look.Failed:
			failed = append(failed, s)
		case look.NeedsYou:
			unwell = append(unwell, s)
		case look.ToDo:
			toDo += len(f.Result.Items)
		case look.Skipped:
			skipped = append(skipped, s)
		}
	}
	work, atWork := p.rowAtWork(r)
	waits, waitsYou := p.rowWaitsOnYou(r)
	switch {
	case len(failed) == 1:
		head, _ := failure(p.results[failed[0].Name].Result)
		row.State, row.Says = look.Failed, look.Red(head)
	case len(failed) > 1:
		row.State, row.Says = look.Failed, look.Red(fmt.Sprintf("%d failed", len(failed)))
	case waitsYou:
		row.State, row.Says = look.NeedsYou, look.Orange(waits)
	case len(unwell) > 0:
		row.State, row.Says = look.NeedsYou, p.listRow(unwell[0]).Says
	case atWork:
		row.State, row.Says = look.Running, work.doingSays(len(r.steps) > 1, false)
	case finished < len(r.steps):
		row.State, row.Says = look.Queued, look.Muted(p.rowWaiting(r, finished))
	case toDo > 0:
		row.State, row.Says = look.ToDo, look.White(fmt.Sprintf("%d to do at the end", toDo))
	case len(skipped) > 0:
		row.State, row.Says = look.Skipped, p.listRow(skipped[0]).Says
	default:
		row.State, row.Says = look.Done, p.doneSays(r)
	}
	return row
}

// rowAtWork is the first of r's steps at work, by when it started, if one
// is.
func (p *Pretty) rowAtWork(r applyRow) (runningStep, bool) {
	for _, run := range p.running {
		if p.atWork(run) && slices.ContainsFunc(r.steps, func(s event.Step) bool { return s.Name == run.step }) {
			return run, true
		}
	}
	return runningStep{}, false
}

// rowWaitsOnYou is what r waits on you for, if it does: the card up, for a
// step by hand holding one of its steps up; or one of its steps saying what
// you're to do.
func (p *Pretty) rowWaitsOnYou(r applyRow) (string, bool) {
	if p.card != nil && p.rowOf(p.card.Step) == r.name {
		return "waits for you: " + p.card.Title, true
	}
	for _, run := range p.running {
		if run.todo != "" && p.rowOf(run.step) == r.name {
			return run.doing, true
		}
	}
	return "", false
}

// rowWaiting is what r says while none of its steps is at work and not all
// have finished, finished of them: what the first waiting for something
// waits for, as waiting says it; how many are done first, and which waits,
// when some are, unless it's named as the row is.
func (p *Pretty) rowWaiting(r applyRow, finished int) string {
	var first event.Step
	words := ""
	for _, s := range r.steps {
		if _, done := p.results[s.Name]; done {
			continue
		}
		w := p.waitWords(s)
		if words == "" || words == "waiting" && w != "waiting" {
			first, words = s, w
		}
	}
	if finished == 0 || len(r.steps) == 1 {
		return words
	}
	if title := p.title(first.Name); title != r.name {
		words = title + " " + words
	}
	return fmt.Sprintf("%d of %d · %s", finished, len(r.steps), words)
}

// waitWords are what a step not finished and not at work waits for: what
// its doing says, once it's started; else what waiting says. "waiting for
// X" says it waits for X.
func (p *Pretty) waitWords(s event.Step) string {
	w := p.waiting(s)
	if r, ok := p.runningOf(s.Name); ok && strings.HasPrefix(r.doing, "waiting for ") {
		w = r.doing
	}
	if x, ok := strings.CutPrefix(w, "waiting for "); ok {
		return "waits for " + x
	}
	return w
}

// doneSays is what a row whose steps are all well says: its step's own
// words, when it has one; else that they're all done.
func (p *Pretty) doneSays(r applyRow) string {
	if len(r.steps) == 1 {
		return p.listRow(r.steps[0]).Says
	}
	return look.Muted(fmt.Sprintf("all %d done", len(r.steps)))
}

// lamps are the run's lights as it stands, or the rows it will run while
// it's getting ready: a row each, as it stands on the screen, counting its
// steps once all are done.
func (p *Pretty) lamps() []look.Lamp {
	var lamps []look.Lamp
	for _, r := range p.shownRows() {
		l := look.Lamp{Name: r.name, State: p.rowLook(r).State}
		if l.State == look.Done {
			l.Count = fmt.Sprint(len(r.steps))
		}
		lamps = append(lamps, l)
	}
	return lamps
}

// stillToDo is whether the step by hand called item is among what r, its
// step's result, has left to do.
func stillToDo(r check.Result, item string) bool {
	return slices.ContainsFunc(r.Items, func(it check.Item) bool { return it.ID == steps.ManualName+":"+item })
}

// stepsByArea are the run's steps by area, in the views' order, the boot's
// first: the order the record lists what's to do in.
func (p *Pretty) stepsByArea() []event.Step {
	var out []event.Step
	for _, area := range viewOrder {
		for _, s := range p.start.Steps {
			if viewArea(s.Area) == area {
				out = append(out, s)
			}
		}
	}
	return out
}
