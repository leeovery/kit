package testguard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestStubsStandInForEveryStubbedProgram(t *testing.T) {
	s, err := writeStubs(filepath.Join(t.TempDir(), "bin"), filepath.Join(t.TempDir(), "runs"))
	if err != nil {
		t.Fatalf("writeStubs() error = %v", err)
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0o100 == 0 {
			t.Errorf("stub %s mode %v, want it executable", e.Name(), info.Mode())
		}
		names = append(names, e.Name())
	}
	want := []string{"brew", "claude", "defaults", "gh", "git", "launchctl", "mas", "op", "open", "osascript", "sudo", "tmutil", "tmux"}
	if !slices.Equal(names, want) {
		t.Errorf("stubs = %q, want %q", names, want)
	}
}

func TestStubsNoteEachRunAndFail(t *testing.T) {
	// A record in a directory whose name the stubs' script must quote.
	dir := filepath.Join(t.TempDir(), "it's a record")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := writeStubs(filepath.Join(t.TempDir(), "bin"), filepath.Join(dir, "runs"))
	if err != nil {
		t.Fatalf("writeStubs() error = %v", err)
	}
	if runs := s.runs(); runs != nil {
		t.Errorf("before any ran, runs() = %q, want none", runs)
	}

	for _, run := range [][]string{
		{"brew", "list", "--formula"},
		{"osascript", "-e", `display notification "it's done" with title "kit"`},
		{"sudo"},
	} {
		err := exec.Command(filepath.Join(s.dir, run[0]), run[1:]...).Run()
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
			t.Errorf("stub %s: error = %v, want exit status 1", run[0], err)
		}
	}

	want := []string{
		"ran brew list --formula",
		`ran osascript -e display notification "it's done" with title "kit"`,
		"ran sudo",
	}
	if got := s.runs(); !slices.Equal(got, want) {
		t.Errorf("runs() = %q, want %q", got, want)
	}
}
