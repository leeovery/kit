// Package capture draws kit's screens for the README's art (demo/), through
// kit's own faces (internal/render), in-process: a fixture is a still,
// drawn from a fixed run; a scenario is a clip, played in real time from
// events and keys scripted at moments into it, its clock reading a fixed
// day. Neither runs a program, nor reads or writes anything of the Mac's:
// no config, state, home or network. The clips' steps are the render
// tests' own (internal/render/rendertest): the boot's order, a new Mac's
// steps and 1Password's sign-in.
package capture

import (
	"fmt"
	"io"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// Fixture is a still: a run's screen, printed as kit prints it.
type Fixture struct {
	Name, About string
	draw        func(out io.Writer, width int) error
}

// Draw prints the fixture to out, a terminal width columns wide: kit's
// faces go no wider than kit's look allows, whatever the terminal.
func (f Fixture) Draw(out io.Writer, width int) error { return f.draw(out, width) }

var fixtures = []Fixture{
	{Name: "status", About: "kit status on a Mac in daily use, two things waiting on you", draw: status},
}

// Fixtures are every fixture, by name.
func Fixtures() []Fixture { return fixtures }

// FixtureNamed is the fixture called name.
func FixtureNamed(name string) (Fixture, error) {
	for _, f := range fixtures {
		if f.Name == name {
			return f, nil
		}
	}
	return Fixture{}, fmt.Errorf("no fixture %q (there are %s)", name, names(fixtures, func(f Fixture) string { return f.Name }))
}

// status is kit status's report, as its face prints it once the run's
// done.
func status(out io.Writer, width int) error {
	face := render.NewPretty(out, width, false)
	for _, e := range statusRun() {
		face.Emit(e)
	}
	return face.Close()
}

// statusRun is kit status on a Mac in daily use: backed up, everything
// declared in place, but for an app declared and not yet installed, and a
// tool installed by hand, not declared.
func statusRun() []event.Event {
	at := time.Date(2026, 11, 20, 16, 12, 0, 0, time.UTC)
	packages := func(name, title, row string) event.Step {
		return event.Step{Name: name, Title: title, Area: "Drift", Row: row, Tool: "Homebrew", Part: render.PartPackages}
	}
	steps := []event.Step{
		packages("homebrew", "Homebrew", "Homebrew formulae"),
		packages("brew", "Formulae", "Homebrew formulae"),
		packages("cask", "Casks", "Homebrew casks"),
		{Name: "mas", Title: "App Store", Area: "Drift", Row: "App Store apps", Part: render.PartPackages},
		{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings", Part: render.PartSettings},
		{Name: "power", Title: "Power settings", Area: "Drift", Row: "Mac settings", Part: render.PartSettings},
		{Name: "file", Title: "Linked files", Area: "Drift", Part: render.PartFiles},
		{Name: "secret", Title: "Secrets", Area: "Drift", Part: render.PartSecrets},
		{Name: "config-private", Title: "Privacy", Area: "Config", Part: render.PartConfig},
		{Name: "config-sync", Title: "Sync", Area: "Config", Part: render.PartConfig},
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "arq", Title: "Arq", Area: "Backups"},
		{Name: "disk", Title: "Disk space", Area: "Mac"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
		{Name: "claude-code", Title: "claude-code", Area: "Steps"},
	}
	ok := func(step, summary string, counts map[string]int) event.Event {
		return event.StepFinished{Time: at, Step: step, Result: check.Result{State: check.OK, Summary: summary, Counts: counts}}
	}
	installed := func(n int) map[string]int { return map[string]int{"installed": n} }
	waiting := func(step, summary string, n int, it check.Item) event.Event {
		return event.StepFinished{Time: at, Step: step, Result: check.Result{State: check.Attention, Summary: summary, Counts: installed(n), Items: []check.Item{it}}}
	}
	return []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Version: "0.1.2", Steps: steps},
		ok("homebrew", "/opt/homebrew", nil),
		waiting("brew", "64 declared, all installed, 1 not declared", 64,
			check.Item{ID: "brew:htop", Name: "htop", State: "extra", Since: at.Add(-3 * 24 * time.Hour)}),
		waiting("cask", "32 declared, 31 installed", 31,
			check.Item{ID: "cask:figma", Name: "figma", State: "missing", Action: "install"}),
		ok("mas", "6 declared, all installed", installed(6)),
		ok("defaults", "99 declared, all set", installed(99)),
		ok("power", "6 declared, all set", installed(6)),
		ok("file", "61 linked", map[string]int{"linked": 61}),
		ok("secret", "12 in place", installed(12)),
		event.StepFinished{Time: at, Step: "config-private", Result: check.Result{State: check.OK, Summary: "private on GitHub", Glance: "private"}},
		event.StepFinished{Time: at, Step: "config-sync", Result: check.Result{State: check.OK, Summary: "pushed", Glance: "pushed"}},
		ok("time-machine", "last backup 15:48", nil),
		ok("arq", "last backup 15:30", nil),
		ok("disk", "62% free", nil),
		ok("fonts", "16 installed", nil),
		ok("claude-code", "installed, 2.1", nil),
		event.RunFinished{Time: at, Duration: 1600 * time.Millisecond, Counts: map[check.State]int{check.OK: 13, check.Attention: 2}},
	}
}

// names are each of all's names, in words.
func names[T any](all []T, name func(T) string) string {
	out := make([]string, len(all))
	for i, a := range all {
		out[i] = name(a)
	}
	return look.InWords(out)
}
