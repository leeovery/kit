// Package gitconfig is git's global settings as a kind: each declared in a
// [git config] section, a line a setting in the form of the command that
// sets it (git config --global <key> <value>), compared with what git
// config --global lists. A setting set otherwise was changed with git
// config on the Mac, so applying leaves it: kit reconcile adopts the Mac's
// value or puts the declared one back.
package gitconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// GitConfig is git's global settings, driven through git.
type GitConfig struct {
	run runner.Runner
	// declared are the declared values, by key.
	declared map[string]string
}

// New returns git's global settings, run through run.
func New(run runner.Runner) *GitConfig {
	return &GitConfig{run: run, declared: map[string]string{}}
}

func (g *GitConfig) Name() string    { return "git-config" }
func (g *GitConfig) Title() string   { return "git's settings" }
func (g *GitConfig) Program() string { return "git" }

// Verb says a setting as declared is set.
func (g *GitConfig) Verb() string { return "set" }

// Diverges reports a setting set otherwise as changed on purpose: with git
// config, on the Mac.
func (g *GitConfig) Diverges(string) bool { return true }

// Key is a key as git matches it: its section and last part in any case,
// a subsection between them as written.
func (g *GitConfig) Key(name string) string {
	first, rest, ok := strings.Cut(name, ".")
	if !ok {
		return strings.ToLower(name)
	}
	sub, last, ok := strings.CutLast(rest, ".")
	if !ok {
		return strings.ToLower(first) + "." + strings.ToLower(rest)
	}
	return strings.ToLower(first) + "." + sub + "." + strings.ToLower(last)
}

// Values reads each setting's value, one word after its key: quoted when it
// holds spaces.
func (g *GitConfig) Values(list config.List) (config.List, error) {
	g.declared = make(map[string]string, len(list.Entries))
	for _, e := range list.Entries {
		words, err := config.Words(e.Value)
		if err != nil {
			return list, fmt.Errorf("%s: %s: %w", e.Pos(), e.Name, err)
		}
		if len(words) != 1 {
			return list, fmt.Errorf("%s: %s: one value after the key, in quotes when it holds spaces", e.Pos(), e.Name)
		}
		g.declared[g.Key(e.Name)] = words[0]
	}
	return list, nil
}

// settings are git's global settings: each key, as git lists it, with its
// values, in order.
func (g *GitConfig) settings(ctx context.Context) (map[string][]string, []string, error) {
	res, err := g.run.Run(ctx, runner.Command{Name: "git", Args: []string{"config", "--global", "--list", "-z"}})
	switch {
	case err != nil && res.ExitCode == 1 && len(res.Stdout) == 0:
		// No global config file yet: nothing set.
		return map[string][]string{}, nil, nil
	case err != nil:
		return nil, nil, err
	}
	values := make(map[string][]string)
	var keys []string
	for entry := range strings.SplitSeq(string(res.Stdout), "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		if _, ok := values[key]; !ok {
			keys = append(keys, key)
		}
		values[key] = append(values[key], value)
	}
	return values, keys, nil
}

// Installed lists the settings set.
func (g *GitConfig) Installed(ctx context.Context) ([]kind.Installed, error) {
	_, keys, err := g.settings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]kind.Installed, len(keys))
	for i, key := range keys {
		out[i] = kind.Installed{Name: key, Explicit: true}
	}
	return out, nil
}

// Resolve knows every key: one not set is missing.
func (g *GitConfig) Resolve(_ context.Context, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = name
	}
	return out, nil
}

// Differs says, of the declared settings set, how each set otherwise is.
func (g *GitConfig) Differs(ctx context.Context, names []string) (map[string]string, error) {
	values, _, err := g.settings(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string][]string, len(values))
	for key, v := range values {
		byKey[g.Key(key)] = v
	}
	out := make(map[string]string)
	for _, name := range names {
		want, have := g.declared[g.Key(name)], byKey[g.Key(name)]
		switch {
		case len(have) > 1:
			out[name] = fmt.Sprintf("set %d times", len(have))
		case len(have) == 1 && have[0] != want:
			out[name] = "set to " + config.Quote(have[0])
		}
	}
	return out, nil
}

// Value is a setting's value as it's set, to declare it by.
func (g *GitConfig) Value(ctx context.Context, name string) (string, error) {
	values, _, err := g.settings(ctx)
	if err != nil {
		return "", err
	}
	for key, v := range values {
		if g.Key(key) == g.Key(name) {
			if len(v) > 1 {
				return "", fmt.Errorf("%s is set %d times: settle it with git config --global", name, len(v))
			}
			return config.Quote(v[0]), nil
		}
	}
	return "", fmt.Errorf("%s isn't set", name)
}

// Install sets each declared setting, in place of any value it has.
func (g *GitConfig) Install(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		value, ok := g.declared[g.Key(name)]
		if !ok {
			errs = append(errs, fmt.Errorf("%s isn't declared", name))
			continue
		}
		if _, err := g.run.Run(ctx, runner.Command{Name: "git", Args: []string{"config", "--global", "--replace-all", name, value}}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// Remove unsets each setting, every value it has.
func (g *GitConfig) Remove(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		res, err := g.run.Run(ctx, runner.Command{Name: "git", Args: []string{"config", "--global", "--unset-all", name}})
		// 5: it wasn't set.
		if err != nil && res.ExitCode != 5 {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

var _ interface {
	kind.Kind
	kind.Valued
	kind.Diverger
	kind.Keyed
} = (*GitConfig)(nil)
