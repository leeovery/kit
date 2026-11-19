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
// Mac's own paths section lists, then the shared declarations', then the
// system's, each once; a relative one (node_modules/.bin), which only a
// shell in a project folder finds anything in, is left out. home is what ~
// expands to. kit ignores the PATH it inherited, so it finds the same
// programs from a terminal, launchd or an agent.
func (c *Config) SearchPath(home, mac string) ([]string, error) {
	dirs, err := c.ShellPath(home, mac)
	if err != nil {
		return nil, err
	}
	return SearchPathOf(dirs), nil
}

// SearchPathOf is kit's own PATH from shellPath, as ShellPath gives it:
// its absolute directories, then the system's, each once.
func SearchPathOf(shellPath []string) []string {
	var out []string
	for _, dir := range slices.Concat(shellPath, systemPath) {
		if filepath.IsAbs(dir) && !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return out
}

// ShellPath is the PATH the shell puts ahead of the one it inherits, on the
// Mac named mac: every directory the paths sections list, the Mac's then
// the shared declarations', in order, each once, in the place it first
// appears, ~ expanded and relative ones kept. A Mac's line repeating a
// shared one raises it on that Mac.
func (c *Config) ShellPath(home, mac string) ([]string, error) {
	p, err := c.PathLists(home, mac)
	if err != nil {
		return nil, err
	}
	return p.Shell(), nil
}

// PathLists are the directories the paths sections list on one Mac, each
// section's in order, ~ expanded and relative ones kept: the Mac's own,
// and the shared declarations'.
type PathLists struct{ Own, Shared []string }

// PathLists reads the paths sections on the Mac named mac, home what ~
// expands to.
func (c *Config) PathLists(home, mac string) (PathLists, error) {
	own, err := c.paths(home, mac)
	if err != nil {
		return PathLists{}, err
	}
	shared, err := c.paths(home, Shared)
	if err != nil {
		return PathLists{}, err
	}
	return PathLists{Own: own, Shared: shared}, nil
}

// Shell is the shell's PATH p makes (ShellPath): the Mac's directories,
// then the shared ones, each once, in the place it first appears.
func (p PathLists) Shell() []string {
	var out []string
	for _, dir := range slices.Concat(p.Own, p.Shared) {
		if !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return out
}

// Adding is p with dirs declared last in the Mac's own paths section, as
// kit path add and setup's snapshot declare them.
func (p PathLists) Adding(dirs ...string) PathLists {
	return PathLists{Own: slices.Concat(p.Own, dirs), Shared: p.Shared}
}

// paths are the directories scope's paths section lists, in order, ~
// expanded.
func (c *Config) paths(home, scope string) ([]string, error) {
	entries, _, err := c.declared(PathsKind, scope)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if err := checkDir(e.Name); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Pos(), err)
		}
		dir := e.Name
		if rest, ok := strings.CutPrefix(dir, "~"); ok {
			dir = home + rest
		}
		out = append(out, filepath.Clean(dir))
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
