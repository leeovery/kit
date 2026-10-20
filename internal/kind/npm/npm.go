// Package npm is npm's kind: the packages installed globally. A package may
// be declared with a version, as in typescript@5, kept for installing; it
// matches on its name alone.
package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// installTimeout is how long an install may take.
const installTimeout = 10 * time.Minute

// NPM is npm, driven through a runner.
type NPM struct {
	run runner.Runner
	// home is where npm runs, so the Node version it runs under is the
	// default one, never a project's.
	home string
}

// kindName names the kind.
const kindName = "npm"

// Descriptor is npm's global packages' kind, as kit's shared code knows it.
var Descriptor = kind.Descriptor{
	Name:    kindName,
	Label:   "npm packages",
	Group:   kind.Packages,
	Section: &config.Section{Kind: kindName, Header: "npm packages", Form: config.Names, Sorted: true, Held: true},
	Help: &kind.Help{
		What:   "npm's global packages",
		Add:    "Install global npm packages, and declare them",
		Remove: "Uninstall global npm packages, and undeclare them",
	},
	Row: "Developer tools",
	New: func(d *kind.Deps) kind.Kind { return New(d.Run, d.Home) },
}

// New returns npm, driven through run, run from home.
func New(run runner.Runner, home string) *NPM {
	return &NPM{run: run, home: home}
}

func (*NPM) Name() string    { return kindName }
func (*NPM) Program() string { return "npm" }

func (n *NPM) npm(ctx context.Context, timeout time.Duration, args ...string) (runner.Result, error) {
	return n.run.Run(ctx, runner.Command{Name: "npm", Args: args, Dir: n.home, Timeout: timeout})
}

// Installed lists the packages installed globally, each installed for
// itself. npm ls exits 1 over problems it finds, such as a missing peer,
// yet lists what's there all the same.
func (n *NPM) Installed(ctx context.Context) ([]kind.Installed, error) {
	res, err := n.npm(ctx, 0, "ls", "--global", "--depth=0", "--json")
	if _, exited := errors.AsType[*runner.ExitError](err); err != nil && (!exited || len(res.Stdout) == 0) {
		return nil, err
	}
	var listing struct {
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal(res.Stdout, &listing); err != nil {
		return nil, fmt.Errorf("read npm ls's answer: %w", err)
	}
	names := make([]string, 0, len(listing.Dependencies))
	for name := range listing.Dependencies {
		names = append(names, name)
	}
	slices.Sort(names)
	installed := make([]kind.Installed, len(names))
	for i, name := range names {
		installed[i] = kind.Installed{Name: name, Explicit: true}
	}
	return installed, nil
}

// Key is a package's name, without the version it may be declared with: the
// last @ that doesn't start a scope.
func (*NPM) Key(name string) string {
	if i := strings.LastIndex(name, "@"); i > 0 {
		return name[:i]
	}
	return name
}

// Resolve takes every name as a package's: one that isn't fails to install.
func (*NPM) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Install installs names globally, each at the version it's declared with.
func (n *NPM) Install(ctx context.Context, names []string) error {
	_, err := n.npm(ctx, installTimeout, slices.Concat([]string{"install", "--global"}, names)...)
	return err
}

// Remove uninstalls names globally.
func (n *NPM) Remove(ctx context.Context, names []string) error {
	keys := make([]string, len(names))
	for i, name := range names {
		keys[i] = n.Key(name)
	}
	_, err := n.npm(ctx, 0, slices.Concat([]string{"uninstall", "--global"}, keys)...)
	return err
}
