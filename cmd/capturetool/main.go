// Command capturetool draws kit's screens for the README's art, as the
// tapes in demo/ record them: through kit's own faces, in-process, from the
// runs the render tests hold (internal/capture). It runs no program, never
// runs kit, and reads or writes nothing of the Mac's: no config, state,
// home or network. It draws in truecolor, as Ghostty and Terminal show.
//
// Usage:
//
//	capturetool --list               the fixtures and scenarios
//	capturetool --fixture status     a still, printed at the terminal's width
//	capturetool --fixture reconcile  a still, a window of its own, held
//	capturetool --scenario boot      a clip, played full size in real time
//	capturetool --stills             the windows of their own, for record.sh
//
// A still that's a window of its own is drawn on a cleared screen, then
// held, live, till a line on stdin, or its end, stops it. A scenario waits
// for a line on stdin before it starts, so a tape shows it from its first
// frame; it plays its cues, then holds its last screen, live, till another
// line, or stdin's end, stops it.
package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/leeovery/kit/internal/capture"
)

func main() {
	size := func() (int, int) {
		w, h, err := term.GetSize(os.Stdout.Fd())
		if err != nil {
			return 80, 24
		}
		return w, h
	}
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, size); err != nil {
		fmt.Fprintln(os.Stderr, "capturetool:", err)
		os.Exit(1)
	}
}

// clear clears the screen, the cursor at its top left, as a fresh window
// opens.
const clear = "\x1b[2J\x1b[H"

// run does what args ask, reading lines from in, drawing at out, a
// terminal size says the size of.
func run(ctx context.Context, args []string, in io.Reader, out io.Writer, size func() (int, int)) error {
	flags := flag.NewFlagSet("capturetool", flag.ContinueOnError)
	flags.SetOutput(out)
	fixture := flags.String("fixture", "", "print the fixture `name`d, a still")
	scenario := flags.String("scenario", "", "play the scenario `name`d, a clip")
	list := flags.Bool("list", false, "list the fixtures and scenarios")
	stills := flags.Bool("stills", false, "list the stills that are windows of their own, a line each: name, look, columns, lines and the words each shows once drawn, tab-separated")
	if err := flags.Parse(args); err != nil {
		return err
	}
	colour := &colorprofile.Writer{Forward: out, Profile: colorprofile.TrueColor}
	switch {
	case flags.NArg() > 0:
		return fmt.Errorf("unexpected %q: name a fixture or a scenario by its flag", flags.Arg(0))
	case *list:
		return listAll(out)
	case *stills:
		return listStills(out)
	case *fixture != "" && *scenario != "":
		return errors.New("--fixture and --scenario: one at a time")
	case *fixture != "":
		f, err := capture.FixtureNamed(*fixture)
		if err != nil {
			return err
		}
		return draw(ctx, f, in, out, capture.Screen{Out: colour, Size: size})
	case *scenario != "":
		s, err := capture.ScenarioNamed(*scenario)
		if err != nil {
			return err
		}
		return play(ctx, s, in, out, capture.Screen{Out: colour, Size: size})
	}
	return errors.New("name a fixture or a scenario: --list lists them")
}

// draw draws f at screen: printed, there and then; held, the screen
// cleared first, till a line comes from in, or its end.
func draw(ctx context.Context, f capture.Fixture, in io.Reader, out io.Writer, screen capture.Screen) error {
	if !f.Held() {
		return f.Draw(ctx, screen, nil)
	}
	ctx, stop := untilALine(ctx, bufio.NewScanner(in))
	defer stop()
	if _, err := io.WriteString(out, clear); err != nil {
		return err
	}
	return f.Draw(ctx, screen, nil)
}

// play plays s at screen once a line comes from in, the screen cleared
// first, till the next line, or in's end.
func play(ctx context.Context, s capture.Scenario, in io.Reader, out io.Writer, screen capture.Screen) error {
	lines := bufio.NewScanner(in)
	if !lines.Scan() {
		return lines.Err()
	}
	ctx, stop := untilALine(ctx, lines)
	defer stop()
	if _, err := io.WriteString(out, clear); err != nil {
		return err
	}
	return s.Play(ctx, screen, nil)
}

// untilALine is ctx, done once lines reads its next line, or ends.
func untilALine(ctx context.Context, lines *bufio.Scanner) (context.Context, context.CancelFunc) {
	ctx, stop := context.WithCancel(ctx)
	go func() {
		lines.Scan()
		stop()
	}()
	return ctx, stop
}

// listStills lists the stills that are windows of their own, as record.sh
// makes their tapes from: each its name, the look it's recorded in, its
// size and the words it shows once drawn, tab-separated.
func listStills(out io.Writer) error {
	w := bufio.NewWriter(out)
	for _, f := range capture.Fixtures() {
		if f.Held() {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", f.Name, cmp.Or(string(f.Look), "ghostty"), f.Width, f.Height, f.Shows)
		}
	}
	return w.Flush()
}

func listAll(out io.Writer) error {
	w := tabwriter.NewWriter(out, 0, 0, 1, ' ', 0)
	for _, f := range capture.Fixtures() {
		size := ""
		if f.Held() {
			size = fmt.Sprintf(", %d×%d", f.Width, f.Height)
		}
		_, _ = fmt.Fprintf(w, "--fixture %s\t%s%s\n", f.Name, f.About, size)
	}
	for _, s := range capture.Scenarios() {
		_, _ = fmt.Fprintf(w, "--scenario %s\t%s, %d×%d\n", s.Name, s.About, s.Width, s.Height)
	}
	return w.Flush()
}
