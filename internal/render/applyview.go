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
// taken, then the work list, a block an area, each step's row as it stands,
// a window on it around what's running, so it's never taller than the
// terminal.
func (p *Pretty) applyLive() []string {
	done, total := len(p.results), len(p.start.Steps)
	top := []string{look.Lights(p.lamps()...), "",
		"  " + look.Bar(done, len(p.running), total, look.Done) + "  " + look.Says(look.White(fmt.Sprintf("%d of %d", done, total)), look.Cyan(fmt.Sprintf("%d running", len(p.running))), look.Muted(seconds(time.Since(p.began)))),
		""}
	list, focus := p.workList(false)
	if room := max(p.height()-1-len(top), 3); len(list) > room {
		from := min(max(focus-room/3, 0), len(list)-room)
		// A window starting between two areas starts with the next.
		if list[from] == "" {
			from++
		}
		list = list[from:min(from+room, len(list))]
	}
	return append(top, list...)
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

// workList is applying's list of steps, a block an area in the views'
// order, its header counting how many are done, each step a row: running,
// waiting on you, waiting, or as it finished; done says the list is the
// run's last word, its headers without the count. The boot's steps, shown
// as it ran, have their light, and a row here only once one isn't well.
// focus is the line of the first step running, or the last finished, for a
// window on the list to keep in view.
func (p *Pretty) workList(final bool) (lines []string, focus int) {
	running := map[string]runningStep{}
	for _, r := range p.running {
		running[r.step] = r
	}
	focus = -1
	last := 0
	for _, area := range viewOrder {
		var steps, shown []event.Step
		finished := 0
		for _, s := range p.start.Steps {
			if viewArea(s.Area) != area {
				continue
			}
			steps = append(steps, s)
			f, ok := p.results[s.Name]
			if ok {
				finished++
			}
			if area != boot.AreaBooted || ok && f.Result.State != check.OK {
				shown = append(shown, s)
			}
		}
		if len(shown) == 0 {
			continue
		}
		note := fmt.Sprintf("%d of %d", finished, len(steps))
		if final {
			note = ""
		}
		lines = append(lines, look.Header(area, note))
		for _, s := range shown {
			var row look.Row
			r, isRunning := running[s.Name]
			f, isDone := p.results[s.Name]
			switch {
			case isRunning && r.todo != "":
				// Waiting on you: what for, then what to do, under it.
				row = look.Row{State: look.NeedsYou, Name: r.title, Says: look.Orange(r.doing), Under: todoLines(r.todo, p.width-6)}
				if focus < 0 {
					focus = len(lines)
				}
			case isRunning:
				says := []string{look.Cyan(r.doing)}
				if c := p.command[s.Name]; c != "" {
					says = append(says, look.Dim(c))
				}
				// What the command running prints shows under its row, its
				// last lines, on a line from its mark.
				row = look.Row{State: look.Running, Name: r.title, Says: look.Says(says...), Under: look.Output(lastOf(p.output[s.Name], keptRunning)...)}
				if focus < 0 {
					focus = len(lines)
				}
			case isDone:
				row = p.workRow(f, s.Part)
				last = len(lines)
			default:
				row = look.Row{State: look.Queued, Name: p.title(s.Name), Says: look.Muted(p.waiting(s))}
			}
			if isRunning || p.output[s.Name] != nil && isDone && f.Result.State == check.Failed {
				lines = append(lines, look.Timeline("  ", p.width, row)...)
			} else {
				lines = append(lines, look.Rows("  ", p.width, row)...)
			}
		}
		lines = append(lines, "")
	}
	if focus < 0 {
		focus = last
	}
	return lines[:max(len(lines)-1, 0)], focus
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
	list, _ := p.workList(true)
	out = append(append(out, ""), list...)
	for i, l := range out {
		out[i] = ansi.Truncate(l, p.width, "…")
	}
	return out
}
