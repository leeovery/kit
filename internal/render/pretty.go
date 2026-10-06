package render

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
)

// spinnerFrames turn while steps run. Braille is in every Mac's fonts, as
// Terminal.app shows them before any other font is installed.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerEvery = 80 * time.Millisecond

// The face's styles, in the terminal's own basic colours, so they follow
// its theme, light or dark.
var (
	bold      = lipgloss.NewStyle().Bold(true)
	faint     = lipgloss.NewStyle().Faint(true)
	accent    = lipgloss.NewStyle().Foreground(lipgloss.Cyan).Bold(true)
	green     = lipgloss.NewStyle().Foreground(lipgloss.Green)
	yellow    = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	red       = lipgloss.NewStyle().Foreground(lipgloss.Red)
	stateMark = map[check.State]string{
		check.OK:        green.Render("✓"),
		check.Attention: yellow.Render("!"),
		check.Failed:    red.Render("✗"),
		check.Deferred:  faint.Render("·"),
	}
)

// banner is kit's wordmark, which status and apply open with. Block
// elements and box drawing are in every Mac's fonts, as Terminal.app shows
// them before any other font is installed.
var banner = []string{
	"██╗  ██╗ ██╗ ████████╗",
	"██║ ██╔╝ ██║ ╚══██╔══╝",
	"█████╔╝  ██║    ██║",
	"██╔═██╗  ██║    ██║",
	"██║  ██╗ ██║    ██║",
	"╚═╝  ╚═╝ ╚═╝    ╚═╝",
}

// bannerCommands are the commands that open with the banner: the long
// reports. The others open with a line.
var bannerCommands = []string{"status", "apply", "apply --plan"}

// Layout: a step's mark is indented markIndent; its title follows two
// columns on; its items' bullets are two columns in from the title.
const (
	markIndent = 4
	titleAt    = markIndent + 3
	itemAt     = titleAt + 2
	// maxItems is how many of a step's items are shown before the rest are
	// counted.
	maxItems = 8
	// ruleWidth is the widest a rule goes.
	ruleWidth = 76
)

// Pretty is the face at a terminal: kit's banner or a heading line, a
// spinner while steps run, then the steps under their areas, each with what
// needs attention under it, one thing a line, and a summary. Its writer
// brings the colour down to what the terminal shows.
type Pretty struct {
	mu      sync.Mutex
	w       io.Writer
	width   int
	animate bool
	steps   []event.Step
	results map[string]event.StepFinished
	// column is where steps' text starts, past the widest title, and
	// nameWidth where a step's things' tags start, past their names.
	column    int
	nameWidth int
	running   []runningStep
	// spinning is whether the spinner's line is on screen.
	spinning bool
	frame    int
	stop     chan struct{}
	stopped  chan struct{}
	err      error
}

// NewPretty returns the pretty face, writing to w, which is width columns
// wide. It animates a spinner while steps run when animate is true.
func NewPretty(w io.Writer, width int, animate bool) *Pretty {
	return &Pretty{w: w, width: max(width, 40), animate: animate}
}

func (p *Pretty) Emit(e event.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch e := e.(type) {
	case event.RunStarted:
		p.steps, p.results = e.Steps, make(map[string]event.StepFinished, len(e.Steps))
		for _, s := range e.Steps {
			p.column = max(p.column, ansi.StringWidth(p.title(s.Name)))
		}
		p.column += titleAt + 3
		p.writeHeading(e)
	case event.StepStarted:
		p.running = append(without(p.running, e.Step), runningStep{step: e.Step, title: p.title(e.Step), doing: cmp.Or(e.Doing, "checking")})
		p.spin()
	case event.StepFinished:
		p.running = without(p.running, e.Step)
		p.results[e.Step] = e
		p.drawSpinner()
	case event.RunFinished:
		p.stopSpinner()
		p.writeSteps()
		p.writeSummary(e.Counts)
	}
}

// title is a step's title, or its name when it has none.
func (p *Pretty) title(step string) string {
	for _, s := range p.steps {
		if s.Name == step && s.Title != "" {
			return s.Title
		}
	}
	return step
}

