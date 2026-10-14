package render

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
)

// Sized has the face ask size how big the terminal is, as it draws: its
// columns and its lines.
func (p *Pretty) Sized(size func() (int, int)) *Pretty {
	p.size = size
	return p
}

// height is how many lines the terminal has.
func (p *Pretty) height() int {
	if p.size == nil {
		return 40
	}
	_, h := p.size()
	return max(h, 12)
}

// applyLive is applying's live part in place under its heading, in a
// window too small for its screen: the lights, the bar and its counts,
// then what's happening and what's wrong: macOS asking something first,
// then the card up for you; then, in the views' order, each step running,
// with the last lines its commands printed; each waiting on you, with what
// to do; each finished unwell, a line each. What finished well, what's
// left to do, and what's still to start, the bar and the lights count: the
// whole list is applying's last word. It's never taller than the
// terminal: when what finished unwell won't all fit, the last are counted.
func (p *Pretty) applyLive() []string {
	bar, counts := p.progress()
	top := []string{look.Lights(p.lamps()...), "", "  " + bar + "  " + counts, ""}
	running := map[string]runningStep{}
	for _, r := range p.running {
		running[r.step] = r
	}
	type block struct {
		lines  []string
		unwell bool
	}
	// macOS asking something comes first: the run waits on it.
	blocks := []block{{lines: append(p.askingLines(p.width), p.cardLines(p.width, false)...)}}
	kept, unwell := len(blocks[0].lines), 0
	for _, area := range viewOrder {
		for _, s := range p.start.Steps {
			if viewArea(s.Area) != area {
				continue
			}
			if r, ok := running[s.Name]; ok {
				if p.carded(r.step) {
					continue
				}
				lines := look.Timeline("  ", p.width, p.runningRow(r, p.width))
				blocks = append(blocks, block{lines: lines})
				kept += len(lines)
				continue
			}
			if f, ok := p.results[s.Name]; ok && !slices.Contains([]look.State{look.Done, look.Skipped, look.ToDo}, p.ended(f)) {
				row := p.workRow(f, s.Part)
				row.Under = nil
				blocks = append(blocks, block{lines: look.Rows("  ", p.width, row), unwell: true})
				unwell++
			}
		}
	}
	room := max(p.height()-1-len(top), 3)
	// What finished unwell has the room what's running leaves, a line kept
	// to count the rest, when they won't all fit.
	fits := room - kept
	if kept+unwell > room {
		fits--
	}
	var list []string
	left, at := 0, 0
	for _, b := range blocks {
		switch {
		case !b.unwell:
			list = append(list, b.lines...)
		case fits > 0:
			list = append(list, b.lines...)
			fits--
		default:
			if left == 0 {
				at = len(list)
			}
			left++
		}
	}
	if left > 0 {
		list = slices.Insert(list, at, "  "+look.Muted(fmt.Sprintf("and %d more", left)))
	}
	return append(top, list[:min(len(list), room)]...)
}

// runningRow is a running step's row, w wide: waiting on you, what for, then
// what to do, under it, or, with its card up, that it needs you; at work,
// what it's doing and the command running, with the last lines it printed
// under it.
func (p *Pretty) runningRow(r runningStep, w int) look.Row {
	switch {
	case r.todo != "":
		return look.Row{State: look.NeedsYou, Name: r.title, Says: look.Orange(r.doing), Under: todoLines(r.todo, w-6)}
	case p.needsYou(r):
		return look.Row{State: look.NeedsYou, Name: r.title, Says: look.Orange("needs you")}
	}
	return look.Row{State: look.Running, Name: r.title, Says: p.workSays(r), Under: look.Output(lastOf(p.output[r.step], keptRunning)...)}
}

// workSays is what a step at work says: what it's doing, in cyan, how far
// it's got, when it counts, and the command running.
func (p *Pretty) workSays(r runningStep) string {
	says := r.counted(look.Cyan(r.doing))
	if c := p.command[r.step]; c != "" {
		says = append(says, look.Dim(look.OneLine(c)))
	}
	return look.Says(says...)
}

// busy is how many steps are at work, and how many wait on you now: the
// card up, and each step saying what you're to do. A card waiting its turn
// isn't counted till it's up.
func (p *Pretty) busy() (working, needs int) {
	if p.card != nil {
		needs++
	}
	for _, r := range p.running {
		switch {
		case r.todo != "" && !p.carded(r.step):
			needs++
		case p.atWork(r):
			working++
		}
	}
	return working, needs
}

