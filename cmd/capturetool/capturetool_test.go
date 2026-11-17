package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func size() (int, int) { return 120, 34 }

// A fixture prints in truecolor, whatever the terminal says it shows, at
// the terminal's width: the status still is pretty-colour.golden's screen.
func TestFixture(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "internal", "render", "testdata", "pretty-colour.golden"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--fixture", "status"}, strings.NewReader(""), &out, size); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want) {
		t.Errorf("printed\n%s\nwant\n%s", out.String(), want)
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
