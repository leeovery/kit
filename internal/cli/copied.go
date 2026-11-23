package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/runner"
)

// A line copied for a kind's program, as a README gives it, with kit and
// the kind's name in front: kit brew install --cask --force owner/tap/tool.
// The program runs it as given, then kit-config records each name it
// installed, or stops recording each it uninstalled: names alone, never
// the line's flags.

// newLineCommands are kit <kind> install and uninstall, for a kind whose
// command takes a line copied for its program (kind.LineHelp).
func newLineCommands(a *app, d kind.Descriptor) []*cobra.Command {
	l := d.Help.Line
	program, install, uninstall := d.Name, l.Install, l.Uninstall
	installs := a.lineCommand(d, install,
		fmt.Sprintf("Run a copied %s %s line, then declare what it installed", program, install),
		paragraphs(
			fmt.Sprintf("Run a line copied for %[1]s %[2]s, as a README gives it, with kit in front: kit %[1]s %[2]s %[3]s runs %[1]s %[2]s %[3]s, its output shown as it goes.", program, install, l.Example),
			fmt.Sprintf("Then each name it installed is declared for this Mac, or every Mac with --shared, %s. "+
				"The line's flags are this install's alone: kit-config records the names. "+
				"A name declared already stays as it is, and one that didn't install isn't declared. "+
				"The change is committed and pushed to the config repository.", l.Declares),
			fmt.Sprintf("kit's own flags in the line are --shared, and --note, which records why after each name: %[1]s has neither. "+
				"kit's other flags, such as --plain, go before %[1]s, never in the line. "+
				"A file's path or an address, in place of a name, is refused before anything runs.", program),
		))
	installs.Flags().Bool("shared", false, "declare for every Mac, not this one alone")
	installs.Flags().String("note", "", "why it's declared, kept after each name")
	uninstalls := a.lineCommand(d, uninstall,
		fmt.Sprintf("Run a copied %s %s line, then undeclare what it uninstalled", program, uninstall),
		paragraphs(
			fmt.Sprintf("Run a line copied for %[1]s %[2]s, with kit in front, as kit %[1]s %[3]s does, "+
				"then take each name no longer installed out of this Mac's declarations file. "+
				"One declared for every Mac needs --shared, which takes it out of the shared file, every Mac's list: without it, nothing runs. "+
				"A name still installed stays declared. The change is committed and pushed to the config repository.", program, uninstall, install),
			fmt.Sprintf("kit's own flag in the line is --shared; kit's other flags go before %s.", program),
		))
	uninstalls.Flags().Bool("shared", false, "take it out of the shared file, every Mac's list")
	return []*cobra.Command{installs, uninstalls}
}

// paragraphs is a command's long help, of paragraphs, each wrapped to fit
// its plain help, as cobra prints it, whose text is the words given.
func paragraphs(paras ...string) string {
	wrapped := make([]string, len(paras))
	for i, p := range paras {
		wrapped[i] = strings.Join(look.Wrap(p, 78), "\n")
	}
	return strings.Join(wrapped, "\n\n")
}

// lineCommand is kit <kind> <verb>, running a line copied for d's program's
// verb. cobra reads none of its flags, as the line's are the program's:
// kit's own in it are picked out by hand (readLine), and kit's other flags,
// before its name, are read as it starts (lineAfter).
func (a *app) lineCommand(d kind.Descriptor, verb, short, long string) *cobra.Command {
	var line []string
	return &cobra.Command{
		Use:                verb + " <name>...",
		Short:              short,
		Long:               long,
		DisableFlagParsing: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) (err error) {
			line, err = a.lineAfter(cmd)
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := readLine(line, verb == d.Help.Line.Install)
			switch {
			case err != nil:
				return usageError{err}
			case l.help:
				return cmd.Help()
			}
			names, err := d.Help.Line.Names(l.args)
			if err != nil {
				return err
			}
			command := strings.Join(slices.Concat([]string{d.Name, verb}, l.args), " ")
			return a.preparedAs(cmd.Context(), command, d.Name+"-"+verb, func(ctx context.Context, r *run) error {
				return a.runLine(ctx, r, d, verb, l, once(names))
			})
		},
	}
}

// lineAfter reads kit's own flags before cmd's name in kit's command line,
// cmd being one that takes a line copied for a program, as kit starts, and
// returns the line: what's after its name. cobra gives cmd both, run
// together, and the line can hold flags of kit's names, as brew's
// --verbose, which are the program's.
func (a *app) lineAfter(cmd *cobra.Command) ([]string, error) {
	words := len(strings.Fields(cmd.CommandPath())) - 1
	var before []string
	for i, arg := range a.args {
		if strings.HasPrefix(arg, "-") {
			before = append(before, arg)
			continue
		}
		if words--; words > 0 {
			continue
		}
		if err := cmd.InheritedFlags().Parse(before); err != nil {
			return nil, usageError{err}
		}
		a.ready(cmd)
		return a.args[i+1:], nil
	}
	return nil, fmt.Errorf("%s isn't in kit's command line", cmd.CommandPath())
}

