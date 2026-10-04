package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// machineFile holds this Mac's name, in the state directory.
const machineFile = "machine"

// ReadMachine reads this Mac's name from the state directory: "" when it has
// none yet.
func ReadMachine(stateDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, machineFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read this Mac's name: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteMachine records this Mac's name in the state directory, whole or not
// at all.
func WriteMachine(stateDir, name string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("make the state directory: %w", err)
	}
	tmp, err := os.CreateTemp(stateDir, machineFile+".*")
	if err != nil {
		return fmt.Errorf("record this Mac's name: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(name + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("record this Mac's name: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("record this Mac's name: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("record this Mac's name: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(stateDir, machineFile)); err != nil {
		return fmt.Errorf("record this Mac's name: %w", err)
	}
	return nil
}
