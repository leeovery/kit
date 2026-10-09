package render

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
)

// Plain is the face without a terminal: the same words as Pretty, a line
// each, no colour, no animation. Each step's lines start with its name, so
// they read with grep. It never asks anything.
type Plain struct {
	mu    sync.Mutex
	w     io.Writer
	order ordered
	err   error
}

// NewPlain returns the plain face, writing to w.
func NewPlain(w io.Writer) *Plain {
	return &Plain{w: w}
}

func (p *Plain) Emit(e event.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch e := e.(type) {
	case event.RunStarted:
		p.order.start(e.Steps)
		p.printf("kit %s · %s\n", e.Command, e.Machine)
	case event.StepFinished:
		for _, f := range p.order.finish(e) {
			text := f.Result.Summary
			if f.Result.State == check.Failed || f.Result.State == check.Deferred {
				text = f.Result.Reason
			}
			p.printf("%s %s %s\n", f.Step, f.Result.State, text)
			for _, g := range doneGroups(f.Result.Done) {
				p.printf("%s %s %s\n", f.Step, past(g.action), strings.Join(g.names, ", "))
			}
			for _, g := range groupItems(f.Result.Items) {
				line := fmt.Sprintf("%s %s %s", f.Step, g.key(), strings.Join(g.names, ", "))
				if g.action != "" {
					line += " (to " + g.action + ")"
				}
				p.printf("%s\n", line)
			}
		}
	case event.RunFinished:
		p.printf("%s\n", summary(e.Counts))
	case event.SelfTest:
		for _, t := range e.Tests {
			p.printf("self-test %s %s %s\n", t.State, t.Name, strings.Join(t.Says, " · "))
		}
	case event.DeviceCode:
		p.printf("%s enter %s at %s, by %s\n", e.Step, e.Code, e.URI, e.Expires.Format("15:04"))
	}
}

func (p *Plain) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

// Close returns the first error writing, if any.
func (p *Plain) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}
