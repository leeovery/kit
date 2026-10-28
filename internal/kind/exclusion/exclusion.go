// Package exclusion is the backup exclusions as a kind: paths declared in a
// [backup exclusions] section (~ for the home folder, * globs), kept out of
// the backups of the tools the Mac uses, through each tool's driver: its
// own exclusions, or those of the tool it follows (backup.Excluders). A
// plain path is kept out fixed, which holds before its folder exists. The
// globs are expanded on every check, and a new folder matching one is kept
// out where it is, so with a tool doing that without a password, kit's
// scheduled run keeps it out within the hour.
package exclusion

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/backup"
	"github.com/leeovery/kit/internal/backup/tool"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// Exclusions is the backup exclusions, kept out by excluders.
type Exclusions struct {
	run       runner.Runner
	home      string
	excluders []tool.Excluder
	admin     func(ctx context.Context) bool
	// matched are the paths a glob matched, by name: kept out where they
	// are; declared every path declared, a glob by its matches.
	matched, declared map[string]bool
}

// kindName names the kind.
const kindName = "backup-exclusion"

// Descriptor is the backup exclusions' kind, as kit's shared code knows it.
var Descriptor = kind.Descriptor{
	Name:    kindName,
	Label:   "Backup exclusions",
	Group:   kind.Settings,
	Section: &config.Section{Kind: kindName, Header: "backup exclusions", Form: config.Paths, Sorted: true},
	Help: &kind.Help{
		What:   "Folders the backups leave out",
		Add:    "Leave folders out of backups, and declare them",
		Remove: "Put folders back in backups, and undeclare them",
	},
	Row: "Mac settings",
	New: func(d *kind.Deps) kind.Kind { return New(d.Run, d.Home, backup.Excluders(d.Backups)) },
}

// errNoTool is the kind's error on a Mac with no backup tool keeping
// exclusions.
var errNoTool = errors.New("no backup tool this Mac uses keeps them out: name the tools in [backups]")

// New returns the backup exclusions for the user whose home is home, kept
// out by excluders.
func New(run runner.Runner, home string, excluders []tool.Excluder) *Exclusions {
	return &Exclusions{run: run, home: home, excluders: excluders}
}

func (x *Exclusions) Name() string { return kindName }

// Program is the excluders' programs.
func (x *Exclusions) Program() string { return strings.Join(x.Commands(), " and ") }

