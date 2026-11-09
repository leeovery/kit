package restore

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
)

// Line is a [restore] line: a path in the home folder, from ~ and cleaned,
// as in ~/Code, a folder or a file; and what in it the restore leaves out,
// each relative to it, a path or a pattern as rsync takes one.
type Line struct {
	Path  string
	Skips []string
	// Pos is where its line is, as in laptop/declarations:12.
	Pos string
}

// skipOption is a line's option naming what in its path to leave out.
const skipOption = "--skip"

// Lines reads list's [restore] lines, as ParseLine does each. A path is
// restored once, so one that's another's, or in it, is refused, naming the
// line.
func Lines(list config.List) ([]Line, error) {
	var lines []Line
	for _, e := range list.Entries {
		l, err := ParseLine(e)
		if err != nil {
			return nil, err
		}
		for _, other := range lines {
			if within(l.Path, other.Path) || within(other.Path, l.Path) {
				return nil, fmt.Errorf("%s: %s and %s (%s) overlap: a path is restored by one line", l.Pos, l.Path, other.Path, other.Pos)
			}
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// ParseLine reads a [restore] line, as ReadLine does, saying where it is
// when it doesn't read.
func ParseLine(e config.Entry) (Line, error) {
	l, err := ReadLine(e.Name, e.Value)
	if err != nil {
		return l, fmt.Errorf("%s: %w", e.Pos(), err)
	}
	l.Pos = e.Pos()
	return l, nil
}

// ReadLine reads a [restore] line: its path, in the home folder, from ~;
// then its options, --skip and what in the path to leave out, relative to
// it, as often as wanted.
func ReadLine(name, options string) (Line, error) {
	p, err := homePath(name)
	if err != nil {
		return Line{}, err
	}
	l := Line{Path: p}
	words, err := config.Words(options)
	if err != nil {
		return l, err
	}
	for i := 0; i < len(words); i++ {
		if words[i] != skipOption {
			return l, fmt.Errorf("%q isn't one of a restore line's options: %s", words[i], skipOption)
		}
		if i+1 == len(words) || strings.HasPrefix(words[i+1], "--") {
			return l, errors.New("--skip needs what to leave out after it, relative to the line's path")
		}
		i++
		if err := checkSkip(words[i]); err != nil {
			return l, err
		}
		l.Skips = append(l.Skips, words[i])
	}
	return l, nil
}

// homePath is name, a path from ~, cleaned: an error when it isn't in the
// home folder.
func homePath(name string) (string, error) {
	rest, ok := strings.CutPrefix(name, "~/")
	cleaned := path.Clean(strings.TrimLeft(rest, "/"))
	if !ok || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%s isn't in the home folder: a line's path starts ~/, as in ~/Code", name)
	}
	return "~/" + cleaned, nil
}

// checkSkip says why skip can't name what in a line's path to leave out:
// nil when it can. It's relative to the path, never absolute, and never
// leads out of it.
func checkSkip(skip string) error {
	parts := strings.Split(strings.TrimSuffix(skip, "/"), "/")
	switch {
	case strings.HasPrefix(skip, "/"):
		return fmt.Errorf("--skip %s: what to leave out is relative to the line's path, never absolute", skip)
	case slices.Contains(parts, ".."):
		return fmt.Errorf("--skip %s: what to leave out is in the line's path, and .. leads out of it", skip)
	case slices.ContainsFunc(parts, func(p string) bool { return p == "" || p == "." }):
		return fmt.Errorf("--skip %q: what to leave out is a path in the line's, without empty or . parts, as in logs or cache/*", skip)
	}
	return nil
}

// within reports whether p, a path from ~, is dir or in it, case aside, as
// macOS's file system takes a path.
func within(p, dir string) bool {
	p, dir = strings.ToLower(p), strings.ToLower(dir)
	return p == dir || strings.HasPrefix(p, dir+"/")
}
