package brew

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// lineHelp is how kit brew takes a line copied for brew: kit brew install
// --cask firefox.
var lineHelp = &kind.LineHelp{
	Program: "brew",
	Install: kind.LineVerb{Words: []string{"install"}, More: "kit adds --adopt to a line of casks it can vet, as its own cask installs do, " +
		"so the app already there is taken over, or replaced when it's an older copy of the same major version: " +
		"only to a line whose flags change neither where nor how a cask installs, such as --cask or --verbose; any other line runs as given."},
	Uninstall: kind.LineVerb{Words: []string{"uninstall"}, More: "With --zap, an app's settings kit prefs saved go too, as with kit cask remove --zap; " +
		"without it, they're kept."},
	Example:  "--cask --force owner/tap/tool",
	Declares: "as a cask or a formula, whichever brew installed it as, a tap's named with its tap",
	DryRun:   []string{"-n", "--dry-run"},
	Zap:      "--zap",
	Names:    lineNames, Kinds: lineKinds,
}

// Line runs l, a line copied for brew, as given, in Homebrew's turn to
// change things, watched for going quiet, its sudo asking kit for an
// administrator's password, as kit's own installs and uninstalls run. An
// install runs after brew update, as kit runs brew without updating
// itself, and adopts the apps already there of the casks it installs, as
// kit's own cask installs do (lineAdoption).
func (f formulae) Line(ctx context.Context, l kind.CopiedLine) error {
	cmd := runner.Command{Name: "brew", Args: slices.Concat(l.Verb, l.Args)}
	if !l.Installs {
		defer f.take(ctx, f.Name())()
		return f.watched(ctx, kind.UninstallWords.Doing, f.sudoed(ctx, f.batched(cmd)))
	}
	names, _ := lineNames(l.Args)
	cmd.Timeout = installTimeout(len(names))
	if !l.DryRun {
		f.updated(ctx)
	}
	defer f.take(ctx, f.Name())()
	c := casks(f)
	a, adopting := c.lineAdoption(ctx, l)
	if len(a.refused) > 0 {
		return kind.Joined(a.refused)
	}
	if adopting {
		cmd.Args = slices.Concat(l.Verb, []string{"--adopt"}, l.Args)
	}
	err := f.watched(ctx, kind.InstallWords.Doing, f.sudoed(ctx, f.batched(cmd)))
	if err != nil && adopting {
		return kind.Joined(c.replace(ctx, a, err))
	}
	return err
}

// Refuses says why l, an install line, isn't to run, as kit's own install
// of a cask in it would refuse it, its app already there not one it takes
// over (lineAdoption): nil when it runs.
func (f formulae) Refuses(ctx context.Context, l kind.CopiedLine) error {
	a, _ := casks(f).lineAdoption(ctx, l)
	return kind.Joined(a.refused)
}

// plainFlags are brew install's flags that change neither where nor how a
// cask installs: what its names are given as, and what brew says.
var plainFlags = []string{"--cask", "--casks", "--verbose", "-v", "--quiet", "-q", "--debug", "-d", "--display-times"}

// lineAdoption is how the casks l installs, that aren't installed yet, go
// in, each as kit's own install of it would (adoption), and whether kit
// adopts their apps already there, as its own installs do: when every one
// is a cask kit can vet; one kit can't, as a tap's, has the line run as
// given. Only a line whose flags are plainFlags alone is judged, so a
// replace (replace) installs each as l would; another, or a dry run, runs
// as given, nothing refused.
func (c casks) lineAdoption(ctx context.Context, l kind.CopiedLine) (adoption, bool) {
	plain := !slices.ContainsFunc(l.Args, func(arg string) bool { return strings.HasPrefix(arg, "-") && !slices.Contains(plainFlags, arg) })
	if l.DryRun || !plain {
		return adoption{}, false
	}
	var all adoption
	for _, name := range l.New[casksName] {
		a := c.adoption(ctx, []string{name})
		all.dir = cmp.Or(all.dir, a.dir)
		all.adopt = append(all.adopt, a.adopt...)
		all.plain = append(all.plain, a.plain...)
		all.refused = append(all.refused, a.refused...)
	}
	return all, len(all.adopt) > 0 && len(all.plain) == 0
}

// lineNames are the names in args, a line for brew install or uninstall:
// its flags left out, and the value after one that takes it there rather
// than after an =, as --appdir does. A formula's file, or an address, is
// refused, as kit-config declares names alone.
func lineNames(args []string) ([]string, error) {
	var names []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "-"):
			if takesValue(arg) {
				i++
			}
		case isFileOrAddress(arg):
			return nil, fmt.Errorf("%s is a file or an address, which kit-config can't declare: give brew a name", arg)
		default:
			names = append(names, arg)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("the line names nothing for brew: give it a name, as in kit brew install jq")
	}
	return names, nil
}

// takesValue reports whether flag, of brew install's, takes the argument
// after it as its value: --cc, --bottle-arch, --language, and the folders
// a cask's artifacts go in, as --appdir; never one with its value after an
// =.
func takesValue(flag string) bool {
	if strings.Contains(flag, "=") {
		return false
	}
	return slices.Contains([]string{"--cc", "--bottle-arch", "--language"}, flag) || strings.HasPrefix(flag, "--") && strings.HasSuffix(flag, "dir")
}

// isFileOrAddress reports whether arg, in brew's line, is a file or an
// address rather than a name: a path, a formula's or a cask's file, or a
// URL.
func isFileOrAddress(arg string) bool {
	if strings.Contains(arg, "://") || strings.HasSuffix(strings.ToLower(arg), ".rb") {
		return true
	}
	return slices.ContainsFunc([]string{"/", "./", "../", "~"}, func(start string) bool { return strings.HasPrefix(arg, start) })
}

// lineKinds are the kinds the things args, a line for brew, names can be:
// casks with --cask, formulae with --formula, else either, formulae first,
// as brew takes a name that's both as the formula.
func lineKinds(args []string) []string {
	switch {
	case slices.ContainsFunc(args, func(arg string) bool { return arg == "--cask" || arg == "--casks" }):
		return []string{casksName}
	case slices.ContainsFunc(args, func(arg string) bool { return arg == "--formula" || arg == "--formulae" }):
		return []string{formulaeName}
	}
	return []string{formulaeName, casksName}
}
