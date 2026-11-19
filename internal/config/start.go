package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
)

// First is what a new config repository's first files say: its one Mac, by
// name, and what it is; the kit that made it, as the oldest that reads it,
// when it's a release; the terminal a new Mac's boot hands over to, none
// when ""; and Homebrew's folders, put on every Mac's PATH, HomebrewPaths
// but in tests.
type First struct {
	Mac, Description, Kit, Terminal string
	Homebrew                        []string
}

// HomebrewPaths are Homebrew's folders on a Mac: kit's own PATH is [paths]
// then the system's, so without them in [paths] kit's runs find no brew.
var HomebrewPaths = []string{"/opt/homebrew/bin", "/opt/homebrew/sbin"}

// Start writes a new config repository's first files in dir, where it has
// no kit.toml yet: kit.toml, its format, the Mac as the primary, and the
// Mac's table; the Mac's folder, with its declarations file, empty; and the
// shared declarations, Homebrew's folders on every Mac's PATH (sharedStart).
// A declarations file there already is left as it is. It refuses a name
// that can't be a Mac's, and a dir that holds a kit.toml.
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
	shared, err := sharedStart(f.Homebrew)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, Shared), 0o755); err != nil {
		return err
	}
	for file, content := range map[string][]byte{DeclFile(f.Mac): nil, DeclFile(Shared): shared} {
		if err := writeNew(filepath.Join(dir, file), content); err != nil {
			return err
		}
	}
	return writeAtomic(path, text.Bytes())
}

// sharedStart is a new config's shared declarations: homebrew, Homebrew's
// folders, in [paths], as kit path add --shared --note Homebrew declares
// them; nothing without them.
func sharedStart(homebrew []string) ([]byte, error) {
	if len(homebrew) == 0 {
		return nil, nil
	}
	d := ownLeading[slices.IndexFunc(ownLeading, func(d sectionDef) bool { return d.kind == PathsKind })]
	text := bytes.NewBufferString("[" + d.header + "]\n")
	for _, dir := range homebrew {
		line, err := entryText(d, dir, Entry{Name: dir, Note: "Homebrew"})
		if err != nil {
			return nil, err
		}
		text.WriteString(line + "\n")
	}
	return text.Bytes(), nil
}

// writeNew writes content to path, unless a file's there already.
func writeNew(path string, content []byte) error {
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
