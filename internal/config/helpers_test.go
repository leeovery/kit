package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

// twoMacs is a kit.toml knowing two Macs.
const twoMacs = `format = 1
primary = "laptop"

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"
`

// writeRepo writes files, by name, into a new config repository, and
// returns its directory.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// loadRepo writes files into a new config repository, kit.toml knowing two
// Macs unless files give one, and loads it.
func loadRepo(t *testing.T, files map[string]string) *config.Config {
	t.Helper()
	if _, ok := files[config.File]; !ok {
		files[config.File] = twoMacs
	}
	cfg, err := config.Load(writeRepo(t, files))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cfg
}
