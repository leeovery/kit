package render

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
)

// The parts of Config a step counts towards, as its Part says: Packages,
// Settings, Files and Secrets are rolled up into a row each, the config
// repository's steps into its own.
const (
	PartPackages = "Packages"
	PartSettings = "Settings"
	PartFiles    = "Files"
	PartSecrets  = "Secrets"
	PartConfig   = "kit-config"
)

// rollups are Config's rows of counts, in order: what each says its count
// is, and which of its steps' counts it adds up.
var rollups = []struct{ part, verb, count string }{
	{PartPackages, "installed", "installed"},
	{PartSettings, "set", "installed"},
	{PartFiles, "linked", "linked"},
	{PartSecrets, "in place", "installed"},
}

// configStep is the step a run of changes ends with, committing and
// pushing them, named as the config repository is.
const configStep = PartConfig

// wholeMac are the commands that look at the whole Mac: they open with the
// wordmark and end with a summary. Run for steps named, they're aimed at
// those, as other commands are: a timeline, without the wordmark.
var wholeMac = []string{"status", "apply", "apply --plan", "nightly", "reconcile"}

// viewOrder is the order the views show areas in: the config's drift and
// its repository are one, Config.
var viewOrder = []string{"Backups", "Jobs", "Mac", "Config", "Steps", "Manual", "Checks"}

// viewArea is the area a step of area shows in.
func viewArea(area string) string {
	if area == "Drift" {
		return "Config"
	}
	return cmp.Or(area, "Checks")
}

// spinning are the running mark's frames, turning.
var spinning = []string{"◐", "◓", "◑", "◒"}

const spinEvery = 120 * time.Millisecond

// drawEvery is the least time between drawings of the live part for what a
// command prints.
const drawEvery = 50 * time.Millisecond

// Pretty is the face at a terminal, in kit's look. A command looking at the
// whole Mac opens with the wordmark, what ran, the Mac and when beside it;
// its steps show by area, Config rolled up, and a summary ends it. A
// command aimed at one thing is a timeline: a row a step, each as its turn
// comes. While steps run, a line says which. Its writer brings the colour
// down to what the terminal shows.
type Pretty struct {
	mu      sync.Mutex
	w       io.Writer
	width   int
	animate bool
	start   event.RunStarted
	whole   bool
	// home is whether the face is bare kit's while its checks run: the
	// wordmark with the Mac and the time, then the loader, and nothing more,
	// as bare kit's view follows.
	home    bool
	results map[string]event.StepFinished
	order   ordered
	running []runningStep
	// live is how many lines the live part, redrawn in place, takes on
	// screen: the loader, or a running step's row and what it printed.
	live int
	// output are the last lines each step's commands printed while it
	// changed things, and command the command printing them.
	output  map[string][]string
	command map[string]string
	frame   int
	// began is when the run began, by the clock, for how long it's taken;
	// drawn when the live part was last drawn.
	began, drawn time.Time
	// tab is whether the terminal shows a run's progress on its tab, as
	// Ghostty does.
	tab bool
	// opened is whether the run's heading is on screen: shown as kit
	// prepares the run, or as it starts; preparing is what kit's doing
	// before it starts.
	opened    bool
	preparing string
	// hidden is whether the cursor is hidden, while something is live.
	hidden bool
	// aside is whether the face has stepped aside for a question asked
	// mid-run: it draws nothing, and holds what it writes, till it's back.
	aside bool
	// full is whether applying's live view has the whole screen, shown what
	// it shows, and size how big the terminal is.
	full  bool
	shown []string
	// shownAll is what's left on screen of the run: its heading, and its
	// last word.
	shownAll []string
	size     func() (int, int)
	// out is what's being written, sent to the terminal whole at the end of
	// an event or a turn of the spinner, for the terminal to show at once.
	out     strings.Builder
	stop    chan struct{}
	stopped chan struct{}
	err     error
}

// NewPretty returns the pretty face, writing to w, which is width columns
// wide: its lines go no wider than kit's look allows. It animates while
// steps run when animate is true.
func NewPretty(w io.Writer, width int, animate bool) *Pretty {
	return &Pretty{w: w, width: min(max(width, 40), look.Width), animate: animate}
}

