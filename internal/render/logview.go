package render

import (
	"cmp"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/look"
)

// LogView shows a run's log, read from path, in plain lines: a heading, then
// each step, with the commands it ran under it, each with its exit code and
// how long it took; then the run's summary.
func LogView(w io.Writer, path string, records []logs.Record) error {
	v := logView{w: w}
	var run logs.Record
	var finished *logs.Record
	steps := map[string]logs.Record{}
	commands := map[string][]logs.Record{}
	var order []string
	for _, r := range records {
		switch r.Event {
		case "run_started":
			run = r
			for _, s := range r.Steps {
				order = append(order, s.Name)
			}
		case "step_finished":
			steps[r.Step] = r
		case "command":
			commands[r.Step] = append(commands[r.Step], r)
		case "run_finished":
			finished = &r
		}
	}

	heading := "kit " + run.Command + " · " + run.Machine + " · " + run.Time.Format("2 Jan 2006 15:04:05")
	if finished != nil {
		heading += " · " + duration(finished.DurationMS)
	}
	v.write(heading + "\n" + path + "\n\n")

	titles := map[string]string{}
	column := 0
	for _, s := range run.Steps {
		titles[s.Name] = s.Title
		column = max(column, ansi.StringWidth(s.Title))
	}
	column += 3
	if cmds := commands[""]; len(cmds) > 0 {
		v.write("  run\n")
		v.commands(cmds)
	}
	for _, name := range order {
		title := titles[name]
		pad := strings.Repeat(" ", max(column-ansi.StringWidth(title), 1))
		s, ok := steps[name]
		if !ok || s.Result == nil {
			v.write("✗ " + title + pad + "didn't finish\n")
			v.commands(commands[name])
			continue
		}
		took := duration(s.DurationMS) + "  "
		if s.Result.State == check.Deferred {
			took = ""
		}
		v.write(plainMarks[s.Result.State] + " " + title + pad + took + what(*s.Result) + "\n")
		v.commands(commands[name])
	}
	if finished != nil {
		v.write("\n" + summary(finished.Counts) + "\n")
	} else {
		v.write("\nThe run didn't finish\n")
	}
	return v.err
}

// plainMarks are the states' marks in plain lines.
var plainMarks = map[check.State]string{check.OK: "✓", check.Attention: "!", check.Failed: "✗", check.Deferred: "·"}

type logView struct {
	w   io.Writer
	err error
}

// commands writes a step's commands, a line each: how it ended and how long
// it took, in columns, then the command, with what went wrong under one that
// failed.
func (v *logView) commands(cmds []logs.Record) {
	for _, c := range cmds {
		var outcome string
		switch {
		case c.Exit != nil && *c.Exit >= 0:
			outcome = fmt.Sprintf("exit %d", *c.Exit)
		case c.DurationMS > 0:
			outcome = "ended"
		default:
			outcome = "didn't run"
		}
		line := fmt.Sprintf("    %-10s  %6s  %s", outcome, duration(c.DurationMS), c.Command)
		v.write(line + "\n")
		if (c.Exit == nil || *c.Exit != 0) && c.Error != "" {
			v.write("      " + c.Error + "\n")
		}
	}
}

func (v *logView) write(s string) {
	if v.err != nil {
		return
	}
	_, v.err = io.WriteString(v.w, s)
}