// writeHeading writes the run's heading: the banner, with the command, the
// Mac and the time beside its last line, or under it on a narrow terminal;
// or, for a short command, a line and a rule.
func (p *Pretty) writeHeading(e event.RunStarted) {
	about := e.Command + " · " + e.Machine
	if !e.Time.IsZero() {
		about += " · " + e.Time.Format("15:04")
	}
	if !slices.Contains(bannerCommands, e.Command) {
		p.write("\n  " + accent.Render("kit") + " " + bold.Render(e.Command) + faint.Render(" · "+strings.TrimPrefix(about, e.Command+" · ")) + "\n")
		p.write("  " + faint.Render(p.rule()) + "\n\n")
		return
	}
	p.write("\n")
	last := len(banner) - 1
	for i, line := range banner {
		text := "  " + accent.Render(line)
		if i == last && 2+ansi.StringWidth(line)+3+ansi.StringWidth(about) <= p.width {
			text += "   " + faint.Render(about)
		}
		p.write(text + "\n")
	}
	if 2+ansi.StringWidth(banner[last])+3+ansi.StringWidth(about) > p.width {
		p.write("  " + faint.Render(about) + "\n")
	}
	p.write("\n")
}

// rule is a horizontal line as wide as the terminal allows.
func (p *Pretty) rule() string {
	return strings.Repeat("─", min(p.width, ruleWidth)-2)
}

// writeSteps writes every step's result, under its area, the areas in bare
// kit's order; a run whose steps have no areas (adding, removing,
// reconciling) has no headings.
func (p *Pretty) writeSteps() {
	byArea := map[string][]event.Step{}
	areas := false
	for _, s := range p.steps {
		area := cmp.Or(s.Area, "Checks")
		areas = areas || s.Area != ""
		byArea[area] = append(byArea[area], s)
	}
	for _, area := range areaOrder {
		steps := byArea[area]
		if len(steps) == 0 {
			continue
		}
		if areas {
			head := "  " + bold.Render(area) + " "
			p.write(head + faint.Render(strings.Repeat("─", max(min(p.width, ruleWidth)-ansi.StringWidth(head), 4))) + "\n")
		}
		for _, s := range steps {
			if f, ok := p.results[s.Name]; ok {
				p.writeStep(f)
			}
		}
		p.write("\n")
	}
}

// writeStep writes a step's line, then what applying it did and what needs
// attention, a thing a line.
func (p *Pretty) writeStep(f event.StepFinished) {
	r := f.Result
	title := p.title(f.Step)
	text := what(r)
	switch r.State {
	case check.Failed:
		text = red.Render(text)
	case check.Deferred:
		text = faint.Render(text)
	}
	pad := strings.Repeat(" ", max(p.column-titleAt-ansi.StringWidth(title), 1))
	// A long summary wraps under itself, never past the terminal's edge.
	lines := strings.Split(ansi.Wordwrap(text, max(p.width-p.column-1, 20), " ,"), "\n")
	p.write(strings.Repeat(" ", markIndent) + stateMark[r.State] + "  " + title + pad + lines[0] + "\n")
	for _, line := range lines[1:] {
		p.write(strings.Repeat(" ", p.column) + strings.TrimLeft(line, " ") + "\n")
	}
	items := slices.Concat(
		slices.DeleteFunc(slices.Clone(r.Items), func(it check.Item) bool { return it.Quiet != "" }),
		slices.DeleteFunc(slices.Clone(r.Items), func(it check.Item) bool { return it.Quiet == "" }),
	)
	// Tags line up a column past the longest name shown, while that leaves
	// them room.
	p.nameWidth = 0
	for _, it := range slices.Concat(r.Done, items) {
		p.nameWidth = max(p.nameWidth, ansi.StringWidth(it.Name))
	}
	p.nameWidth = min(p.nameWidth, (p.width-itemAt-2)*2/3)
	for i, it := range r.Done {
		if i == maxItems-2 && len(r.Done) > maxItems {
			p.writeMore(len(r.Done)-i, past(it.Action))
			break
		}
		p.writeItem(it.Name, green.Render(past(it.Action)), false)
	}
	for i, it := range items {
		if i == maxItems-2 && len(items) > maxItems {
			p.writeMore(len(items)-i, "")
			break
		}
		p.writeThing(it)
	}
}

// writeThing writes an item that needs attention: a problem with what to do
// under it; anything else with what's wrong beside it, or under it when
// there's no room.
func (p *Pretty) writeThing(it check.Item) {
	if it.State == "problem" || it.State == "manual" {
		p.writeItem(yellow.Render(it.Name), "", false)
		if it.Detail != "" {
			p.writeUnder(faint.Render("→ " + it.Detail))
		}
		return
	}
	tags := []string{label(it.State)}
	if it.Quiet != "" {
		tags = append(tags, cmp.Or(quietTags[it.Quiet], it.Quiet))
	}
	if it.Action != "" {
		tags = append(tags, "to "+it.Action)
	}
	if it.Detail != "" {
		tags = append(tags, it.Detail)
	}
	tag := strings.Join(tags, " · ")
	if it.Quiet != "" {
		p.writeItem(faint.Render(it.Name), faint.Render(tag), true)
		return
	}
	p.writeItem(it.Name, yellow.Render(tag), false)
}

