// Package render is kit's faces: how a run's events are shown. Pretty at a
// terminal, plain without one, JSON on request. Each is a sink that shows
// steps in the order the run listed them, whatever order they finish in.
package render

import (
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/status"
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

// itemGroup is a step's items of one state, quiet for one reason or loud,
// and with one action or none.
type itemGroup struct {
	state  string
	quiet  string
	action string
	names  []string
}

// key is the group's state, and why it's quiet, when it is, as plain lines
// print it: extra, or extra:new.
func (g itemGroup) key() string {
	if g.quiet == "" {
		return g.state
	}
	return g.state + ":" + g.quiet
}

// id tells groups apart: by state, why they're quiet, and their action.
func (g itemGroup) id() string {
	return g.key() + "|" + g.action
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
			g := itemGroup{state: it.State, quiet: it.Quiet, action: it.Action}
			i, ok := at[g.id()]
			if !ok {
				i = len(groups)
				at[g.id()] = i
				groups = append(groups, g)
			}
			groups[i].names = append(groups[i].names, name)
		}
	}
	return groups
}

// doneGroups are what applying a step dealt with, by action, in the order
// each action first comes, each named as done: installed.
func doneGroups(items []check.Item) []itemGroup {
	var groups []itemGroup
	at := make(map[string]int)
	for _, it := range items {
		i, ok := at[it.Action]
		if !ok {
			i = len(groups)
			at[it.Action] = i
			groups = append(groups, itemGroup{action: it.Action})
		}
		groups[i].names = append(groups[i].names, it.Name)
	}
	return groups
}

// past is an action as done: install, installed.
func past(action string) string {
	if irregular, ok := map[string]string{"run": "ran", "write": "wrote"}[action]; ok {
		return irregular
	}
	if strings.HasSuffix(action, "e") {
		return action + "d"
	}
	return action + "ed"
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

// Tee is a face showing on each of faces: every event to each, and each
// closed, the first error returned.
func Tee(faces ...Face) Face {
	return tee(faces)
}

type tee []Face

func (t tee) Emit(e event.Event) {
	for _, f := range t {
		f.Emit(e)
	}
}

func (t tee) Close() error {
	var first error
	for _, f := range t {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Collector is a face that shows nothing, and keeps the run's status
// document for whoever needs it once the run's done.
type Collector struct {
	status.Builder
}

// Close does nothing: the document is the collector's to give.
func (*Collector) Close() error { return nil }
