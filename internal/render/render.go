// Package render is kit's faces: how a run's events are shown. Pretty at a
// terminal, plain without one, JSON on request. Each is a sink that shows
// steps in the order the run listed them, whatever order they finish in.
package render

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
)

// Face shows a run's events, and finishes once the run has.
type Face interface {
	event.Sink
	// Close finishes the face: what it shows at the end, and anything it
	// was still showing taken down.
	Close() error
}

// ordered releases steps' results in the order the run listed its steps,
// whatever order they finish in.
type ordered struct {
	steps []event.Step
	done  map[string]event.StepFinished
	next  int
}

func (o *ordered) start(steps []event.Step) {
	o.steps = steps
	o.done = make(map[string]event.StepFinished, len(steps))
	o.next = 0
}

// finish notes e, and returns the steps it lets through, in order.
func (o *ordered) finish(e event.StepFinished) []event.StepFinished {
	o.done[e.Step] = e
	var ready []event.StepFinished
	for o.next < len(o.steps) {
		f, ok := o.done[o.steps[o.next].Name]
		if !ok {
			break
		}
		ready = append(ready, f)
		o.next++
	}
	return ready
}

// title is the title the run gave step, or its name.
func (o *ordered) title(step string) string {
	for _, s := range o.steps {
		if s.Name == step && s.Title != "" {
			return s.Title
		}
	}
	return step
}

// itemLabels are how items of each state are counted for people, singular
// and plural.
var itemLabels = map[string][2]string{
	"missing":           {"missing", "missing"},
	"extra":             {"not declared", "not declared"},
	"unused-dependency": {"unused dependency", "unused dependencies"},
}

// quietLabels say why items don't need attention yet.
var quietLabels = map[string]string{
	"new":       "new, under a day",
	"snoozed":   "snoozed",
	"temporary": "temporary",
}

// itemGroup is a step's items of one state, quiet for one reason or loud.
type itemGroup struct {
	state string
	quiet string
	names []string
}

// label counts the group for people, as in "2 unused dependencies", or "1
// not declared (new, under a day)".
func (g itemGroup) label() string {
	forms, ok := itemLabels[g.state]
	if !ok {
		forms = [2]string{strings.ReplaceAll(g.state, "-", " "), strings.ReplaceAll(g.state, "-", " ")}
	}
	form := forms[0]
	if len(g.names) != 1 {
		form = forms[1]
	}
	label := fmt.Sprintf("%d %s", len(g.names), form)
	if g.quiet != "" {
		label += " (" + cmp.Or(quietLabels[g.quiet], g.quiet) + ")"
	}
	return label
}

// key is the group's state, and why it's quiet, when it is, as plain lines
// print it: extra, or extra:new.
func (g itemGroup) key() string {
	if g.quiet == "" {
		return g.state
	}
	return g.state + ":" + g.quiet
}

// groupItems groups items by state, and why they're quiet, in the order each
// group first comes, loud groups before quiet ones.
func groupItems(items []check.Item) []itemGroup {
	var groups []itemGroup
	at := make(map[string]int)
	for _, loud := range []bool{true, false} {
		for _, it := range items {
			if (it.Quiet == "") != loud {
				continue
			}
			name := it.Name
			if it.Detail != "" {
				name += " (" + it.Detail + ")"
			}
			g := itemGroup{state: it.State, quiet: it.Quiet}
			i, ok := at[g.key()]
			if !ok {
				i = len(groups)
				at[g.key()] = i
				groups = append(groups, g)
			}
			groups[i].names = append(groups[i].names, name)
		}
	}
	return groups
}

// summary is a run's last line: how its steps stood.
func summary(counts map[check.State]int) string {
	var parts []string
	if n := counts[check.Attention]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s attention", n, plural(n, "needs", "need")))
	}
	if n := counts[check.Failed]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d couldn't be checked", n))
	}
	if n := counts[check.Deferred]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d deferred", n))
	}
	if len(parts) == 0 {
		return "Nothing needs attention"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// what is what a result says, for its step's line: its summary, or why it
// failed or was deferred.
func what(r check.Result) string {
	switch r.State {
	case check.Failed:
		return "couldn't check: " + r.Reason
	case check.Deferred:
		return "deferred: " + r.Reason
	}
	return r.Summary
}