// quietTags say briefly why an item doesn't need attention yet.
var quietTags = map[string]string{"new": "new", "snoozed": "snoozed", "temporary": "temporary"}

// label is an item's state as people read it: not declared, for extra.
func label(state string) string {
	if forms, ok := itemLabels[state]; ok {
		return forms[0]
	}
	return strings.ReplaceAll(state, "-", " ")
}

// writeItem writes a thing's line, its bullet under the step's title, and
// tag beside it when both fit, else under it; a name too long for a line is
// cut short.
func (p *Pretty) writeItem(name, tag string, quiet bool) {
	room := p.width - itemAt - 2
	bullet := "·"
	if quiet {
		bullet = faint.Render(bullet)
	}
	lead := strings.Repeat(" ", itemAt) + bullet + " "
	if tag == "" {
		p.write(lead + ansi.Truncate(name, room, "…") + "\n")
		return
	}
	if w := max(p.nameWidth, ansi.StringWidth(name)); w+3+ansi.StringWidth(tag) <= room {
		p.write(lead + name + strings.Repeat(" ", w-ansi.StringWidth(name)+3) + tag + "\n")
		return
	}
	p.write(lead + ansi.Truncate(name, room, "…") + "\n")
	p.writeUnder(tag)
}

// writeUnder writes text on a line of its own, under a thing's name.
func (p *Pretty) writeUnder(text string) {
	p.write(strings.Repeat(" ", itemAt+2) + ansi.Truncate(text, p.width-itemAt-2, "…") + "\n")
}

// writeMore counts the things left unshown, done as verb when it's given.
func (p *Pretty) writeMore(n int, verb string) {
	text := fmt.Sprintf("… and %d more", n)
	if verb != "" {
		text += " " + verb
	}
	p.write(strings.Repeat(" ", itemAt) + faint.Render(text) + "\n")
}

// writeSummary writes the run's last lines: a rule, then how its steps
// stood, marked.
func (p *Pretty) writeSummary(counts map[check.State]int) {
	mark := stateMark[check.OK]
	style := green
	switch {
	case counts[check.Failed] > 0:
		mark, style = stateMark[check.Failed], red
	case counts[check.Attention]+counts[check.Deferred] > 0:
		mark, style = stateMark[check.Attention], yellow
	}
	p.write("  " + faint.Render(p.rule()) + "\n")
	p.write(strings.Repeat(" ", markIndent) + mark + "  " + style.Render(summary(counts)) + "\n\n")
}

// spin starts the spinner, when the face animates and it isn't turning.
func (p *Pretty) spin() {
	if !p.animate || p.stop != nil {
		p.drawSpinner()
		return
	}
	p.stop, p.stopped = make(chan struct{}), make(chan struct{})
	go func(stop, stopped chan struct{}) {
		defer close(stopped)
		tick := time.NewTicker(spinnerEvery)
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

// drawSpinner draws the spinner's line, naming the steps running, over the
// last one: none when nothing runs or the face doesn't animate.
func (p *Pretty) drawSpinner() {
	if !p.animate {
		return
	}
	p.clearSpinner()
	if len(p.running) == 0 {
		return
	}
	text := fmt.Sprintf("%s %d of %d · %s", spinnerFrames[p.frame%len(spinnerFrames)], len(p.results), len(p.steps), doing(p.running))
	p.write(strings.Repeat(" ", markIndent) + faint.Render(ansi.Truncate(text, p.width-markIndent-1, "…")))
	p.spinning = true
}

// clearSpinner takes the spinner's line down.
func (p *Pretty) clearSpinner() {
	if p.spinning {
		p.write("\r" + ansi.EraseEntireLine)
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
	var groups []string
	titles := map[string][]string{}
	for _, r := range running {
		if _, ok := titles[r.doing]; !ok {
			groups = append(groups, r.doing)
		}
		titles[r.doing] = append(titles[r.doing], r.title)
	}
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = g + " " + strings.Join(titles[g], ", ")
	}
	return strings.Join(parts, " · ")
}