// copiedLine is a line copied for a kind's program, kit's own flags taken
// out of it: --shared; --note, for an install; and -h or --help, which
// show kit's help.
type copiedLine struct {
	args   []string
	shared bool
	note   string
	help   bool
}

// readLine takes kit's own flags out of args, a line copied for a program,
// --note among them when notes says so, its value after it or after an =.
func readLine(args []string, notes bool) (copiedLine, error) {
	var l copiedLine
	for i := 0; i < len(args); i++ {
		arg := args[i]
		note, noted := strings.CutPrefix(arg, "--note=")
		switch {
		case arg == "--shared":
			l.shared = true
		case arg == "-h" || arg == "--help":
			l.help = true
		case notes && noted:
			l.note = note
		case notes && arg == "--note" && i+1 < len(args):
			i++
			l.note = args[i]
		case notes && arg == "--note":
			return l, errors.New("--note needs a value: why it's declared")
		default:
			l.args = append(l.args, arg)
		}
	}
	return l, nil
}

// runLine runs l through the program of d's kind, then has each of names
// declared, installed, or undeclared, uninstalled, a step each, and one
// commit and push of the config's changes. Uninstalling, a name declared
// for every Mac is refused without --shared, before anything runs.
func (a *app) runLine(ctx context.Context, r *run, d kind.Descriptor, verb string, l copiedLine, names []string) error {
	liner, ks, err := copiedKinds(r, d, l.args)
	if err != nil {
		return err
	}
	installing := verb == d.Help.Line.Install
	if !installing {
		if err := sharedRefused(r, ks, names, l.shared); err != nil {
			return err
		}
	}
	ctx, _, stop := a.holdAdmin(ctx, r, adminWanted(ks, names))
	defer stop()
	words, doing := d.Installs(), "declaring"
	if !installing {
		words, doing = d.Removes(), "undeclaring"
	}
	program := liner.Program()
	steps := []event.Step{{Name: program, Title: program}}
	doings := map[string]string{program: words.Doing}
	for _, name := range names {
		steps = append(steps, event.Step{Name: name, Title: name})
		doings[name] = doing
	}
	c := startChangesOf(r, steps, doings)
	c.step(ctx, program, func(ctx context.Context) check.Result {
		if err := liner.Line(event.WithChanging(ctx), verb, l.args); err != nil {
			return check.Result{State: check.Failed, Reason: couldnt(words) + engine.Reason(err)}
		}
		return check.Result{State: check.OK, Summary: words.Done}
	})
	found := &lookup{kinds: ks, names: names, listed: map[string]listing{}}
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			if installing {
				return declareInstalled(ctx, r, c, found, name, l)
			}
			return undeclareUninstalled(ctx, c, found, name, l.shared, words)
		})
	}
	c.sync(ctx, commitMessage(verb, d.Name, c.whoWrote(names), r.machine, l.note))
	return c.finish()
}

// copiedKinds are the run's kind d describes, which runs a line, and the
// kinds the things args, its line, names can be, in the order its program
// takes a name more than one has.
func copiedKinds(r *run, d kind.Descriptor, args []string) (lineRunner, []kind.Kind, error) {
	k, err := r.kindNamed(d.Name)
	if err != nil {
		return nil, nil, err
	}
	liner, ok := k.(lineRunner)
	if !ok {
		return nil, nil, fmt.Errorf("kit %s runs no line of %s's", d.Name, k.Program())
	}
	var ks []kind.Kind
	for _, name := range d.Help.Line.Kinds(args) {
		k, err := r.kindNamed(name)
		if err != nil {
			return nil, nil, err
		}
		ks = append(ks, k)
	}
	return liner, ks, nil
}

// adminWanted are names, as the things of each of ks that can need an
// administrator's password to install or uninstall would be, for kit to
// ask for it first, when one does (holdAdmin).
func adminWanted(ks []kind.Kind, names []string) map[string][]string {
	wanted := map[string][]string{}
	for _, k := range ks {
		if _, ok := k.(kind.Admin); ok {
			wanted[k.Name()] = names
		}
	}
	return wanted
}

// lineRunner is a kind that runs a line copied for its program.
type lineRunner interface {
	kind.Kind
	kind.Liner
}

