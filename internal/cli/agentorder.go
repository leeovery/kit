package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
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
it, when either may come first. A step done by hand that's raised with the
rest at the end is placed as they are. --json gives the same as one
document.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The steps of kit apply at a terminal, as a new Mac's is, with
			// nothing a run at one starts started, nothing shown and no log.
			r, err := a.build("apply", "", render.Tee(), true)
			if err != nil {
				return err
			}
			err = a.order(cmd.Context(), r, args[0])
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	}
}

// spot is a step, one of a step's things, or a thing no step puts in place,
// as a program on kit's PATH, in a run's order, and what's said of it there.
type spot struct {
	Step  string `json:"step,omitempty"`
	Thing string `json:"thing,omitempty"`
	Says  string `json:"says"`
}

// name is the spot as kit names it: the step, or its thing, as in
// cask:firefox; a thing of no step, as a program on kit's PATH is.
func (s spot) name() string {
	if s.Step == "" {
		return s.Thing + " (on PATH)"
	}
	return strings.TrimSuffix(s.Step+":"+s.Thing, ":")
}

// placing is where a step, or one of a step's things, falls in a run: what's
// in place by the time it applies (Before), what comes after it (After),
// the steps always placed that are in no order with it (Unordered), and
// those the run hasn't (Absent). A step done by hand raised with the rest
// at the end is placed as the step raising them, RaisedBy. Fails says why
// it fails at once, when its line is wrong; Never why it never applies,
// when it waits for what never comes.
type placing struct {
	Schema    int    `json:"schema"`
	Machine   string `json:"machine"`
	Name      string `json:"name"`
	Step      string `json:"step"`
	Thing     string `json:"thing,omitempty"`
	RaisedBy  string `json:"raised_by,omitempty"`
	Fails     string `json:"fails,omitempty"`
	Never     string `json:"never,omitempty"`
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
func (a *app) order(ctx context.Context, r *run, name string) error {
	o, err := newOrdering(r, name)
	if err != nil {
		return err
	}
	p := o.place(ctx)
	p.Machine = r.machine
	if a.json {
		return render.WriteJSON(a.Stdout, p)
	}
	_, err = io.WriteString(a.Stdout, strings.Join(p.lines(), "\n")+"\n")
	return err
}

// ordering is a run's steps, ordered, and what's being placed among them:
// its name, as given, and the step or thing it is (target), or, for a step
// done by hand raised with the rest at the end, the step raising them; what
// the steps and things wait for beyond the run's order (holds); and whether
// the run installs a thing (declares).
type ordering struct {
	pipeline *engine.Pipeline
	steps    []engine.Step
	byName   map[string]engine.Step
	name     string
	target   engine.Wait
	raisedBy string
	holds    engine.Holds
	declares func(thing string) bool
}

// newOrdering is r's steps, with name placed among them: a step of the
// run, a thing declared for this Mac of one of its kinds, as in
// cask:firefox, or a step done by hand. One the run hasn't is an error
// saying what it has.
func newOrdering(r *run, name string) (*ordering, error) {
	o := &ordering{pipeline: r.pipeline, steps: r.pipeline.Steps(), byName: map[string]engine.Step{}, name: name, target: engine.Wait{Step: name}, declares: r.declares}
	for _, s := range o.steps {
		o.byName[s.Name] = s
	}
	o.holds = r.holds(o.byName)
	kindName, thing, isThing := strings.Cut(name, ":")
	_, isStep := o.byName[name]
	switch {
	case isThing && o.byName[kindName].Name == "":
		return nil, o.noStep(kindName)
	case isThing && !slices.Contains(r.kinds, kindName):
		return nil, fmt.Errorf("%s has no things of its own: name a step, or a kind's thing, as in cask:firefox", kindName)
	case isThing && !r.declares(name):
		return nil, fmt.Errorf("%s isn't declared for this Mac, or is declared off: kit list %s lists what is", name, kindName)
	case isThing:
		o.target = engine.Wait{Step: kindName, Thing: thing}
	case isStep:
	case r.raisedAtTheEnd(name) && o.byName[steps.ManualName].Name != "":
		o.target, o.raisedBy = engine.Wait{Step: steps.ManualName}, steps.ManualName
	default:
		return nil, o.noStep(name)
	}
	return o, nil
}

// noStep is the error for a step called name the run hasn't, saying the
// steps it has.
func (o *ordering) noStep(name string) error {
	names := make([]string, len(o.steps))
	for i, s := range o.steps {
		names[i] = s.Name
	}
	return fmt.Errorf("no step named %s on this Mac: one of %s", name, strings.Join(names, ", "))
}

// raisedAtTheEnd reports whether name is a step done by hand of this
// Mac's, raised with the rest at the end, as a line nothing comes after is,
// rather than a step of its own.
func (r *run) raisedAtTheEnd(name string) bool {
	list, err := r.cfg.List(config.ManualKind, r.machine)
	return err == nil && slices.ContainsFunc(list.Entries, func(e config.Entry) bool { return e.Name == name })
}

// holds are what the run's steps, byName, and their things wait for beyond
// its order (engine.Holds): a step of your own, its secrets, before it
// applies; a kind whose things wait for steps by hand (kind.Carded), as
// secrets their store's sign-in, those steps, before any of them; and a line
// held back, its section's waits.
func (r *run) holds(byName map[string]engine.Step) engine.Holds {
	cards := map[string][]engine.Wait{}
	for _, name := range r.kinds {
		carded, ok := r.kindsByName[name].(kind.Carded)
		if !ok {
			continue
		}
		for _, c := range carded.Cards() {
			if _, ok := byName[c.Name]; ok {
				cards[name] = append(cards[name], engine.Wait{Step: c.Name})
			}
		}
	}
	return engine.Holds{
		First: func(step string) []engine.Wait {
			if byName[step].Area == steps.AreaSteps {
				return byName[step].Later
			}
			return cards[step]
		},
		Gates: func(step, thing string) []engine.Wait {
			return slices.Concat(cards[step], r.heldFor(step, thing))
		},
	}
}

// heldFor are the waits of the section of the kind called kindName that
// holds thing back, when one does.
func (r *run) heldFor(kindName, thing string) []engine.Wait {
	entries := r.lists[kindName].Entries
	i := slices.IndexFunc(entries, func(e config.Entry) bool { return sameThing(e.Name, thing) })
	if i < 0 || entries[i].Hold == "" {
		return nil
	}
	for _, h := range r.placing[kindName].Held {
		if h.Hold == entries[i].Hold {
			return h.Waits
		}
	}
	return nil
}

// covers reports whether ahead, what's in place by the time a step applies,
// has the step or thing being placed.
func (o *ordering) covers(ahead []engine.Wait) bool {
	return slices.ContainsFunc(ahead, func(w engine.Wait) bool {
		return w.Step == o.target.Step && (w.Thing == "" || o.target.Thing != "" && sameThing(w.Thing, o.target.Thing))
	})
}

// aheadOf is what's in place by the time the step called name applies.
func (o *ordering) aheadOf(name string) []engine.Wait {
	return o.pipeline.Ahead(engine.Wait{Step: name}, o.holds)
}

// place places the step or thing: what's ahead of it, the steps whose
// lead-up has it, and the steps always placed, wherever they fall; or, for
// a step whose line is wrong, why it fails.
func (o *ordering) place(ctx context.Context) placing {
	p := placing{Schema: agentSchema, Name: o.name, Step: o.target.Step, Thing: o.target.Thing, RaisedBy: o.raisedBy, Before: []spot{}, After: []spot{}, Unordered: []spot{}}
	if p.Fails = o.fails(ctx); p.Fails != "" {
		return p
	}
	ahead := o.pipeline.Ahead(o.target, o.holds)
	settings := o.where(steps.PrefsName, ahead)
	for _, w := range ahead {
		if p.Never == "" && o.never(w) {
			p.Never = "it waits for " + spot{Step: w.Step, Thing: w.Thing}.name() + ", which isn't declared for this Mac, so it's never installed"
		}
		p.Before = append(p.Before, spot{Step: w.Step, Thing: w.Thing, Says: o.saidBefore(w, settings)})
	}
	for _, s := range o.steps {
		if s.Name != o.target.Step && o.covers(o.aheadOf(s.Name)) {
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

// fails is why the step placed fails at once, when it's one of the user's
// own lines, a step of their own or a step done by hand, that's wrong: its
// line doesn't read, or it comes after what isn't a step. Such a step has
// nothing to apply, and its check, which says why, runs nothing.
func (o *ordering) fails(ctx context.Context) string {
	s := o.byName[o.target.Step]
	own := s.Area == steps.AreaSteps || len(s.ByHand) == 1 && s.ByHand[0].Name == s.Name
	if o.target.Thing != "" || o.raisedBy != "" || !own || s.Apply != nil {
		return ""
	}
	return s.Check(ctx).Reason
}

// never reports whether w, ahead of the step or thing placed, is a kind's
// thing the run never installs, as it isn't declared for this Mac: what
// waits for it is never applied.
func (o *ordering) never(w engine.Wait) bool {
	_, isKind := kinds.Named(w.Step)
	return w.Thing != "" && isKind && !o.declares(w.Step+":"+w.Thing)
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
	_, ok := o.byName[name]
	switch {
	case !ok:
		return notInRun
	case name == o.target.Step:
		return isIt
	case slices.ContainsFunc(ahead, func(w engine.Wait) bool { return w.Step == name && w.Thing == "" }):
		return comesBefore
	case o.covers(o.aheadOf(name)):
		return comesAfter
	}
	return inNoOrder
}

// saidBefore is what's said of w, in place by the time the step or thing
// placed applies: a step finished, a thing in place, a program on kit's
// PATH; an app installed but likely never opened, its settings restored
// where settings says the prefs step falls.
func (o *ordering) saidBefore(w engine.Wait, settings falls) string {
	s := o.byName[w.Step]
	switch {
	case w.Step == "":
		return "a program, waited for till it's on kit's PATH, whatever installs it"
	case o.never(w):
		return "not declared for this Mac: never installed"
	case w.Thing != "" && isApps(w.Step):
		return "installed, likely never opened yet; " + restored(settings, "its")
	case w.Thing != "":
		return cmp.Or(s.Verb, "in place")
	}
	says := o.said(s)
	_, isKind := kinds.Named(s.Name)
	switch {
	case isApps(s.Name):
		says += "; likely never opened yet; " + restored(settings, "their")
	case !isKind && s.Area != steps.AreaManual && s.Name != steps.PrefsName:
		says += ", finished"
	}
	return says
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
		says := []string{s.Title + ": each " + cmp.Or(s.Verb, "installed") + " unless it fails"}
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

// lines are the placing as kit agent order prints it: a heading, then why
// it never applies, if it doesn't, and what comes before, after and in no
// order with it, a step or thing a line; or why it fails.
func (p placing) lines() []string {
	heading := p.Name + " on a new Mac"
	if p.RaisedBy != "" {
		heading += ": a step done by hand, raised with the rest by " + p.RaisedBy
	}
	lines := hanging("", heading)
	if p.Fails != "" {
		return append(append(lines, ""), hanging("", "It fails at once, and never applies: "+p.Fails)...)
	}
	if p.Never != "" {
		lines = append(append(lines, ""), hanging("", "It never applies: "+p.Never+".")...)
	}
	width := 0
	for _, s := range slices.Concat(p.Before, p.After, p.Unordered, p.Absent) {
		width = max(width, len(s.name()))
	}
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
	section("Before it", p.Before, "nothing: it can be among the first")
	section("After it", p.After, "nothing comes after it")
	section("In no order with it: either may come first", p.Unordered, "")
	section("Not in this Mac's run", p.Absent, "")
	return lines
}
