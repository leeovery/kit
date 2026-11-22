package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// What can be done about drift.
const (
	adopt     = "adopt"
	remove    = "remove"
	install   = "install"
	undeclare = "undeclare"
	snooze    = "snooze"
	// revert puts back what's declared, in place of what the Mac has: the
	// declared file, or value.
	revert = "revert"
	// leave leaves a thing installed by hand so, quiet for good.
	leave = "leave"
)

// reconcileSchema is kit reconcile --json's document's version.
const reconcileSchema = 1

// reconcileOptions are kit reconcile's flags.
type reconcileOptions struct {
	adopt, remove, install, undeclare, snooze, revert, leave bool
	shared, all                                              bool
	note                                                     string
}

// driftItem is an item of drift, of a kind, and what can be done about it.
type driftItem struct {
	check.Item
	Kind    string   `json:"kind"`
	Choices []string `json:"choices"`
}

// decision is what to do about an item, and where it's declared when it's
// adopted.
type decision struct {
	item   driftItem
	action string
	shared bool
}

func newReconcileCommand(a *app) *cobra.Command {
	var opts reconcileOptions
	cmd := &cobra.Command{
		Use:         "reconcile [<kind>...] [<id>...]",
		Short:       "Settle drift: adopt, remove, install, undeclare, revert, leave or snooze what differs from the config",
		Annotations: map[string]string{brief: "settle what differs from the config"},
		Long: `Settle drift: what's installed but not declared, declared but not installed,
left by something since removed, or installed by hand that a cask installs. At
a terminal, kit goes through each item that needs attention (--all: every item,
new and snoozed too, but those left by hand), asking what to do with it, then
does it all, committing and pushing the config's changes.
Name kinds (kit reconcile brew cask) to go through only theirs. It brings in
kit-config's remote commits first, leaving your edits as they are.

Without a terminal, or with --json, it lists the items with their ids and
choices. Name items' ids, with what to do with them, to settle those, in one
run and one commit:

  kit reconcile brew:ffmpeg --adopt [--shared] [--note "why"]
  kit reconcile brew:node@20 --remove
  kit reconcile cask:zoom --install
  kit reconcile brew:jq --undeclare [--shared]
  kit reconcile cask:firefox --snooze           (quiet for 7 days)
  kit reconcile cask:spaceman --leave           (by hand, quiet for good)
  kit reconcile file:~/.zshrc --revert          (kit-config's copy back)
  kit reconcile go:example.com/a go:example.com/b --remove`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a.pull = true
			r, err := a.prepare(cmd.Context(), "reconcile", "reconcile")
			if err != nil {
				return err
			}
			err = a.reconcile(cmd.Context(), r, args, opts)
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.adopt, "adopt", false, "declare it, for this Mac or with --shared every Mac (a setting removed: accept it gone)")
	f.BoolVar(&opts.remove, "remove", false, "uninstall it")
	f.BoolVar(&opts.install, "install", false, "install it, as it's declared")
	f.BoolVar(&opts.undeclare, "undeclare", false, "take it out of this Mac's file, or the shared one with --shared")
	f.BoolVar(&opts.snooze, "snooze", false, "quiet it for 7 days")
	f.BoolVar(&opts.revert, "revert", false, "put back what's declared, in place of what the Mac has")
	f.BoolVar(&opts.leave, "leave", false, "leave it installed by hand, and stop asking")
	f.BoolVar(&opts.shared, "shared", false, "for every Mac: adopt into, or undeclare from, the shared file")
	f.BoolVar(&opts.all, "all", false, "at a terminal, every item, quiet ones too")
	f.StringVar(&opts.note, "note", "", "why it's adopted, kept after its name")
	return cmd
}

func (a *app) reconcile(ctx context.Context, r *run, args []string, opts reconcileOptions) error {
	// A kind's name narrows the items to that kind's; anything else is an
	// item's id, its kind before a colon.
	var kinds, ids []string
	for _, arg := range args {
		if _, ok := r.drifters[arg]; ok {
			kinds = append(kinds, arg)
		} else {
			ids = append(ids, arg)
		}
	}
	// At a terminal, asking, the heading shows at once, and what kit's doing
	// while it looks.
	asking := len(ids) == 0 && !a.json && a.pretty(a.Stdout)
	if asking {
		r.sink.Emit(event.Preparing{Time: r.now, Command: r.command, Machine: r.machine, Doing: "looking for what differs from the config"})
	}
	items, err := a.driftItems(ctx, r, kinds)
	if asking {
		r.sink.Emit(event.Preparing{Time: r.now, Command: r.command, Machine: r.machine})
	}
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		decisions := make([]decision, 0, len(ids))
		for _, id := range ids {
			d, err := decide(items, id, opts)
			if err != nil {
				return err
			}
			decisions = append(decisions, d)
		}
		return a.carryOut(ctx, r, decisions, opts.note)
	}
	if a.json || !a.pretty(a.Stdout) {
		return a.listDrift(r, items)
	}
	decisions, err := a.askAbout(ctx, r, items, opts)
	if err != nil {
		return err
	}
	if len(decisions) == 0 {
		said := "Nothing to reconcile"
		if slices.ContainsFunc(items, func(it driftItem) bool { return toAsk(it, opts) }) {
			said = "Nothing decided"
		}
		_, err := fmt.Fprint(a.colors(a.Stdout), look.Rows("  ", look.Width, look.Row{State: look.Done, Name: said})[0]+"\n")
		return err
	}
	return a.carryOut(ctx, r, decisions, opts.note)
}

