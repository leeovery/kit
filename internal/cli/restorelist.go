package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/steps"
)

// restoreList is what [restore] is, as kit restore remove says where a
// path isn't.
const restoreList = "restore list"

// restoreOptions are kit restore add's options: what in the path to leave
// out, as often as wanted, and where it's declared, and why.
type restoreOptions struct {
	skips []string
	addOptions
}

// newRestoreCommand is kit restore: the paths a new Mac's data restore
// carries across.
func newRestoreCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     config.RestoreKind,
		Short:   "Data a new Mac carries across from the Mac it started from",
		GroupID: groupFiles,
	}
	var opts restoreOptions
	add := &cobra.Command{
		Use:   "add <path>",
		Short: "Declare a path of data a new Mac carries across (--shared: every Mac)",
		Long: `Declare a path in the home folder, a folder or a file, as data a new Mac
carries across: code, history, ledgers, a tool's state. A new Mac started
from this one restores it from this Mac, else from a backup of it.

What kit links, prefs saves or a kind puts in place, kit's own folders and
what the backups leave out are left out by themselves, never a --skip; a
path that's one of those, or in one, or one another line holds or is in, is
refused. --skip names what in it is neither the person's data nor kit's, as
a tool's credentials its own setup makes again: relative to the path, a path
or a pattern as rsync takes one, as often as wanted.

Adding a line copies nothing. On a Mac in use, a path added later is
restored only by kit apply --restore-from time-machine|arq, and only while
it's absent or empty: one holding data counts as here, and is never
touched.` + declaredWhere,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, config.RestoreKind+" add", func(ctx context.Context, r *run) error {
				return a.addRestore(ctx, r, args[0], opts)
			})
		},
	}
	add.Flags().StringArrayVar(&opts.skips, "skip", nil, "what in the path to leave out, relative to it, as in logs or cache/*")
	add.Flags().BoolVar(&opts.shared, "shared", false, "declare for every Mac, not this one alone")
	add.Flags().StringVar(&opts.note, "note", "", "why it's declared, kept after it")
	var shared bool
	remove := &cobra.Command{
		Use:     "remove <path>...",
		Aliases: []string{"rm"},
		Short:   "Take paths off the restore list",
		Long: `Take paths off the restore list: a new Mac no longer carries them across.
Nothing on this Mac is copied or deleted.` + undeclaredWhere,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, config.RestoreKind+" remove", func(ctx context.Context, r *run) error {
				return a.removeRestore(ctx, r, args, shared)
			})
		},
	}
	remove.Flags().BoolVar(&shared, "shared", false, "take it out of the shared file, every Mac's list")
	cmd.AddCommand(add, remove)
	return cmd
}

// addRestore declares the path name, and what in it to leave out, in
// [restore], this Mac's or, with --shared, every Mac's, then commits and
// pushes. A line that wouldn't read, or that the restore would refuse on
// a Mac it's for, is refused before anything's written. It copies nothing.
func (a *app) addRestore(ctx context.Context, r *run, name string, opts restoreOptions) error {
	words := make([]string, 0, 2*len(opts.skips))
	for _, skip := range opts.skips {
		words = append(words, "--skip", config.Quote(skip))
	}
	value := strings.Join(words, " ")
	line, err := restore.ReadLine(r.tildePaths([]string{name})[0], value)
	if err != nil {
		return err
	}
	scope := r.scope(opts.shared)
	line.Pos = config.DeclFile(scope)
	c := startChanges(r, []string{line.Path})
	c.step(ctx, line.Path, func(ctx context.Context) check.Result {
		e := config.Entry{Name: line.Path, Value: value, Note: opts.note}
		if err := r.checkDeclare(config.RestoreKind, scope, e); err != nil {
			return failed(err)
		}
		if err := r.restoreFits(ctx, scope, line); err != nil {
			return failed(err)
		}
		said, err := c.declare(config.RestoreKind, scope, e)
		if err != nil {
			return failed(err)
		}
		return check.Result{State: check.OK, Summary: "declared in " + scope + said + "; nothing copied"}
	})
	c.sync(ctx, commitMessage("add", config.RestoreKind, []string{line.Path}, r.machine, opts.note))
	return c.finish()
}

