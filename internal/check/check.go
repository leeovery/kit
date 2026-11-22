// Package check is what a step's check finds: how the step stands, and the
// things that need attention.
package check

import (
	"slices"
	"strings"
	"time"
)

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
	// ToDo is what the person at the Mac is to do about it, when it's
	// theirs alone to do and applying at a terminal raises it at its end,
	// with the steps by hand: none for anything else.
	ToDo *ToDo `json:"to_do,omitempty"`
	// Later is whether it's known to be still to come, by itself, as a
	// project's MCP server whose folder isn't there yet: nothing for the
	// person to settle.
	Later bool `json:"later,omitempty"`
	// App is the bundle id of the app it is, for a thing installed by
	// hand: what leaving it so goes by.
	App string `json:"app,omitempty"`
}

// Look is how a check's look went at something besides what it checks:
// Unseen says why it couldn't look, when it couldn't, so what it doesn't
// report of that may be there still. Apps are the bundle ids of every app
// installed by hand it saw, reported or not: what's remembered of each
// lasts while it's there. They're kit's own, never in its JSON.
type Look struct {
	Unseen string   `json:"unseen,omitempty"`
	Apps   []string `json:"-"`
}

// ToDo is something for the person at the Mac to do, as applying raises it
// at its end, one at a time: what to do, its title; why; its steps, in
// order; what kit watches for to tell it's done, in words, as in "Time
// Machine has a disk"; the group it's in; the keys of a choice, when it's
// one; whether the person says when it's done (space), its step's choice
// answering that (hand.Done), rather than kit telling by itself; and
// whether it comes first, before the steps by hand the config declares,
// rather than after them.
type ToDo struct {
	Title    string   `json:"title"`
	Why      string   `json:"why,omitempty"`
	Steps    []string `json:"steps,omitempty"`
	Watch    string   `json:"watch,omitempty"`
	Group    string   `json:"group,omitempty"`
	Keys     []Key    `json:"keys,omitempty"`
	SaysDone bool     `json:"says_done,omitempty"`
	First    bool     `json:"first,omitempty"`
}

// Key is a key answering a choice, and what it does.
type Key struct {
	Key  string `json:"key"`
	Does string `json:"does"`
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

// Saying is r with its summary said in parts, each kept whole: kit's own
// words a part each, and what a check printed one part, whatever it holds.
// A part that says nothing isn't one.
func (r Result) Saying(parts ...string) Result {
	parts = slices.DeleteFunc(slices.Clone(parts), func(p string) bool { return p == "" })
	r.Summary, r.Parts = strings.Join(parts, "; "), nil
	if len(parts) > 1 {
		r.Parts = parts
	}
	return r
}

// Adding is r with parts said after what its summary says.
func (r Result) Adding(parts ...string) Result {
	return r.Saying(slices.Concat(r.Says(), parts)...)
}

// Says is the parts of r's summary: the summary whole when it wasn't said
// in parts, or was written since without them, and none when it says
// nothing.
func (r Result) Says() []string {
	switch {
	case len(r.Parts) > 0 && strings.Join(r.Parts, "; ") == r.Summary:
		return r.Parts
	case r.Summary == "":
		return nil
	}
	return []string{r.Summary}
}

// Result is what a check found.
type Result struct {
	State State `json:"state"`
	// Summary says how the step stands, as in "129 declared, all installed":
	// its parts joined by "; ", when it's said in parts.
	Summary string `json:"summary,omitempty"`
	// Parts are the summary's parts, when Saying says it in more than one,
	// as in "installed", "declared in laptop": a face shows each apart.
	Parts []string `json:"parts,omitempty"`
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
	// HandLook is how the check's look went at the things installed by
	// hand its kind could take over: none when it made none.
	HandLook *Look `json:"by_hand,omitempty"`
	// Done are the items applying the step dealt with, each with the action
	// it took.
	Done []Item `json:"done,omitempty"`
}
