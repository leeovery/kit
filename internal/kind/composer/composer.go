// Package composer is Composer's kind: the packages required globally. A
// package may be declared with a constraint, as in laravel/valet:^4.0, kept
// for installing; it matches on its name alone.
package composer

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// installTimeout is how long an install may take.
const installTimeout = 10 * time.Minute

// Composer is Composer, driven through a runner.
type Composer struct {
	run runner.Runner
}

// New returns Composer, driven through run.
func New(run runner.Runner) *Composer {
	return &Composer{run: run}
}

func (*Composer) Name() string    { return "composer" }
func (*Composer) Title() string   { return "Composer" }
func (*Composer) Program() string { return "composer" }

func (c *Composer) global(ctx context.Context, timeout time.Duration, args ...string) (runner.Result, error) {
	return c.run.Run(ctx, runner.Command{Name: "composer", Args: slices.Concat([]string{"global"}, args), Timeout: timeout})
}

// Installed lists the packages required globally, each installed for
// itself; what they need isn't listed.
func (c *Composer) Installed(ctx context.Context) ([]kind.Installed, error) {
	res, err := c.global(ctx, 0, "show", "--direct", "--format=json")
	if err != nil {
		return nil, err
	}
	var listing struct {
		Installed []struct {
			Name string `json:"name"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(res.Stdout, &listing); err != nil {
		return nil, fmt.Errorf("read composer global show's answer: %w", err)
	}
	installed := make([]kind.Installed, len(listing.Installed))
	for i, p := range listing.Installed {
		installed[i] = kind.Installed{Name: p.Name, Explicit: true}
	}
	return installed, nil
}

// Key is a package's name, without the constraint it may be declared with.
func (*Composer) Key(name string) string {
	name, _, _ = strings.Cut(name, ":")
	return strings.ToLower(name)
}

// Resolve takes every name as a package's: one that isn't fails to install.
func (*Composer) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Install requires names globally, each with the constraint it's declared
// with.
func (c *Composer) Install(ctx context.Context, names []string) error {
	_, err := c.global(ctx, installTimeout, slices.Concat([]string{"require", "--no-interaction"}, names)...)
	return err
}

// Remove removes names from the global requirements, and what they alone
// needed.
func (c *Composer) Remove(ctx context.Context, names []string) error {
	keys := make([]string, len(names))
	for i, name := range names {
		keys[i] = c.Key(name)
	}
	_, err := c.global(ctx, installTimeout, slices.Concat([]string{"remove", "--no-interaction"}, keys)...)
	return err
}
