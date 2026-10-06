// Package exclusion is the backup exclusions as a kind: paths declared in a
// [backup exclusions] section (~ for the home folder, * globs), kept out of
// Time Machine, which Arq inherits through its "Skip items excluded by Time
// Machine rules". A plain path is a fixed-path exclusion, which holds before
// its folder exists and needs an administrator's password. The globs are
// expanded on every check, and a new folder matching one is excluded where
// it is, a sticky exclusion, which needs no password, so kit's scheduled run
// excludes it within the hour. Arq honours sticky exclusions as it does
// fixed-path ones (tested on 6 Oct 2026).
package exclusion

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// TimeMachinePrefs is Time Machine's settings, whose SkipPaths are its
// fixed-path exclusions, as defaults names the domain: read through macOS's
// settings service, which has a change the moment it's made, where the file
// on disk lags behind.
const TimeMachinePrefs = "/Library/Preferences/com.apple.TimeMachine"

// Exclusions is the backup exclusions, driven through tmutil.
type Exclusions struct {
	run   runner.Runner
	home  string
	prefs string
	admin func(ctx context.Context) bool
	// matched are the paths a glob matched, by name: excluded sticky.
	matched map[string]bool
}

// New returns the backup exclusions for the user whose home is home, with
// Time Machine's settings at prefs.
func New(run runner.Runner, home, prefs string) *Exclusions {
	return &Exclusions{run: run, home: home, prefs: prefs}
}

func (x *Exclusions) Name() string    { return "backup-exclusion" }
func (x *Exclusions) Title() string   { return "Backup exclusions" }
func (x *Exclusions) Program() string { return "tmutil" }

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
// for itself, there or not, as a fixed-path exclusion holds before its
// folder exists.
func (x *Exclusions) Expand(list config.List) (config.List, error) {
	out := list
	out.Entries = nil
	x.matched = map[string]bool{}
	for _, e := range list.Entries {
		if !strings.ContainsAny(e.Name, "*?[") {
			out.Entries = append(out.Entries, e)
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
		}
	}
	return out, nil
}

// Installed lists Time Machine's fixed-path exclusions, and the glob matches
// excluded where they are.
func (x *Exclusions) Installed(ctx context.Context) ([]kind.Installed, error) {
	res, err := x.run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"export", x.prefs, "-"}})
	if err != nil {
		return nil, err
	}
	v, err := plist.Read(string(res.Stdout))
	if err != nil {
		return nil, err
	}
	prefs, _ := v.(map[string]any)
	skip, _ := prefs["SkipPaths"].([]any)
	var out []kind.Installed
	fixed := map[string]bool{}
	for _, p := range skip {
		if path, ok := p.(string); ok {
			out = append(out, kind.Installed{Name: x.shown(path), Explicit: true})
			fixed[x.shown(path)] = true
		}
	}
	var sticky []string
	for name := range x.matched {
		if !fixed[name] {
			sticky = append(sticky, x.full(name))
		}
	}
	if len(sticky) == 0 {
		return out, nil
	}
	slices.Sort(sticky)
	res, err = x.run.Run(ctx, runner.Command{Name: "tmutil", Args: append([]string{"isexcluded"}, sticky...)})
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(string(res.Stdout)) {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "[Excluded]"); ok {
			out = append(out, kind.Installed{Name: x.shown(strings.TrimSpace(path)), Explicit: true})
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

// Install excludes each path from Time Machine, one at a time so one that
// fails leaves the rest: a glob's match where it is, sticky; a plain path
// as a fixed-path exclusion, through sudo, which needs Full Disk Access for
// the terminal kit runs in.
func (x *Exclusions) Install(ctx context.Context, names []string) error {
	var fixed, sticky []string
	for _, name := range names {
		if x.matched[name] {
			sticky = append(sticky, name)
		} else {
			fixed = append(fixed, name)
		}
	}
	var errs []error
	for _, name := range sticky {
		if _, err := x.run.Run(ctx, runner.Command{Name: "tmutil", Args: []string{"addexclusion", x.full(name)}}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(append(errs, x.each(ctx, "addexclusion", fixed))...)
}

// Unattended is the glob matches among names: excluding one where it is
// needs no password, so the scheduled run does it.
func (x *Exclusions) Unattended(names []string) []string {
	var out []string
	for _, name := range names {
		if x.matched[name] {
			out = append(out, name)
		}
	}
	return out
}

// Remove stops excluding each path.
func (x *Exclusions) Remove(ctx context.Context, names []string) error {
	return x.each(ctx, "removeexclusion", names)
}

func (x *Exclusions) each(ctx context.Context, verb string, names []string) error {
	var errs []error
	for _, name := range names {
		if _, err := x.run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "tmutil", verb, "-p", x.full(name)}}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// NeedsAdmin is the plain paths among names: tmutil needs root for
// fixed-path exclusions.
func (x *Exclusions) NeedsAdmin(_ context.Context, names []string) ([]string, error) {
	var out []string
	for _, name := range names {
		if !x.matched[name] {
			out = append(out, name)
		}
	}
	return out, nil
}

// SetAdmin is how the kind finds out whether an administrator's password is
// at hand.
func (x *Exclusions) SetAdmin(held func(ctx context.Context) bool) { x.admin = held }

// Blocked holds up every path missing while kit has no administrator's
// password.
func (x *Exclusions) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	blocked := map[string]string{}
	if x.admin == nil || x.admin(ctx) {
		return blocked, nil
	}
	for name := range missing {
		if !x.matched[name] {
			blocked[name] = "waiting for an administrator's password: kit apply at a terminal asks for it"
		}
	}
	return blocked, nil
}

var _ interface {
	kind.Kind
	kind.Expander
	kind.Unattended
	kind.Admin
	kind.Blocker
} = (*Exclusions)(nil)
