package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// List is what one kind declares for one Mac: the shared file's entries, then
// the Mac's own file's.
type List struct {
	Kind    string
	Entries []Entry
}

// Names are the list's names, in order.
func (l List) Names() []string {
	names := make([]string, len(l.Entries))
	for i, e := range l.Entries {
		names[i] = e.Name
	}
	return names
}

// Entry is a name a list declares, and where.
type Entry struct {
	Name string
	// File is the file that declares it, as in brew or brew.laptop.
	File string
	// Line is its line in that file.
	Line int
	// Group is the comment heading the group it's in, if any.
	Group string
	// Note is the comment after it, saying why it's there, if any.
	Note string
}

// entryName is what a name in a list may be: no spaces, and nothing a
// package's name never holds, so a typo shows as an error.
var entryName = regexp.MustCompile(`^[A-Za-z0-9@._+/-]+$`)

// List reads what kind declares for the Mac named mac: its shared file, named
// after the kind, then the Mac's, as in brew then brew.laptop. Either may be
// missing. A name may be in one file, once; and a file for a Mac the config
// doesn't know is an error, so a misnamed one is never silently ignored.
func (c *Config) List(kind, mac string) (List, error) {
	if !c.Knows(mac) {
		return List{}, fmt.Errorf("no Mac named %s in %s: one of %s", mac, File, c.macList())
	}
	if err := c.checkMacFiles(kind); err != nil {
		return List{}, err
	}
	shared, err := readList(c.Dir, kind)
	if err != nil {
		return List{}, err
	}
	own, err := readList(c.Dir, kind+"."+mac)
	if err != nil {
		return List{}, err
	}
	where := make(map[string]Entry, len(shared))
	for _, e := range shared {
		where[e.Name] = e
	}
	for _, e := range own {
		if s, ok := where[e.Name]; ok {
			return List{}, fmt.Errorf("%s:%d: %s is in %s too (line %d): a name goes in the shared file or a Mac's, not both", e.File, e.Line, e.Name, s.File, s.Line)
		}
	}
	return List{Kind: kind, Entries: append(shared, own...)}, nil
}

// checkMacFiles finds kind's files for a Mac the config doesn't know.
func (c *Config) checkMacFiles(kind string) error {
	matches, err := filepath.Glob(filepath.Join(c.Dir, kind+".*"))
	if err != nil {
		return fmt.Errorf("look for %s's files: %w", kind, err)
	}
	for _, m := range matches {
		name := filepath.Base(m)
		if mac := strings.TrimPrefix(name, kind+"."); !c.Knows(mac) {
			return fmt.Errorf("%s: no Mac named %s in %s (one of %s): rename or remove the file", name, mac, File, c.macList())
		}
	}
	return nil
}

// readList reads the list in the file named name in dir: nothing, when there
// isn't one.
func readList(dir, name string) ([]Entry, error) {
	f, err := os.Open(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	return parseList(name, f)
}

// parseList reads the list file named name from r.
func parseList(name string, r io.Reader) ([]Entry, error) {
	var entries []Entry
	seen := make(map[string]int)
	group := ""
	scanner := bufio.NewScanner(r)
	for n := 1; scanner.Scan(); n++ {
		text, note := splitComment(scanner.Text())
		if text == "" {
			if heading, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "#"); ok {
				group = strings.TrimSpace(heading)
			}
			continue
		}
		if !entryName.MatchString(text) {
			return nil, fmt.Errorf("%s:%d: %q isn't a name: one name a line, a comment after a space and #", name, n, text)
		}
		if first, ok := seen[text]; ok {
			return nil, fmt.Errorf("%s:%d: %s is already at line %d", name, n, text, first)
		}
		seen[text] = n
		entries = append(entries, Entry{Name: text, File: name, Line: n, Group: group, Note: note})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return entries, nil
}

// splitComment splits a line into what's before its comment and the comment:
// a # at the line's start, or after a space or tab. Both are trimmed.
func splitComment(line string) (text, comment string) {
	for i, r := range line {
		if r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		}
	}
	return strings.TrimSpace(line), ""
}

// Where are the entries for name in kind's files: the shared file's, then
// each Mac's, by the Macs' names. A name a file doesn't declare isn't in it.
func (c *Config) Where(kind, name string) ([]Entry, error) {
	var found []Entry
	for _, file := range c.ListFiles(kind) {
		entries, err := readList(c.Dir, file)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Name == name {
				found = append(found, e)
			}
		}
	}
	return found, nil
}

// ListFiles are kind's list files: the shared one, then each Mac's, by the
// Macs' names, whether or not they're there.
func (c *Config) ListFiles(kind string) []string {
	files := []string{kind}
	for _, mac := range c.MacNames() {
		files = append(files, kind+"."+mac)
	}
	return files
}
