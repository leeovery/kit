package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

func TestMachine(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state", "kit")

	name, err := config.ReadMachine(state)
	if err != nil || name != "" {
		t.Fatalf("ReadMachine() before any = %q, %v, want none", name, err)
	}
	for _, want := range []string{"laptop", "studio"} {
		if err := config.WriteMachine(state, want); err != nil {
			t.Fatalf("WriteMachine(%q) error = %v", want, err)
		}
		if got, err := config.ReadMachine(state); err != nil || got != want {
			t.Errorf("ReadMachine() = %q, %v, want %q", got, err, want)
		}
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "machine" {
		t.Errorf("the state directory holds %v (%v), want the machine file alone", entries, err)
	}
	if info, err := os.Stat(state); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the state directory: %v, %v, want it private", info, err)
	}
}

func TestReadMachineTrimsTheFile(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "machine"), []byte("  laptop\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := config.ReadMachine(state); err != nil || got != "laptop" {
		t.Errorf("ReadMachine() = %q, %v, want laptop", got, err)
	}
}
