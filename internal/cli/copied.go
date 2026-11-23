package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/runner"
)

// addLineCommands adds kit <kind> install and uninstall to cmd, for a kind
// whose command takes a line copied for its program (kind.LineHelp), as a
// README gives it, with kit and the kind's name in front: kit brew install
// --cask --force owner/tap/tool. The program runs it as given, then
// kit-config records each name it installed, or stops recording each it
// uninstalled: names alone, never the line's flags. A verb of more than
// one word has a command of kit's for each word before its last, as in kit
// gh extension install.
func addLineCommands(a *app, cmd *cobra.Command, d kind.Descriptor) {
	l := d.Help.Line
	noun, program := d.Name, l.Program
	install, uninstall := strings.Join(l.Install.Words, " "), strings.Join(l.Uninstall.Words, " ")
	installs := a.lineCommand(d, l.Install, true,
		fmt.Sprintf("Run a copied %s %s line, then declare what it installed", program, install),
		paragraphs(
			fmt.Sprintf("Run a line copied for %[1]s %[2]s, as a README gives it, with kit in front: kit %[3]s %[2]s %[4]s runs %[1]s %[2]s %[4]s, its output shown as it goes.", program, install, noun, l.Example),
			fmt.Sprintf("Then each name it installed is declared for this Mac, or every Mac with --shared, %s. "+
				"The line's flags are this install's alone: kit-config records the names. "+
				"A name installed already is declared too, and one that didn't install isn't. "+
				"With %s, nothing is declared. "+
				"The change is committed and pushed to the config repository.", l.Declares, strings.Join(l.DryRun, " or ")),
			fmt.Sprintf("Before anything runs, each name is checked as kit %[1]s add checks it, and the whole line is refused if one can't be declared, "+
				"or needs an administrator's password with no terminal to ask at. %[2]s", noun, l.Install.More),
			fmt.Sprintf("kit's own flags in the line are --shared, and --note, which records why after each name: %[1]s has neither. "+
				"kit's other flags, such as --plain, go before %[2]s, never in the line. "+
				"A file's path or an address, in place of a name, is refused before anything runs.", program, noun),
		))
	installs.Flags().Bool("shared", false, "declare for every Mac, not this one alone")
	installs.Flags().String("note", "", "why it's declared, kept after each name")
	uninstalls := a.lineCommand(d, l.Uninstall, false,
		fmt.Sprintf("Run a copied %s %s line, then undeclare what it uninstalled", program, uninstall),
		paragraphs(
			fmt.Sprintf("Run a line copied for %[1]s %[2]s, with kit in front, as kit %[3]s %[4]s does, "+
				"then take each name no longer installed out of this Mac's declarations file, by whichever name it's declared: "+
				"the one given, its short name or its full one. "+
				"A name still installed stays declared. The change is committed and pushed to the config repository.", program, uninstall, noun, install),
			fmt.Sprintf("One declared for every Mac needs --shared, which takes it out of the shared file, every Mac's list: "+
				"without it, the whole line is refused before anything runs. %s", l.Uninstall.More),
			fmt.Sprintf("kit's own flag in the line is --shared; kit's other flags go before %s.", noun),
		))
	uninstalls.Flags().Bool("shared", false, "take it out of the shared file, every Mac's list")
	parentOf(cmd, l.Install.Words, program).AddCommand(installs)
	parentOf(cmd, l.Uninstall.Words, program).AddCommand(uninstalls)
}

// parentOf is the command under cmd that holds the command of a verb's
// last word, of words: one for each word before it, made when it isn't
// there yet.
func parentOf(cmd *cobra.Command, words []string, program string) *cobra.Command {
	for _, word := range words[:len(words)-1] {
		i := slices.IndexFunc(cmd.Commands(), func(c *cobra.Command) bool { return c.Name() == word })
		if i >= 0 {
			cmd = cmd.Commands()[i]
			continue
		}
		sub := &cobra.Command{Use: word, Short: fmt.Sprintf("Run a copied %s %s line", program, word)}
		cmd.AddCommand(sub)
		cmd = sub
	}
	return cmd
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

// lineCommand is kit <kind> <verb>, running a line copied for d's
// program's verb v, which installs or uninstalls. cobra reads none of its
// flags, as the line's are the program's: kit's own in it are picked out
// by hand (readLine), and kit's other flags, before its name, are read as
// it starts (lineAfter).
func (a *app) lineCommand(d kind.Descriptor, v kind.LineVerb, installs bool, short, long string) *cobra.Command {
	var given []string
	h := d.Help.Line
	return &cobra.Command{
		Use:                v.Words[len(v.Words)-1] + " <name>...",
		Aliases:            v.Aliases,
		Short:              short,
		Long:               long,
		DisableFlagParsing: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) (err error) {
			given, err = a.lineAfter(cmd)
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := readLine(given, installs)
			switch {
			case err != nil:
				return usageError{err}
			case l.help:
				return cmd.Help()
			}
			if l.names, err = h.Names(l.args); err != nil {
				return err
			}
			l.names, l.verb, l.installs = once(l.names), v.Words, installs
			l.dryRun = slices.ContainsFunc(l.args, func(arg string) bool { return slices.Contains(h.DryRun, arg) })
			command := strings.Join(slices.Concat([]string{d.Name}, v.Words, l.args), " ")
			return a.preparedAs(cmd.Context(), command, d.Name+"-"+strings.Join(v.Words, "-"), func(ctx context.Context, r *run) error {
				return a.runLine(ctx, r, d, l)
			})
		},
	}
}