// ShowTabProgress has the face show a run's progress on the terminal's tab
// too, for a terminal that does: Ghostty.
func (p *Pretty) ShowTabProgress() *Pretty {
	p.tab = true
	return p
}

// homeLoader is bare kit's face while its checks run: the wordmark at once,
// then the loader, which its view replaces.
func homeLoader(w io.Writer, width int, animate bool) *Pretty {
	p := NewPretty(w, width, animate)
	p.home = true
	return p
}

func (p *Pretty) Emit(e event.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.flush()
	switch e := e.(type) {
	case event.Preparing:
		p.open(e.Command, e.Machine, e.Time, nil)
		p.preparing = e.Doing
		p.spin()
	case event.RunStarted:
		p.start, p.results = e, make(map[string]event.StepFinished, len(e.Steps))
		p.output, p.command, p.began = map[string][]string{}, map[string]string{}, time.Now()
		p.order.start(e.Steps)
		p.preparing = ""
		p.clearLive()
		p.open(e.Command, e.Machine, e.Time, e.Only)
		if p.animate && p.whole && p.applying() {
			p.enterFullScreen()
		}
	case event.StepStarted:
		p.running = append(without(p.running, e.Step), runningStep{step: e.Step, title: p.title(e.Step), doing: cmp.Or(e.Doing, "checking")})
		p.spin()
	case event.StepFinished:
		p.running = without(p.running, e.Step)
		p.results[e.Step] = e
		if !p.byArea() {
			if ready := p.order.finish(e); len(ready) > 0 {
				p.clearLive()
				for _, f := range ready {
					p.lines(look.Timeline("  ", p.width, p.row(f))...)
				}
			}
		}
		p.drawSpinner()
	case event.Output:
		line := strings.TrimRight(strings.ReplaceAll(ansi.Strip(e.Line), "\t", "    "), " ")
		p.output[e.Step] = append(lastOf(p.output[e.Step], keptFailing-1), line)
		p.command[e.Step] = e.Command
		// A command can print faster than a terminal is worth redrawing:
		// the spinner's turn draws what came since.
		if time.Since(p.drawn) >= drawEvery {
			p.drawSpinner()
		}
	case event.RunFinished:
		p.stopSpinner()
		var last []string
		switch {
		case p.home:
		case p.byArea() && p.applying():
			last = p.applied(e)
		case p.byArea():
			last = append(p.report(), p.foot(e)...)
		case p.whole:
			last = append([]string{""}, p.foot(e)...)
		}
		p.lines(last...)
		p.shownAll = append(p.shownAll, last...)
	}
}

// Shown is what the face has left on screen of the run, a line a line: its
// heading, and its last word.
func (p *Pretty) Shown() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.shownAll)
}

// open shows the run's heading, once: a blank line, then, for a command
// looking at the whole Mac, the wordmark with what ran, the Mac and when.
func (p *Pretty) open(command, machine string, at time.Time, only []string) {
	if p.opened {
		return
	}
	p.opened = true
	p.whole = p.home || slices.Contains(wholeMac, command) && len(only) == 0
	heading := []string{""}
	if p.whole {
		heading = append(append(heading, look.Head(look.Meta(command, machine, when(at))...)...), "")
	}
	p.lines(heading...)
	p.shownAll = heading
}

// byArea is whether the run shows its steps by area: one looking at the
// whole Mac, whose steps have areas. Reconciling's are things, not steps,
// and show as a timeline.
func (p *Pretty) byArea() bool {
	return p.whole && slices.ContainsFunc(p.start.Steps, func(s event.Step) bool { return s.Area != "" })
}

// applying is whether the run applies, so its report shows what changed and
// what needs attention, not every step.
func (p *Pretty) applying() bool { return p.start.Command == "apply" }

func (p *Pretty) step(name string) event.Step {
	for _, s := range p.start.Steps {
		if s.Name == name {
			return s
		}
	}
	return event.Step{Name: name}
}