// progress is the run's bar, and the counts after it: how many of how many
// steps are done, what failed, what's running, what needs you, what's left
// to do, and how long it's taken.
func (p *Pretty) progress() (bar, counts string) {
	working, needs := p.busy()
	failed, later := 0, 0
	for _, f := range p.results {
		switch p.ended(f) {
		case look.Failed:
			failed++
		case look.ToDo:
			later++
		}
	}
	done, total := len(p.results), len(p.start.Steps)
	parts := []string{look.White(fmt.Sprintf("%d of %d", done, total))}
	if failed > 0 {
		parts = append(parts, look.Red(fmt.Sprintf("%d failed", failed)))
	}
	if working > 0 {
		parts = append(parts, look.Cyan(fmt.Sprintf("%d running", working)))
	}
	if needs > 0 {
		parts = append(parts, look.Orange(fmt.Sprintf("%d %s you", needs, plural(needs, "needs", "need"))))
	}
	if later > 0 {
		parts = append(parts, look.White(fmt.Sprintf("%d to do", later)))
	}
	parts = append(parts, look.Muted(took(p.now().Sub(p.began))))
	return look.Bar(done, working, total, look.Done), look.Says(parts...)
}

// lamps are the run's lights as it stands, or the steps it will run while
// it's getting ready: an area with a step waiting on you, or failed, says
// so at once; one a stopped run didn't finish is grey; then one done shows
// how it stood; one that's left you something to do says so; one running
// is running, and the rest wait.
func (p *Pretty) lamps() []look.Lamp {
	planned := p.start.Steps
	if planned == nil {
		planned = p.planned
	}
	var lamps []look.Lamp
	for _, area := range viewOrder {
		var finished []look.State
		n, now, asking, cut := 0, false, false, false
		for _, s := range planned {
			if viewArea(s.Area) != area {
				continue
			}
			n++
			st := p.standing(s.Name)
			f, done := p.results[s.Name]
			switch {
			case done:
				finished = append(finished, st)
				cut = cut || wasStopped(f)
			case st == look.NeedsYou:
				asking = true
			case st == look.Running:
				now = true
			}
		}
		switch {
		case n == 0:
			continue
		case asking:
			lamps = append(lamps, look.Lamp{Name: area, State: look.NeedsYou})
		case slices.Contains(finished, look.Failed):
			lamps = append(lamps, look.Lamp{Name: area, State: look.Failed})
		case cut:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Skipped})
		case len(finished) == n:
			lamps = append(lamps, lamp(area, finished))
		case slices.Contains(finished, look.ToDo):
			lamps = append(lamps, look.Lamp{Name: area, State: look.ToDo})
		case now:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Running})
		default:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Queued})
		}
	}
	return lamps
}

// workList is applying's list of steps, the run's last word: a block an
// area in the views' order, each step a row as recordRow has it, what
// belongs to it on a line from its mark. The boot's steps, shown as it ran,
// have their light, and a row here only once one isn't well.
func (p *Pretty) workList() (lines []string) {
	for _, area := range viewOrder {
		var shown []event.Step
		for _, s := range p.start.Steps {
			if viewArea(s.Area) != area {
				continue
			}
			if f, ok := p.results[s.Name]; area != boot.AreaBooted || ok && f.Result.State != check.OK {
				shown = append(shown, s)
			}
		}
		if len(shown) == 0 {
			continue
		}
		lines = append(lines, look.Header(area, ""))
		for _, s := range shown {
			lines = append(lines, look.Timeline("  ", p.width, p.recordRow(s))...)
		}
		lines = append(lines, "")
	}
	return lines[:max(len(lines)-1, 0)]
}

// recordRow is a step's row in applying's record: one that failed says
// what went wrong, why under it, then what to run; one that's left you
// something to do says it's below; the rest as listRow has them.
func (p *Pretty) recordRow(s event.Step) look.Row {
	f, isDone := p.results[s.Name]
	switch {
	case isDone && p.ended(f) == look.Failed:
		return p.failedRow(f)
	case isDone && p.toDo(s.Name):
		row := p.toDoRow(f)
		// That it's below is never cut off: what the step says is cut to
		// make room for it.
		below := look.Says("", look.Muted("to do, below"))
		room := p.width - ansi.StringWidth(look.Rows("  ", p.width, look.Row{State: row.State, Name: row.Name})[0]) - 2 - ansi.StringWidth(below)
		row.Says = look.Cut(row.Says, max(room, 1)) + below
		return row
	}
	return p.listRow(s)
}

