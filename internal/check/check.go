// Package check is what a step's check finds: how the step stands, and the
// things that need attention.
package check

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

// Item is one thing a check found that needs attention.
type Item struct {
	// ID names it, the same from run to run: its kind and name, as in
	// brew:jq.
	ID   string `json:"id"`
	Name string `json:"name"`
	// State is what's wrong with it, as in missing.
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
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
