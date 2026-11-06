package cli

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/steps"
)

func newOrderCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   orderTopic + " <name>",
		Short: "Where a step, or a thing as in cask:firefox, falls in a new Mac's run",
		Long: `Say where a step, or one of a step's things as in cask:firefox, falls in
a whole kit apply of this Mac's config, as on a new Mac: what the run has
done by the time it applies, and what comes after it, from the steps kit
apply builds and the order it keeps. The secrets' sync and the restore of
apps' settings are always placed: before it, after it, or in no order with
it, when either may come first. --json gives the same as one document.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The steps of kit apply at a terminal, as a new Mac's is, with
			// nothing a run at one starts started, and nothing shown.
			r, err := a.build("apply", "agent-order", render.Tee(), true)
			if err != nil {
				return err
			}
			err = a.order(r, args[0])
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	}
}

// spot is a step, or one of a step's things, in a run's order, and what's
// said of it there.
type spot struct {
	Step  string `json:"step"`
	Thing string `json:"thing,omitempty"`
	Says  string `json:"says"`
}

// name is the spot as kit names it: the step, or its thing, as in
// cask:firefox.
func (s spot) name() string {
	if s.Thing == "" {
		return s.Step
	}
	return s.Step + ":" + s.Thing
}

// placing is where a step, or one of a step's things, falls in a run: what's
// in place by the time it applies (Before), what comes after it (After),
// the steps always placed that are in no order with it (Unordered), and
// those the run hasn't (Absent).
type placing struct {
	Schema    int    `json:"schema"`
	Machine   string `json:"machine"`
	Name      string `json:"name"`
	Step      string `json:"step"`
	Thing     string `json:"thing,omitempty"`
	Before    []spot `json:"before"`
	After     []spot `json:"after"`
	Unordered []spot `json:"unordered"`
	Absent    []spot `json:"absent,omitempty"`
}

// alwaysPlaced are the steps kit agent order always places, as what a step
// on a new Mac most often assumes is done: the secrets' sync, and the
// restore of apps' settings. Each with why a run hasn't it, when it hasn't.
var alwaysPlaced = []struct{ step, absent string }{
	{secret.Descriptor.Name, "no secrets are declared for this Mac"},
	{steps.PrefsName, "kit prefs isn't on for this Mac, so no app's settings are restored"},
}

// order says where name, a step or one of a step's things, falls in r's
// run: as one document with --json, else as plain text.
func (a *app) order(r *run, name string) error {
	o, err := newOrdering(r, name)
	if err != nil {
		return err
	}
	p := o.place()
	p.Machine = r.machine
	if a.json {
		return render.WriteJSON(a.Stdout, p)
	}
	_, err = io.WriteString(a.Stdout, strings.Join(p.lines(), "\n")+"\n")
	return err
}

// ordering is a run's steps, ordered, and the step or thing being placed
// among them: its step, its thing, if it's one, and what it waits for
// before it applies beyond its step's order, as its held section's waits.
type ordering struct {
	pipeline *engine.Pipeline
	steps    []engine.Step
	byName   map[string]engine.Step
	name     string
	step     string
	thing    string
	also     []engine.Wait
}

// newOrdering is r's steps, with name placed among them: a step of the
// run, or a thing declared for this Mac of one of its kinds, as in
// cask:firefox. One the run hasn't is an error saying what it has.
func newOrdering(r *run, name string) (*ordering, error) {
	o := &ordering{pipeline: r.pipeline, steps: r.pipeline.Steps(), byName: map[string]engine.Step{}, name: name, step: name}
	for _, s := range o.steps {
		o.byName[s.Name] = s
	}
	kindName, thing, isThing := strings.Cut(name, ":")
	if isThing {
		o.step, o.thing = kindName, thing
	}
	if _, ok := o.byName[o.step]; !ok {
		_, err := r.pipeline.Planned(engine.Options{Only: []string{o.step}})
		return nil, err
	}
	if !isThing {
		o.also = waitsFirst(o.byName[o.step])
		return o, nil
	}
	if !slices.Contains(r.kinds, kindName) {
		return nil, fmt.Errorf("%s has no things of its own: name a step, or a kind's thing, as in cask:firefox", kindName)
	}
	i := slices.IndexFunc(r.lists[kindName].Entries, func(e config.Entry) bool { return !e.Off && sameThing(e.Name, thing) })
	if i < 0 {
		return nil, fmt.Errorf("%s isn't declared for this Mac, or is declared off: kit list %s lists what is", name, kindName)
	}
	if hold := r.lists[kindName].Entries[i].Hold; hold != "" {
		for _, h := range r.placing[kindName].Held {
			if h.Hold == hold {
				o.also = h.Waits
			}
		}
	}
	return o, nil
}

// waitsFirst are what step waits for before it applies anything, beyond
// its order: a step of your own waits for its secrets first, where other
// steps wait for what they hold as Later partway through.
func waitsFirst(step engine.Step) []engine.Wait {
	if step.Area == steps.AreaSteps {
		return step.Later
	}
	return nil
}

// covers reports whether ahead, what's in place by the time a step applies,
// has the step or thing being placed.
func (o *ordering) covers(ahead []engine.Wait) bool {
	return slices.ContainsFunc(ahead, func(w engine.Wait) bool {
		return w.Step == o.step && (w.Thing == "" || o.thing != "" && sameThing(w.Thing, o.thing))
	})
}

// place places the step or thing: what's ahead of it, the steps whose
// lead-up has it, and the steps always placed, wherever they fall.
func (o *ordering) place() placing {
	p := placing{Schema: agentSchema, Name: o.name, Step: o.step, Thing: o.thing, Before: []spot{}, After: []spot{}, Unordered: []spot{}}
	ahead := o.pipeline.Ahead(o.step, o.also...)
	settings := o.where(steps.PrefsName, ahead)
	for _, w := range ahead {
		p.Before = append(p.Before, spot{Step: w.Step, Thing: w.Thing, Says: o.saidBefore(w, settings)})
	}
	for _, s := range o.steps {
		if s.Name != o.step && o.covers(o.pipeline.Ahead(s.Name, waitsFirst(s)...)) {
			p.After = append(p.After, spot{Step: s.Name, Says: o.said(s)})
		}
	}
	for _, always := range alwaysPlaced {
		switch o.where(always.step, ahead) {
		case notInRun:
			p.Absent = append(p.Absent, spot{Step: always.step, Says: always.absent})
		case inNoOrder:
			says := o.said(o.byName[always.step])
			if slices.ContainsFunc(ahead, func(w engine.Wait) bool { return w.Step == always.step }) {
				says += "; only those listed before it are sure to come first"
			}
			p.Unordered = append(p.Unordered, spot{Step: always.step, Says: says})
		}
	}
	return p
}

// falls is where a step falls against the step or thing placed.
type falls int

const (
	notInRun falls = iota
	comesBefore
	comesAfter
	inNoOrder
	isIt
)

// where is where the step called name falls against the step or thing
// placed, ahead being what's ahead of that.
func (o *ordering) where(name string, ahead []engine.Wait) falls {
	s, ok := o.byName[name]
	switch {
	case !ok:
		return notInRun
	case name == o.step:
		return isIt
	case slices.ContainsFunc(ahead, func(w engine.Wait) bool { return w.Step == name && w.Thing == "" }):
		return comesBefore
	case o.covers(o.pipeline.Ahead(name, waitsFirst(s)...)):
		return comesAfter
	}
	return inNoOrder
}

// saidBefore is what's said of w, in place by the time the step or thing
// placed applies: an app installed but never opened, its settings restored
// where settings says the prefs step falls.
func (o *ordering) saidBefore(w engine.Wait, settings falls) string {
	s := o.byName[w.Step]
	if w.Thing == "" {
		says := o.said(s)
		if _, isKind := kinds.Named(s.Name); !isKind && s.Area != steps.AreaManual && s.Name != steps.PrefsName {
			says += ", done"
		}
		if isApps(s.Name) {
			says += ", never opened; " + restored(settings, "their")
		}
		return says
	}
	if isApps(w.Step) {
		return "installed, never opened; " + restored(settings, "its")
	}
	return cmp.Or(s.Verb, "in place")
}

// said is what's said of the step s, wherever it falls: what it puts in
// place, or does.
func (o *ordering) said(s engine.Step) string {
	switch {
	case s.Name == steps.PrefsName:
		var apps []string
		for _, after := range s.After {
			if isApps(after) {
				apps = append(apps, kinds.Label(after))
			}
		}
		if len(apps) == 0 {
			return "apps' settings restored"
		}
		return "apps' settings restored, once " + inWords(apps, "and") + " are installed"
	case len(s.ByHand) == 1 && s.ByHand[0].Name == s.Name:
		return s.ByHand[0].Title + ", done by hand"
	case s.Name == steps.ManualName:
		return "steps done by hand, " + cmp.Or(s.Waiting, "each as it comes")
	}
	if _, isKind := kinds.Named(s.Name); isKind {
		says := []string{s.Title + ", every one " + cmp.Or(s.Verb, "installed")}
		for _, w := range s.Later {
			if card, ok := o.byName[w.Step]; ok && w.Thing == "" && len(card.ByHand) == 1 {
				says = append(says, "once "+card.ByHand[0].Title+" is done")
			}
		}
		return strings.Join(says, ", ")
	}
	switch {
	case s.Area == steps.AreaSteps:
		return "a step of your own"
	case s.Title != s.Name:
		return s.Title
	}
	return "a step of kit's"
}

// isApps reports whether the step called name is a kind's whose things are
// apps.
func isApps(name string) bool {
	return slices.Contains(kinds.Apps(), name)
}

// restored says when an app's settings are restored, where settings says
// the prefs step falls against the step or thing placed, whose being
// whose.
func restored(settings falls, whose string) string {
	switch settings {
	case notInRun:
		return "no settings restored: kit prefs isn't on"
	case comesBefore:
		return whose + " settings restored by prefs, before it"
	case comesAfter:
		return whose + " settings restored by prefs, after it"
	case isIt:
		return whose + " settings restored by it"
	}
	return whose + " settings restored by prefs, which may come before or after it"
}

// lines are the placing as kit agent order prints it: a heading, then what
// comes before, after and in no order with it, a step or thing a line.
func (p placing) lines() []string {
	width := 0
	for _, s := range slices.Concat(p.Before, p.After, p.Unordered, p.Absent) {
		width = max(width, len(s.name()))
	}
	lines := []string{p.Name + " on a new Mac"}
	section := func(title string, spots []spot, none string) {
		if len(spots) == 0 && none == "" {
			return
		}
		lines = append(lines, "", title)
		if len(spots) == 0 {
			lines = append(lines, "  "+none)
		}
		for _, s := range spots {
			lines = append(lines, hanging(fmt.Sprintf("  %-*s  ", width, s.name()), s.Says)...)
		}
	}
	section("Before it", p.Before, "nothing kit installs: it can be among the first")
	section("After it", p.After, "nothing comes after it")
	section("In no order with it: either may come first", p.Unordered, "")
	section("Not in this Mac's run", p.Absent, "")
	return lines
}