// Commands are the excluders' programs, each once.
func (x *Exclusions) Commands() []string {
	var out []string
	for _, ex := range x.excluders {
		if p := ex.Program(); !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// Verb says a path as declared is excluded.
func (x *Exclusions) Verb() string { return "excluded" }

// full is a path as declared, ~ expanded.
func (x *Exclusions) full(name string) string {
	if rest, ok := strings.CutPrefix(name, "~/"); ok {
		return filepath.Join(x.home, rest)
	}
	return name
}

// shown is a full path as kit shows it, ~ for the home folder.
func (x *Exclusions) shown(path string) string {
	if rest, ok := strings.CutPrefix(path, x.home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// Expand replaces each glob with what matches it now; a plain path stands
// for itself, there or not, as a fixed exclusion holds before its folder
// exists.
func (x *Exclusions) Expand(list config.List) (config.List, error) {
	out := list
	out.Entries = nil
	x.matched, x.declared = map[string]bool{}, map[string]bool{}
	for _, e := range list.Entries {
		if !strings.ContainsAny(e.Name, "*?[") {
			out.Entries = append(out.Entries, e)
			x.declared[e.Name] = true
			continue
		}
		matches, err := filepath.Glob(x.full(e.Name))
		if err != nil {
			return list, fmt.Errorf("%s: %q isn't a pattern kit reads: %w", e.Pos(), e.Name, err)
		}
		for _, m := range matches {
			match := e
			match.Name = x.shown(m)
			out.Entries = append(out.Entries, match)
			x.matched[match.Name] = true
			x.declared[match.Name] = true
		}
	}
	return out, nil
}

// Installed lists the paths kept out of the backups: one declared while
// every excluder keeps it out, and one declared nowhere while any does, so
// it's seen.
func (x *Exclusions) Installed(ctx context.Context) ([]kind.Installed, error) {
	if len(x.excluders) == 0 {
		return nil, errNoTool
	}
	var here []string
	for name := range x.matched {
		here = append(here, x.full(name))
	}
	slices.Sort(here)
	var order []string
	keepers := map[string]int{}
	for _, ex := range x.excluders {
		paths, err := ex.Excluded(ctx, x.run, here)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, p := range paths {
			name := x.shown(p)
			if seen[name] {
				continue
			}
			seen[name] = true
			if keepers[name] == 0 {
				order = append(order, name)
			}
			keepers[name]++
		}
	}
	var out []kind.Installed
	for _, name := range order {
		if keepers[name] == len(x.excluders) || !x.declared[name] {
			out = append(out, kind.Installed{Name: name, Explicit: true})
		}
	}
	return out, nil
}

// Resolve knows every path.
func (x *Exclusions) Resolve(_ context.Context, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = name
	}
	return out, nil
}

// Install keeps each path out of every excluder's backups, one at a time so
// one that fails leaves the rest: a glob's match where it is, first; a
// plain path fixed.
func (x *Exclusions) Install(ctx context.Context, names []string) error {
	var here, fixed []string
	for _, name := range names {
		if x.matched[name] {
			here = append(here, name)
		} else {
			fixed = append(fixed, name)
		}
	}
	var errs []error
	for _, ex := range x.excluders {
		for _, name := range here {
			errs = append(errs, named(name, kind.AdminAgain(ctx, func() error { return ex.Exclude(ctx, x.run, x.full(name), false) })))
		}
		for _, name := range fixed {
			errs = append(errs, named(name, kind.AdminAgain(ctx, func() error { return ex.Exclude(ctx, x.run, x.full(name), true) })))
		}
	}
	return errors.Join(errs...)
}

// Remove stops keeping each path out, through the excluders keeping it
// out.
func (x *Exclusions) Remove(ctx context.Context, names []string) error {
	var errs []error
	for _, ex := range x.excluders {
		kept, err := ex.Excluded(ctx, x.run, nil)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, name := range names {
			if path := x.full(name); slices.Contains(kept, path) {
				errs = append(errs, named(name, kind.AdminAgain(ctx, func() error { return ex.Include(ctx, x.run, path) })))
			}
		}
	}
	return errors.Join(errs...)
}

// named is err, keeping the path called name out or no longer, saying
// which.
func named(name string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// asAdmin reports whether keeping the path called name out needs an
// administrator's password, for any excluder.
func (x *Exclusions) asAdmin(name string) bool {
	fixed := !x.matched[name]
	return slices.ContainsFunc(x.excluders, func(ex tool.Excluder) bool { return ex.NeedsAdmin(fixed) })
}

// Unattended is those of names kept out with no password, so the scheduled
// run keeps them out.
func (x *Exclusions) Unattended(names []string) []string {
	var out []string
	for _, name := range names {
		if !x.asAdmin(name) {
			out = append(out, name)
		}
	}
	return out
}

// NeedsAdmin is those of names that need an administrator's password to
// keep out.
func (x *Exclusions) NeedsAdmin(_ context.Context, names []string) ([]string, error) {
	var out []string
	for _, name := range names {
		if x.asAdmin(name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// SetAdmin is how the kind finds out whether an administrator's password is
// at hand.
func (x *Exclusions) SetAdmin(held func(ctx context.Context) bool) { x.admin = held }

// Blocked holds up every path missing that needs an administrator's
// password while kit has none.
func (x *Exclusions) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]kind.Block, error) {
	blocked := map[string]kind.Block{}
	if x.admin == nil || x.admin(ctx) {
		return blocked, nil
	}
	for name := range missing {
		if x.asAdmin(name) {
			blocked[name] = kind.Block{Why: "waiting for an administrator's password: kit apply at a terminal asks for it"}
		}
	}
	return blocked, nil
}

var _ interface {
	kind.Kind
	kind.Commanded
	kind.Expander
	kind.Unattended
	kind.Admin
	kind.Blocker
} = (*Exclusions)(nil)
