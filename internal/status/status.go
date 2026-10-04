// Package status is the status document: how each of a Mac's steps stands,
// as kit status --json prints it, and as the primary Mac will read the
// others'.
package status

import (
	"sync"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
)

// Schema is the document's version: it changes when a field's meaning does.
const Schema = 1

// Document is how a Mac's steps stand.
type Document struct {
	Schema  int    `json:"schema"`
	Kit     string `json:"kit"`
	Machine string `json:"machine"`
	// Attention says whether any step needs attention: checked and found
	// wanting, couldn't be checked, or deferred.
	Attention bool   `json:"attention"`
	Steps     []Step `json:"steps"`
}

// Step is how one step stands.
type Step struct {
	// ID is the step's name, the same from run to run.
	ID    string `json:"id"`
	Title string `json:"title"`
	check.Result
}

// Builder builds a document from a run's events, as a sink.
type Builder struct {
	mu    sync.Mutex
	doc   Document
	index map[string]int
}

// Emit takes in an event.
func (b *Builder) Emit(e event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch e := e.(type) {
	case event.RunStarted:
		b.doc = Document{Schema: Schema, Kit: e.Version, Machine: e.Machine, Steps: make([]Step, 0, len(e.Steps))}
		b.index = make(map[string]int, len(e.Steps))
		for i, s := range e.Steps {
			b.doc.Steps = append(b.doc.Steps, Step{ID: s.Name, Title: s.Title})
			b.index[s.Name] = i
		}
	case event.StepFinished:
		if i, ok := b.index[e.Step]; ok {
			b.doc.Steps[i].Result = e.Result
		}
	}
}

// Document is the document built so far. A step that hasn't finished stands
// as failed, as nothing found out how it stands.
func (b *Builder) Document() Document {
	b.mu.Lock()
	defer b.mu.Unlock()
	doc := b.doc
	doc.Steps = make([]Step, len(b.doc.Steps))
	copy(doc.Steps, b.doc.Steps)
	for i := range doc.Steps {
		if doc.Steps[i].State == "" {
			doc.Steps[i].Result = check.Result{State: check.Failed, Reason: "the run ended before it finished"}
		}
		if doc.Steps[i].State != check.OK {
			doc.Attention = true
		}
	}
	return doc
}
