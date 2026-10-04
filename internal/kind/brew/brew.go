// Package brew is Homebrew's kinds, formulae and casks, and the step that
// checks Homebrew is there.
package brew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// StepName is the name of the step that checks Homebrew is there, which
// the formulae and casks need.
const StepName = "homebrew"

// Homebrew is Homebrew, driven through run.
type Homebrew struct {
	Run runner.Runner
}

// Step checks brew is on kit's PATH, and where Homebrew is.
func (h Homebrew) Step() engine.Step {
	return engine.Step{
		Name:  StepName,
		Title: "Homebrew",
		Check: func(ctx context.Context) check.Result {
			res, err := h.brew(ctx, "--prefix")
			if errors.Is(err, runner.ErrNotFound) {
				return check.Result{State: check.Attention, Summary: "not installed: brew isn't on kit's PATH"}
			}
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			return check.Result{State: check.OK, Summary: strings.TrimSpace(string(res.Stdout))}
		},
	}
}

// Formulae is the brew kind.
func (h Homebrew) Formulae() kind.Kind {
	return formulae{h}
}

// Casks is the cask kind.
func (h Homebrew) Casks() kind.Kind {
	return casks{h}
}

func (h Homebrew) brew(ctx context.Context, args ...string) (runner.Result, error) {
	return h.Run.Run(ctx, runner.Command{Name: "brew", Args: args})
}

// lines runs brew with args, and returns what it printed, a line each.
func (h Homebrew) lines(ctx context.Context, args ...string) ([]string, error) {
	res, err := h.brew(ctx, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for line := range strings.Lines(string(res.Stdout)) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

type formulae struct{ Homebrew }

func (formulae) Name() string  { return "brew" }
func (formulae) Title() string { return "Formulae" }

// Installed lists the formulae installed, by full name, from three listings
// run side by side: every formula, the leaves (needed by no other), and the
// leaves installed for themselves.
func (f formulae) Installed(ctx context.Context) ([]kind.Installed, error) {
	listings := [][]string{
		{"list", "--formula", "--full-name", "-1"},
		{"leaves"},
		{"leaves", "--installed-on-request"},
	}
	found := make([][]string, len(listings))
	errs := make([]error, len(listings))
	var wg sync.WaitGroup
	for i, args := range listings {
		wg.Go(func() { found[i], errs[i] = f.lines(ctx, args...) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	leaves, onRequest := set(found[1]), set(found[2])
	installed := make([]kind.Installed, 0, len(found[0]))
	for _, name := range found[0] {
		installed = append(installed, kind.Installed{Name: name, Explicit: onRequest[name], Needed: !leaves[name]})
	}
	return installed, nil
}

func (f formulae) Resolve(ctx context.Context, names []string) (map[string]string, error) {
	return resolve(ctx, f.Homebrew, "--formula", names)
}

type casks struct{ Homebrew }

func (casks) Name() string  { return "cask" }
func (casks) Title() string { return "Casks" }

// Installed lists the casks installed, by full name, each installed for
// itself.
func (c casks) Installed(ctx context.Context) ([]kind.Installed, error) {
	names, err := c.lines(ctx, "list", "--cask", "--full-name", "-1")
	if err != nil {
		return nil, err
	}
	installed := make([]kind.Installed, len(names))
	for i, name := range names {
		installed[i] = kind.Installed{Name: name, Explicit: true}
	}
	return installed, nil
}

func (c casks) Resolve(ctx context.Context, names []string) (map[string]string, error) {
	return resolve(ctx, c.Homebrew, "--cask", names)
}

// info is what brew info --json=v2 says of formulae and casks, as far as
// the names each goes by.
type info struct {
	Formulae []struct {
		Name     string   `json:"name"`
		FullName string   `json:"full_name"`
		Aliases  []string `json:"aliases"`
		Oldnames []string `json:"oldnames"`
	} `json:"formulae"`
	Casks []struct {
		Token     string   `json:"token"`
		FullToken string   `json:"full_token"`
		OldTokens []string `json:"old_tokens"`
	} `json:"casks"`
}

// resolve asks brew info for names' full names, all at once. brew info
// says nothing at all when any name is unknown, so then it asks of each
// alone, leaving out those it doesn't know.
func resolve(ctx context.Context, h Homebrew, which string, names []string) (map[string]string, error) {
	resolved, err := lookUp(ctx, h, which, names)
	if err == nil {
		return resolved, nil
	}
	if _, exited := errors.AsType[*runner.ExitError](err); !exited {
		return nil, err
	}
	resolved = make(map[string]string, len(names))
	for _, name := range names {
		one, err := lookUp(ctx, h, which, []string{name})
		if _, exited := errors.AsType[*runner.ExitError](err); exited {
			continue
		}
		if err != nil {
			return nil, err
		}
		maps.Copy(resolved, one)
	}
	return resolved, nil
}

// lookUp asks brew info of names in one go, and maps each to its full name
// by every name brew says it goes by.
func lookUp(ctx context.Context, h Homebrew, which string, names []string) (map[string]string, error) {
	res, err := h.brew(ctx, slices.Concat([]string{"info", "--json=v2", which}, names)...)
	if err != nil {
		return nil, err
	}
	var in info
	if err := json.Unmarshal(res.Stdout, &in); err != nil {
		return nil, fmt.Errorf("read brew info's answer: %w", err)
	}
	goesBy := make(map[string]string)
	for _, f := range in.Formulae {
		for _, n := range slices.Concat([]string{f.Name, f.FullName, "homebrew/core/" + f.Name}, f.Aliases, f.Oldnames) {
			goesBy[n] = f.FullName
		}
	}
	for _, c := range in.Casks {
		for _, n := range slices.Concat([]string{c.Token, c.FullToken, "homebrew/cask/" + c.Token}, c.OldTokens) {
			goesBy[n] = c.FullToken
		}
	}
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		if full, ok := goesBy[name]; ok {
			resolved[name] = full
		}
	}
	return resolved, nil
}

func set(names []string) map[string]bool {
	s := make(map[string]bool, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}
