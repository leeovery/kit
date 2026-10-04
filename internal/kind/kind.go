// Package kind is kinds: lists of things of one sort, such as Homebrew's
// formulae, compared with what's on the Mac. A kind says what's installed,
// and the full name of a thing known by another; the comparison, and the
// step it makes, are the same for every kind.
package kind

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// Installed is a thing a kind finds on the Mac.
type Installed struct {
	// Name is its full name, as in owner/tap/tool.
	Name string
	// Explicit is whether it was installed for itself, rather than only as
	// another's dependency.
	Explicit bool
	// Needed is whether something else installed needs it.
	Needed bool
}

// Kind is a list of things of one sort.
type Kind interface {
	// Name names the kind, its items and its step, as in brew; the config
	// declares it in the section that names it, as in [homebrew formulae].
	Name() string
	// Title is the step's title, as in Formulae.
	Title() string
	// Program is the program the kind runs to find what's installed, as in
	// brew: while it isn't installed, there's nothing of the kind to find.
	Program() string
	// Installed lists what's on the Mac.
	Installed(ctx context.Context) ([]Installed, error)
	// Resolve finds the full names of things known by other names, such as
	// aliases and old names. A name the kind doesn't know at all is left
	// out.
	Resolve(ctx context.Context, names []string) (map[string]string, error)
	// Install installs names.
	Install(ctx context.Context, names []string) error
	// Remove uninstalls names.
	Remove(ctx context.Context, names []string) error
}

// Blocker is a kind that can say why some things can't be installed now.
type Blocker interface {
	// Blocked says, of the things missing (declared names, by their full
	// names), why each that can't be installed now can't, by declared name.
	Blocked(ctx context.Context, missing map[string]string, installed []Installed) (map[string]string, error)
}

// Admin is a kind some of whose installs need an administrator's password,
// which the command settles before anything is installed, so none asks
// mid-run.
type Admin interface {
	// NeedsAdmin finds which of names, to install, need the password.
	NeedsAdmin(ctx context.Context, names []string) ([]string, error)
	// SetAdmin is how the kind finds out whether the password is at hand:
	// while it's unset, kit isn't installing, and nothing's held up for it.
	SetAdmin(held func(ctx context.Context) bool)
}

// Dependents is a kind that knows what installed needs a thing.
type Dependents interface {
	// NeededBy lists what's installed that needs name.
	NeededBy(ctx context.Context, name string) ([]string, error)
}

// Declarer is a kind declared outside the config repository, in a file of
// its own that kit reads but doesn't write: tmux's plugins, in tmux's
// config, where the plugin manager reads them.
type Declarer interface {
	// Declared is what the kind's file declares.
	Declared() (config.List, error)
	// HowToDeclare says how to declare, or undeclare, name by hand.
	HowToDeclare(name string) string
}

// Valued is a kind whose declarations carry a value after each name: an MCP
// server's definition, as claude mcp add's options.
type Valued interface {
	// Values reads what list declares, values and all: the list as it
	// compares, the entries declared off marked so. A value that doesn't
	// read is refused.
	Values(list config.List) (config.List, error)
	// Value is the value to declare name with, as it's installed.
	Value(ctx context.Context, name string) (string, error)
}

// Describer is a kind that says what a thing is, for the note kit writes
// beside it when it declares it: a login item's app's name.
type Describer interface {
	// Describe says what name is: "" when it can't.
	Describe(ctx context.Context, name string) string
}

// Differ is a kind whose things can be installed otherwise than they're
// declared: an MCP server whose definition has changed.
type Differ interface {
	// Differs says, of the declared names installed, how each installed
	// otherwise than declared differs, by declared name.
	Differs(ctx context.Context, names []string) (map[string]string, error)
}

// Keyed is a kind whose things match by part of their names: an App Store
// app by its id, an npm package without its version.
type Keyed interface {
	// Key is the part of name things match by.
	Key(name string) string
}

// Finder is a kind that finds what a person means by what they type, as an
// App Store app from its name, before it's installed and declared.
type Finder interface {
	// Find finds what typed means: one thing, or several to choose from.
	Find(ctx context.Context, typed string) ([]Found, error)
}

// Found is a thing a Finder found: the name to declare it by, and a label
// saying what it is, to choose by.
type Found struct {
	Name  string
	Label string
}

// Install is the action that installs an item.
const Install = "install"

// The states of a kind's items.
const (
	Missing          = "missing"
	Extra            = "extra"
	UnusedDependency = "unused-dependency"
	// Changed is a thing installed otherwise than declared: applying
	// installs it again, as declared.
	Changed = "changed"
)

// Step is the step that checks k, what declared lists against what's on
// the Mac, and applies it: installing what's missing, as it can. It needs
// the steps named in needs.
func Step(k Kind, declared config.List, needs ...string) engine.Step {
	return engine.Step{
		Name:  k.Name(),
		Title: k.Title(),
		Needs: needs,
		Check: func(ctx context.Context) check.Result { return Compare(ctx, k, declared) },
		Apply: func(ctx context.Context, found check.Result) error {
			var names []string
			for _, it := range found.Items {
				if it.Action == Install {
					names = append(names, it.Name)
				}
			}
			if len(names) == 0 {
				return nil
			}
			return k.Install(ctx, names)
		},
	}
}

