// Package apps is what kit knows of the apps kit.toml names: the terminal a
// new Mac's boot hands over to, and the password manager. The boot installs
// both before anything else, so kit knows each by name: its cask, its
// bundle, and, for a terminal, how to open it running a command and how to
// tell kit is running in it.
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

// PasswordManager is a password manager a new Mac's boot installs.
type PasswordManager struct {
	// Name is the password manager as kit.toml names it, as in 1password.
	Name string
	// Title is its name as the Mac shows it, as in 1Password.
	Title string
	// Cask is the Homebrew cask that installs it, and Bundle the app it
	// puts in the Applications folder.
	Cask, Bundle string
	// CLI is the cask that installs its command-line tool, from whichever
	// tap the config declares it, and Program the tool, which kit reads
	// secrets through.
	CLI, Program string
	// SignIn is what to do on a new Mac for the tool to answer; after a line
	// break, the setting to turn on.
	SignIn string
}

// PasswordManagers are the password managers kit knows, by name.
var PasswordManagers = map[string]PasswordManager{
	"1password": {
		Name: "1password", Title: "1Password", Cask: "1password", Bundle: "1Password.app", CLI: "1password-cli", Program: "op",
		SignIn: "sign in with your phone's 1Password, then turn on\nSettings › Developer › Integrate with 1Password CLI",
	},
}

// CLICask is the cask that installs the password manager's tool: as the
// casks declared name it, from a tap or not, so the config's own install
// is the one there; its own name, when they don't.
func (pm PasswordManager) CLICask(declared []string) string {
	for _, cask := range declared {
		if cask == pm.CLI || strings.HasSuffix(cask, "/"+pm.CLI) {
			return cask
		}
	}
	return pm.CLI
}

// Names lists the names in known, in order, for a message.
func Names[T any](known map[string]T) string {
	return strings.Join(slices.Sorted(maps.Keys(known)), ", ")
}
