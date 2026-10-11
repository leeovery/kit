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

// applyLive is applying's live part, in place under its heading: the
// lights, the bar with how many steps are done, running and how long it's
// taken, then what's happening and what's wrong: macOS asking something
// first, then, in the views' order, each step running, with the last lines
// its commands printed; each waiting on you, with what to do; each finished
// unwell, a line each. What finished
// well, and what's still to start, the bar and the lights count: the whole
// list is applying's last word. It's never taller than the terminal: when
// what finished unwell won't all fit, the last are counted.
func (p *Pretty) applyLive() []string {
	done, total := len(p.results), len(p.start.Steps)
	top := []string{look.Lights(p.lamps()...), "",
		"  " + look.Bar(done, len(p.running), total, look.Done) + "  " + look.Says(look.White(fmt.Sprintf("%d of %d", done, total)), look.Cyan(fmt.Sprintf("%d running", len(p.running))), look.Muted(seconds(time.Since(p.began)))),
		""}
	running := map[string]runningStep{}
	for _, r := range p.running {
		running[r.step] = r
	}
	type block struct {
		lines  []string
		unwell bool
	}
	// macOS asking something comes first: the run waits on it.
	blocks := []block{{lines: p.askingLines()}}
	kept, unwell := len(blocks[0].lines), 0
	for _, area := range viewOrder {
		for _, s := range p.start.Steps {
			if viewArea(s.Area) != area {
				continue
			}
			if r, ok := running[s.Name]; ok {
				lines := p.runningLines(r)
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

// runningLines are a running step's lines: waiting on you, what for, then
// what to do, under it; at work, what it's doing and the command running,
// with the last lines it printed, on a line from its mark.
func (p *Pretty) runningLines(r runningStep) []string {
	if r.todo != "" {
		return look.Timeline("  ", p.width, look.Row{State: look.NeedsYou, Name: r.title, Says: look.Orange(r.doing), Under: todoLines(r.todo, p.width-6)})
	}
	says := []string{look.Cyan(r.doing)}
	if c := p.command[r.step]; c != "" {
		says = append(says, look.Dim(c))
	}
	return look.Timeline("  ", p.width, look.Row{State: look.Running, Name: r.title, Says: look.Says(says...), Under: look.Output(lastOf(p.output[r.step], keptRunning)...)})
}

// lamps are the run's lights as it stands, or the steps it will run while
// it's getting ready: an area running, waiting on you, waiting, or done and
// how it stood.
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
		now, asking := false, false
		for _, s := range planned {
			if viewArea(s.Area) != area {
				continue
			}
			steps = append(steps, s)
			if f, ok := p.results[s.Name]; ok {
				finished = append(finished, f)
			}
			r, isRunning := running[s.Name]
			now = now || isRunning
			asking = asking || r.todo != ""
		}
		switch {
		case len(steps) == 0:
			continue
		case len(finished) == len(steps):
			lamps = append(lamps, lamp(area, finished))
		case asking:
			lamps = append(lamps, look.Lamp{Name: area, State: look.NeedsYou})
		case now:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Running})
		default:
			lamps = append(lamps, look.Lamp{Name: area, State: look.Queued})
		}
	}
	return lamps
}

// workList is applying's list of steps, the run's last word: a block an
// area in the views' order, each step a row, as it finished, or, when the
// run was stopped, running or waiting. The boot's steps, shown as it ran,
// have their light, and a row here only once one isn't well.
func (p *Pretty) workList() (lines []string) {
	running := map[string]runningStep{}
	for _, r := range p.running {
		running[r.step] = r
	}
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
			r, isRunning := running[s.Name]
			f, isDone := p.results[s.Name]
			switch {
			case isRunning:
				lines = append(lines, p.runningLines(r)...)
			case isDone && p.output[s.Name] != nil && f.Result.State == check.Failed:
				lines = append(lines, look.Timeline("  ", p.width, p.workRow(f, s.Part))...)
			case isDone:
				lines = append(lines, look.Rows("  ", p.width, p.workRow(f, s.Part))...)
			default:
				lines = append(lines, look.Rows("  ", p.width, look.Row{State: look.Queued, Name: p.title(s.Name), Says: look.Muted(p.waiting(s))})...)
			}
		}
		lines = append(lines, "")
	}
	return lines[:max(len(lines)-1, 0)]
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
	var hints []string
	for _, it := range f.Result.Items {
		switch {
		case it.Quiet != "" && it.Detail != "":
			// Too new to count as drift, but applying says why it's not done:
			// waiting for a password, say.
			row.Under = append(row.Under, look.Says(look.White(it.Name), look.Muted(it.Detail)))
		case it.Quiet != "":
		case IsDrift(it):
			says := []string{look.Orange(it.Name), look.Muted(wrong(part, it))}
			if it.Detail != "" {
				says = append(says, look.Muted(it.Detail))
			}
			if !it.Since.IsZero() {
				says = append(says, look.Muted(since(p.start.Time.Sub(it.Since))))
			}
			row.Under = append(row.Under, look.Says(says...))
			hint := "kit reconcile"
			if it.Action != "" || it.State == "missing" {
				hint = "kit apply"
			}
			if !slices.Contains(hints, hint) {
				hints = append(hints, hint)
			}
		case it.Detail != "":
			row.Under = append(row.Under, todo(it.Detail))
		}
	}
	for _, hint := range hints {
		row.Under = append(row.Under, look.Todo(look.Cmd(hint)))
	}
	return row
}

// IsDrift is whether it is drift: something installed, declared, linked or
// set otherwise than the config says.
func IsDrift(it check.Item) bool {
	_, ok := driftStates[it.State]
	return ok
}

// applied is applying's last word, under its heading: the lights, the bar
// with how the run ended, then the whole work list as it finished.
func (p *Pretty) applied(e event.RunFinished) []string {
	out := []string{look.Cut(look.Lights(p.lamps()...), p.width), ""}
	out = append(out, p.foot(e)...)
	list := p.workList()
	out = append(append(out, ""), list...)
	for i, l := range out {
		out[i] = ansi.Truncate(l, p.width, "…")
	}
	return out
}
