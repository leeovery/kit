// Package apps is what kit knows of the apps kit.toml names: the terminal a
// new Mac's boot hands over to, which the boot installs before anything
// else, so kit knows it by name: its cask, its bundle, how to open it
// running a command and how to tell kit is running in it.
package apps

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
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
	// runs are the app's arguments, before a command, that have it run the
	// command.
	runs []string
}

// Terminals are the terminals kit knows, by name. Ghostty's way of running
// a command was proven in a virtual machine (9 Oct): opened so, it runs the
// command once, with its arguments, and asks nothing of its own.
var Terminals = map[string]Terminal{
	"ghostty": {Name: "ghostty", Title: "Ghostty", Cask: "ghostty", Bundle: "Ghostty.app", Program: "ghostty", runs: []string{"-e"}},
}

// Launch is open's arguments that start a new instance of the terminal,
// from the Applications folder at dir, running command: by the app's path,
// as Launch Services may not know a newly installed app by name yet.
func (t Terminal) Launch(dir string, command ...string) []string {
	return slices.Concat([]string{"-na", filepath.Join(dir, t.Bundle), "--args"}, t.runs, command)
}

// Names lists the names in known, in order, for a message.
func Names[T any](known map[string]T) string {
	return strings.Join(slices.Sorted(maps.Keys(known)), ", ")
}
