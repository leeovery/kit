package render

import (
	"fmt"
	"slices"
	"strings"

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
// then, in the views' order, each step running, with the last lines its
// commands printed; each waiting on you, with what to do; each finished
// unwell, a line each. What finished well, and what's still to start, the
// bar and the lights count: the whole list is applying's last word. It's
// never taller than the terminal: when what finished unwell won't all fit,
// the last are counted.
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
	blocks := []block{{lines: p.askingLines(p.width)}}
	kept, unwell := len(blocks[0].lines), 0
	for _, area := range viewOrder {
		for _, s := range p.start.Steps {
			if viewArea(s.Area) != area {
				continue
			}
			if r, ok := running[s.Name]; ok {
				lines := look.Timeline("  ", p.width, p.runningRow(r, p.width))
				blocks = append(blocks, block{lines: lines})
				kept += len(lines)
				continue
			}
			if f, ok := p.results[s.Name]; ok && state(f.Result.State) != look.Done && f.Result.State != check.Deferred {
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
// what to do, under it; at work, what it's doing and the command running,
// with the last lines it printed under it.
func (p *Pretty) runningRow(r runningStep, w int) look.Row {
	if r.todo != "" {
		return look.Row{State: look.NeedsYou, Name: r.title, Says: look.Orange(r.doing), Under: todoLines(r.todo, w-6)}
	}
	return look.Row{State: look.Running, Name: r.title, Says: p.workSays(r), Under: look.Output(lastOf(p.output[r.step], keptRunning)...)}
}

// workSays is what a step at work says: what it's doing, in cyan, how far
// it's got, when it counts, and the command running.
func (p *Pretty) workSays(r runningStep) string {
	says := r.counted(look.Cyan(r.doing))
	if c := p.command[r.step]; c != "" {
		says = append(says, look.Dim(c))
	}
	return look.Says(says...)
}

// busy is how many steps are at work, and how many wait on you.
func (p *Pretty) busy() (working, needs int) {
	for _, r := range p.running {
		switch {
		case r.todo != "":
			needs++
		case r.atWork():
			working++
		}
	}
	return working, needs
}

// progress is the run's bar, and the counts after it: how many of how many
// steps are done, what failed, what's running, what needs you, and how long
// it's taken.
func (p *Pretty) progress() (bar, counts string) {
	working, needs := p.busy()
	failed := 0
	for _, f := range p.results {
		if f.Result.State == check.Failed && !wasStopped(f) {
			failed++
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
	parts = append(parts, look.Muted(took(p.now().Sub(p.began))))
	return look.Bar(done, working, total, look.Done), look.Says(parts...)
}

// lamps are the run's lights as it stands, or the steps it will run while
// it's getting ready: an area with a step waiting on you, or failed, says
// so at once; one a stopped run didn't finish is grey; then one done shows
// how it stood, one running is running, and the rest wait.
func (p *Pretty) lamps() []look.Lamp {
	running := map[string]runningStep{}
	for _, r := range p.running {
		running[r.step] = r
	}
	planned := p.start.Steps
	if planned == nil {
		planned = p.planned
	}
	var lamps []look.Lamp
	for _, area := range viewOrder {
		var steps []event.Step
		var finished []event.StepFinished
		now, asking, broke, cut := false, false, false, false
		for _, s := range planned {
			if viewArea(s.Area) != area {
				continue
			}
			steps = append(steps, s)
			if f, ok := p.results[s.Name]; ok {
				finished = append(finished, f)
				broke = broke || f.Result.State == check.Failed && !wasStopped(f)
				cut = cut || wasStopped(f)
			}
			r, isRunning := running[s.Name]
			now = now || isRunning && r.atWork()
			asking = asking || r.todo != ""
		}
		switch {
		case len(steps) == 0:
			continue
		case asking:
			lamps = append(lamps, look.Lamp{Name: area, State: look.NeedsYou})
		case broke:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Failed})
		case cut:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Skipped})
		case len(finished) == len(steps):
			lamps = append(lamps, lamp(area, finished))
		case now:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Running})
		default:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Queued})
		}
	}
	return lamps
}

// workList is applying's list of steps, the run's last word: a block an
// area in the views' order, each step a row as listRow has it, what
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
			lines = append(lines, look.Timeline("  ", p.width, p.listRow(s))...)
		}
		lines = append(lines, "")
	}
	return lines[:max(len(lines)-1, 0)]
}

// listRow is a step's row in applying's list as it stands: running; done,
// as workRow has it; cut off by the run stopping, or never run before it
// stopped, grey; or waiting its turn.
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
	case isDone:
		return p.workRow(f, s.Part)
	}
	return look.Row{State: look.Queued, Name: p.title(s.Name), Says: look.Muted(p.waiting(s))}
}

// waiting is what a step says while it waits its turn: what for, when it's
// a step it waits for, not yet done.
func (p *Pretty) waiting(s event.Step) string {
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
		for _, l := range wrap(part, width) {
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
// first, a few things named, or counted, then what the step says; what
// needs attention in it, and what to run, under it.
func (p *Pretty) workRow(f event.StepFinished, part string) look.Row {
	row := resultRow(p.title(f.Step), f.Result)
	row.Under = nil
	if out := p.output[f.Step]; f.Result.State == check.Failed && len(out) > 0 {
		what, _, _ := strings.Cut(f.Result.Reason, ":")
		row.Says = look.Says(look.Red(what), look.Dim(p.command[f.Step]))
		row.Under = look.Output(lastOf(out, keptFailing)...)
	}
	if done := doneGroups(f.Result.Done); len(done) > 0 {
		var did []string
		for _, g := range done {
			// A step of the user's own was applied; a kind's things are named,
			// or counted when there are many.
			if g.action == "run" {
				did = append(did, look.Muted("applied"))
				continue
			}
			names := strings.Join(g.names, ", ")
			if len(g.names) > 3 {
				names = fmt.Sprint(len(g.names))
			}
			did = append(did, look.Muted(past(g.action)+" "+names))
		}
		row.Says = look.Says(append(did, row.Says)...)
	}
	row.Under = append(row.Under, p.left(f, part)...)
	return row
}

// left is what's left to do about a finished step, a line each, as its row
// in applying's list has it under it: each thing that needs attention, or
// why it's not done yet, and what to run.
func (p *Pretty) left(f event.StepFinished, part string) []string {
	var lines, hints []string
	for _, it := range f.Result.Items {
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
				says = append(says, look.Muted(since(p.start.Time.Sub(it.Since))))
			}
			lines = append(lines, look.Says(says...))
			hint := "kit reconcile"
			if it.Action != "" || it.State == "missing" {
				hint = "kit apply"
			}
			if !slices.Contains(hints, hint) {
				hints = append(hints, hint)
			}
		case it.Detail != "":
			lines = append(lines, todo(it.Detail))
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
// finished; a run stopped says so, and where to carry on.
func (p *Pretty) applied(e event.RunFinished) []string {
	stopped := p.stoppedRun()
	out := []string{look.Lights(p.lamps()...)}
	if stopped {
		out = append(out, p.stoppedFoot(e))
	} else {
		out = append(out, p.foot(e)...)
	}
	out = append(append(out, ""), p.workList()...)
	if stopped {
		out = append(out, "", "  "+look.Todo(look.Cmd("kit apply")+look.Muted(" carries on from here")))
	}
	for i, l := range out {
		out[i] = ansi.Truncate(l, p.width, "…")
	}
	return out
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
