package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// First is what a new config repository's first files say: its one Mac, by
// name, and what it is; the kit that made it, as the oldest that reads it,
// when it's a release; and the terminal a new Mac's boot hands over to,
// none when "".
type First struct {
	Mac, Description, Kit, Terminal string
}

// Start writes a new config repository's first files in dir, where it has
// no kit.toml yet: kit.toml, its format, the Mac as the primary, and the
// Mac's table; and the Mac's folder, with its declarations file, empty. It
// refuses a name that can't be a Mac's, and a dir that holds a kit.toml.
func Start(dir string, f First) error {
	switch {
	case !ValidName(f.Mac):
		return fmt.Errorf("%q can't be a Mac's name: use lower-case letters, digits and hyphens", f.Mac)
	case f.Mac == Shared:
		return fmt.Errorf("a Mac can't be called %s: that's the file every Mac reads", Shared)
	}
	path := filepath.Join(dir, File)
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s has a %s already", dir, File)
	}
	settings := struct {
		Format     int    `toml:"format"`
		MinimumKit string `toml:"minimum_kit,omitempty"`
		Primary    string `toml:"primary"`
		Terminal   string `toml:"terminal,omitempty"`
	}{Format: Format, Primary: f.Mac, Terminal: f.Terminal}
	if _, ok := parseVersion(f.Kit); ok {
		settings.MinimumKit = f.Kit
	}
	var text bytes.Buffer
	enc := toml.NewEncoder(&text)
	if err := enc.Encode(settings); err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	text.WriteString("\n[macs." + f.Mac + "]\n")
	if err := enc.Encode(macFile{Description: f.Description}); err != nil {
		return fmt.Errorf("write %s's description: %w", f.Mac, err)
	}
	if _, err := read(dir, text.String(), nil); err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, f.Mac), 0o755); err != nil {
		return err
	}
	decl := filepath.Join(dir, DeclFile(f.Mac))
	if _, err := os.Lstat(decl); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(decl, nil, 0o644); err != nil {
			return err
		}
	}
	return writeAtomic(path, text.Bytes())
}
