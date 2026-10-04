package testguard

import (
	"path/filepath"
	"strings"
)

// resolve returns path with its symlinks resolved as far as it exists, and
// the rest as it is: where it leads, or would once made. The rest is never
// cleaned before what leads to it is resolved, as a .. after a link leaves
// where the link leads, not where the link is.
func resolve(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	dir, name := filepath.Split(path)
	parent := strings.TrimSuffix(dir, string(filepath.Separator))
	if parent == "" {
		return path
	}
	return filepath.Join(resolve(parent), name)
}

// within reports whether path is dir or somewhere under it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}
