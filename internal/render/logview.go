package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/logs"
)

// LogView shows a run's log, read from path: a heading, then each step, as
// the run's face showed it, with the commands it ran under it, each with its
// exit code and how long it took; then the run's summary.
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

	heading := bold.Render("kit "+run.Command) + faint.Render(" · "+run.Machine+" · "+run.Time.Format("2 Jan 2006 15:04:05"))
	if finished != nil {
		heading += faint.Render(" · " + duration(finished.DurationMS))
	}
	v.write(heading + "\n" + faint.Render(path) + "\n\n")

	titles := map[string]string{}
	column := 0
	for _, s := range run.Steps {
		titles[s.Name] = s.Title
		column = max(column, ansi.StringWidth(s.Title))
	}
	column += 3
	if cmds := commands[""]; len(cmds) > 0 {
		v.write(faint.Render("  run") + "\n")
		v.commands(cmds)
	}
	for _, name := range order {
		title := titles[name]
		pad := strings.Repeat(" ", max(column-ansi.StringWidth(title), 1))
		s, ok := steps[name]
		if !ok || s.Result == nil {
			v.write(red.Render("✗") + " " + title + pad + red.Render("didn't finish") + "\n")
			v.commands(commands[name])
			continue
		}
		took := faint.Render(duration(s.DurationMS)) + "  "
		if s.Result.State == check.Deferred {
			took = ""
		}
		v.write(stateMark[s.Result.State] + " " + title + pad + took + what(*s.Result) + "\n")
		v.commands(commands[name])
	}
	if finished != nil {
		v.write("\n" + summary(finished.Counts) + "\n")
	} else {
		v.write("\n" + red.Render("The run didn't finish") + "\n")
	}
	return v.err
}

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
		if c.Exit != nil && *c.Exit == 0 {
			v.write(faint.Render(line) + "\n")
			continue
		}
		v.write(red.Render(line) + "\n")
		if c.Error != "" {
			v.write(red.Render("      "+c.Error) + "\n")
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