// failedRow is a failed step's row in applying's record: what applying did,
// then what went wrong, red; why under it, or, when nothing says, the last
// line it printed; then what to run to try it again.
func (p *Pretty) failedRow(f event.StepFinished) look.Row {
	head, why := failure(f.Result)
	if out := p.output[f.Step]; why == "" && len(out) > 0 {
		why = out[len(out)-1]
	}
	row := look.Row{State: look.Failed, Name: p.title(f.Step), Says: look.Says(append(did(f.Result.Done), look.Red(head))...)}
	if why != "" {
		row.Under = append(row.Under, look.Muted(why))
	}
	row.Under = append(row.Under, look.Todo(look.Cmd("kit apply "+f.Step)))
	return row
}

// listRow is a step's row in applying's list as it stands: running; left
// for you to do, as toDoRow has it; done, as workRow has it; cut off by the
// run stopping, or never run before it stopped, grey; or waiting its turn.
func (p *Pretty) listRow(s event.Step) look.Row {
	f, isDone := p.results[s.Name]
	r, isRunning := p.runningOf(s.Name)
	cut, isCut := p.cut[s.Name]
	switch {
	case isRunning:
		return p.runningRow(r, p.width)
	case isDone && wasStopped(f) && isCut:
		return look.Row{State: look.Skipped, Name: p.title(s.Name), Says: look.Says(cut.counted(look.Muted("stopped"))...)}
	case isDone && wasStopped(f):
		return look.Row{State: look.Skipped, Name: p.title(s.Name), Says: look.Muted("not run")}
	case isDone && p.toDo(s.Name):
		return p.toDoRow(f)
	case isDone:
		return p.workRow(f, s.Part)
	}
	return look.Row{State: look.Queued, Name: p.title(s.Name), Says: look.Muted(p.waiting(s))}
}

// toDoRow is the row of a finished step that's left the person at the Mac
// something to do: what applying did, then what the step says, or why it
// was deferred, white.
func (p *Pretty) toDoRow(f event.StepFinished) look.Row {
	says := f.Result.Summary
	if f.Result.State == check.Deferred {
		says = f.Result.Reason
	}
	parts := did(f.Result.Done)
	if says != "" {
		parts = append(parts, saying(look.ToDo, says))
	}
	return look.Row{State: look.ToDo, Name: p.title(f.Step), Says: look.Says(parts...)}
}

// waiting is what a step says while it waits its turn: its own words, when
// it has some; else what for, when it's a step it waits for, not yet done.
func (p *Pretty) waiting(s event.Step) string {
	if s.Waiting != "" {
		return s.Waiting
	}
	for _, step := range s.Waits {
		if _, done := p.results[step]; !done {
			return "waiting for " + p.title(step)
		}
	}
	return "waiting"
}

// todoLines are what to do, wrapped to width: an arrow, then the words.
// What follows a line break is what to act on, as a setting's path, on a
// line of its own, in white.
func todoLines(text string, width int) []string {
	var lines []string
	for i, part := range strings.Split(text, "\n") {
		for _, l := range look.Wrap(part, width) {
			switch {
			case len(lines) == 0:
				lines = append(lines, look.Todo(look.Muted(l)))
			case i > 0:
				lines = append(lines, "  "+look.White(l))
			default:
				lines = append(lines, "  "+look.Muted(l))
			}
		}
	}
	return lines
}

// workRow is a finished step's row in applying's list: what applying did
// first, a few things named, or counted, then what the step says; why a
// failed step failed, the last lines it printed (printedFailing), then
// what's left of it, under it.
func (p *Pretty) workRow(f event.StepFinished, part string) look.Row {
	row := resultRow(p.title(f.Step), f.Result, part, p.start.Time)
	if f.Result.State == check.Failed && len(p.output[f.Step]) > 0 {
		row.Says, row.Under = p.printedFailing(f, part, keptFailing)
	}
	if did := did(f.Result.Done); len(did) > 0 {
		row.Says = look.Says(append(did, row.Says)...)
	}
	return row
}

// did is what applying a step did, as its row says it first: what each
// action did, a few things named, or counted; a step of the user's own,
// that it was applied.
func did(done []check.Item) []string {
	var parts []string
	for _, g := range doneGroups(done) {
		if g.action == "run" {
			parts = append(parts, look.Muted("applied"))
			continue
		}
		names := strings.Join(g.names, ", ")
		if len(g.names) > 3 {
			names = fmt.Sprint(len(g.names))
		}
		parts = append(parts, look.Muted(past(g.action)+" "+names))
	}
	return parts
}

