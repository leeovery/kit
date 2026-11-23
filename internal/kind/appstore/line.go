package appstore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// lineHelp is how kit mas takes a line copied for mas: kit mas install
// 497799835.
var lineHelp = &kind.LineHelp{
	Program: "mas",
	Install: kind.LineVerb{Words: []string{"install"}, Aliases: map[string][]string{"install": {"get", "purchase"}},
		More: "Each id is looked up in the App Store first, as kit mas add looks one up. " +
			"mas needs an administrator's password, which kit asks for before it runs, as kit mas add does: " +
			"with no terminal to ask at, the line is refused."},
	Uninstall: kind.LineVerb{Words: []string{"uninstall"}, DryRun: []string{"--dry-run"},
		More: "An app's found by its id, whatever name it's declared by. " +
			"mas needs an administrator's password, which kit asks for before it runs. " +
			"The settings kit prefs saved for an app are kept, to come back if it's installed again: " +
			"kit mas remove --zap, in place of the line, removes them."},
	Example:  "497799835",
	Declares: "by its name and id, as kit mas add declares it, as in xcode@497799835",
	Usage:    "<id>...",
	Names:    lineNames,
}

// Line runs l, a line copied for mas, as given, through sudo, as mas needs
// root, but for a dry run, which doesn't. An install runs as kit's own
// installs do, once kit has the password, should it be asking for it
// again: mas going quiet before it's said a word, as it does with no one
// signed in to the App Store, ends it, saying so.
func (s *AppStore) Line(ctx context.Context, l kind.CopiedLine) error {
	args := slices.Concat(l.Verb, l.Args)
	switch {
	case l.DryRun:
		_, err := s.run.Run(ctx, runner.Command{Name: "mas", Args: args})
		return err
	case !l.Installs:
		return kind.AdminAgain(ctx, func() error {
			_, err := s.sudoed(ctx, args, 0, nil)
			return err
		})
	case !kind.AwaitAdmin(ctx):
		return cmp.Or(ctx.Err(), errors.New(passwordNotGiven))
	}
	ids, _ := lineNames(l.Args)
	return s.installing(ctx, args, installTimeout*time.Duration(len(ids)), false)
}

// Refuses refuses no line: an app's id is looked up, and judged, as kit
// mas add does, before it runs.
func (*AppStore) Refuses(context.Context, kind.CopiedLine) error { return nil }

// lineNames are the apps' ids in args, a line for mas install or
// uninstall: its flags left out. One that isn't an id, or a flag that has
// mas take every app, or bundle ids, is refused, as kit-config declares
// an app by its id.
func lineNames(args []string) ([]string, error) {
	var ids []string
	for _, arg := range args {
		switch {
		case arg == "--all" || arg == "--bundle":
			return nil, fmt.Errorf("kit-config declares each app by its id, so takes no %s: give mas the apps' ids", arg)
		case strings.HasPrefix(arg, "-"):
		case !isID(arg):
			return nil, fmt.Errorf("%s isn't an App Store app's id: give mas the id, as in kit mas install 497799835", arg)
		default:
			ids = append(ids, arg)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("the line names nothing for mas: give it an app's id, as in kit mas install 497799835")
	}
	return ids, nil
}

// isID reports whether arg is an app's id: a whole number above 0.
func isID(arg string) bool {
	n, err := strconv.ParseUint(arg, 10, 64)
	return err == nil && n > 0
}