func (p *Pretty) title(step string) string {
	s := p.step(step)
	return cmp.Or(s.Title, s.Name)
}

// when is a run's time, as the wordmark's line says it.
func when(t time.Time) string { return t.Format("Mon 2 Jan · 15:04") }

// report is the run's steps by area, a block an area, in the views'
// order.
func (p *Pretty) report() []string {
	areas := map[string][]event.StepFinished{}
	for _, s := range p.start.Steps {
		if f, ok := p.results[s.Name]; ok {
			a := viewArea(s.Area)
			areas[a] = append(areas[a], f)
		}
	}
	var out []string
	for _, area := range viewOrder {
		fs := areas[area]
		if len(fs) == 0 {
			continue
		}
		var rows []look.Row
		if area == "Config" {
			rows = p.configRows(fs)
		} else {
			for _, f := range fs {
				rows = append(rows, p.row(f))
			}
		}
		out = append(append(out, look.Block(area, p.width, rows...)...), "")
	}
	return out
}

// lamp is an area's light: how its steps stand at worst, and how many there
// are, when they're all well.
func lamp(area string, fs []event.StepFinished) look.Lamp {
	l := look.Lamp{Name: area, State: look.Done}
	for _, f := range fs {
		if s := state(f.Result.State); worse(s, l.State) {
			l.State = s
		}
	}
	if l.State == look.Done {
		l.Count = fmt.Sprint(len(fs))
	}
	return l
}

// state is how a step's result shows: a deferred step wasn't checked, so is
// skipped.
func state(s check.State) look.State {
	switch s {
	case check.Attention:
		return look.NeedsYou
	case check.Failed:
		return look.Failed
	case check.Deferred:
		return look.Skipped
	}
	return look.Done
}

// worse is whether a is worse than b: failed, then needs you, then skipped.
func worse(a, b look.State) bool {
	rank := map[look.State]int{look.Done: 0, look.Skipped: 1, look.NeedsYou: 2, look.Failed: 3}
	return rank[a] > rank[b]
}

// row is a step's row, as resultRow has it: a run of changes with nothing
// to commit skips that step; a step that applying did something about says
// it was applied.
func (p *Pretty) row(f event.StepFinished) look.Row {
	r := f.Result
	row := resultRow(p.title(f.Step), r)
	if out := p.output[f.Step]; r.State == check.Failed && len(out) > 0 {
		what, _, _ := strings.Cut(r.Reason, ":")
		row.Says = look.Says(look.Red(what), look.Dim(p.command[f.Step]))
		row.Under = append(look.Output(out...), row.Under...)
	}
	if f.Step == configStep && r.State == check.OK && r.Summary == "nothing changed" {
		row.State, row.Says = look.Skipped, look.Muted("nothing to commit")
	}
	if len(r.Done) > 0 && p.byArea() {
		row.Says = look.Says(look.Muted("applied"), row.Says)
	}
	return row
}

// saying is a result's words as a row says them: the parts of a summary
// after dots, the first in the state's colour.
func saying(s look.State, text string) string {
	parts := strings.Split(text, "; ")
	for i, part := range parts {
		if i == 0 {
			parts[i] = look.Words(s, part)
		} else {
			parts[i] = look.Muted(part)
		}
	}
	return look.Says(parts...)
}

// todo is what to do about something: one of kit's commands in white, other
// words muted.
func todo(detail string) string {
	if strings.HasPrefix(detail, "kit ") {
		return look.Todo(look.Cmd(detail))
	}
	return look.Todo(look.Muted(detail))
}

