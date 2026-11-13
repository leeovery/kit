package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/leeovery/kit/internal/whole"
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
	if err := whole.Synced(filepath.Join(stateDir, machineFile), []byte(name+"\n"), 0o600); err != nil {
		return fmt.Errorf("record this Mac's name: %w", err)
	}
	return nil
}
