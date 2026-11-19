package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kinds"
)

// A config starting on a Mac puts Homebrew's folders on every Mac's PATH,
// as kit path add --shared --note Homebrew declares them, as kit's PATH is
// [paths] then the system's alone: without them its runs find no brew. A
// declarations file there already is kept as it is.
func TestAConfigStartsWithHomebrewOnThePath(t *testing.T) {
	const homebrew = "[paths]\n/opt/homebrew/bin   # Homebrew\n/opt/homebrew/sbin   # Homebrew\n"
	for _, tt := range []struct {
		name, there, want string
	}{
		{name: "new", want: homebrew},
		{name: "shared there already", there: "[paths]\n~/bin\n", want: "[paths]\n~/bin\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.there != "" {
				if err := os.MkdirAll(filepath.Join(dir, config.Shared), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, config.DeclFile(config.Shared)), []byte(tt.there), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := config.Start(dir, config.First{Mac: "laptop", Kit: "0.1.0", Homebrew: config.HomebrewPaths}); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, config.DeclFile(config.Shared))); err != nil || string(got) != tt.want {
				t.Errorf("shared/declarations = %q, %v; want %q", got, err, tt.want)
			}
			if got, err := os.ReadFile(filepath.Join(dir, config.DeclFile("laptop"))); err != nil || len(got) != 0 {
				t.Errorf("laptop/declarations = %q, %v; want it empty", got, err)
			}
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			path, err := cfg.SearchPath("/Users/someone", "laptop")
			if want := "/opt/homebrew/bin"; err != nil || tt.there == "" && !slices.Contains(path, want) {
				t.Errorf("SearchPath() = %q, %v; want it holding %s", path, err, want)
			}
		})
	}
}
