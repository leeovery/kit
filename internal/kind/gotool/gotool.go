// Package gotool is the kind of programs go install puts in Go's bin
// folder, each named by the package it's built from, as in
// golang.org/x/tools/cmd/goimports. A version may follow, as in
// …/goimports@v0.49.0; without one, the latest is installed.
package gotool

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// installTimeout is how long building one may take.
const installTimeout = 10 * time.Minute

// Go is Go's tool installs, driven through a runner.
type Go struct {
	run runner.Runner
}

// New returns Go's tool installs, driven through run.
func New(run runner.Runner) *Go {
	return &Go{run: run}
}

func (*Go) Name() string    { return "go" }
func (*Go) Title() string   { return "Go tools" }
func (*Go) Program() string { return "go" }

// program is a program in Go's bin folder, and the package it was built
// from: "" when it doesn't say.
type program struct {
	path string
	pkg  string
}

// name is what kit calls it: its package, or its file's name.
func (p program) name() string {
	if p.pkg != "" {
		return p.pkg
	}
	return filepath.Base(p.path)
}

// bin is Go's bin folder: GOBIN, else the first GOPATH's bin.
func (g *Go) bin(ctx context.Context) (string, error) {
	res, err := g.run.Run(ctx, runner.Command{Name: "go", Args: []string{"env", "GOBIN", "GOPATH"}})
	if err != nil {
		return "", err
	}
	// A line each, GOBIN's first, empty when it's unset.
	lines := strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		return strings.TrimSpace(lines[0]), nil
	}
	if len(lines) < 2 || strings.TrimSpace(lines[1]) == "" {
		return "", errors.New("go env says neither GOBIN nor GOPATH")
	}
	gopath, _, _ := strings.Cut(strings.TrimSpace(lines[1]), string(os.PathListSeparator))
	return filepath.Join(gopath, "bin"), nil
}

// programs are the programs in Go's bin folder, each with the package go
// version -m says it was built from.
func (g *Go) programs(ctx context.Context) ([]program, error) {
	dir, err := g.bin(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Go's bin folder: %w", err)
	}
	var programs []program
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		programs = append(programs, program{path: filepath.Join(dir, e.Name())})
	}
	if len(programs) == 0 {
		return nil, nil
	}
	args := []string{"version", "-m"}
	for _, p := range programs {
		args = append(args, p.path)
	}
	// go version -m exits 1 when a file isn't a Go program, yet reports
	// every one that is.
	res, err := g.run.Run(ctx, runner.Command{Name: "go", Args: args})
	if _, exited := errors.AsType[*runner.ExitError](err); err != nil && !exited {
		return nil, err
	}
	pkgs := make(map[string]string)
	current := ""
	for line := range strings.Lines(string(res.Stdout)) {
		if !strings.HasPrefix(line, "\t") {
			current, _, _ = strings.Cut(line, ": ")
			continue
		}
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "path" {
			pkgs[current] = fields[1]
		}
	}
	for i := range programs {
		programs[i].pkg = pkgs[programs[i].path]
	}
	return programs, nil
}

// Installed lists the programs in Go's bin folder, by package, each
// installed for itself.
func (g *Go) Installed(ctx context.Context) ([]kind.Installed, error) {
	programs, err := g.programs(ctx)
	if err != nil {
		return nil, err
	}
	installed := make([]kind.Installed, len(programs))
	for i, p := range programs {
		installed[i] = kind.Installed{Name: p.name(), Explicit: true}
	}
	return installed, nil
}

// Key is a package, without the version it may be declared with.
func (*Go) Key(name string) string {
	name, _, _ = strings.Cut(name, "@")
	return name
}

// Resolve takes every name as a package's: one that isn't fails to install.
func (*Go) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Install builds and installs each package named, at the version it's
// declared with, else the latest: one at a time, as go install takes
// packages of one module together only.
func (g *Go) Install(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		if !strings.Contains(name, "@") {
			name += "@latest"
		}
		_, err := g.run.Run(ctx, runner.Command{Name: "go", Args: []string{"install", name}, Timeout: installTimeout})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Remove deletes the programs built from the packages named: go has no
// uninstall.
func (g *Go) Remove(ctx context.Context, names []string) error {
	programs, err := g.programs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		i := slices.IndexFunc(programs, func(p program) bool { return p.name() == g.Key(name) })
		if i < 0 {
			errs = append(errs, fmt.Errorf("no program in Go's bin folder is built from %s", name))
			continue
		}
		if err := os.Remove(programs[i].path); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", programs[i].path, err))
		}
	}
	return errors.Join(errs...)
}