// removeRestore takes each path out of [restore], this Mac's (and the
// shared one, with --shared), then commits and pushes.
func (a *app) removeRestore(ctx context.Context, r *run, names []string, shared bool) error {
	paths := r.tildePaths(names)
	for i, p := range paths {
		if line, err := restore.ReadLine(p, ""); err == nil {
			paths[i] = line.Path
		}
	}
	c := startChanges(r, paths)
	for _, p := range paths {
		c.step(ctx, p, func(context.Context) check.Result {
			written, err := r.restoreWritten(p)
			switch {
			case err != nil:
				return failed(err)
			case len(written) == 0:
				return check.Result{State: check.Failed, Reason: p + " isn't on this Mac's " + restoreList}
			}
			said := make([]string, 0, len(written))
			for _, name := range written {
				res := c.undeclare(config.RestoreKind, name, shared, restoreList)
				if res.State != check.OK {
					return res
				}
				said = append(said, res.Summary)
			}
			return check.Result{State: check.OK, Summary: strings.Join(said, "; ")}
		})
	}
	c.sync(ctx, commitMessage("remove", config.RestoreKind, paths, r.machine, ""))
	return c.finish()
}

// restoreWritten are the paths of this Mac's [restore] lines, the shared
// file's first, as written, that are p, a path from ~ and cleaned, however
// they're spelt, as in ~/Code/.
func (r *run) restoreWritten(p string) ([]string, error) {
	list, err := r.cfg.List(config.RestoreKind, r.machine)
	if err != nil {
		return nil, err
	}
	var written []string
	for _, e := range list.Entries {
		line, err := restore.ReadLine(e.Name, "")
		if (e.Name == p || err == nil && line.Path == p) && !slices.Contains(written, e.Name) {
			written = append(written, e.Name)
		}
	}
	return written, nil
}

// restoreFits says why line, declared in scope's file, would be refused by
// the restore of a Mac it's for, every Mac for the shared file: nil when it
// wouldn't. Each Mac's [restore] lines must read, none of them line's path,
// in it or holding it, but one written as line's path is, which declaring
// judges; and the path mustn't be kit's on the Mac.
func (r *run) restoreFits(ctx context.Context, scope string, line restore.Line) error {
	macs := []string{scope}
	if scope == config.Shared {
		macs = r.cfg.MacNames()
	}
	for _, mac := range macs {
		err := r.restoreFitsOn(ctx, mac, line)
		switch {
		case err != nil && scope == config.Shared:
			return fmt.Errorf("on %s, %w", mac, err)
		case err != nil:
			return err
		}
	}
	return nil
}

// restoreFitsOn says why mac's restore would refuse line: nil when it
// wouldn't.
func (r *run) restoreFitsOn(ctx context.Context, mac string, line restore.Line) error {
	list, err := r.cfg.List(config.RestoreKind, mac)
	if err != nil {
		return err
	}
	lines, err := restore.Lines(list)
	if err != nil {
		return err
	}
	// Lines reads a line of each entry, in turn.
	for i, other := range lines {
		written := list.Entries[i].Name
		switch {
		case written == line.Path:
			continue
		case other.Path == line.Path:
			return fmt.Errorf("%s: %s is listed already, as %s (%s)", line.Pos, line.Path, written, other.Pos)
		case overlaps(other.Path, line.Path):
			return fmt.Errorf("%s: %s and %s (%s) overlap: a path is restored by one line", line.Pos, line.Path, other.Path, other.Pos)
		}
	}
	features, err := r.featuresOf(mac)
	if err != nil {
		return err
	}
	owned, err := restore.ReadOwned(ctx, r.restoreMac(mac, features.on(steps.FeaturePrefs)))
	if err != nil {
		return err
	}
	_, err = restore.NewFilter(line, owned)
	return err
}

// restoreMac is mac, as the restore reads what's kit's on it, prefs on for
// it when prefs is.
func (r *run) restoreMac(mac string, prefs bool) restore.Mac {
	kinds := make([]kind.Kind, len(r.allKinds))
	for i, name := range r.allKinds {
		kinds[i] = r.kindsByName[name]
	}
	files := r.files
	if mac != r.machine {
		files = &linked.Files{Repo: r.repo, Home: r.homeDir, StateDir: r.stateDir, Scopes: []string{config.Shared, mac}}
	}
	dirs := config.Dirs{Config: r.repo.Dir, State: r.stateDir, Logs: r.logsDir, Data: r.dataDir}
	return restore.Mac{Config: r.cfg, Name: mac, Home: r.homeDir, Dirs: dirs, Linked: files, Prefs: prefs, Kinds: kinds}
}

// overlaps reports whether a and b, paths from ~, are one path, or one is
// in the other, case aside, as macOS's file system takes a path and the
// restore reads its lines.
func overlaps(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