// configRows are Config's rows: what needs attention, a row a thing, saying
// what's wrong, for how long and what to run; then a row a part counting
// what's well, and the config repository's row.
func (p *Pretty) configRows(fs []event.StepFinished) []look.Row {
	var rows []look.Row
	counts := map[string]int{}
	quiet := map[string]map[string]int{}
	present := map[string]bool{}
	var repo []string
	for _, f := range fs {
		r, part := f.Result, p.step(f.Step).Part
		if r.State == check.Failed || r.State == check.Deferred {
			rows = append(rows, p.row(f))
		}
		for _, it := range r.Items {
			if it.Quiet == "" {
				rows = append(rows, p.thing(part, it))
				continue
			}
			if quiet[part] == nil {
				quiet[part] = map[string]int{}
			}
			quiet[part][it.Quiet]++
		}
		if r.State == check.Failed || r.State == check.Deferred {
			continue
		}
		switch part {
		case PartConfig:
			repo = append(repo, look.Muted(cmp.Or(r.Glance, strings.TrimPrefix(r.Summary, "all "))))
		case PartPackages, PartSettings, PartFiles, PartSecrets:
			present[part] = true
			for _, ru := range rollups {
				if ru.part == part {
					counts[part] += r.Counts[ru.count]
				}
			}
		default:
			if len(r.Items) == 0 {
				rows = append(rows, p.row(f))
			}
		}
	}
	for _, ru := range rollups {
		if !present[ru.part] {
			continue
		}
		says := []string{look.Muted(fmt.Sprintf("%d %s", counts[ru.part], ru.verb))}
		for _, reason := range []string{"new", "snoozed", "temporary"} {
			if n := quiet[ru.part][reason]; n > 0 {
				says = append(says, look.Dim(fmt.Sprintf("%d %s", n, quietWords[reason])))
			}
		}
		rows = append(rows, look.Row{State: look.Done, Name: ru.part, Says: look.Says(says...)})
	}
	if len(repo) > 0 {
		rows = append(rows, look.Row{State: look.Done, Name: PartConfig, Says: look.Says(repo...)})
	}
	return rows
}

// quietWords say why things don't need attention yet, after their count.
var quietWords = map[string]string{"new": "new", "snoozed": "snoozed", "temporary": "for now"}

// thing is a row for something that needs attention, of a part: its name,
// what's wrong, for how long, and what to run about it.
func (p *Pretty) thing(part string, it check.Item) look.Row {
	says := []string{look.Orange(wrong(part, it))}
	if _, drift := driftStates[it.State]; drift && it.Detail != "" {
		says = append(says, look.Muted(it.Detail))
	}
	if !it.Since.IsZero() {
		says = append(says, look.Muted(since(p.start.Time.Sub(it.Since))))
	}
	hint := "kit reconcile"
	if it.Action != "" || it.State == "missing" {
		hint = "kit apply"
	}
	row := look.Row{State: look.NeedsYou, Name: it.Name, Says: look.Says(append(says, look.Todo(look.Cmd(hint)))...)}
	// What to run is never cut off: when the row is too long for it, it
	// goes on the line under it.
	if ansi.StringWidth(look.Rows("  ", 1<<16, row)[0]) > p.width {
		row.Says, row.Under = look.Says(says...), []string{look.Todo(look.Cmd(hint))}
	}
	return row
}

// wrong says what's wrong with a thing of a part, in words: installed, not
// declared; or, of a setting, set, not declared.
func wrong(part string, it check.Item) string {
	verb := cmp.Or(map[string]string{PartSettings: "set", PartSecrets: "synced"}[part], "installed")
	switch it.State {
	case "extra":
		return verb + ", not declared"
	case "missing":
		return "declared, not " + verb
	case "unused-dependency":
		return "installed for something since removed"
	case "changed", "diverged":
		return "changed from what's declared"
	case "dead":
		return "a dead link"
	case "edited":
		return "edited, not committed"
	case "added":
		return "new, not committed"
	case "deleted":
		return "deleted, not committed"
	}
	return cmp.Or(it.Detail, strings.ReplaceAll(it.State, "-", " "))
}

