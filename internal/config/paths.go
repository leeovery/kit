package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// PathsFile lists the directories kit puts on its PATH, in order.
const PathsFile = "paths"

// systemPath are the system's own directories, which kit's PATH ends with
// whatever the config lists.
var systemPath = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

// SearchPath is kit's own PATH: the directories the config's paths file
// lists, in order, then the system's, each once. home is what ~ expands to.
// kit ignores the PATH it inherited, so it finds the same programs from a
// terminal, launchd or an agent.
func (c *Config) SearchPath(home string) ([]string, error) {
	listed, err := c.paths(home)
	if err != nil {
		return nil, err
	}
	var path []string
	for _, dir := range slices.Concat(listed, systemPath) {
		if !slices.Contains(path, dir) {
			path = append(path, dir)
		}
	}
	return path, nil
}

// paths reads the paths file: none, when there isn't one.
func (c *Config) paths(home string) ([]string, error) {
	f, err := os.Open(filepath.Join(c.Dir, PathsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", PathsFile, err)
	}
	defer func() { _ = f.Close() }()

	var dirs []string
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		text, _ := splitComment(scanner.Text())
		if text == "" {
			continue
		}
		dir := text
		if rest, ok := strings.CutPrefix(text, "~"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			dir = home + rest
		}
		if !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("%s:%d: %q isn't an absolute directory (~ may start one)", PathsFile, n, text)
		}
		dirs = append(dirs, filepath.Clean(dir))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", PathsFile, err)
	}
	return dirs, nil
}
