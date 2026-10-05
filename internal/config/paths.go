package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// systemPath are the system's own directories, which kit's PATH ends with
// whatever the config lists.
var systemPath = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

// SearchPath is kit's own PATH on the Mac named mac: the directories the
// shared declarations' paths section lists, then the Mac's own, then the
// system's, each once; a relative one (node_modules/.bin), which only a
// shell in a project folder finds anything in, is left out. home is what ~
// expands to. kit ignores the PATH it inherited, so it finds the same
// programs from a terminal, launchd or an agent.
func (c *Config) SearchPath(home, mac string) ([]string, error) {
	dirs, err := c.ShellPath(home, mac)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, dir := range slices.Concat(dirs, systemPath) {
		if filepath.IsAbs(dir) && !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return out, nil
}

// ShellPath is the PATH the shell puts ahead of the one it inherits, on the
// Mac named mac: every directory the paths sections list, the shared
// declarations' then the Mac's, in order, each once, ~ expanded and
// relative ones kept.
func (c *Config) ShellPath(home, mac string) ([]string, error) {
	var out []string
	for _, scope := range []string{Shared, mac} {
		entries, err := c.declared(PathsKind, scope)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if err := checkDir(e.Name); err != nil {
				return nil, fmt.Errorf("%s: %w", e.Pos(), err)
			}
			dir := e.Name
			if rest, ok := strings.CutPrefix(dir, "~"); ok {
				dir = home + rest
			}
			dir = filepath.Clean(dir)
			if !slices.Contains(out, dir) {
				out = append(out, dir)
			}
		}
	}
	return out, nil
}

// checkDir refuses what a paths section can't hold: ~ but as the start of
// the home folder, a colon (the PATH's separator), or a $ (the shell's
// expansion, which kit doesn't do).
func checkDir(dir string) error {
	switch {
	case strings.ContainsAny(dir, ":$"):
		return fmt.Errorf("%q can't hold a colon or a $", dir)
	case strings.HasPrefix(dir, "~") && dir != "~" && !strings.HasPrefix(dir, "~/"):
		return fmt.Errorf("%q: ~ only starts the home folder, as in ~/bin", dir)
	}
	return nil
}