// whatsLeft is what's left to do about a step's result r, of part, in a run
// started at start, as every row of the step has it under it (status, the
// log, applying's list and its panels): each thing that needs attention, by
// name, with what to do about it under it when there's something to say, or
// why it's not done yet; then what to run.
func whatsLeft(r check.Result, part string, start time.Time) []string {
	var lines, hints []string
	for _, it := range r.Items {
		switch {
		case it.Quiet != "" && it.Detail != "":
			// Too new to count as drift, but applying says why it's not done:
			// waiting for a password, say.
			lines = append(lines, look.Says(look.White(it.Name), look.Muted(it.Detail)))
		case it.Quiet != "":
		case IsDrift(it):
			says := []string{look.Orange(it.Name), look.Muted(wrong(part, it))}
			if it.Detail != "" {
				says = append(says, look.Muted(it.Detail))
			}
			if !it.Since.IsZero() {
				says = append(says, look.Muted(since(start.Sub(it.Since))))
			}
			lines = append(lines, look.Says(says...))
			hint := "kit reconcile"
			if it.Action != "" || it.State == "missing" {
				hint = "kit apply"
			}
			if !slices.Contains(hints, hint) {
				hints = append(hints, hint)
			}
		default:
			lines = append(lines, look.Orange(it.Name))
			if it.Detail != "" {
				lines = append(lines, todo(it.Detail))
			}
		}
	}
	for _, hint := range hints {
		lines = append(lines, look.Todo(look.Cmd(hint)))
	}
	return lines
}

// IsDrift is whether it is drift: something installed, declared, linked or
// set otherwise than the config says.
func IsDrift(it check.Item) bool {
	_, ok := driftStates[it.State]
	return ok
}

// applied is applying's last word, under its heading: the lights, the bar
// with how the run ended under them, then the whole work list as it
// finished, then what it's left you to do; a run stopped says so, and
// where to carry on.
func (p *Pretty) applied(e event.RunFinished) []string {
	stopped := p.stoppedRun()
	out := []string{look.Lights(p.lamps()...)}
	if stopped {
		out = append(out, p.stoppedFoot(e))
	} else {
		out = append(out, p.foot(e)...)
	}
	out = append(append(out, ""), p.workList()...)
	if tasks := p.tasks(); len(tasks) > 0 {
		out = append(out, "", look.Header("To do", fmt.Sprintf("%d, in any order: kit apply checks them", len(tasks))))
		out = append(out, look.Tasks(p.width, tasks...)...)
	}
	if stopped {
		out = append(out, "", "  "+look.Todo(look.Cmd("kit apply")+look.Muted(" carries on from here")))
	}
	for i, l := range out {
		out[i] = ansi.Truncate(l, p.width, "…")
	}
	return out
}

// tasks are what the run's left the person at the Mac to do, in the order
// the record lists its steps: each thing by name, with how to do it (taskHow).
func (p *Pretty) tasks() []look.Task {
	var tasks []look.Task
	for _, steps := range p.gridSteps() {
		for _, s := range steps {
			if !p.toDo(s.Name) {
				continue
			}
			for _, it := range p.results[s.Name].Result.Items {
				tasks = append(tasks, look.Task{Title: it.Name, How: p.taskHow(s.Name, it)})
			}
		}
	}
	return tasks
}

// taskHow is how to do it, a thing step left to do: a step by hand's steps,
// as its card had them, when it came up with some; else what the thing
// says.
func (p *Pretty) taskHow(step string, it check.Item) []string {
	_, line, _ := strings.Cut(it.ID, ":")
	if c, ok := p.cards[cardID(step, line)]; ok && it.State == check.ByHand && len(c.Steps) > 0 {
		return c.Steps
	}
	if it.Detail != "" {
		return []string{it.Detail}
	}
	return nil
}

// stoppedRun is whether the run was stopped before it finished.
func (p *Pretty) stoppedRun() bool {
	for _, f := range p.results {
		if wasStopped(f) {
			return true
		}
	}
	return false
}

// stoppedFoot is how a stopped run ended, after its bar: stopped, how many
// of its steps were done, how many of those failed, and how long it took.
// What was cut off, or never ran, isn't counted as failing; what needs you
// is in the list, as the line has no room for it.
func (p *Pretty) stoppedFoot(e event.RunFinished) string {
	done, failed := 0, 0
	for _, f := range p.results {
		switch {
		case wasStopped(f):
		case f.Result.State == check.Failed:
			done++
			failed++
		default:
			done++
		}
	}
	n := len(p.start.Steps)
	parts := []string{look.Muted("stopped"), look.White(fmt.Sprintf("%d of %d done", done, n))}
	if failed > 0 {
		parts = append(parts, look.Red(fmt.Sprintf("%d failed", failed)))
	}
	parts = append(parts, look.Muted(took(e.Duration)))
	return "  " + look.Bar(done, 0, n, look.Done) + "  " + look.Says(parts...)
}