// duration says how long ms milliseconds is, to a tenth of a second.
func duration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// LogRun shows a run's log in kit's look, width columns wide: the run as a
// row, how it stood and when; then its steps on a timeline, each after the
// time it started, the commands it ran on the line under it, each with how
// it ended and how long it took; then the run's summary.
func LogRun(w io.Writer, width int, records []logs.Record) error {
	width = min(max(width, 40), look.Width)
	var run logs.Record
	var finished *logs.Record
	results := map[string]logs.Record{}
	started := map[string]time.Time{}
	commands := map[string][]logs.Record{}
	for _, r := range records {
		switch r.Event {
		case "run_started":
			run = r
		case "step_started":
			if _, ok := started[r.Step]; !ok {
				started[r.Step] = r.Time
			}
		case "step_finished":
			results[r.Step] = r
		case "command":
			commands[r.Step] = append(commands[r.Step], r)
		case "run_finished":
			finished = &r
		}
	}
	worst := look.Done
	for _, r := range results {
		if r.Result != nil && worse(state(r.Result.State), worst) {
			worst = state(r.Result.State)
		}
	}
	if finished == nil {
		worst = look.Failed
	}
	head := look.Row{State: worst, Name: strings.Join(append([]string{"kit", run.Command}, run.Only...), " "), Says: look.Muted(run.Time.Format("Mon 2 Jan 15:04"))}
	for _, c := range commands[""] {
		head.Under = append(head.Under, ran(c, width-4)...)
	}
	out := append([]string{""}, look.Rows("  ", width, head)...)
	out = append(out, "")
	for _, s := range run.Steps {
		r, ok := results[s.Name]
		row := look.Row{State: look.Failed, Name: cmp.Or(s.Title, s.Name), Says: look.Red("didn't finish")}
		if ok && r.Result != nil {
			row = resultRow(row.Name, *r.Result)
		}
		var under []string
		for _, c := range commands[s.Name] {
			under = append(under, ran(c, width-logGutter-2)...)
		}
		row.Under = append(under, row.Under...)
		at := started[s.Name]
		if at.IsZero() {
			at = r.Time
		}
		out = append(out, logStep(at, width, row)...)
	}
	out = append(out, "")
	if finished == nil {
		out = append(out, look.Summary(look.Red("the run didn't finish")))
	} else {
		parts := append(tally(finished.Counts), look.White(fmt.Sprintf("%d fine", finished.Counts[check.OK])), look.Muted(duration(finished.DurationMS)))
		out = append(out, look.Summary(parts...))
	}
	_, err := io.WriteString(w, strings.Join(out, "\n")+"\n")
	return err
}

// logGutter is how far a step's row is in, in a run's log: past the margin
// and the time it started.
const logGutter = 12

// logStep is a step of a run's log: the time it started, then its row; what
// it ran on the timeline under it.
func logStep(at time.Time, width int, row look.Row) []string {
	when := strings.Repeat(" ", 8)
	if !at.IsZero() {
		when = look.Dim(at.Format("15:04:05"))
	}
	lines := look.Timeline("", width-logGutter, row)
	for i, l := range lines {
		if i == 0 {
			lines[i] = "  " + when + "  " + l
		} else {
			lines[i] = strings.Repeat(" ", logGutter) + l
		}
	}
	return lines
}

// ran is a command a run's step ran, as its log has it: the command, how it
// ended and how long it took, red when it failed, with what went wrong.
func ran(c logs.Record, w int) []string {
	took := duration(c.DurationMS)
	var line string
	switch {
	case c.Exit != nil && *c.Exit >= 0:
		line = look.Ran(c.Command, *c.Exit, took, w)
	case c.DurationMS > 0:
		line = look.Says(look.Red(look.Squeeze(c.Command, w-ansi.StringWidth(" · ended · "+took))), look.Red("ended"), look.Red(took))
	default:
		line = look.Says(look.Red(look.Squeeze(c.Command, w-ansi.StringWidth(" · didn't run"))), look.Red("didn't run"))
	}
	out := []string{line}
	if (c.Exit == nil || *c.Exit != 0) && c.Error != "" {
		out = append(out, look.Muted(c.Error))
	}
	return out
}

// resultRow is a step's row from its result: its title, and what the
// result says, its parts after dots; what to do about what needs attention
// in it, on the lines under it.
func resultRow(title string, r check.Result) look.Row {
	row := look.Row{State: state(r.State), Name: title}
	switch {
	case r.State == check.Failed && strings.HasPrefix(r.Reason, "couldn't"):
		row.Says = saying(look.Failed, r.Reason)
	case r.State == check.Failed:
		row.Says = look.Says(look.Red("couldn't check"), look.Muted(r.Reason))
	case r.State == check.Deferred:
		row.Says = look.Says(look.Muted("not checked"), look.Muted(r.Reason))
	default:
		row.Says = saying(row.State, r.Summary)
	}
	for _, it := range r.Items {
		if it.Quiet == "" && it.Detail != "" {
			row.Under = append(row.Under, todo(it.Detail))
		}
	}
	return row
}
