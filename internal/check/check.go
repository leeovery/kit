// Package check is what a step's check finds: how the step stands, and the
// things that need attention.
package check

import "time"

// State is how a step stands.
type State string

const (
	// OK is a step done and right.
	OK State = "ok"
	// Attention is a step checked and found to need attention, such as
	// packages declared but missing.
	Attention State = "attention"
	// Failed is a step whose check couldn't find out.
	Failed State = "failed"
	// Deferred is a step not checked, as what it needs isn't met.
	Deferred State = "deferred"
)

// Phase is what a step was doing.
type Phase string

const (
	// Checking is a step's check, before or after it's applied.
	Checking Phase = "check"
	// Applying is a step's apply.
	Applying Phase = "apply"
)

// Item is one thing a check found that needs attention, or will.
type Item struct {
	// ID names it, the same from run to run: its kind and name, as in
	// brew:jq.
	ID   string `json:"id"`
	Name string `json:"name"`
	// State is what's wrong with it, as in missing.
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// Quiet says why it doesn't need attention yet, when it doesn't: new,
	// snoozed or temporary.
	Quiet string `json:"quiet,omitempty"`
	// Since is when it was first seen, when that's known.
	Since time.Time `json:"since,omitzero"`
	// Action is what applying the step would do about it, as in install:
	// none when it would leave it.
	Action string `json:"action,omitempty"`
}

// The states of items that are the person's to do: a problem only they can
// put right, as a backup disk to add, what to do its detail; and a step by
// hand.
const (
	Problem = "problem"
	ByHand  = "manual"
)

// ToDo reports whether r has left something for the person at the Mac to
// do, and only that: attention whose every item is theirs to do. Nothing's
// wrong with the step: it's to do.
func (r Result) ToDo() bool {
	if r.State != Attention || len(r.Items) == 0 {
		return false
	}
	for _, it := range r.Items {
		if it.State != Problem && it.State != ByHand {
			return false
		}
	}
	return true
}

// NotAllowed is the state of the item of a check that couldn't look at how
// its step stands, as macOS hasn't let kit, till the person at the Mac
// allows it: its name says what isn't allowed, its detail where to allow
// it.
const NotAllowed = "not-allowed"

// Refused is r's item saying what macOS hasn't let its check do, when it
// couldn't look till the person at the Mac allows it.
func (r Result) Refused() (Item, bool) {
	for _, it := range r.Items {
		if it.State == NotAllowed {
			return it, true
		}
	}
	return Item{}, false
}

// Actions reports whether any of r's items has an action: something
// applying the step would do, though nothing needs attention yet.
func (r Result) Actions() bool {
	for _, it := range r.Items {
		if it.Action != "" {
			return true
		}
	}
	return false
}

// Result is what a check found.
type Result struct {
	State State `json:"state"`
	// Summary says how the step stands, as in "129 declared, all installed".
	Summary string `json:"summary,omitempty"`
	// Glance is the step's summary as the at-a-glance view shows it, short,
	// as in "Arq 01:05": none when the step has nothing worth a glance.
	Glance string `json:"glance,omitempty"`
	// Reason says why a check failed, or the step was deferred.
	Reason string `json:"reason,omitempty"`
	// Phase is what a failed step was doing as it failed, as the run knows
	// it: checking or applying. None where nothing says.
	Phase Phase `json:"phase,omitempty"`
	// Counts are figures behind the summary, as in declared: 129.
	Counts map[string]int `json:"counts,omitempty"`
	// Items are the things that need attention.
	Items []Item `json:"items,omitempty"`
	// Done are the items applying the step dealt with, each with the action
	// it took.
	Done []Item `json:"done,omitempty"`
}
