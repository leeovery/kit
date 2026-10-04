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

// Pretty is the face at a terminal: a heading, a line for each step with
// what needs attention under it, a spinner while steps run, and a summary.
// Its writer brings the colour down to what the terminal shows.
type Pretty struct {
	mu      sync.Mutex
	w       io.Writer
	width   int
	animate bool
	order   ordered
	// column is where steps' text starts, past the widest title.
	column  int
	running []runningStep
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
		p.order.start(e.Steps)
		for _, s := range e.Steps {
			p.column = max(p.column, ansi.StringWidth(p.order.title(s.Name)))
		}
		p.column += 3
		p.write(bold.Render("kit "+e.Command) + faint.Render(" · "+e.Machine) + "\n\n")
	case event.StepStarted:
		p.running = append(without(p.running, e.Step), runningStep{step: e.Step, title: p.order.title(e.Step), doing: cmp.Or(e.Doing, "checking")})
		p.spin()
	case event.StepFinished:
		p.running = without(p.running, e.Step)
		ready := p.order.finish(e)
		if len(ready) > 0 {
			p.clearSpinner()
			for _, f := range ready {
				p.writeStep(f)
			}
		}
		p.drawSpinner()
	case event.RunFinished:
		p.stopSpinner()
		line := summary(e.Counts)
		style := green
		if e.Counts[check.Attention]+e.Counts[check.Failed]+e.Counts[check.Deferred] > 0 {
			style = yellow
		}
		p.write("\n" + style.Render(line) + "\n")
	}
}

// writeStep writes a step's line, and what needs attention under it.
func (p *Pretty) writeStep(f event.StepFinished) {
	r := f.Result
	title := p.order.title(f.Step)
	text := what(r)
	switch r.State {
	case check.Failed:
		text = red.Render(text)
	case check.Deferred:
		text = faint.Render(text)
	}
	pad := strings.Repeat(" ", max(p.column-ansi.StringWidth(title), 1))
	p.write(stateMark[r.State] + " " + title + pad + text + "\n")
	indent := strings.Repeat(" ", 2+p.column)
	for _, g := range groupItems(r.Items) {
		for i, line := range wrapList(g.label()+": ", g.names, p.width-len(indent)) {
			switch {
			case g.quiet != "":
				line = faint.Render(line)
			case i == 0:
				line = yellow.Render(g.label()+":") + strings.TrimPrefix(line, g.label()+":")
			}
			p.write(indent + line + "\n")
		}
	}
}

// wrapList lays out names after lead, run together with commas, in lines
// at most width wide, as far as names allow; lines after the first are
// indented two columns.
func wrapList(lead string, names []string, width int) []string {
	var lines []string
	line := lead
	hang := "  "
	for i, name := range names {
		word := name
		if i < len(names)-1 {
			word += ","
		}
		switch {
		case line == lead || line == hang:
			line += word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) > width:
			lines = append(lines, line)
			line = hang + word
		default:
			line += " " + word
		}
	}
	return append(lines, line)
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
	text := spinnerFrames[p.frame%len(spinnerFrames)] + " " + doing(p.running)
	p.write(faint.Render(ansi.Truncate(text, p.width-1, "…")))
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