// foot is how the run ended: the counts and how long it took; when
// applying, after the bar.
func (p *Pretty) foot(e event.RunFinished) []string {
	parts := tally(e.Counts)
	done := 0
	for _, f := range p.results {
		done += len(f.Result.Done)
	}
	settled := e.Counts[check.OK]
	if f, ok := p.results[configStep]; ok && f.Result.State == check.OK {
		settled--
	}
	switch {
	case p.applying() && done > 0:
		parts = append(parts, look.White(fmt.Sprintf("%d done", done)))
	case !p.byArea():
		parts = append(parts, look.White(fmt.Sprintf("%d settled", settled)))
	default:
		parts = append(parts, look.White(fmt.Sprintf("%d fine", e.Counts[check.OK])))
	}
	parts = append(parts, look.Muted(seconds(e.Duration)))
	if !p.applying() {
		return []string{look.Summary(parts...)}
	}
	ended := look.Done
	switch {
	case e.Counts[check.Failed] > 0:
		ended = look.Failed
	case e.Counts[check.Attention] > 0:
		ended = look.NeedsYou
	}
	n := len(p.start.Steps)
	return []string{look.Cut("  "+look.Bar(n, 0, n, ended)+"  "+look.Says(parts...), p.width)}
}

// tally is what went wrong in a run, counted: what failed, what needs
// attention, and what wasn't checked.
func tally(counts map[check.State]int) []string {
	var parts []string
	if n := counts[check.Failed]; n > 0 {
		parts = append(parts, look.Red(fmt.Sprintf("%d failed", n)))
	}
	if n := counts[check.Attention]; n > 0 {
		parts = append(parts, look.Orange(fmt.Sprintf("%d %s you", n, plural(n, "needs", "need"))))
	}
	if n := counts[check.Deferred]; n > 0 {
		parts = append(parts, look.Muted(fmt.Sprintf("%d not checked", n)))
	}
	return parts
}

// seconds is how long a run took, to a tenth of a second.
func seconds(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }

// spin starts the spinner, when the face animates and it isn't turning.
func (p *Pretty) spin() {
	if !p.animate || p.stop != nil {
		p.drawSpinner()
		return
	}
	p.stop, p.stopped = make(chan struct{}), make(chan struct{})
	go func(stop, stopped chan struct{}) {
		defer close(stopped)
		tick := time.NewTicker(spinEvery)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				p.mu.Lock()
				p.frame++
				p.drawSpinner()
				p.flush()
				p.mu.Unlock()
			}
		}
	}(p.stop, p.stopped)
	p.drawSpinner()
}

// drawSpinner draws the live part over the last: on a timeline, the running
// step's row, and the last lines its commands printed on the line under it;
// otherwise the loader, the bar, how many steps are done, and which are
// running. None when nothing runs, or the face doesn't animate.
func (p *Pretty) drawSpinner() {
	if !p.animate || p.aside {
		return
	}
	if p.tab && len(p.start.Steps) > 0 && len(p.running) > 0 {
		p.write(fmt.Sprintf("\x1b]9;4;1;%d\x07", len(p.results)*100/len(p.start.Steps)))
	}
	if p.full {
		p.drawFull()
		return
	}
	mark := look.Cyan(spinning[p.frame%len(spinning)])
	if len(p.running) == 0 {
		if p.preparing != "" && p.start.Command == "" {
			p.drawLive([]string{"  " + mark + " " + look.Muted(p.preparing)})
		} else {
			p.clearLive()
		}
		return
	}
	done, total := len(p.results), len(p.start.Steps)
	text := "  " + mark + " " + look.Bar(done, len(p.running), total, look.Done) + "  " + look.Says(look.White(fmt.Sprintf("%d of %d", done, total)), look.Muted(doing(p.running)))
	lines := []string{text}
	if !p.byArea() {
		r := p.running[0]
		says := []string{look.Cyan(r.doing)}
		if c := p.command[r.step]; c != "" {
			says = append(says, look.Dim(c))
		}
		lines = []string{"  " + mark + " " + look.Strong(r.title) + "  " + look.Says(says...)}
		for _, l := range lastOf(p.output[r.step], keptRunning) {
			lines = append(lines, "  "+look.Dim("│")+" "+look.Muted(l))
		}
	}
	p.drawLive(lines)
}

// The terminal's controls the live part uses: the cursor hidden and shown,
// and an update shown at once, where the terminal can.
const (
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
	syncStart  = "\x1b[?2026h"
	syncEnd    = "\x1b[?2026l"
)

