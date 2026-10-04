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
	// Reason says why a check failed, or the step was deferred.
	Reason string `json:"reason,omitempty"`
	// Counts are figures behind the summary, as in declared: 129.
	Counts map[string]int `json:"counts,omitempty"`
	// Items are the things that need attention.
	Items []Item `json:"items,omitempty"`
}