// sharedRefused says why uninstalling names, things of ks, is refused
// without shared: one's declared for every Mac.
func sharedRefused(r *run, ks []kind.Kind, names []string, shared bool) error {
	if shared {
		return nil
	}
	for _, name := range names {
		for _, k := range ks {
			inShared, err := declaredShared(r, k.Name(), name)
			if err != nil {
				return err
			}
			if inShared {
				return fmt.Errorf("%s is %s; nothing was uninstalled", name, everyMac)
			}
		}
	}
	return nil
}

// declareInstalled declares name, a name in l, as the thing it's installed
// as, found, as kit <kind> add declares a thing installed already; one
// that isn't installed isn't declared.
func declareInstalled(ctx context.Context, r *run, c *changes, found *lookup, name string, l copiedLine) check.Result {
	k, err := found.as(ctx, name)
	switch {
	case err != nil:
		return check.Result{State: check.Failed, Reason: "couldn't tell whether it's installed: " + err.Error()}
	case k == nil:
		return check.Result{State: check.Failed, Reason: "couldn't declare: it isn't installed"}
	}
	verb := descriptorOf(k).Installs().Done
	if h := descriptorOf(k).Help; len(found.kinds) > 1 && h != nil && h.One != "" {
		verb += " as " + h.One
	}
	mine, err := declaredFor(r, k.Name(), name, l.shared)
	if err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't declare: " + err.Error()}
	}
	opts := addOptions{shared: l.shared, note: l.note}
	return declareAdding(ctx, r, c, k, r.scope(l.shared), name, opts, verb, adding{mine: mine})
}

// undeclareUninstalled takes name, a name in a line that uninstalled
// things, said as words, out of this Mac's declarations, and the shared
// ones with shared, of each kind found looks in, once it's installed as
// none of them: one still installed stays declared.
func undeclareUninstalled(ctx context.Context, c *changes, found *lookup, name string, shared bool, words kind.Words) check.Result {
	k, err := found.as(ctx, name)
	switch {
	case err != nil:
		return check.Result{State: check.Failed, Reason: "couldn't tell whether it's installed: " + err.Error()}
	case k != nil:
		return check.Result{State: check.Failed, Reason: "couldn't undeclare: it's still installed"}
	}
	var from []string
	for _, k := range found.kinds {
		out, err := c.undeclareHere(k.Name(), name, shared)
		if err != nil {
			return check.Result{State: check.Failed, Reason: words.Done + ", but couldn't undeclare: " + err.Error()}
		}
		from = append(from, out...)
	}
	if len(from) == 0 {
		return check.Result{State: check.OK}.Saying(words.Done, "it wasn't declared")
	}
	return check.Result{State: check.OK}.Saying(words.Done, "out of "+strings.Join(once(from), " and "))
}

// lookup finds what a line's names are installed as, of kinds, the kinds
// its things can be, each kind's things listed once.
type lookup struct {
	kinds  []kind.Kind
	names  []string
	listed map[string]listing
}

// listing is what a kind has installed, as listed, or why it couldn't be.
type listing struct {
	all []kind.Installed
	err error
}

// as is the kind of kinds name is installed as: the first listing it by
// the name given, else the first listing it by its full name, as that
// kind resolves it, as an alias names a formula; nil for none.
func (l *lookup) as(ctx context.Context, name string) (kind.Kind, error) {
	for _, resolve := range []bool{false, true} {
		for _, k := range l.kinds {
			if in, err := l.has(ctx, k, name, resolve); err != nil || in {
				return k, err
			}
		}
	}
	return nil, nil
}

// has reports whether k lists name installed: by the name given, or, with
// resolve, by its full name, as k resolves it.
func (l *lookup) has(ctx context.Context, k kind.Kind, name string, resolve bool) (bool, error) {
	all, err := l.list(ctx, k)
	if err != nil || !resolve {
		return listsName(all, name), err
	}
	full, err := kind.Resolve(ctx, k, []string{name})
	if err != nil {
		return false, err
	}
	return full[name] != "" && listsName(all, full[name]), nil
}

// list is what k has installed, listed the first time it's asked: nothing
// while k's program isn't installed.
func (l *lookup) list(ctx context.Context, k kind.Kind) ([]kind.Installed, error) {
	if got, ok := l.listed[k.Name()]; ok {
		return got.all, got.err
	}
	all, err := kind.InstalledOf(ctx, k, l.names)
	if errors.Is(err, runner.ErrNotFound) {
		all, err = nil, nil
	}
	l.listed[k.Name()] = listing{all: all, err: err}
	return all, err
}

// listsName reports whether all holds name, in any case, as a package's
// name is taken.
func listsName(all []kind.Installed, name string) bool {
	return slices.ContainsFunc(all, func(it kind.Installed) bool { return strings.EqualFold(it.Name, name) })
}
