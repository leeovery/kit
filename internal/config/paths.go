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
// shared file's paths section lists, then the Mac's own, then the system's,
// each once. home is what ~ expands to. kit ignores the PATH it inherited,
// so it finds the same programs from a terminal, launchd or an agent.
func (c *Config) SearchPath(home, mac string) ([]string, error) {
	var path []string
	for _, file := range []string{Shared, mac} {
		entries, err := c.declared(PathsKind, file)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			dir := e.Name
			if rest, ok := strings.CutPrefix(dir, "~"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
				dir = home + rest
			}
			if !filepath.IsAbs(dir) {
				return nil, fmt.Errorf("%s:%d: %q isn't an absolute directory (~ may start one)", e.File, e.Line, e.Name)
			}
			path = append(path, filepath.Clean(dir))
		}
	}
	var out []string
	for _, dir := range slices.Concat(path, systemPath) {
		if !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return out, nil
}
