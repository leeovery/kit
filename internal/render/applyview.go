package render

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
)

// The terminal's controls the full screen uses: the alternate screen, which
// keeps what was on screen beneath it, and the cursor put in place.
const (
	enterFull = "\x1b[?1049h\x1b[2J"
	leaveFull = "\x1b[?1049l"
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
	return max(h, 16)
}

// enterFullScreen has applying's live view take the whole screen while it
// runs: what was on screen stays beneath it, for the report to follow.
func (p *Pretty) enterFullScreen() {
	p.full, p.shown = true, nil
	p.write(enterFull)
	if !p.hidden {
		p.write(hideCursor)
		p.hidden = true
	}
}

// leaveFullScreen gives the screen back as it was, the run's heading on it.
func (p *Pretty) leaveFullScreen() {
	if p.full {
		p.write(leaveFull)
		p.full, p.shown = false, nil
	}
}

// drawFull draws applying's live view, writing only the lines that changed
// since the last time: the wordmark, the lights, the bar with how many
// steps are done, running and how long it's taken, then the work list, a
// block an area, each step's row as it stands, scrolled to keep what's
// running in view.
func (p *Pretty) drawFull() {
	done, total := len(p.results), len(p.start.Steps)
	top := append([]string{""}, look.Head(look.Meta(p.start.Command, p.start.Machine, when(p.start.Time))...)...)
	top = append(top, "", look.Lights(p.lamps()...),
		"  "+look.Bar(done, len(p.running), total, look.Done)+"  "+look.Says(look.White(fmt.Sprintf("%d of %d", done, total)), look.Cyan(fmt.Sprintf("%d running", len(p.running))), look.Muted(seconds(time.Since(p.began)))),
		"")
	list, focus := p.workList(false)
	room := p.height() - len(top) - 1
	if len(list) > room {
		from := min(max(focus-room/3, 0), len(list)-room)
		list = list[from : from+room]
	}
	frame := append(top, list...)
	for i, l := range frame {
		l = look.Cut(l, p.width)
		if i < len(p.shown) && p.shown[i] == l {
			continue
		}
		p.write(fmt.Sprintf("\x1b[%d;1H", i+1) + l + "\x1b[K")
	}
	if len(frame) < len(p.shown) {
		p.write(fmt.Sprintf("\x1b[%d;1H\x1b[J", len(frame)+1))
	}
	p.shown = make([]string, len(frame))
	for i, l := range frame {
		p.shown[i] = look.Cut(l, p.width)
	}
	p.drawn = time.Now()
}

// lamps are the run's lights as it stands: an area running, waiting, or
// done and how it stood.
func (p *Pretty) lamps() []look.Lamp {
	running := map[string]bool{}
	for _, r := range p.running {
		running[r.step] = true
	}
	var lamps []look.Lamp
	for _, area := range viewOrder {
		var steps []event.Step
		var finished []event.StepFinished
		now := false
		for _, s := range p.start.Steps {
			if viewArea(s.Area) != area {
				continue
			}
			steps = append(steps, s)
			if f, ok := p.results[s.Name]; ok {
				finished = append(finished, f)
			}
			now = now || running[s.Name]
		}
		switch {
		case len(steps) == 0:
			continue
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

// workList is applying's list of steps, a block an area in the views'
// order, its header counting how many are done, each step a row: running,
// waiting, or as it finished; done says the list is the run's last word,
// its headers without the count. focus is the line of the first step
// running, or the last finished, for a window on the list to keep in view.
func (p *Pretty) workList(final bool) (lines []string, focus int) {
	running := map[string]runningStep{}
	for _, r := range p.running {
		running[r.step] = r
	}
	focus = -1
	last := 0
	for _, area := range viewOrder {
		var steps []event.Step
		finished := 0
		for _, s := range p.start.Steps {
			if viewArea(s.Area) == area {
				steps = append(steps, s)
				if _, ok := p.results[s.Name]; ok {
					finished++
				}
			}
		}
		if len(steps) == 0 {
			continue
		}
		note := fmt.Sprintf("%d of %d", finished, len(steps))
		if final {
			note = ""
		}
		lines = append(lines, look.Header(area, note))
		for _, s := range steps {
			var row look.Row
			r, isRunning := running[s.Name]
			f, isDone := p.results[s.Name]
			switch {
			case isRunning:
				says := []string{look.Cyan(r.doing)}
				if c := p.command[s.Name]; c != "" {
					says = append(says, look.Dim(c))
				}
				row = look.Row{State: look.Running, Name: r.title, Says: look.Says(says...)}
				if focus < 0 {
					focus = len(lines)
				}
			case isDone:
				row = p.workRow(f, s.Part)
				last = len(lines)
			default:
				row = look.Row{State: look.Queued, Name: p.title(s.Name), Says: look.Muted("waiting")}
			}
			lines = append(lines, look.Rows("  ", p.width, row)...)
		}
		lines = append(lines, "")
	}
	if focus < 0 {
		focus = last
	}
	return lines[:max(len(lines)-1, 0)], focus
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
		row.Under = look.Output(out...)
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
	out := []string{look.Cut(look.Lights(p.lamps()...), p.width)}
	out = append(out, p.foot(e)...)
	list, _ := p.workList(true)
	out = append(append(out, ""), list...)
	for i, l := range out {
		out[i] = ansi.Truncate(l, p.width, "…")
	}
	return out
}
