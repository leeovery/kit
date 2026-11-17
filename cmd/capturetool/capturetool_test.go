package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func size() (int, int) { return 120, 34 }

// A fixture prints in truecolor, whatever the terminal says it shows: the
// status still's words in kit's own colours.
func TestFixture(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--fixture", "status"}, strings.NewReader(""), &out, size); err != nil {
		t.Fatal(err)
	}
	// kit's pink, #FF4F9A, as a truecolor escape.
	if got := out.String(); !strings.Contains(got, "\x1b[38;2;255;79;154m") || !strings.Contains(ansi.Strip(got), "2 need you · 13 fine") {
		t.Errorf("printed\n%s\nwant the status still in truecolor", got)
	}
}

// --list names every fixture and scenario, and the size each scenario is
// made for.
func TestList(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--list"}, strings.NewReader(""), &out, size); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--fixture status", "--scenario boot", "--scenario arrival", "120×34"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("listed\n%s\nwant %q", out.String(), want)
		}
	}
}

// A scenario starts on a line from stdin, the screen cleared first, and
// ends on the next, or stdin's end: with none, it draws nothing.
func TestScenarioWaitsForItsStart(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--scenario", "boot"}, strings.NewReader(""), &out, size); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("drew %q before its start", out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"--scenario", "boot"}, strings.NewReader("\n"), &out, size); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "\x1b[2J\x1b[H") {
		t.Errorf("drew %q; want the screen cleared first", out.String())
	}
}

// What it can't do, it says, naming what it can.
func TestRunRefuses(t *testing.T) {
	for _, tt := range []struct {
		args []string
		says string
	}{
		{nil, "--list lists them"},
		{[]string{"--fixture", "nothing"}, "status"},
		{[]string{"--scenario", "nothing"}, "boot and arrival"},
		{[]string{"--fixture", "status", "--scenario", "boot"}, "one at a time"},
		{[]string{"status"}, `unexpected "status"`},
	} {
		err := run(context.Background(), tt.args, strings.NewReader(""), &bytes.Buffer{}, size)
		if err == nil || !strings.Contains(err.Error(), tt.says) {
			t.Errorf("run(%q) = %v; want an error saying %q", tt.args, err, tt.says)
		}
	}
}
