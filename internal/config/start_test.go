package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kinds"
)

// A config starting on a Mac holds kit.toml and the Mac's own declarations
// file, empty, and no shared folder: that waits till something's declared
// for every Mac. kit's PATH has Homebrew's folders all the same. A Mac's
// declarations file there already is kept as it is.
func TestAConfigStartsWithTheMacsOwnFileAlone(t *testing.T) {
	for _, tt := range []struct {
		name, there, want string
	}{
		{name: "new"},
		{name: "the Mac's file there already", there: "[paths]\n~/bin\n", want: "[paths]\n~/bin\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.there != "" {
				if err := os.MkdirAll(filepath.Join(dir, "laptop"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, config.DeclFile("laptop")), []byte(tt.there), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := config.Start(dir, config.First{Mac: "laptop", Kit: "0.1.0"}); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, config.DeclFile("laptop"))); err != nil || string(got) != tt.want {
				t.Errorf("laptop/declarations = %q, %v; want %q", got, err, tt.want)
			}
			if _, err := os.Lstat(filepath.Join(dir, config.Shared)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the shared folder: %v; want none", err)
			}
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			path, err := cfg.SearchPath("/Users/someone", "laptop")
			if err != nil || !slices.Contains(path, "/opt/homebrew/bin") || !slices.Contains(path, "/opt/homebrew/sbin") {
				t.Errorf("SearchPath() = %q, %v; want Homebrew's folders on it", path, err)
			}
		})
	}
}

// The first line declared for every Mac makes the shared folder, and its
// declarations file, holding the line in its section.
func TestTheFirstSharedLineMakesTheSharedFolder(t *testing.T) {
	dir := t.TempDir()
	if err := config.Start(dir, config.First{Mac: "laptop"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir, kinds.Sections())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Declare(config.PathsKind, config.Shared, config.Entry{Name: "~/bin", Note: "mine"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, config.DeclFile(config.Shared))); err != nil || string(got) != "[paths]\n~/bin   # mine\n" {
		t.Errorf("shared/declarations = %q, %v; want the line in its section", got, err)
	}
}
