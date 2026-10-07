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
	// spinning is whether the spinner's line is on screen.
	spinning bool
	frame    int
	stop     chan struct{}
	stopped  chan struct{}
	err      error
}

// NewPretty returns the pretty face, writing to w, which is width columns
// wide: its lines go no wider than kit's look allows. It animates while
// steps run when animate is true.
func NewPretty(w io.Writer, width int, animate bool) *Pretty {
	return &Pretty{w: w, width: min(max(width, 40), look.Width), animate: animate}
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
	switch e := e.(type) {
	case event.RunStarted:
		p.start, p.results = e, make(map[string]event.StepFinished, len(e.Steps))
		p.order.start(e.Steps)
		p.whole = p.home || slices.Contains(wholeMac, e.Command) && len(e.Only) == 0
		p.write("\n")
		if p.whole {
			p.lines(look.Head(look.Meta(e.Command, e.Machine, when(e.Time))...)...)
			p.write("\n")
		}
	case event.StepStarted:
		p.running = append(without(p.running, e.Step), runningStep{step: e.Step, title: p.title(e.Step), doing: cmp.Or(e.Doing, "checking")})
		p.spin()
	case event.StepFinished:
		p.running = without(p.running, e.Step)
		p.results[e.Step] = e
		if !p.byArea() {
			if ready := p.order.finish(e); len(ready) > 0 {
				p.clearSpinner()
				for _, f := range ready {
					p.lines(look.Timeline("  ", p.width, p.row(f))...)
				}
			}
		}
		p.drawSpinner()
	case event.RunFinished:
		p.stopSpinner()
		switch {
		case p.home:
		case p.byArea():
			p.lines(p.report()...)
			p.lines(p.foot(e)...)
		case p.whole:
			p.write("\n")
			p.lines(p.foot(e)...)
		}
	}
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

// report is the run's steps by area, a block an area, in the views' order;
// when applying, the lights lead, and only what changed or needs attention
// shows.
func (p *Pretty) report() []string {
	areas := map[string][]event.StepFinished{}
	for _, s := range p.start.Steps {
		if f, ok := p.results[s.Name]; ok {
			a := viewArea(s.Area)
			areas[a] = append(areas[a], f)
		}
	}
	var out []string
	var lamps []look.Lamp
	for _, area := range viewOrder {
		fs := areas[area]
		if len(fs) == 0 {
			continue
		}
		lamps = append(lamps, lamp(area, fs))
		var rows []look.Row
		if area == "Config" {
			rows = p.configRows(fs)
		} else {
			for _, f := range fs {
				if !p.applying() || changed(f) {
					rows = append(rows, p.row(f))
				}
			}
		}
		if len(rows) > 0 {
			out = append(append(out, look.Block(area, p.width, rows...)...), "")
		}
	}
	if p.applying() {
		out = append([]string{look.Cut(look.Lights(lamps...), p.width), ""}, out...)
	}
	return out
}

// changed is whether applying f's step did something, or left it needing
// attention.
func changed(f event.StepFinished) bool {
	return len(f.Result.Done) > 0 || f.Result.State != check.OK
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
// what's wrong, for how long and what to run; when applying, what it did;
// then, unless applying, a row a part counting what's well, and the config
// repository's row.
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
		for _, it := range r.Done {
			rows = append(rows, look.Row{State: look.Done, Name: it.Name, Says: look.Muted(past(it.Action))})
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
			if !p.applying() && len(r.Items) == 0 {
				rows = append(rows, p.row(f))
			}
		}
	}
	if p.applying() {
		return rows
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
				p.mu.Unlock()
			}
		}
	}(p.stop, p.stopped)
	p.drawSpinner()
}

// drawSpinner draws the loader's line over the last one: on a timeline, the
// running step's row; otherwise the bar, how many steps are done, and which
// are running. None when nothing runs, or the face doesn't animate.
func (p *Pretty) drawSpinner() {
	if !p.animate {
		return
	}
	p.clearSpinner()
	if len(p.running) == 0 {
		return
	}
	mark := look.Cyan(spinning[p.frame%len(spinning)])
	done, total := len(p.results), len(p.start.Steps)
	text := "  " + mark + " " + look.Bar(done, len(p.running), total, look.Done) + "  " + look.Says(look.White(fmt.Sprintf("%d of %d", done, total)), look.Muted(doing(p.running)))
	if !p.byArea() {
		r := p.running[0]
		text = "  " + mark + " " + look.Strong(r.title) + "  " + look.Cyan(r.doing)
	}
	p.write(look.Cut(text, p.width))
	p.spinning = true
}

// clearSpinner takes the spinner's line down.
func (p *Pretty) clearSpinner() {
	if p.spinning {
		p.write("\r\x1b[2K")
		p.spinning = false
	}
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
	p.clearSpinner()
}

func (p *Pretty) lines(ls ...string) {
	for _, l := range ls {
		p.write(l + "\n")
	}
}

func (p *Pretty) write(s string) {
	if p.err != nil {
		return
	}
	_, p.err = io.WriteString(p.w, s)
}

// Close stops the spinner, if it's turning, and returns the first error
// writing, if any.
func (p *Pretty) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopSpinner()
	if p.err != nil {
		return fmt.Errorf("write to the terminal: %w", p.err)
	}
	return nil
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
