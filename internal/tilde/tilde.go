// Package tilde writes a path in the home folder from ~, as the config and
// what kit says have it, and reads one back.
package tilde

import (
	"path/filepath"
	"strings"
)

// Expand is path in full: one starting ~/ in home, else path as it is.
func Expand(home, path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

// Shown is path as kit shows it: from ~ when it's in home, else as it is.
func Shown(home, path string) string {
	if rest, ok := strings.CutPrefix(path, home+"/"); ok && home != "" {
		return "~/" + rest
	}
	return path
}