// driftItems are the drift steps' items (only those of the steps named in
// only, when it names any): what differs from the config, quiet or not,
// each with what can be done about it.
func (a *app) driftItems(ctx context.Context, r *run, only []string) ([]driftItem, error) {
	var items []driftItem
	for _, name := range r.drift {
		if len(only) > 0 && !slices.Contains(only, name) {
			continue
		}
		d := r.drifters[name]
		res := drift.Quieten(d.check(ctx), r.record, r.now, nil)
		if err := unchecked(name, res); err != nil {
			return nil, err
		}
		for _, it := range res.Items {
			items = append(items, driftItem{Item: it, Kind: name, Choices: choiceActions(d.choices(it))})
		}
	}
	return items, nil
}

// decide is what the flags say to do with the item id.
func decide(items []driftItem, id string, opts reconcileOptions) (decision, error) {
	i := slices.IndexFunc(items, func(it driftItem) bool { return it.ID == id })
	if i < 0 {
		return decision{}, fmt.Errorf("no item %s: kit reconcile lists them", id)
	}
	item := items[i]
	var actions []string
	for action, set := range map[string]bool{adopt: opts.adopt, remove: opts.remove, install: opts.install, undeclare: opts.undeclare, snooze: opts.snooze, revert: opts.revert, leave: opts.leave} {
		if set {
			actions = append(actions, action)
		}
	}
	switch {
	case len(actions) == 0:
		return decision{}, fmt.Errorf("say what to do with %s: %s", id, flagList(item.Choices))
	case len(actions) > 1:
		slices.Sort(actions)
		return decision{}, fmt.Errorf("one thing at a time: --%s", strings.Join(actions, " and --"))
	case !slices.Contains(item.Choices, actions[0]):
		return decision{}, fmt.Errorf("%s can't be %s: %s", id, past(actions[0]), flagList(item.Choices))
	case actions[0] == leave && opts.shared:
		return decision{}, fmt.Errorf("%s is left by hand on this Mac only: drop --shared", id)
	}
	return decision{item: item, action: actions[0], shared: opts.shared}, nil
}

// flagList is choices as their flags, as in --adopt, --remove or --snooze.
func flagList(choices []string) string {
	flags := make([]string, len(choices))
	for i, c := range choices {
		flags[i] = "--" + c
	}
	if len(flags) == 1 {
		return flags[0]
	}
	return strings.Join(flags[:len(flags)-1], ", ") + " or " + flags[len(flags)-1]
}

// past is an action as done, as reconcile says it.
func past(action string) string {
	switch action {
	case adopt, install, revert:
		return action + "ed"
	case leave:
		return "left"
	}
	return action + "d"
}

// listDrift prints the items, as plain lines or one JSON document.
func (a *app) listDrift(r *run, items []driftItem) error {
	if a.json {
		if items == nil {
			items = []driftItem{}
		}
		if err := render.WriteJSON(a.Stdout, struct {
			Schema  int         `json:"schema"`
			Machine string      `json:"machine"`
			Items   []driftItem `json:"items"`
		}{reconcileSchema, r.machine, items}); err != nil {
			return err
		}
	} else {
		var b strings.Builder
		fmt.Fprintf(&b, "kit reconcile · %s\n", r.machine)
		for _, it := range items {
			fmt.Fprintf(&b, "%s %s", it.ID, it.State)
			if it.Quiet != "" {
				fmt.Fprintf(&b, ":%s", it.Quiet)
			}
			if !it.Since.IsZero() {
				fmt.Fprintf(&b, " since %s", it.Since.Format("2 Jan"))
			}
			if it.Detail != "" {
				fmt.Fprintf(&b, " (%s)", it.Detail)
			}
			fmt.Fprintf(&b, ": %s\n", flagList(it.Choices))
		}
		if len(items) == 0 {
			b.WriteString("Nothing to reconcile\n")
		}
		if _, err := fmt.Fprint(a.Stdout, b.String()); err != nil {
			return err
		}
	}
	return nil
}

// toAsk reports whether a terminal asks about it: when it needs attention,
// or, with --all, unless it's left by hand.
func toAsk(it driftItem, opts reconcileOptions) bool {
	return it.Quiet == "" || opts.all && it.Quiet != drift.Left
}