// lineAfter reads kit's own flags before cmd's name in kit's command line,
// cmd being one that takes a line copied for a program, as kit starts, and
// returns the line: what's after its name. cobra gives cmd both, run
// together, and the line can hold flags of kit's names, as brew's
// --verbose, which are the program's. kit's own take no values, so a word
// that isn't a flag is a command's name.
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

// copiedLine is a line copied for a kind's program: the program's verb, as
// words, and whether it installs; the arguments after it, kit's own flags
// taken out: --shared; --note, for an install; and -h or --help, which
// show kit's help; the names in it; and whether it's a dry run.
type copiedLine struct {
	verb, args, names []string
	installs, dryRun  bool
	shared, help      bool
	note              string
}

// errNoteUninstalling is --note given to an uninstall.
var errNoteUninstalling = errors.New("--note records why a name's declared: an uninstall declares nothing, so takes none")

// readLine takes kit's own flags out of args, a line copied for a program,
// --note among them for an install, its value after it or after an =.
func readLine(args []string, installs bool) (copiedLine, error) {
	var l copiedLine
	for i := 0; i < len(args); i++ {
		arg := args[i]
		note, noted := strings.CutPrefix(arg, "--note=")
		switch {
		case arg == "--shared":
			l.shared = true
		case arg == "-h" || arg == "--help":
			l.help = true
		case (noted || arg == "--note") && !installs:
			return l, errNoteUninstalling
		case noted:
			l.note = note
		case arg == "--note" && i+1 < len(args):
			i++
			l.note = args[i]
		case arg == "--note":
			return l, errors.New("--note needs a value: why it's declared")
		default:
			l.args = append(l.args, arg)
		}
	}
	return l, nil
}

// copiedKinds are the run's kind d describes, which runs a line, and the
// kinds the things args, its line, names can be, in the order its program
// takes a name more than one has.
func copiedKinds(r *run, d kind.Descriptor, args []string) (kind.Liner, []kind.Kind, error) {
	k, err := r.kindNamed(d.Name)
	if err != nil {
		return nil, nil, err
	}
	liner, ok := k.(kind.Liner)
	if !ok {
		return nil, nil, fmt.Errorf("kit %s runs no line of %s's", d.Name, d.Help.Line.Program)
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

// lookup finds what a line's names are installed as, of kinds, the kinds
// its things can be, each kind's things listed once, and each name's full
// name resolved once.
type lookup struct {
	kinds  []kind.Kind
	names  []string
	listed map[string]listing
	full   map[string]string
}

// newLookup is a lookup of names, things of kinds, as they are now.
func newLookup(kinds []kind.Kind, names []string) *lookup {
	return &lookup{kinds: kinds, names: names, listed: map[string]listing{}, full: map[string]string{}}
}

// listing is what a kind has installed, as listed, or why it couldn't be.
type listing struct {
	all []kind.Installed
	err error
}

// as is the kind of kinds name is installed as: the first listing it by
// the name given; else the first that knows it, when that one lists it by
// its full name, as an alias names a formula; nil for none.
func (l *lookup) as(ctx context.Context, name string) (kind.Kind, error) {
	for _, k := range l.kinds {
		all, err := l.list(ctx, k)
		if err != nil || listsName(all, name) {
			return k, err
		}
	}
	for _, k := range l.kinds {
		full, err := l.resolved(ctx, k, name)
		switch {
		case err != nil:
			return nil, err
		case full == "":
			continue
		}
		if all, _ := l.list(ctx, k); listsName(all, full) {
			return k, nil
		}
		return nil, nil
	}
	return nil, nil
}

// settle is the kind of kinds name is, and whether it's installed: the
// one it's installed as; else the first that knows it, as its program
// installs a name more than one kind has; nil when none does.
func (l *lookup) settle(ctx context.Context, name string) (kind.Kind, bool, error) {
	if k, err := l.as(ctx, name); err != nil || k != nil {
		return k, k != nil, err
	}
	for _, k := range l.kinds {
		full, err := l.resolved(ctx, k, name)
		if err != nil || full != "" {
			return k, false, err
		}
	}
	return nil, false, nil
}

// resolved is name's full name, as k resolves it, once: "" when k doesn't
// know it.
func (l *lookup) resolved(ctx context.Context, k kind.Kind, name string) (string, error) {
	key := k.Name() + ":" + name
	if full, ok := l.full[key]; ok {
		return full, nil
	}
	full, err := kind.Resolve(ctx, k, []string{name})
	if err != nil {
		return "", err
	}
	l.full[key] = full[name]
	return full[name], nil
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

// declaredAs are the names each of names, things of k, is declared by in
// this Mac's list, shared or its own: as given, or a tap's thing by its
// short name or its full one (sameThing); else, with resolve, by any that's
// the same thing as k resolves them, as an alias, resolved all at once.
func declaredAs(ctx context.Context, r *run, k kind.Kind, names []string, resolve bool) (map[string][]string, error) {
	declared := r.lists[k.Name()].Names()
	as := map[string][]string{}
	var unmatched []string
	for _, name := range names {
		if i := slices.IndexFunc(declared, func(d string) bool { return sameThing(strings.ToLower(d), strings.ToLower(name)) }); i >= 0 {
			as[name] = declared[i : i+1]
		} else {
			unmatched = append(unmatched, name)
		}
	}
	if !resolve || len(unmatched) == 0 || len(declared) == 0 {
		return as, nil
	}
	full, err := kind.Resolve(ctx, k, once(slices.Concat(unmatched, declared)))
	if err != nil {
		return nil, err
	}
	for _, name := range unmatched {
		if thing, ok := full[name]; ok {
			as[name] = slices.DeleteFunc(slices.Clone(declared), func(d string) bool { return !strings.EqualFold(full[d], thing) })
		}
	}
	return as, nil
}
