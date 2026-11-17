// Package capture draws kit's screens for the README's art (demo/), through
// kit's own faces (internal/render), in-process: a fixture is a still,
// drawn from a fixed run; a scenario is a clip, played in real time from
// events and keys scripted at moments into it, its clock reading a fixed
// day. Neither runs a program, nor reads or writes anything of the Mac's:
// no config, state, home or network. What they draw from is the render
// tests' own (internal/render/rendertest): the status run its golden file
// holds, the boot's order, a new Mac's steps and 1Password's sign-in.
package capture

import (
	"fmt"
	"io"

	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
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
	{Name: "status", About: "kit status on a Mac with some of everything, the run pretty.golden holds", draw: status},
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
	for _, e := range rendertest.StatusRun() {
		face.Emit(e)
	}
	return face.Close()
}

// names are each of all's names, in words.
func names[T any](all []T, name func(T) string) string {
	out := make([]string, len(all))
	for i, a := range all {
		out[i] = name(a)
	}
	return look.InWords(out)
}