// askAbout asks, of each item that needs attention (every item, with
// --all, but those left by hand), what to do with it, one after another:
// every question before anything is done. Stopping keeps what's decided so
// far.
func (a *app) askAbout(ctx context.Context, r *run, items []driftItem, opts reconcileOptions) ([]decision, error) {
	var asked []driftItem
	var offered [][]choice
	var qs []ask.Question
	for _, it := range items {
		if !toAsk(it, opts) {
			continue
		}
		dr := r.drifters[it.Kind]
		cs := dr.choices(it.Item)
		answers := make([]render.Answer, len(cs))
		for i, c := range cs {
			answers[i] = render.Answer{Words: c.words, Shared: c.shared}
		}
		drifted := render.Drifted{Name: it.Name, Kind: it.Kind, What: dr.describe(it.Item), Detail: it.Detail, Since: it.Since}
		q := render.ReconcileQuestion(drifted, answers, r.now)
		if sh, ok := dr.(shower); ok {
			if more := sh.show(ctx, it.Item); more != "" {
				q.More = look.Output(strings.Split(more, "\n")...)
			}
		}
		asked, offered, qs = append(asked, it), append(offered, cs), append(qs, q)
	}
	picked, err := a.Walk(ctx, "Reconcile", qs)
	switch {
	case errors.Is(err, ask.ErrCancelled):
		return nil, fmt.Errorf("%w: nothing was changed", ask.ErrCancelled)
	case err != nil && !errors.Is(err, ask.ErrStopped):
		return nil, err
	}
	var decisions []decision
	for i, p := range picked {
		if p < len(offered[i]) {
			decisions = append(decisions, decision{item: asked[i], action: offered[i][p].action, shared: offered[i][p].shared})
		}
	}
	return decisions, nil
}

// carryOut does what was decided, an item at a time, then commits and
// pushes the config's changes.
func (a *app) carryOut(ctx context.Context, r *run, decisions []decision, note string) error {
	what := make([]string, len(decisions))
	for i, d := range decisions {
		what[i] = d.action + " " + d.item.ID
	}
	// What will need an administrator's password, installing or
	// uninstalling, it's asked for first.
	installs, wanted := make(map[string][]string), make(map[string][]string)
	for _, d := range decisions {
		_, isKind := r.drifters[d.item.Kind].(kindDrifter)
		if (isKind || r.isAdminStep(d.item.Kind)) && (d.action == install || d.action == adopt && d.item.State == kind.ByHand) {
			installs[d.item.Kind] = append(installs[d.item.Kind], d.item.Name)
			wanted[d.item.Kind] = append(wanted[d.item.Kind], d.item.Name)
		}
		if isKind && d.action == remove {
			wanted[d.item.Kind] = append(wanted[d.item.Kind], d.item.Name)
		}
	}
	ctx, held, stop := a.holdAdmin(ctx, r, wanted)
	defer stop()
	waiting := make(map[string]bool)
	for kindName, names := range installs {
		for name := range waitingForAdmin(ctx, r, kindName, names, held) {
			waiting[kindName+":"+name] = true
		}
	}
	steps := make([]event.Step, len(decisions))
	doings := make(map[string]string, len(decisions))
	for i, d := range decisions {
		steps[i] = event.Step{Name: d.item.ID, Title: d.item.Name}
		doings[d.item.ID] = doingOf(r.drifters[d.item.Kind].choices(d.item.Item), d.action, d.shared)
	}
	c := startChangesOf(r, steps, doings)
	for _, d := range decisions {
		c.step(ctx, d.item.ID, func(ctx context.Context) check.Result {
			if waiting[d.item.ID] {
				return check.Result{State: check.Failed, Reason: fmt.Sprintf(adminWait, "reconcile")}
			}
			return carryOutOne(ctx, r, c, d, note)
		})
	}
	msg := fmt.Sprintf("kit reconcile (%s): %s", r.machine, strings.Join(what, ", "))
	if note != "" {
		msg += ": " + note
	}
	c.sync(ctx, msg)
	return c.finish()
}

// carryOutOne does what d decided about its item. Any answer but leaving
// an item left by hand ends the leave: the last answer holds.
func carryOutOne(ctx context.Context, r *run, c *changes, d decision, note string) check.Result {
	if d.item.Quiet == drift.Left && d.action != leave {
		if err := drift.Unleave(r.stateDir, d.item.App); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
	}
	switch d.action {
	case snooze:
		if err := drift.Snooze(r.stateDir, d.item.Item, r.now); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		return check.Result{State: check.OK, Summary: "snoozed till " + r.now.Add(drift.SnoozeFor).Format("2 Jan")}
	case leave:
		if err := drift.Leave(r.stateDir, r.now, d.item.App); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		return check.Result{State: check.OK, Summary: "left installed by hand"}
	}
	return r.drifters[d.item.Kind].settle(ctx, r, c, d, note)
}
