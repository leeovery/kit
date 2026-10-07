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
	"github.com/leeovery/kit/internal/look"
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
	width   int
	pretty  bool
	now     func() time.Time
	builder status.Builder
	start   event.RunStarted
	started bool
	// live is what shows while the checks run, at a terminal: the wordmark
	// at once, then the loader.
	live *Pretty
}

// NewGlance returns the at-a-glance face, writing to w, which is width
// columns wide: kit's look when pretty, words otherwise, as the plain face
// has them.
func NewGlance(w io.Writer, width int, pretty bool, now func() time.Time) *Glance {
	g := &Glance{w: w, width: min(max(width, 40), look.Width), pretty: pretty, now: now}
	if pretty {
		g.live = homeLoader(w, width, true)
	}
	return g
}

func (g *Glance) Emit(e event.Event) {
	if s, ok := e.(event.RunStarted); ok {
		g.started, g.start = true, s
	}
	g.builder.Emit(e)
	if g.live != nil {
		g.live.Emit(e)
	}
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
	if g.pretty {
		if err := g.live.Close(); err != nil {
			return err
		}
		return g.home(doc)
	}
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
	fmt.Fprintf(&b, "kit · %s\n", g.start.Machine)
	for _, a := range areas {
		fmt.Fprintf(&b, "%-*s %-*s  %s\n", markWidth, a.state, width, a.name, a.text)
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

// home is the view in kit's look, under the wordmark the loader showed: a
// row an area, the config's drift and its repository one area, Config: what
// needs attention in it, or how it stands.
func (g *Glance) home(doc status.Document) error {
	parts := make(map[string]string, len(g.start.Steps))
	for _, s := range g.start.Steps {
		parts[s.Name] = s.Part
	}
	byArea := make(map[string][]status.Step)
	for _, s := range doc.Steps {
		a := viewArea(s.Area)
		byArea[a] = append(byArea[a], s)
	}
	var rows []look.Row
	for _, name := range viewOrder {
		if steps := byArea[name]; len(steps) > 0 {
			rows = append(rows, g.homeRow(name, steps, parts))
		}
	}
	_, err := io.WriteString(g.w, strings.Join(look.Rows("  ", g.width, rows...), "\n")+"\n")
	return err
}

// homeRow is an area's row: what needs attention in it, the first named
// and the rest counted; else what its steps say at a glance.
func (g *Glance) homeRow(name string, steps []status.Step, parts map[string]string) look.Row {
	row := look.Row{State: look.Done, Name: name}
	var needs []string
	var glances []string
	for _, s := range steps {
		if st := state(s.State); worse(st, row.State) {
			row.State = st
		}
		switch s.State {
		case check.Failed, check.Deferred:
			needs = append(needs, look.Says(look.Words(state(s.State), s.Title), look.Muted(s.Reason)))
			continue
		}
		for _, it := range s.Items {
			if it.Quiet != "" {
				continue
			}
			if row.State == look.Done {
				row.State = look.NeedsYou
			}
			needs = append(needs, g.homeThing(s, it, parts[s.ID]))
		}
		if s.Glance != "" {
			glances = append(glances, look.Muted(s.Glance))
		}
	}
	switch {
	case len(needs) > 1:
		row.Says = look.Says(needs[0], look.Muted(fmt.Sprintf("%d more", len(needs)-1)))
	case len(needs) == 1:
		row.Says = needs[0]
	case name == "Config":
		row.Says = look.Says(append([]string{look.Muted("nothing to reconcile")}, glances...)...)
	case name == "Steps":
		row.Says = look.Muted(fmt.Sprintf("%d done", len(steps)))
	case len(glances) > 0:
		row.Says = look.Says(glances...)
	default:
		row.Says = look.Muted("all well")
	}
	return row
}

// homeThing says what needs attention, briefly: a thing's name and what's
// wrong, for how long; a step of the user's own, and what its check says;
// a check's problem as it says it.
func (g *Glance) homeThing(s status.Step, it check.Item, part string) string {
	_, isDrift := driftStates[it.State]
	switch {
	case s.Area == "Steps":
		return look.Says(look.Orange(s.Title), look.Muted(s.Summary))
	case !isDrift:
		return look.Orange(it.Name)
	}
	text := look.Orange(it.Name + " " + wrong(part, it))
	if !it.Since.IsZero() {
		text = look.Says(text, look.Muted(since(g.now().Sub(it.Since))))
	}
	return text
}
