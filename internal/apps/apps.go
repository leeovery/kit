// Package apps is what kit knows of the Mac's apps: whether one's
// installed, by its bundle id; and the apps kit.toml names: the terminal a
// new Mac's boot hands over to, which the boot installs before anything
// else, so kit knows it by name: its cask, its bundle, how to tell kit is
// running in it, and its driver, which opens its windows.
package apps

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// Terminal is a terminal a new Mac's boot can hand over to.
type Terminal struct {
	// Name is the terminal as kit.toml names it, as in ghostty.
	Name string
	// Title is its name as the Mac shows it, as in Ghostty.
	Title string
	// Cask is the Homebrew cask that installs it, and Bundle the app it
	// puts in the Applications folder.
	Cask, Bundle string
	// Program is what it sets TERM_PROGRAM to for what it runs: how kit
	// knows it's running in it.
	Program string
	// Driver opens its windows.
	Driver Driver
}

// Window is a terminal window to open: running Command, given Env, and,
// Maximized, as big as the screen allows, for that window alone; else as
// big as the terminal's own settings make it.
type Window struct {
	Command, Env []string
	Maximized    bool
}

// Driver opens windows of one terminal, the app at app, as only it knows
// how: one that can't size a window opens it at the terminal's own size.
type Driver interface {
	Open(ctx context.Context, run runner.Runner, app string, w Window) error
}

// Terminals are the terminals kit knows, by name.
var Terminals = map[string]Terminal{
	"ghostty": {Name: "ghostty", Title: "Ghostty", Cask: "ghostty", Bundle: "Ghostty.app", Program: "ghostty", Driver: ghostty{}},
}

// Open opens a window of the terminal, from the Applications folder at dir.
func (t Terminal) Open(ctx context.Context, run runner.Runner, dir string, w Window) error {
	return t.Driver.Open(ctx, run, filepath.Join(dir, t.Bundle), w)
}

// ghostty is Ghostty's driver. A new instance, opened by the app's path (as
// Launch Services may not know a newly installed app by name yet) with -e,
// runs the command once, with its arguments, and asks nothing of its own:
// proven in a virtual machine (9 Oct). Its options go on its command line,
// before -e, for that instance alone, over its settings: each written twice,
// as macOS reads a launched app's arguments in pairs (-key value) and opens
// any left over as files, so one option alone would take -e as its value
// and leave the command's path, which Ghostty asks to run, then runs in a
// tab of its own (proven in a virtual machine, 12 Oct).
type ghostty struct{}

func (ghostty) Open(ctx context.Context, run runner.Runner, app string, w Window) error {
	args := []string{"-na", app, "--args"}
	if w.Maximized {
		args = append(args, "--maximize=true", "--maximize=true")
	}
	args = slices.Concat(args, []string{"-e"}, w.Command)
	_, err := run.Run(ctx, runner.Command{Name: "open", Args: args, Env: w.Env})
	return err
}

// Names lists the names in known, in order, for a message.
func Names[T any](known map[string]T) string {
	return strings.Join(slices.Sorted(maps.Keys(known)), ", ")
}
