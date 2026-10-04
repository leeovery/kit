// Package kind is kinds: lists of things of one sort, such as Homebrew's
// formulae, compared with what's on the Mac. A kind says what's installed,
// and the full name of a thing known by another; the comparison, and the
// step it makes, are the same for every kind.
package kind

import (
	"context"
	"fmt"
	"slices"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
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
	// Name names the kind, its list files and its step, as in brew.
	Name() string
	// Title is the step's title, as in Formulae.
	Title() string
	// Installed lists what's on the Mac.
	Installed(ctx context.Context) ([]Installed, error)
	// Resolve finds the full names of things known by other names, such as
	// aliases and old names. A name the kind doesn't know at all is left
	// out.
	Resolve(ctx context.Context, names []string) (map[string]string, error)
}

// The states of a kind's items.
const (
	Missing          = "missing"
	Extra            = "extra"
	UnusedDependency = "unused-dependency"
)

// Step is the step that checks k: what declared lists, against what's on the
// Mac. It needs the steps named in needs.
func Step(k Kind, declared config.List, needs ...string) engine.Step {
	return engine.Step{
		Name:  k.Name(),
		Title: k.Title(),
		Needs: needs,
		Check: func(ctx context.Context) check.Result { return Compare(ctx, k, declared) },
	}
}

// Compare checks what declared lists against what k finds installed: a
// declared thing is ok installed for any reason, or missing; an undeclared
// one installed for itself, and needed by nothing, is extra; one installed
// only as a dependency, and needed by nothing now, is an unused dependency.
func Compare(ctx context.Context, k Kind, declared config.List) check.Result {
	installed, err := k.Installed(ctx)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	byName := make(map[string]Installed, len(installed))
	for _, it := range installed {
		byName[it.Name] = it
	}
	matched := make(map[string]bool)
	var unmatched []string
	for _, e := range declared.Entries {
		if _, ok := byName[e.Name]; ok {
			matched[e.Name] = true
		} else {
			unmatched = append(unmatched, e.Name)
		}
	}
	var missing, unknown []string
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
			case byName[full].Name != "":
				matched[full] = true
			default:
				missing = append(missing, name)
			}
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

	declaredCount := len(declared.Entries)
	missingCount := len(missing) + len(unknown)
	res := check.Result{
		State:   check.OK,
		Summary: fmt.Sprintf("%d declared, all installed", declaredCount),
		Counts: map[string]int{
			"declared": declaredCount, "installed": declaredCount - missingCount,
			"missing": missingCount, "extra": len(extra), "unused_dependencies": len(unused),
		},
	}
	if missingCount > 0 {
		res.Summary = fmt.Sprintf("%d declared, %d installed", declaredCount, declaredCount-missingCount)
	}
	if declaredCount == 0 {
		res.Summary = "none declared"
	}
	res.Items = slices.Concat(
		items(k, missing, Missing, ""),
		items(k, unknown, Missing, "unknown"),
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
