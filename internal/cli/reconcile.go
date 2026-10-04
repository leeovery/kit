package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/render"
)

// What can be done about drift.
const (
	adopt     = "adopt"
	remove    = "remove"
	install   = "install"
	undeclare = "undeclare"
	snooze    = "snooze"
)

// reconcileSchema is kit reconcile --json's document's version.
const reconcileSchema = 1

// reconcileOptions are kit reconcile's flags.
type reconcileOptions struct {
	adopt, remove, install, undeclare, snooze bool
	shared, all                               bool
	group, note                               string
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
	group  string
}

func newReconcileCommand(a *app) *cobra.Command {
	var opts reconcileOptions
	cmd := &cobra.Command{
		Use:   "reconcile [<id>]",
		Short: "Settle drift: adopt, remove, install, undeclare or snooze what differs from the config",
		Long: `Settle drift: what's installed but not declared, declared but not installed,
or left by something since removed. At a terminal, kit goes through each item
that needs attention (--all: every item, new and snoozed too), asking what to
do with it, then does it all, committing and pushing the config's changes.

Without a terminal, or with --json, it lists the items with their ids and
choices. Name an item's id, with what to do with it, to settle that one:

  kit reconcile brew:ffmpeg --adopt [--shared] [--group "<heading>"] [--note "why"]
  kit reconcile brew:node@20 --remove
  kit reconcile cask:zoom --install
  kit reconcile brew:jq --undeclare [--shared]
  kit reconcile cask:firefox --snooze           (quiet for 7 days)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := a.prepare("reconcile", "reconcile")
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
	f.BoolVar(&opts.adopt, "adopt", false, "declare it: for this Mac, or every Mac with --shared")
	f.BoolVar(&opts.remove, "remove", false, "uninstall it")
	f.BoolVar(&opts.install, "install", false, "install it, as it's declared")
	f.BoolVar(&opts.undeclare, "undeclare", false, "take it out of this Mac's file, or the shared one with --shared")
	f.BoolVar(&opts.snooze, "snooze", false, "quiet it for 7 days")
	f.BoolVar(&opts.shared, "shared", false, "for every Mac: adopt into, or undeclare from, the shared file")
	f.BoolVar(&opts.all, "all", false, "at a terminal, every item, quiet ones too")
	f.StringVar(&opts.group, "group", "", "the group an adopted item goes in")
	f.StringVar(&opts.note, "note", "", "why it's adopted, kept after its name")
	return cmd
}

func (a *app) reconcile(ctx context.Context, r *run, args []string, opts reconcileOptions) error {
	items, err := a.driftItems(ctx, r)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		d, err := decide(items, args[0], opts)
		if err != nil {
			return err
		}
		return a.carryOut(ctx, r, []decision{d}, opts.note)
	}
	if a.json || !a.pretty(a.Stdout) {
		return a.listDrift(r, items)
	}
	decisions, err := a.askAbout(ctx, r, items, opts)
	if err != nil {
		return err
	}
	if len(decisions) == 0 {
		_, err := fmt.Fprintln(a.Stdout, "Nothing to reconcile")
		return err
	}
	return a.carryOut(ctx, r, decisions, opts.note)
}

// driftItems are the kinds' items: what differs from the config, quiet or
// not, each with what can be done about it.
func (a *app) driftItems(ctx context.Context, r *run) ([]driftItem, error) {
	var items []driftItem
	for _, name := range r.kinds {
		k := r.kindsByName[name]
		res := drift.Quieten(kind.Compare(ctx, k, r.lists[name]), r.record, r.now)
		if res.State == check.Failed {
			return nil, fmt.Errorf("couldn't check %s: %s", k.Title(), res.Reason)
		}
		_, ownFile := k.(kind.Declarer)
		for _, it := range res.Items {
			items = append(items, driftItem{Item: it, Kind: name, Choices: choices(it, !ownFile)})
		}
	}
	return items, nil
}

// choices are what can be done about it: adopting and undeclaring only
// where kit writes the kind's declarations.
func choices(it check.Item, writable bool) []string {
	var all []string
	switch it.State {
	case kind.Extra:
		all = []string{adopt, remove, snooze}
	case kind.UnusedDependency:
		all = []string{remove, adopt, snooze}
	case kind.Missing:
		if it.Action == kind.Install {
			all = []string{install, undeclare, snooze}
		} else {
			all = []string{undeclare, snooze}
		}
	default:
		all = []string{snooze}
	}
	if writable {
		return all
	}
	return slices.DeleteFunc(all, func(c string) bool { return c == adopt || c == undeclare })
}

// decide is what the flags say to do with the item id.
func decide(items []driftItem, id string, opts reconcileOptions) (decision, error) {
	i := slices.IndexFunc(items, func(it driftItem) bool { return it.ID == id })
	if i < 0 {
		return decision{}, fmt.Errorf("no item %s: kit reconcile lists them", id)
	}
	item := items[i]
	var actions []string
	for action, set := range map[string]bool{adopt: opts.adopt, remove: opts.remove, install: opts.install, undeclare: opts.undeclare, snooze: opts.snooze} {
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
	}
	return decision{item: item, action: actions[0], shared: opts.shared, group: opts.group}, nil
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
	case snooze, undeclare:
		return action + "d"
	case adopt, install:
		return action + "ed"
	}
	return action + "d"
}

// listDrift prints the items, as plain lines or one JSON document; it
// returns attention when any isn't quiet.
func (a *app) listDrift(r *run, items []driftItem) error {
	loud := slices.ContainsFunc(items, func(it driftItem) bool { return it.Quiet == "" })
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
	if loud {
		return attention{}
	}
	return nil
}

// The answers askAbout offers, by what they do.
var answerFor = map[string]string{
	"declare it, for this Mac":  adopt,
	"declare it, for every Mac": adopt + "-shared",
	"uninstall it":              remove,
	"install it":                install,
	"undeclare it":              undeclare,
	"snooze it for 7 days":      snooze,
	"leave it for now":          "",
	"stop here":                 "stop",
}

// askAbout asks, of each item that needs attention (every item, with
// --all), what to do with it, and for one adopted, which group of the file
// it goes in: every question before anything is done.
func (a *app) askAbout(ctx context.Context, r *run, items []driftItem, opts reconcileOptions) ([]decision, error) {
	var decisions []decision
	for _, it := range items {
		if it.Quiet != "" && !opts.all {
			continue
		}
		var answers []string
		for _, c := range it.Choices {
			switch c {
			case adopt:
				answers = append(answers, "declare it, for this Mac", "declare it, for every Mac")
			case remove:
				answers = append(answers, "uninstall it")
			case install:
				answers = append(answers, "install it")
			case undeclare:
				answers = append(answers, "undeclare it")
			case snooze:
				answers = append(answers, "snooze it for 7 days")
			}
		}
		answers = append(answers, "leave it for now", "stop here")
		i, err := a.Choose(ctx, describe(it, r.now), answers)
		if errors.Is(err, ask.ErrCancelled) {
			return nil, errors.New("cancelled: nothing was changed")
		}
		if err != nil {
			return nil, err
		}
		action := answerFor[answers[i]]
		switch action {
		case "":
			continue
		case "stop":
			return decisions, nil
		}
		d := decision{item: it, action: strings.TrimSuffix(action, "-shared"), shared: strings.HasSuffix(action, "-shared"), group: opts.group}
		if d.action == adopt && d.group == "" {
			if d.group, err = a.askGroup(ctx, r, d); err != nil {
				return nil, err
			}
		}
		decisions = append(decisions, d)
	}
	return decisions, nil
}

// askGroup asks which group of the file an adopted item goes in.
func (a *app) askGroup(ctx context.Context, r *run, d decision) (string, error) {
	file := d.item.Kind + "." + r.machine
	if d.shared {
		file = d.item.Kind
	}
	headings, err := r.cfg.Groups(file)
	if err != nil {
		return "", err
	}
	options := []string{config.ToBeSorted}
	for _, h := range headings {
		if h != config.ToBeSorted {
			options = append(options, h)
		}
	}
	i, err := a.Choose(ctx, fmt.Sprintf("Which group of %s for %s?", file, d.item.Name), options)
	if errors.Is(err, ask.ErrCancelled) {
		return "", errors.New("cancelled: nothing was changed")
	}
	if err != nil {
		return "", err
	}
	return options[i], nil
}

// describe says what an item is, for a question about it.
func describe(it driftItem, now time.Time) string {
	what := map[string]string{
		kind.Extra:            "installed, not declared",
		kind.Missing:          "declared, not installed",
		kind.UnusedDependency: "installed for something since removed, needed by nothing",
	}[it.State]
	if it.Detail != "" {
		what += " (" + it.Detail + ")"
	}
	if !it.Since.IsZero() {
		what += fmt.Sprintf(", for %s", age(now.Sub(it.Since)))
	}
	return fmt.Sprintf("%s (%s): %s. What now?", it.Name, it.Kind, what)
}

// age says how long d is, roughly.
func age(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case days >= 2:
		return fmt.Sprintf("%d days", days)
	case days == 1:
		return "a day"
	}
	return "under a day"
}

// carryOut does what was decided, an item at a time, then commits and
// pushes the config's changes.
func (a *app) carryOut(ctx context.Context, r *run, decisions []decision, note string) error {
	names := make([]string, len(decisions))
	what := make([]string, len(decisions))
	for i, d := range decisions {
		names[i] = d.item.ID
		what[i] = d.action + " " + d.item.ID
	}
	installs := make(map[string][]string)
	for _, d := range decisions {
		if d.action == install {
			installs[d.item.Kind] = append(installs[d.item.Kind], d.item.Name)
		}
	}
	held, stop := a.holdAdmin(ctx, r, installs)
	defer stop()
	waiting := make(map[string]bool)
	for kindName, names := range installs {
		for name := range waitingForAdmin(ctx, r, kindName, names, held) {
			waiting[kindName+":"+name] = true
		}
	}
	c := startChanges(r, names)
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

func carryOutOne(ctx context.Context, r *run, c *changes, d decision, note string) check.Result {
	k := r.kindsByName[d.item.Kind]
	switch d.action {
	case adopt:
		file := d.item.Kind + "." + r.machine
		if d.shared {
			file = d.item.Kind
		}
		group := d.group
		if group == config.ToBeSorted {
			group = ""
		}
		return addOne(ctx, r, c, k, file, d.item.Name, group, addOptions{shared: d.shared, note: note})
	case remove, undeclare:
		return removeOne(ctx, r, c, k, d.item.Name, d.shared)
	case install:
		if err := k.Install(ctx, []string{d.item.Name}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't install: " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "installed"}
	case snooze:
		if err := drift.Update(r.stateDir, func(rec *drift.Record) { rec.Snooze(d.item.ID, r.now) }); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		return check.Result{State: check.OK, Summary: "snoozed till " + r.now.Add(drift.SnoozeFor).Format("2 Jan")}
	}
	return check.Result{State: check.Failed, Reason: "nothing to do: " + d.action}
}
