// Package devtool is the kinds of developer tools' own packages: npm's and
// Composer's global packages and the GitHub CLI's extensions. Each is a
// program that lists what it has installed and installs and removes things
// by name, as its table says; a name may carry what's kept for installing,
// as a version, and matches on the rest.
package devtool

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

// table is a developer tool's kind: what it's called, its command, and how
// it lists, installs and removes its packages.
type table struct {
	name, label string
	help        kind.Help
	program     string
	// fromHome runs the program from the home folder.
	fromHome bool
	// list lists what's installed, and listed reads the names from what it
	// answered, or failed with.
	list   []string
	listed func(res runner.Result, err error) ([]string, error)
	// key is the part of a name things match by.
	key             func(name string) string
	install, remove command
}

// command is installing or removing: the arguments before the names, how
// long it may take (0 for no limit), whether it takes one name a command,
// and what it's given of a declared name: the name itself, when nil.
type command struct {
	args    []string
	timeout time.Duration
	each    bool
	name    func(string) string
}

// tenMinutes is how long installing a global package may take.
const tenMinutes = 10 * time.Minute

// NPMDescriptor is npm's global packages' kind. A package may be declared
// with a version, as in typescript@5, kept for installing. npm runs from
// the home folder, so the Node version it runs under is the default one,
// never a project's.
var NPMDescriptor = table{
	name: "npm", label: "npm packages", program: "npm", fromHome: true,
	help: kind.Help{
		What:   "npm's global packages",
		Add:    "Install global npm packages, and declare them",
		Remove: "Uninstall global npm packages, and undeclare them",
	},
	list:    []string{"ls", "--global", "--depth=0", "--json"},
	listed:  npmListed,
	key:     npmKey,
	install: command{args: []string{"install", "--global"}, timeout: tenMinutes},
	remove:  command{args: []string{"uninstall", "--global"}, name: npmKey},
}.descriptor()

// ComposerDescriptor is Composer's global packages' kind. A package may be
// declared with a constraint, as in laravel/valet:^4.0, kept for
// installing. Composer's commands run without a progress bar, which it
// prints a line a step when it's not at a terminal.
var ComposerDescriptor = table{
	name: "composer", label: "Composer packages", program: "composer",
	help: kind.Help{
		What:   "Composer's global packages",
		Add:    "Install global Composer packages, and declare them",
		Remove: "Uninstall global Composer packages, and undeclare them",
	},
	list:    []string{"global", "show", "--direct", "--format=json"},
	listed:  composerListed,
	key:     composerKey,
	install: command{args: []string{"global", "require", "--no-interaction", "--no-progress"}, timeout: tenMinutes},
	remove:  command{args: []string{"global", "remove", "--no-interaction", "--no-progress"}, timeout: tenMinutes, name: composerKey},
}.descriptor()

// GHDescriptor is the GitHub CLI's extensions' kind, each named by its
// repository, as in github/gh-stack, and installed and removed one at a
// time, as gh takes them.
var GHDescriptor = table{
	name: "gh", label: "GitHub extensions", program: "gh",
	help: kind.Help{
		What:   "The GitHub CLI's extensions",
		Add:    "Install GitHub CLI extensions, as owner/repo, and declare them",
		Remove: "Uninstall GitHub CLI extensions, and undeclare them",
	},
	list:    []string{"extension", "list"},
	listed:  ghListed,
	key:     strings.ToLower,
	install: command{args: []string{"extension", "install"}, timeout: 5 * time.Minute, each: true},
	remove:  command{args: []string{"extension", "remove"}, each: true, name: ghCommand},
}.descriptor()

// descriptor is t's kind, as kit's shared code knows it.
func (t table) descriptor() kind.Descriptor {
	return kind.Descriptor{
		Name:    t.name,
		Label:   t.label,
		Group:   kind.Packages,
		Section: config.Section{Kind: t.name, Header: strings.ToLower(t.label), Form: config.Names, Sorted: true, Held: true},
		Help:    &t.help,
		Row:     "Developer tools",
		New:     func(d *kind.Deps) kind.Kind { return &tool{table: t, run: d.Run, home: d.Home} },
	}
}

// tool is a developer tool's kind, driven through a runner.
type tool struct {
	table
	run  runner.Runner
	home string
}

func (t *tool) Name() string           { return t.name }
func (t *tool) Program() string        { return t.program }
func (t *tool) Key(name string) string { return t.key(name) }

func (t *tool) exec(ctx context.Context, timeout time.Duration, args ...string) (runner.Result, error) {
	cmd := runner.Command{Name: t.program, Args: args, Timeout: timeout}
	if t.fromHome {
		cmd.Dir = t.home
	}
	return t.run.Run(ctx, cmd)
}

// Installed lists what the program lists, each installed for itself; what
// they need isn't listed.
func (t *tool) Installed(ctx context.Context) ([]kind.Installed, error) {
	names, err := t.listed(t.exec(ctx, 0, t.list...))
	if err != nil {
		return nil, err
	}
	installed := make([]kind.Installed, len(names))
	for i, name := range names {
		installed[i] = kind.Installed{Name: name, Explicit: true}
	}
	return installed, nil
}

func (t *tool) Install(ctx context.Context, names []string) error {
	return t.do(ctx, t.install, names)
}

func (t *tool) Remove(ctx context.Context, names []string) error {
	return t.do(ctx, t.remove, names)
}

// do runs c with names: as one command, or a command each, every one tried.
func (t *tool) do(ctx context.Context, c command, names []string) error {
	given := names
	if c.name != nil {
		given = make([]string, len(names))
		for i, name := range names {
			given[i] = c.name(name)
		}
	}
	if !c.each {
		_, err := t.exec(ctx, c.timeout, slices.Concat(c.args, given)...)
		return err
	}
	var errs []error
	for _, name := range given {
		_, err := t.exec(ctx, c.timeout, slices.Concat(c.args, []string{name})...)
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// npmListed reads npm ls's packages, sorted. npm ls exits 1 over problems it
// finds, such as a missing peer, yet lists what's there all the same.
func npmListed(res runner.Result, err error) ([]string, error) {
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
	return names, nil
}

// npmKey is a package's name, without the version it may be declared with:
// the last @ that doesn't start a scope.
func npmKey(name string) string {
	if i := strings.LastIndex(name, "@"); i > 0 {
		return name[:i]
	}
	return name
}

// composerListed reads the packages required globally.
func composerListed(res runner.Result, err error) ([]string, error) {
	if exit, exited := errors.AsType[*runner.ExitError](err); exited && strings.Contains(exit.FullStderr+string(res.Stdout), "create a composer.json file") {
		// No global composer.json yet, as on a new Mac: nothing installed.
		return nil, nil
	}
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
	names := make([]string, len(listing.Installed))
	for i, p := range listing.Installed {
		names[i] = p.Name
	}
	return names, nil
}

// composerKey is a package's name, without the constraint it may be
// declared with.
func composerKey(name string) string {
	name, _, _ = strings.Cut(name, ":")
	return strings.ToLower(name)
}

// ghListed reads the extensions installed from repositories, by
// repository. gh extension list prints a line each: the command, the
// repository and the version, between tabs.
func ghListed(res runner.Result, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.Lines(string(res.Stdout)) {
		fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(fields) < 2 || strings.Count(fields[1], "/") != 1 {
			continue
		}
		names = append(names, fields[1])
	}
	return names, nil
}

// ghCommand is an extension's command's name, which gh removes it by: its
// repository's, without gh-.
func ghCommand(name string) string {
	return strings.TrimPrefix(name[strings.LastIndex(name, "/")+1:], "gh-")
}
