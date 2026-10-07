package render

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/status"
)

// areaOrder is the order the at-a-glance view shows areas in.
var areaOrder = []string{"Backups", "Jobs", "Mac", "Drift", "Config", "Steps", "Manual", "Checks"}

// driftStates say what a drift item is, briefly.
var driftStates = map[string]string{
	"extra":             "not declared",
	"missing":           "missing",
	"unused-dependency": "unused",
	"changed":           "changed",
	"diverged":          "diverged",
	"dead":              "dead link",
	"edited":            "not committed",
	"added":             "new, not committed",
	"deleted":           "deleted, not committed",
}

// Glance is the face of bare kit: nothing as the run goes, and at its end a
// line an area, saying what needs attention in it, and what to run about
// it, or that all's well.
type Glance struct {
	w       io.Writer
	pretty  bool
	now     func() time.Time
	builder status.Builder
	machine string
	started bool
}

// NewGlance returns the at-a-glance face, writing to w: marks and colour
// when pretty, words otherwise, as the plain face has them.
func NewGlance(w io.Writer, pretty bool, now func() time.Time) *Glance {
	return &Glance{w: w, pretty: pretty, now: now}
}

func (g *Glance) Emit(e event.Event) {
	if s, ok := e.(event.RunStarted); ok {
		g.started, g.machine = true, s.Machine
	}
	g.builder.Emit(e)
}

// area is one line of the view: what stands worst in it, and what to say.
type area struct {
	name  string
	state check.State
	text  string
}

// Close prints the view, when a run started.
func (g *Glance) Close() error {
	if !g.started {
		return nil
	}
	doc := g.builder.Document()
	byArea := make(map[string][]status.Step)
	for _, s := range doc.Steps {
		name := cmp.Or(s.Area, "Checks")
		byArea[name] = append(byArea[name], s)
	}
	var areas []area
	width, markWidth := 0, 0
	for _, name := range areaOrder {
		if steps := byArea[name]; len(steps) > 0 {
			a := g.areaOf(name, steps)
			areas = append(areas, a)
			width = max(width, len(name))
			markWidth = max(markWidth, len(a.state))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "kit · %s\n", g.machine)
	for _, a := range areas {
		mark := fmt.Sprintf("%-*s", markWidth, a.state)
		name := fmt.Sprintf("%-*s", width, a.name)
		if g.pretty {
			mark = cmp.Or(stateMark[a.state], stateMark[check.Attention])
			name = bold.Render(name)
		}
		fmt.Fprintf(&b, "%s %s  %s\n", mark, name, a.text)
	}
	_, err := io.WriteString(g.w, b.String())
	return err
}

// areaOf is the line for the area name's steps.
func (g *Glance) areaOf(name string, steps []status.Step) area {
	a := area{name: name, state: check.OK}
	var wrong, glances []string
	quiet := make(map[string]int)
	for _, s := range steps {
		switch s.State {
		case check.Failed:
			a.state = check.Failed
			wrong = append(wrong, s.Title+": "+s.Reason)
			continue
		case check.Deferred:
			if a.state == check.OK {
				a.state = check.Attention
			}
			wrong = append(wrong, s.Title+": "+s.Reason)
			continue
		}
		for _, it := range s.Items {
			if it.Quiet != "" {
				quiet[it.Quiet]++
				continue
			}
			if a.state == check.OK {
				a.state = check.Attention
			}
			wrong = append(wrong, g.describe(s, it))
		}
		if s.Glance != "" {
			glances = append(glances, s.Glance)
		}
	}
	switch {
	case len(wrong) > 0:
		hint := map[string]string{"Drift": "kit reconcile", "Steps": "kit apply"}[name]
		a.text = strings.Join(wrong, ", ") + "  → " + cmp.Or(hint, "kit status")
	case name == "Drift":
		a.text = "nothing to reconcile"
		var notes []string
		for _, reason := range slices.Sorted(maps.Keys(quiet)) {
			notes = append(notes, fmt.Sprintf("%d %s", quiet[reason], cmp.Or(quietLabels[reason], reason)))
		}
		if len(notes) > 0 {
			a.text += " · " + strings.Join(notes, ", ")
		}
	case name == "Steps":
		a.text = fmt.Sprintf("%d done", len(steps))
	case len(glances) > 0:
		a.text = strings.Join(glances, " · ")
	default:
		a.text = "all well"
	}
	return a
}

// describe says what an item that needs attention is, briefly: a drift
// item's kind, name, what's wrong and for how long; a step of the user's
// own, and what its check says; a check's problem as it says it.
func (g *Glance) describe(s status.Step, it check.Item) string {
	what, isDrift := driftStates[it.State]
	switch {
	case s.Area == "Steps":
		return s.Title + ": " + s.Summary
	case !isDrift:
		return it.Name
	}
	text := s.ID + " " + it.Name + " (" + what
	if !it.Since.IsZero() {
		text += ", " + since(g.now().Sub(it.Since))
	}
	return text + ")"
}

// since says how long d is, roughly, in days.
func since(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case days >= 2:
		return fmt.Sprintf("%d days", days)
	case days == 1:
		return "a day"
	}
	return "under a day"
}