// Compare checks what declared lists against what k finds installed: a
// declared thing is ok installed for any reason, or missing; an undeclared
// one installed for itself, and needed by nothing, is extra; one installed
// only as a dependency, and needed by nothing now, is an unused dependency.
// A thing declared off is neither missing nor extra. A kind that can tell
// finds things installed otherwise than declared. While k's program isn't
// installed, nothing declared is fine, and anything declared defers the
// step.
func Compare(ctx context.Context, k Kind, declared config.List) check.Result {
	installed, err := k.Installed(ctx)
	switch {
	case errors.Is(err, runner.ErrNotFound) && len(declared.Entries) == 0:
		return check.Result{State: check.OK, Summary: "none declared; " + k.Program() + " isn't installed"}
	case errors.Is(err, runner.ErrNotFound):
		return check.Result{State: check.Deferred, Reason: "needs " + k.Program() + ", which isn't installed"}
	case err != nil:
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	key := func(name string) string { return name }
	if kd, ok := k.(Keyed); ok {
		key = kd.Key
	}
	byKey := make(map[string]Installed, len(installed))
	for _, it := range installed {
		byKey[key(it.Name)] = it
	}
	// matched are the installed things declared, by name, and installedDeclared
	// the declared names of those declared on.
	matched := make(map[string]bool)
	var unmatched, installedDeclared []string
	off := 0
	for _, e := range declared.Entries {
		it, ok := byKey[key(e.Name)]
		switch {
		case e.Off:
			off++
			if ok {
				matched[it.Name] = true
			}
		case ok:
			matched[it.Name] = true
			installedDeclared = append(installedDeclared, e.Name)
		default:
			unmatched = append(unmatched, e.Name)
		}
	}
	var missing, unknown []string
	fullNames := make(map[string]string)
	if len(unmatched) > 0 {
		resolved, err := k.Resolve(ctx, unmatched)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		for _, name := range unmatched {
			full, known := resolved[name]
			switch {
			case !known:
				unknown = append(unknown, name)
			case byKey[key(full)].Name != "":
				matched[byKey[key(full)].Name] = true
			default:
				missing = append(missing, name)
				fullNames[name] = full
			}
		}
	}
	blocked := map[string]string{}
	if b, ok := k.(Blocker); ok && len(missing) > 0 {
		var err error
		if blocked, err = b.Blocked(ctx, fullNames, installed); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
	}
	var extra, unused []string
	for _, it := range installed {
		if matched[it.Name] || it.Needed {
			continue
		}
		if it.Explicit {
			extra = append(extra, it.Name)
		} else {
			unused = append(unused, it.Name)
		}
	}

	changed := map[string]string{}
	if d, ok := k.(Differ); ok && len(installedDeclared) > 0 {
		var err error
		if changed, err = d.Differs(ctx, installedDeclared); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
	}

	declaredCount := len(declared.Entries) - off
	missingCount := len(missing) + len(unknown)
	res := check.Result{
		State:   check.OK,
		Summary: fmt.Sprintf("%d declared, all installed", declaredCount),
		Counts: map[string]int{
			"declared": declaredCount, "installed": declaredCount - missingCount,
			"missing": missingCount, "extra": len(extra), "unused_dependencies": len(unused),
		},
	}
	if len(changed) > 0 {
		res.Counts["changed"] = len(changed)
	}
	if off > 0 {
		res.Counts["off"] = off
	}
	if missingCount > 0 {
		res.Summary = fmt.Sprintf("%d declared, %d installed", declaredCount, declaredCount-missingCount)
	}
	if declaredCount == 0 {
		res.Summary = "none declared"
	}
	if len(changed) > 0 {
		res.Summary += fmt.Sprintf("; %d changed", len(changed))
	}
	if off > 0 {
		res.Summary += fmt.Sprintf("; %d off", off)
	}
	missingItems := items(k, missing, Missing, "")
	for i, it := range missingItems {
		if reason, ok := blocked[it.Name]; ok {
			missingItems[i].Detail = reason
		} else {
			missingItems[i].Action = Install
		}
	}
	var changedNames []string
	for name := range changed {
		changedNames = append(changedNames, name)
	}
	changedItems := items(k, changedNames, Changed, "")
	for i, it := range changedItems {
		changedItems[i].Detail, changedItems[i].Action = changed[it.Name], Install
	}
	res.Items = slices.Concat(
		missingItems,
		items(k, unknown, Missing, "unknown"),
		changedItems,
		items(k, extra, Extra, ""),
		items(k, unused, UnusedDependency, ""),
	)
	if len(res.Items) > 0 {
		res.State = check.Attention
	}
	return res
}

// items are names as k's items in state, sorted, each with detail.
func items(k Kind, names []string, state, detail string) []check.Item {
	slices.Sort(names)
	out := make([]check.Item, len(names))
	for i, name := range names {
		out[i] = check.Item{ID: k.Name() + ":" + name, Name: name, State: state, Detail: detail}
	}
	return out
}
