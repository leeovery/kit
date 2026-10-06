// Package exclusion is the backup exclusions as a kind: paths declared in a
// [backup exclusions] section (~ for the home folder, * globs), kept out of
// Time Machine as fixed-path exclusions, which Arq inherits through its
// "Skip items excluded by Time Machine rules". The globs are expanded on
// every check, so a new folder that matches one is noticed within the hour.
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
}

// New returns the backup exclusions for the user whose home is home, with
// Time Machine's settings at prefs.
func New(run runner.Runner, home, prefs string) *Exclusions {
	return &Exclusions{run: run, home: home, prefs: prefs}
}

func (x *Exclusions) Name() string    { return "exclusion" }
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
		}
	}
	return out, nil
}

// Installed lists Time Machine's fixed-path exclusions.
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
	for _, p := range skip {
		if path, ok := p.(string); ok {
			out = append(out, kind.Installed{Name: x.shown(path), Explicit: true})
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

// Install excludes each path from Time Machine, through sudo, one at a
// time so one that fails leaves the rest. Time Machine needs Full Disk
// Access for the terminal kit runs in.
func (x *Exclusions) Install(ctx context.Context, names []string) error {
	return x.each(ctx, "addexclusion", names)
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

// NeedsAdmin is names: tmutil needs root for fixed-path exclusions.
func (x *Exclusions) NeedsAdmin(_ context.Context, names []string) ([]string, error) {
	return slices.Clone(names), nil
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
		blocked[name] = "waiting for an administrator's password: kit apply at a terminal asks for it"
	}
	return blocked, nil
}

var _ interface {
	kind.Kind
	kind.Expander
	kind.Admin
	kind.Blocker
} = (*Exclusions)(nil)
