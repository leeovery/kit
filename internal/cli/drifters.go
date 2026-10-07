package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/steps"
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

// shower is a drifter with more to show of an item, below a question about
// it: an edit's diff.
type shower interface {
	show(ctx context.Context, it check.Item) string
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

// choices are what can be done about it.
func (d kindDrifter) choices(it check.Item) []choice {
	adopting := []choice{
		{action: adopt, label: "declare it, for this Mac"},
		{action: adopt, label: "declare it, for every Mac", shared: true},
	}
	if _, ownFile := d.k.(kind.Declarer); ownFile {
		// Declared in a file of its own, which every Mac linking it shares.
		adopting = []choice{{action: adopt, label: "declare it"}}
	}
	uninstalling := []choice{{action: remove, label: "uninstall it"}}
	if _, reverts := d.k.(kind.Reverter); reverts {
		// A setting changed on the Mac: put back what it was.
		uninstalling = []choice{{action: revert, label: "put back what it was"}}
	}
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
	case kind.Diverged:
		all = []choice{
			{action: adopt, label: "keep the Mac's: declared as it is now"},
			{action: revert, label: "put back what's declared"},
			snoozeChoice,
		}
	case kind.Changed:
		all = []choice{{action: install, label: "set it as declared"}}
		if _, valued := d.k.(kind.Valued); valued {
			all = append(all, choice{action: adopt, label: "keep the Mac's: declared as it is now"})
		}
		all = append(all, snoozeChoice)
	default:
		all = []choice{snoozeChoice}
	}
	return all
}

func (d kindDrifter) describe(it check.Item) string {
	if _, reverts := d.k.(kind.Reverter); reverts && it.State == kind.Extra {
		return "changed on this Mac, not declared"
	}
	return map[string]string{
		kind.Extra:            "installed, not declared",
		kind.Missing:          "declared, not installed",
		kind.UnusedDependency: "installed for something since removed, needed by nothing",
		kind.Diverged:         "changed on this Mac from what's declared",
		kind.Changed:          "set otherwise than declared",
	}[it.State]
}

func (d kindDrifter) settle(ctx context.Context, r *run, c *changes, dec decision, note string) check.Result {
	switch {
	case dec.action == adopt && (dec.item.State == kind.Diverged || dec.item.State == kind.Changed):
		return d.adoptValue(ctx, r, c, dec, note)
	case dec.action == revert && dec.item.State == kind.Extra:
		rv, ok := d.k.(kind.Reverter)
		if !ok {
			return check.Result{State: check.Failed, Reason: "kit doesn't know what it was"}
		}
		if err := rv.Revert(event.WithChanging(ctx), dec.item.Name); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't put it back: " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "put back as it was"}
	case dec.action == revert:
		if err := d.k.Install(event.WithChanging(ctx), []string{dec.item.Name}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't put it back: " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "put back as declared"}
	}
	switch dec.action {
	case adopt:
		return addOne(ctx, r, c, d.k, r.scope(dec.shared), dec.item.Name, addOptions{shared: dec.shared, note: note})
	case remove, undeclare:
		return removeOne(ctx, r, c, d.k, dec.item.Name, dec.shared)
	case install:
		if err := d.k.Install(event.WithChanging(ctx), []string{dec.item.Name}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't install: " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "installed"}
	}
	return check.Result{State: check.Failed, Reason: "nothing to do: " + dec.action}
}

// adoptValue declares a thing changed on the Mac as it is now, in place of
// its declared value, in the declarations it's in for this Mac.
func (d kindDrifter) adoptValue(ctx context.Context, r *run, c *changes, dec decision, note string) check.Result {
	v, ok := d.k.(kind.Valued)
	if !ok {
		return check.Result{State: check.Failed, Reason: "kit can't read its value to declare"}
	}
	value, err := v.Value(ctx, dec.item.Name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	where, err := r.where(d.k.Name(), dec.item.Name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	for _, e := range where {
		if e.Scope != config.Shared && e.Scope != r.machine {
			continue
		}
		e.Value = value
		if note != "" {
			e.Note = note
		}
		if err := r.cfg.Replace(d.k.Name(), e.Scope, e); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		c.changed(config.DeclFile(e.Scope))
		return check.Result{State: check.OK, Summary: "declared as it is now, in " + e.Scope}
	}
	return check.Result{State: check.Failed, Reason: dec.item.Name + " isn't declared for this Mac"}
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

// configDrifter is the config repository's edits not committed.
type configDrifter struct {
	repo gitrepo.Repo
	home string
	step engine.Step
}

func (d configDrifter) check(ctx context.Context) check.Result {
	return d.step.Check(ctx)
}

func (d configDrifter) choices(it check.Item) []choice {
	undo := map[string]string{
		steps.ConfigEdited:  "undo it: back to the last commit",
		steps.ConfigAdded:   "undo it: the file to the Bin",
		steps.ConfigDeleted: "undo it: the file back",
	}[it.State]
	return []choice{{action: adopt, label: "commit it, and push"}, {action: revert, label: undo}, snoozeChoice}
}

// diffLines is how much of a change's diff a question shows.
const diffLines = 20

func (d configDrifter) describe(it check.Item) string {
	return map[string]string{
		steps.ConfigEdited:  "edited, not committed",
		steps.ConfigAdded:   "new, not committed",
		steps.ConfigDeleted: "deleted, not committed",
	}[it.State]
}

// show is an edit's diff, its first lines.
func (d configDrifter) show(ctx context.Context, it check.Item) string {
	if it.State != steps.ConfigEdited {
		return ""
	}
	diff, err := d.repo.Diff(ctx, it.Name)
	if err != nil || diff == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	if len(lines) > diffLines {
		lines = append(lines[:diffLines], "…")
	}
	return strings.Join(lines, "\n")
}

func (d configDrifter) settle(ctx context.Context, _ *run, c *changes, dec decision, _ string) check.Result {
	switch dec.action {
	case adopt:
		c.changed(dec.item.Name)
		return check.Result{State: check.OK, Summary: "to commit"}
	case revert:
		if dec.item.State == steps.ConfigAdded {
			binned, err := linked.ToBin(d.home, filepath.Join(d.repo.Dir, dec.item.Name))
			if err != nil {
				return check.Result{State: check.Failed, Reason: "couldn't move it to the Bin: " + err.Error()}
			}
			return check.Result{State: check.OK, Summary: "undone: the file is in the Bin (" + binned + ")"}
		}
		if err := d.repo.Restore(ctx, dec.item.Name); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't undo it: " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "undone: back to the last commit"}
	}
	return check.Result{State: check.Failed, Reason: "nothing to do: " + dec.action}
}

// applyDrifter is drift the step's apply settles, with nothing to declare
// or remove: the shell's PATH, Oh My Zsh.
type applyDrifter struct {
	step engine.Step
	// label is applying, as a terminal offers it.
	label string
}

func (d applyDrifter) check(ctx context.Context) check.Result {
	return d.step.Check(ctx)
}

func (d applyDrifter) choices(check.Item) []choice {
	return []choice{{action: install, label: d.label}, snoozeChoice}
}

func (d applyDrifter) describe(check.Item) string {
	return "not as declared"
}

func (d applyDrifter) settle(ctx context.Context, _ *run, _ *changes, dec decision, _ string) check.Result {
	if err := d.step.Apply(ctx, check.Result{Items: []check.Item{dec.item.Item}}); err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	return check.Result{State: check.OK, Summary: "done"}
}
