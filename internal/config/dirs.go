// Package config reads the config repository, which declares what each Mac
// should have, and keeps this Mac's name in kit's state.
package config

import (
	"errors"
	"fmt"
	"path/filepath"
)

// Dirs are where kit keeps what it reads and writes on a Mac.
type Dirs struct {
	// Config is the config repository.
	Config string
	// State is kit's state: this Mac's name, and later drift and decisions.
	State string
	// Logs holds a log of each run.
	Logs string
	// Data holds what kit keeps for itself that isn't state: the clone of
	// the repository apps' settings are saved to.
	Data string
}

// Locate finds kit's directories, from home and the environment getenv
// reads: the config repository at KIT_CONFIG, else XDG_CONFIG_HOME/kit, else
// ~/.config/kit; the state at XDG_STATE_HOME/kit, else ~/.local/state/kit;
// the logs at ~/Library/Logs/kit; its data at XDG_DATA_HOME/kit, else
// ~/.local/share/kit. A relative XDG directory is ignored, as the
// XDG spec says; a relative KIT_CONFIG is an error, as it would lead wherever
// kit happened to run.
func Locate(home string, getenv func(string) string) (Dirs, error) {
	if home == "" || !filepath.IsAbs(home) {
		return Dirs{}, errors.New("no home directory: HOME must be an absolute path")
	}
	configDir := filepath.Join(home, ".config", "kit")
	if dir := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		configDir = filepath.Join(dir, "kit")
	}
	if dir := getenv("KIT_CONFIG"); dir != "" {
		if !filepath.IsAbs(dir) {
			return Dirs{}, fmt.Errorf("KIT_CONFIG is %q: it must be an absolute path", dir)
		}
		configDir = filepath.Clean(dir)
	}
	stateDir := filepath.Join(home, ".local", "state", "kit")
	if dir := getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		stateDir = filepath.Join(dir, "kit")
	}
	dataDir := filepath.Join(home, ".local", "share", "kit")
	if dir := getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		dataDir = filepath.Join(dir, "kit")
	}
	return Dirs{
		Config: configDir,
		State:  stateDir,
		Logs:   filepath.Join(home, "Library", "Logs", "kit"),
		Data:   dataDir,
	}, nil
}
