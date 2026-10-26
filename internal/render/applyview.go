package render

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

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
// window too small for its screen: the rows' lights, the bar and its counts,
// then what's happening and what's wrong: macOS asking something first,
// then the card up for you; then, in the views' order, each step running,
// with the last lines its commands printed; each waiting on you, with what
// to do; each finished unwell, a line each. What finished well, what's
// left to do, and what's still to start, the bar and the lights count: the
// whole list is applying's last word. It's never taller than the
// terminal: when what finished unwell won't all fit, the last are counted.
func (p *Pretty) applyLive() []string {
	bar, counts := p.progress()
	top := append(look.Lights(p.width, p.lamps()...), "", "  "+bar+"  "+counts, "")
	running := map[string]runningStep{}
	for _, r := range p.running {
		running[r.step] = r
	}
	type block struct {
		lines  []string
		unwell bool
	}
	// macOS asking something comes first: the run waits on it.
	blocks := []block{{lines: append(p.askingLines(p.width), p.cardLines(p.width)...)}}
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

// failedRow is a failed step's row in applying's record: what applying did,
// then what went wrong, red; why under it, or, when nothing says, the last
// line it printed; then what to run to try it again.
func (p *Pretty) failedRow(f event.StepFinished) look.Row {
	head, why := failure(f.Result)
	if out := p.output[f.Step]; why == "" && len(out) > 0 {
		why = out[len(out)-1]
	}
	row := look.Row{State: look.Failed, Name: p.title(f.Step), Says: look.Says(append(did(f.Result.Done, p.step(f.Step), false), look.Red(head))...)}
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
		return look.Row{State: look.Skipped, Name: p.title(s.Name), Says: look.Says(cut.counted(look.Muted("cut off " + cut.doing))...)}
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
	parts := did(f.Result.Done, p.step(f.Step), says != "")
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
	did := did(f.Result.Done, p.step(f.Step), f.Result.Summary != "")
	switch {
	case len(did) > 0 && declaredAll.MatchString(f.Result.Summary):
		row.Says = look.Says(did...)
	case len(did) > 0:
		row.Says = look.Says(append(did, row.Says)...)
	}
	return row
}

// declaredAll is a kind's summary saying no more than that all it declares
// is as declared, as in 24 declared, all synced: what it did says enough.
var declaredAll = regexp.MustCompile(`^\d+ declared, all [a-z]+$`)

// did is what applying step s did, as its row says it first, once each:
// what each action did, a few things named, or counted, in the step's verb
// (installed, set, synced); a step of the user's own, that it was applied,
// unless it says what it did (said). A thing named as the step is isn't
// named again: its row says how it stands.
func did(done []check.Item, s event.Step, said bool) []string {
	var parts []string
	for _, g := range doneGroups(done) {
		names := slices.DeleteFunc(g.names, func(name string) bool { return name == s.Title })
		switch {
		case g.action == "run" && said:
		case g.action == "run":
			parts = append(parts, look.Muted("applied"))
		case len(names) == 0:
		default:
			verb := past(g.action)
			if g.action == kindInstall && s.Did != "" {
				verb = s.Did
			}
			named := strings.Join(names, ", ")
			if len(names) > 3 {
				named = fmt.Sprint(len(names))
			}
			parts = append(parts, look.Muted(verb+" "+named))
		}
	}
	return parts
}

// kindInstall is the action a kind's step takes on a thing missing.
const kindInstall = "install"

// whatsLeft is what's left to do about a step's result r, of part, in a run
// started at start, as every row of the step has it under it (status, the
// log, applying's list and its panels): each thing that needs attention, by
// name, with what to do about it under it when there's something to say, or
// why it's not done yet; then what to run.
func whatsLeft(r check.Result, part string, start time.Time) []string {
	var lines, hints []string
	alike := map[string]int{}
	for _, it := range r.Items {
		if IsDrift(it) && it.Detail != "" {
			alike[it.State+"|"+it.Detail]++
		}
	}
	told := map[string]bool{}
	for _, it := range r.Items {
		// More than a few alike, as Claude's servers waiting for their
		// projects' folders, are one line, counted.
		if key := it.State + "|" + it.Detail; it.Quiet == "" && alike[key] > 3 {
			if !told[key] {
				told[key] = true
				lines = append(lines, look.Says(look.Orange(fmt.Sprintf("%d %s", alike[key], wrong(part, it))), look.Muted(it.Detail)))
			}
			continue
		}
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

// The bar at the head of applying's record, in cells.
const recordBar = 32

// applied is applying's record, its last word, in place of its screen or
// its live part: its head, the wordmark with what ran, the bar and the
// tally, when its heading was drawn live, else the bar and the tally under
// the heading; then what it's left you to do, what failed, what needs you,
// what didn't run, and the rows done, each only when it has any. A run
// stopped says so, and where to carry on.
func (p *Pretty) applied(e event.RunFinished) []string {
	stopped := p.stoppedRun()
	out := p.recordHead(e, stopped)
	for _, section := range [][]string{p.toDoSection(), p.unwellSection(look.Failed), p.unwellSection(look.NeedsYou), p.notRunSection(), p.doneSection()} {
		if len(section) > 0 {
			out = append(append(out, ""), section...)
		}
	}
	if stopped {
		out = append(out, "", "  "+look.Todo(look.Cmd("kit apply")+look.Muted(" carries on from here")))
	}
	for i, l := range out {
		out[i] = ansi.Truncate(l, p.width, "…")
	}
	return out
}

// recordHead is the record's head: the bar, its last cell in the colour
// the run ended in, and the tally, or how far a stopped run got; beside the
// wordmark, with what ran, the Mac and when, when its heading was drawn
// live, as the record takes its place.
func (p *Pretty) recordHead(e event.RunFinished, stopped bool) []string {
	got, says := 1.0, p.tally(e)
	if stopped {
		got, says = p.stoppedSays(e)
	}
	bar := look.Progress(got, recordBar, p.endedAs())
	if p.liveHead == nil {
		return []string{"  " + bar + "  " + says}
	}
	line := headline{p.start.Command, p.start.Machine, when(p.start.Time)}
	return look.Head(line.whole(), bar, says)
}

// endedAs is how the run ended: failed, when a step did; needing you, when
// one does; else well.
func (p *Pretty) endedAs() look.State {
	ended := look.Done
	for _, f := range p.results {
		switch p.ended(f) {
		case look.Failed:
			return look.Failed
		case look.NeedsYou:
			ended = look.NeedsYou
		}
	}
	return ended
}

// tally is how the run ended, counted: what it's left you to do, the steps
// that failed and those that need you, the rows whose steps were all well,
// and how long it took.
func (p *Pretty) tally(e event.RunFinished) string {
	var parts []string
	if n := len(p.tasks()); n > 0 {
		parts = append(parts, look.White(fmt.Sprintf("%d to do", n)))
	}
	if n := len(p.unwell(look.Failed)); n > 0 {
		parts = append(parts, look.Red(fmt.Sprintf("%d failed", n)))
	}
	if n := len(p.unwell(look.NeedsYou)); n > 0 {
		parts = append(parts, look.Orange(fmt.Sprintf("%d %s you", n, plural(n, "needs", "need"))))
	}
	parts = append(parts, look.White(fmt.Sprintf("%d fine", len(p.rowsDone()))), look.Muted(took(e.Duration)))
	return look.Says(parts...)
}

// toDoSection is what the run's left you to do, numbered, each with how to
// do it.
func (p *Pretty) toDoSection() []string {
	tasks := p.tasks()
	if len(tasks) == 0 {
		return nil
	}
	return append([]string{look.Header("To do", fmt.Sprintf("%d · kit apply brings them back", len(tasks)))}, look.Tasks(p.width, tasks...)...)
}

// unwellSection is the steps that ended as st, failed or needing you, a row
// each under its header: named by its row, and by itself too when its row
// has others; what went wrong, or what's left of it, under it.
func (p *Pretty) unwellSection(st look.State) []string {
	unwell := p.unwell(st)
	if len(unwell) == 0 {
		return nil
	}
	label := map[look.State]string{look.Failed: "Failed", look.NeedsYou: "Needs you"}[st]
	out := []string{look.Header(label, fmt.Sprint(len(unwell)))}
	for _, s := range unwell {
		row := p.listRow(s)
		if st == look.Failed {
			row = p.failedRow(p.results[s.Name])
		}
		row.Name = p.recordName(s)
		out = append(out, look.Timeline("  ", p.width, row)...)
	}
	return out
}

// notRunSection is the steps that didn't run to their end, a row each
// under its header, as listRow has it: cut off by the run stopping, and
// what it was doing; never run before it stopped; or not checked, and why.
func (p *Pretty) notRunSection() []string {
	notRun := p.unwell(look.Skipped)
	if len(notRun) == 0 {
		return nil
	}
	out := []string{look.Header("Didn't run", fmt.Sprint(len(notRun)))}
	for _, s := range notRun {
		row := p.listRow(s)
		row.Name = p.recordName(s)
		out = append(out, look.Timeline("  ", p.width, row)...)
	}
	return out
}

// doneSection is the rows whose steps were all well, a line each, in
// order, saying what they did, and nothing more: what of theirs still
// waits needs no one now (what does is to do), and kit status says it.
func (p *Pretty) doneSection() []string {
	done := p.rowsDone()
	if len(done) == 0 {
		return nil
	}
	out := []string{look.Header("Done", fmt.Sprint(len(done)))}
	for _, r := range done {
		out = append(out, look.Timeline("  ", p.width, look.Row{State: look.Done, Name: r.name, Says: p.doneSays(r)})...)
	}
	return out
}

// unwell are the steps that ended as st, by their rows, in order, then the
// steps by hand.
func (p *Pretty) unwell(st look.State) []event.Step {
	var out []event.Step
	for _, s := range p.recordOrder() {
		if f, ok := p.results[s.Name]; ok && p.ended(f) == st {
			out = append(out, s)
		}
	}
	return out
}

// recordOrder are the run's steps as the record lists them: by their
// rows, in order, then the steps by hand.
func (p *Pretty) recordOrder() []event.Step {
	var out []event.Step
	for _, r := range p.shownRows() {
		out = append(out, r.steps...)
	}
	for _, s := range p.start.Steps {
		if row, _ := RowOf(s); row == "" {
			out = append(out, s)
		}
	}
	return out
}

// recordName is the name the record gives step s: its row's, and its own
// after it when the row has more steps than it and it's called otherwise;
// a step by hand's own.
func (p *Pretty) recordName(s event.Step) string {
	title := p.title(s.Name)
	for _, r := range p.shownRows() {
		switch {
		case !slices.ContainsFunc(r.steps, func(t event.Step) bool { return t.Name == s.Name }):
		case len(r.steps) == 1 || title == r.name:
			return r.name
		default:
			return r.name + " · " + title
		}
	}
	return title
}

// rowsDone are the rows whose steps all finished well, in order.
func (p *Pretty) rowsDone() []applyRow {
	var done []applyRow
	for _, r := range p.shownRows() {
		if !slices.ContainsFunc(r.steps, func(s event.Step) bool {
			f, ok := p.results[s.Name]
			return !ok || p.ended(f) != look.Done
		}) {
			done = append(done, r)
		}
	}
	return done
}

// tasks are what the run's left the person at the Mac to do: the to-dos at
// its end not done, in their order, each by its title with how to do it;
// then the rest, in the order of its steps by area, each thing by name,
// with how to do it (taskHow).
func (p *Pretty) tasks() []look.Task {
	var tasks []look.Task
	listed := map[string]bool{}
	if p.todos != nil {
		for _, t := range p.todos.List {
			listed[cardID(t.Step, t.ID)] = true
			if p.leftToDo(t) {
				tasks = append(tasks, look.Task{Title: t.Title, How: t.How})
			}
		}
	}
	for _, s := range p.stepsByArea() {
		if !p.toDo(s.Name) {
			continue
		}
		for _, it := range p.results[s.Name].Result.Items {
			if !listed[cardID(s.Name, it.ID)] {
				tasks = append(tasks, look.Task{Title: it.Name, How: p.taskHow(s.Name, it)})
			}
		}
	}
	return tasks
}

// leftToDo is whether t, one of the to-dos at the run's end, is still to
// do: not done, and still among what its step's result has left, unless
// the run stopped that step before it said.
func (p *Pretty) leftToDo(t event.ToDo) bool {
	f, ok := p.results[t.Step]
	switch {
	case t.Done:
		return false
	case !ok, wasStopped(f):
		return true
	}
	return slices.ContainsFunc(f.Result.Items, func(it check.Item) bool { return it.ID == t.ID })
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

// stoppedSays is how far a stopped run got, of 1, and how it ended: that
// it was stopped, how many of its steps were done, how many of those
// failed, and how long it took. What was cut off, or never ran, isn't
// counted as failing; what's left is in the record.
func (p *Pretty) stoppedSays(e event.RunFinished) (got float64, says string) {
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
	return float64(done) / float64(max(n, 1)), look.Says(parts...)
}
