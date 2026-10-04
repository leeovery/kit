// Package ghext is the kind of the GitHub CLI's extensions, each named by
// its repository, as in github/gh-stack.
package ghext

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// installTimeout is how long installing one may take.
const installTimeout = 5 * time.Minute

// GH is the GitHub CLI's extensions, driven through a runner.
type GH struct {
	run runner.Runner
}

// New returns the GitHub CLI's extensions, driven through run.
func New(run runner.Runner) *GH {
	return &GH{run: run}
}

func (*GH) Name() string    { return "gh" }
func (*GH) Title() string   { return "GitHub CLI extensions" }
func (*GH) Program() string { return "gh" }

// Installed lists the extensions installed from repositories, by
// repository, each installed for itself. gh extension list prints a line
// each: the command, the repository and the version, between tabs.
func (g *GH) Installed(ctx context.Context) ([]kind.Installed, error) {
	res, err := g.run.Run(ctx, runner.Command{Name: "gh", Args: []string{"extension", "list"}})
	if err != nil {
		return nil, err
	}
	var installed []kind.Installed
	for line := range strings.Lines(string(res.Stdout)) {
		fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(fields) < 2 || strings.Count(fields[1], "/") != 1 {
			continue
		}
		installed = append(installed, kind.Installed{Name: fields[1], Explicit: true})
	}
	return installed, nil
}

// Key is a repository, in lowercase, as GitHub takes its names.
func (*GH) Key(name string) string {
	return strings.ToLower(name)
}

// Resolve takes every name as a repository's: one that isn't fails to
// install.
func (*GH) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Install installs the extension from each repository named, one at a time,
// as gh takes them.
func (g *GH) Install(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		_, err := g.run.Run(ctx, runner.Command{Name: "gh", Args: []string{"extension", "install", name}, Timeout: installTimeout})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Remove removes the extension from each repository named, by its command's
// name: the repository's, without gh-.
func (g *GH) Remove(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		command := strings.TrimPrefix(name[strings.LastIndex(name, "/")+1:], "gh-")
		_, err := g.run.Run(ctx, runner.Command{Name: "gh", Args: []string{"extension", "remove", command}})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