// drawLive draws lines as the live part, over the last: back to its first
// line, each line written over in place, and what's left of the last
// cleared; the cursor hidden while it's live.
func (p *Pretty) drawLive(lines []string) {
	if !p.hidden {
		p.write(hideCursor)
		p.hidden = true
	}
	p.write("\r")
	if p.live > 1 {
		p.write(fmt.Sprintf("\x1b[%dA", p.live-1))
	}
	for i, l := range lines {
		if i > 0 {
			p.write("\n")
		}
		p.write(look.Cut(l, p.width) + "\x1b[K")
	}
	if len(lines) < p.live {
		p.write("\x1b[J")
	}
	p.live, p.drawn = len(lines), time.Now()
}

// How many of what a step's commands printed show: while it runs, and once
// it's failed.
const (
	keptRunning = 5
	keptFailing = 8
)

// lastOf is the last n of lines.
func lastOf(lines []string, n int) []string {
	return lines[max(len(lines)-n, 0):]
}

// clearLive takes the live part down: back to its first line, and clear
// from there.
func (p *Pretty) clearLive() {
	if p.live == 0 {
		return
	}
	p.write("\r")
	if p.live > 1 {
		p.write(fmt.Sprintf("\x1b[%dA", p.live-1))
	}
	p.write("\x1b[J")
	p.live = 0
}

// stopSpinner stops the spinner turning, and takes its line down. It
// releases the lock while the spinner's goroutine finishes, which takes it.
func (p *Pretty) stopSpinner() {
	if p.stop != nil {
		close(p.stop)
		stopped := p.stopped
		p.stop = nil
		p.mu.Unlock()
		<-stopped
		p.mu.Lock()
	}
	p.leaveFullScreen()
	p.clearLive()
	if p.hidden {
		p.write(showCursor)
		p.hidden = false
	}
	if p.tab {
		p.write("\x1b]9;4;0\x07")
	}
}

func (p *Pretty) lines(ls ...string) {
	for _, l := range ls {
		p.write(l + "\n")
	}
}

func (p *Pretty) write(s string) {
	p.out.WriteString(s)
}

// flush sends what's been written to the terminal, whole, for it to show at
// once where it can.
func (p *Pretty) flush() {
	if p.aside {
		return
	}
	if p.out.Len() == 0 || p.err != nil {
		p.out.Reset()
		return
	}
	text := p.out.String()
	p.out.Reset()
	if p.animate {
		text = syncStart + text + syncEnd
	}
	_, p.err = io.WriteString(p.w, text)
}

// Close stops the spinner, if it's turning, and returns the first error
// writing, if any.
func (p *Pretty) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopSpinner()
	p.flush()
	if p.err != nil {
		return fmt.Errorf("write to the terminal: %w", p.err)
	}
	return nil
}

// StepAside takes the live part down and keeps it down, for a question kit
// asks mid-run in its place, and returns what puts it back: the live part
// as it then stands, and what the face held meanwhile.
func (p *Pretty) StepAside() (back func()) {
	p.mu.Lock()
	full := p.full
	p.stopSpinner()
	p.flush()
	p.aside = true
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.aside = false
		if full {
			p.enterFullScreen()
		}
		if len(p.running) > 0 {
			p.spin()
		}
		p.flush()
	}
}

// Running are the titles of the steps running.
func (p *Pretty) Running() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	titles := make([]string, len(p.running))
	for i, r := range p.running {
		titles[i] = r.title
	}
	return titles
}

// runningStep is a step running, and what it's doing: checking or
// applying.
type runningStep struct {
	step, title, doing string
}

// without is running without step.
func without(running []runningStep, step string) []runningStep {
	return slices.DeleteFunc(slices.Clone(running), func(r runningStep) bool { return r.step == step })
}

// doing says what the steps running are doing, as in "applying Formulae ·
// checking Casks, App Store".
func doing(running []runningStep) string {
	var kinds []string
	titles := map[string][]string{}
	for _, r := range running {
		if _, ok := titles[r.doing]; !ok {
			kinds = append(kinds, r.doing)
		}
		titles[r.doing] = append(titles[r.doing], r.title)
	}
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = k + " " + strings.Join(titles[k], ", ")
	}
	return strings.Join(parts, " · ")
}
