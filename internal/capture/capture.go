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
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// Fixture is a still: kit's screen, drawn from a fixed run at a moment of
// a day, its clock moved on only by the run's own pauses. One is printed,
// a report, at whatever width; the rest are a window of their own, Width by
// Height, held once drawn, in Ghostty's look, or Terminal's where Look says
// so, and showing Shows once drawn, as their tapes wait for.
type Fixture struct {
	Name, About   string
	Width, Height int
	Look          Look
	Shows         string
	at            time.Time
	draw          func(*stage) error
}

// Look is the terminal a still is recorded in.
type Look string

// The terminals a still is recorded in: Ghostty, where kit runs once the
// boot's handed over, and Terminal, where the boot runs on a new Mac.
const (
	Ghostty  Look = ""
	Terminal Look = "terminal"
)

// Held is whether f is a window of its own, held once it's drawn, rather
// than printed.
func (f Fixture) Held() bool { return f.Width > 0 }

// Whole is whether f is the clip's window, a README's screen, recorded
// whole, whatever it draws in it, as each of the README's screens is, so
// all are one size and their text one scale.
func (f Fixture) Whole() bool { return f.Width == windowColumns && f.Height == windowLines }

// Draw draws f at screen: printed, it returns once it's printed; held, it
// holds till ctx is done, telling held once it's drawn.
func (f Fixture) Draw(ctx context.Context, screen Screen, held func()) error {
	return perform(ctx, screen, stepped(f.at), held, func(s *stage) error {
		s.shows = f.Shows
		return f.draw(s)
	})
}

// fixtures are the README's stills, then the docs pages', each named for
// its page.
var fixtures = slices.Concat([]Fixture{
	{Name: "status", About: "kit status on a Mac in daily use, two things waiting on you", draw: status},
	{Name: "apply-card", About: "kit apply's screen, 1Password's sign-in come up as a card", Width: windowColumns, Height: windowLines,
		Shows: "kit carries on by itself", at: arrivedAt, draw: applyCard},
	{Name: "apply-todos", About: "the to-dos at kit apply's end, the first done, the second's card up", Width: windowColumns, Height: windowLines,
		Shows: "only you can fetch", at: arrivedAt, draw: applyToDos},
	{Name: "boot-sign-in", About: "the boot signing in to GitHub, its QR code the whole window", Width: windowColumns, Height: windowLines, Look: Terminal,
		Shows: "WDJB", at: bootAt, draw: bootSignIn},
	{Name: "reconcile", About: "kit reconcile asking what to do with each thing that drifted", Width: windowColumns, Height: windowLines,
		Shows: "RECONCILE  2 of 3", at: reconcileAt, draw: reconcile},
	{Name: "log", About: "kit log, its runs to pick one from: every hour's by itself, and those by hand", Width: windowColumns, Height: windowLines,
		Shows: "runs of nightly", at: logAt, draw: runLog},
}, docs)

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
// done, as wide as the terminal allows.
func status(s *stage) error {
	width, _ := s.term.Size()
	face := render.NewPretty(s.term.Out, width, false)
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
	return statusAt(at,
		waiting(at, "brew", "64 declared, all installed, 1 not declared", 64,
			check.Item{ID: "brew:htop", Name: "htop", State: kind.Extra, Since: at.Add(-3 * 24 * time.Hour)}),
		waiting(at, "cask", "32 declared, 31 installed", 31,
			check.Item{ID: "cask:figma", Name: "figma", State: kind.Missing, Action: kind.Install}),
	)
}

// statusAt is kit status at at on a Mac in daily use, all fine, but for the
// results instead, by their steps.
func statusAt(at time.Time, instead ...event.StepFinished) []event.Event {
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
	ok := func(step, summary string, counts map[string]int) event.StepFinished {
		return event.StepFinished{Time: at, Step: step, Result: check.Result{State: check.OK, Summary: summary, Counts: counts}}
	}
	glance := func(step, summary string) event.StepFinished {
		return event.StepFinished{Time: at, Step: step, Result: check.Result{State: check.OK, Summary: summary, Glance: summary}}
	}
	results := []event.StepFinished{
		ok("homebrew", "/opt/homebrew", nil),
		ok("brew", "64 declared, all installed", installed(64)),
		ok("cask", "32 declared, all installed", installed(32)),
		ok("mas", "6 declared, all installed", installed(6)),
		ok("defaults", "99 declared, all set", installed(99)),
		ok("power", "6 declared, all set", installed(6)),
		ok("file", "61 linked", map[string]int{"linked": 61}),
		ok("secret", "12 in place", installed(12)),
		{Time: at, Step: "config-private", Result: check.Result{State: check.OK, Summary: "private on GitHub", Glance: "private"}},
		glance("config-sync", "pushed"),
		ok("time-machine", "last backup 15:48", nil),
		ok("arq", "last backup 15:30", nil),
		ok("disk", "62% free", nil),
		ok("fonts", "16 installed", nil),
		ok("claude-code", "installed, 2.1", nil),
	}
	events := []event.Event{event.RunStarted{Time: at, Command: "status", Machine: "laptop", Version: "0.1.2", Steps: steps}}
	counts := map[check.State]int{}
	for _, r := range results {
		if i := slices.IndexFunc(instead, func(f event.StepFinished) bool { return f.Step == r.Step }); i >= 0 {
			r = instead[i]
		}
		counts[r.Result.State]++
		events = append(events, r)
	}
	return append(events, event.RunFinished{Time: at, Duration: 1600 * time.Millisecond, Counts: counts})
}

// installed are counts of n installed.
func installed(n int) map[string]int { return map[string]int{"installed": n} }

// waiting is step's result at at, n installed, it waiting on the person.
func waiting(at time.Time, step, summary string, n int, it check.Item) event.StepFinished {
	return event.StepFinished{Time: at, Step: step, Result: check.Result{State: check.Attention, Summary: summary, Counts: installed(n), Items: []check.Item{it}}}
}

// names are each of all's names, in words.
func names[T any](all []T, name func(T) string) string {
	out := make([]string, len(all))
	for i, a := range all {
		out[i] = name(a)
	}
	return look.InWords(out)
}
