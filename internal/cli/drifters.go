package cli

import (
	"context"
	"fmt"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/linked"
)

// drifter is a step whose items are drift, which kit reconcile settles: a
// kind's, or the linked files'.
type drifter interface {
	// check finds the step's items.
	check(ctx context.Context) check.Result
	// choices are what can be done about it, as a terminal offers them.
	choices(it check.Item) []choice
	// describe says what's wrong with it, for a question about it.
	describe(it check.Item) string
	// settle does what d decided about its item, snoozing aside.
	settle(ctx context.Context, r *run, c *changes, d decision, note string) check.Result
}

// choice is a thing that can be done about an item, as a terminal offers
// it.
type choice struct {
	action string
	label  string
	// shared is whether it's done for every Mac.
	shared bool
}

// snoozeChoice is snoozing, offered for every item.
var snoozeChoice = choice{action: snooze, label: "snooze it for 7 days"}

// choiceActions are choices' actions, each once, in order.
func choiceActions(choices []choice) []string {
	var out []string
	for _, c := range choices {
		if len(out) == 0 || out[len(out)-1] != c.action {
			out = append(out, c.action)
		}
	}
	return out
}

// kindDrifter is a kind's drift: what's installed and not declared, or
// declared and not installed.
type kindDrifter struct {
	k    kind.Kind
	list config.List
}

func (d kindDrifter) check(ctx context.Context) check.Result {
	return kind.Compare(ctx, d.k, d.list)
}

// choices are what can be done about it: adopting and undeclaring only
// where kit writes the kind's declarations.
func (d kindDrifter) choices(it check.Item) []choice {
	_, outside := d.k.(kind.Declarer)
	adopting := []choice{
		{action: adopt, label: "declare it, for this Mac"},
		{action: adopt, label: "declare it, for every Mac", shared: true},
	}
	uninstalling := []choice{{action: remove, label: "uninstall it"}}
	var all []choice
	switch it.State {
	case kind.Extra:
		all = append(append(adopting, uninstalling...), snoozeChoice)
	case kind.UnusedDependency:
		all = append(append(uninstalling, adopting...), snoozeChoice)
	case kind.Missing:
		if it.Action == kind.Install {
			all = append(all, choice{action: install, label: "install it"})
		}
		all = append(all, choice{action: undeclare, label: "undeclare it"}, snoozeChoice)
	default:
		all = []choice{snoozeChoice}
	}
	if !outside {
		return all
	}
	var writable []choice
	for _, c := range all {
		if c.action != adopt && c.action != undeclare {
			writable = append(writable, c)
		}
	}
	return writable
}

func (d kindDrifter) describe(it check.Item) string {
	return map[string]string{
		kind.Extra:            "installed, not declared",
		kind.Missing:          "declared, not installed",
		kind.UnusedDependency: "installed for something since removed, needed by nothing",
	}[it.State]
}

func (d kindDrifter) settle(ctx context.Context, r *run, c *changes, dec decision, note string) check.Result {
	switch dec.action {
	case adopt:
		group := dec.group
		if group == config.ToBeSorted {
			group = ""
		}
		return addOne(ctx, r, c, d.k, r.scope(dec.shared), dec.item.Name, group, addOptions{shared: dec.shared, note: note})
	case remove, undeclare:
		return removeOne(ctx, r, c, d.k, dec.item.Name, dec.shared)
	case install:
		if err := d.k.Install(ctx, []string{dec.item.Name}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't install: " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "installed"}
	}
	return check.Result{State: check.Failed, Reason: "nothing to do: " + dec.action}
}

// fileDrifter is the linked files' drift: files not linked, or linked
// otherwise, and dead links.
type fileDrifter struct {
	files *linked.Files
}

func (d fileDrifter) check(ctx context.Context) check.Result {
	return d.files.Check(ctx)
}

func (d fileDrifter) choices(it check.Item) []choice {
	switch it.State {
	case linked.Diverged:
		return []choice{
			{action: adopt, label: "keep the Mac's copy: into kit-config, then linked"},
			{action: revert, label: "put kit-config's back: the Mac's copy to the Bin"},
			snoozeChoice,
		}
	case linked.Dead:
		return []choice{{action: remove, label: "remove the dead link"}, snoozeChoice}
	}
	return []choice{{action: install, label: "link it"}, snoozeChoice}
}

func (d fileDrifter) describe(it check.Item) string {
	return map[string]string{
		linked.Missing:  "declared, not linked",
		linked.Changed:  "linked otherwise",
		linked.Diverged: "a different file where its link belongs",
		linked.Dead:     "a link to a file kit-config no longer has",
	}[it.State]
}

func (d fileDrifter) settle(ctx context.Context, r *run, c *changes, dec decision, _ string) check.Result {
	switch dec.action {
	case adopt:
		l, err := d.files.Adopt(ctx, dec.item.Name)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		c.changed(l.Path)
		return check.Result{State: check.OK, Summary: "the Mac's copy is in kit-config, linked: " + l.Path}
	case revert:
		binned, err := d.files.Revert(ctx, dec.item.Name)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		return check.Result{State: check.OK, Summary: fmt.Sprintf("linked to kit-config's; the Mac's copy is in the Bin (%s)", d.files.Tilde(binned))}
	case install, remove:
		if err := d.files.Apply(ctx, check.Result{Items: []check.Item{dec.item.Item}}); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		return check.Result{State: check.OK, Summary: map[string]string{install: "linked", remove: "removed"}[dec.action]}
	}
	return check.Result{State: check.Failed, Reason: "nothing to do: " + dec.action}
}
