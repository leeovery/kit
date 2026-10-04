package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ToBeSorted heads the group a name goes in when no group is named, to be
// filed by hand later.
const ToBeSorted = "To be sorted"

// noteGap is what separates a name from the note after it.
const noteGap = "   # "

// lineKind is what a line of a list file is.
type lineKind int

const (
	blankLine lineKind = iota
	// headingLine is a comment that starts a group, after a blank line or at
	// the file's start, or carries on its heading.
	headingLine
	// noteLine is a comment directly after an entry or another note: a note
	// on the entry after it.
	noteLine
	entryLine
)

// listLines is a list file's lines, each with what it is, for editing in
// place: what isn't edited is kept as it is.
type listLines struct {
	lines []string
	kinds []lineKind
	names []string
}

func splitLines(content string) listLines {
	var l listLines
	if content == "" {
		return l
	}
	prev := blankLine
	for line := range strings.SplitSeq(strings.TrimSuffix(content, "\n"), "\n") {
		kind, name := entryLine, ""
		switch trimmed := strings.TrimSpace(line); {
		case trimmed == "":
			kind = blankLine
		case strings.HasPrefix(trimmed, "#"):
			kind = headingLine
			if prev == entryLine || prev == noteLine {
				kind = noteLine
			}
		default:
			name, _ = splitComment(line)
		}
		l.lines = append(l.lines, line)
		l.kinds = append(l.kinds, kind)
		l.names = append(l.names, name)
		prev = kind
	}
	return l
}

func (l listLines) String() string {
	if len(l.lines) == 0 {
		return ""
	}
	return strings.Join(l.lines, "\n") + "\n"
}

// group is a group's span of lines: from its heading to the next heading,
// or the file's end.
type group struct {
	heading    string
	start, end int
}

// groups are the file's groups, in order.
func (l listLines) groups() []group {
	var gs []group
	for i, kind := range l.kinds {
		if kind != headingLine || (i > 0 && l.kinds[i-1] != blankLine) {
			continue
		}
		if len(gs) > 0 {
			gs[len(gs)-1].end = i
		}
		heading := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l.lines[i]), "#"))
		gs = append(gs, group{heading: heading, start: i, end: len(l.lines)})
	}
	return gs
}

func (l listLines) find(heading string) (group, bool) {
	for _, g := range l.groups() {
		if g.heading == heading {
			return g, true
		}
	}
	return group{}, false
}

// entries are the indexes of g's entry lines.
func (l listLines) entries(g group) []int {
	var es []int
	for i := g.start; i < g.end; i++ {
		if l.kinds[i] == entryLine {
			es = append(es, i)
		}
	}
	return es
}

// notesStart is where entry i's block starts: the first of the notes
// directly above it, or i.
func (l listLines) notesStart(i int) int {
	for i > 0 && l.kinds[i-1] == noteLine {
		i--
	}
	return i
}

// headingEnd is the first line after g's heading.
func (l listLines) headingEnd(g group) int {
	i := g.start
	for i < g.end && l.kinds[i] == headingLine {
		i++
	}
	return i
}

func (l *listLines) insert(at int, lines ...string) {
	l.lines = slices.Insert(l.lines, at, lines...)
}

// sortKey is what a name sorts by in its group: its last part, as in bun
// for oven-sh/bun/bun, in any case.
func sortKey(name string) string {
	return strings.ToLower(name[strings.LastIndex(name, "/")+1:])
}

// Groups are the headings of the groups in the list file named file, in
// order: none when there's no file.
func (c *Config) Groups(file string) ([]string, error) {
	content, err := c.readFile(file)
	if err != nil {
		return nil, err
	}
	var headings []string
	for _, g := range splitLines(content).groups() {
		headings = append(headings, g.heading)
	}
	return headings, nil
}

// Declare adds name to the list file named file, as in brew or brew.laptop,
// with note after it: into the group headed group, in its sorted place, made
// when the file has none such; or, when group is "", at the end of "To be
// sorted", made at the bottom when the file has none. The file is made when
// there's none. The rest of the file is kept as it is.
func (c *Config) Declare(file, name, group, note string) error {
	if err := c.checkListFile(file); err != nil {
		return err
	}
	if !entryName.MatchString(name) {
		return fmt.Errorf("%q isn't a name", name)
	}
	content, err := c.readEditable(file)
	if err != nil {
		return err
	}
	l := splitLines(content)
	if slices.Contains(l.names, name) {
		return fmt.Errorf("%s is in %s already", name, file)
	}
	line := name
	if note = strings.TrimSpace(strings.ReplaceAll(note, "\n", " ")); note != "" {
		line += noteGap + note
	}
	if group == "" {
		group = ToBeSorted
	}
	g, ok := l.find(group)
	switch {
	case ok && group == ToBeSorted:
		l.insert(l.endOfEntries(g), line)
	case ok:
		l.insert(l.sortedPlace(g, name), line)
	default:
		at := len(l.lines)
		block := []string{"# " + group, line}
		if tbs, ok := l.find(ToBeSorted); ok && group != ToBeSorted {
			at = tbs.start
			block = append(block, "")
		} else if at > 0 && l.kinds[at-1] != blankLine {
			block = append([]string{""}, block...)
		}
		l.insert(at, block...)
	}
	return c.writeList(file, l, name)
}

// endOfEntries is where a name goes at the end of g: after its last entry,
// or under its heading when it has none.
func (l listLines) endOfEntries(g group) int {
	if es := l.entries(g); len(es) > 0 {
		return es[len(es)-1] + 1
	}
	return l.headingEnd(g)
}

// sortedPlace is where name goes in g: before the first entry, with its
// notes, that sorts after it, else at the end.
func (l listLines) sortedPlace(g group, name string) int {
	key := sortKey(name)
	for _, i := range l.entries(g) {
		if sortKey(l.names[i]) > key {
			return l.notesStart(i)
		}
	}
	return l.endOfEntries(g)
}

// Undeclare removes name from the list file named file, with the notes
// directly above it; a group it leaves empty loses its heading. The rest of
// the file is kept as it is.
func (c *Config) Undeclare(file, name string) error {
	if err := c.checkListFile(file); err != nil {
		return err
	}
	content, err := c.readEditable(file)
	if err != nil {
		return err
	}
	l := splitLines(content)
	i := slices.Index(l.names, name)
	if i < 0 {
		return fmt.Errorf("%s isn't in %s", name, file)
	}
	owner, grouped := l.groupAt(i)
	l.remove(l.notesStart(i), i+1)
	if grouped {
		// The group starts where it did: only lines after its heading went.
		for _, g := range l.groups() {
			if g.start == owner.start && len(l.entries(g)) == 0 {
				l.remove(g.start, g.end)
				break
			}
		}
	}
	return c.writeList(file, l, "")
}

// groupAt is the group line i is in, if any: lines before the first heading
// are in none.
func (l listLines) groupAt(i int) (group, bool) {
	for _, g := range l.groups() {
		if g.start <= i && i < g.end {
			return g, true
		}
	}
	return group{}, false
}

// remove takes out lines [from, to), and a blank line left beside another,
// or at the file's start or end.
func (l *listLines) remove(from, to int) {
	l.lines = slices.Delete(l.lines, from, to)
	*l = splitLines(l.String())
	switch {
	case from < len(l.lines) && l.kinds[from] == blankLine && (from == 0 || l.kinds[from-1] == blankLine):
		l.lines = slices.Delete(l.lines, from, from+1)
	case from == len(l.lines) && from > 0 && l.kinds[from-1] == blankLine:
		l.lines = l.lines[:from-1]
	}
	*l = splitLines(l.String())
}

// writeList writes l to the list file named file, whole or not at all,
// having checked it reads back as a list, with name in it once when it's
// given.
func (c *Config) writeList(file string, l listLines, name string) error {
	content := l.String()
	entries, err := parseList(file, strings.NewReader(content))
	if err != nil {
		return fmt.Errorf("the edit would leave %s unreadable: %w", file, err)
	}
	if name != "" && !slices.ContainsFunc(entries, func(e Entry) bool { return e.Name == name }) {
		return fmt.Errorf("the edit didn't put %s in %s", name, file)
	}
	return writeAtomic(filepath.Join(c.Dir, file), []byte(content))
}

// checkListFile checks file names a list file: a kind's, or a kind's for a
// Mac the config knows.
func (c *Config) checkListFile(file string) error {
	if strings.ContainsAny(file, "/\\") || file == "" || strings.HasPrefix(file, ".") {
		return fmt.Errorf("%q isn't a list file", file)
	}
	if _, mac, ok := strings.Cut(file, "."); ok && !c.Knows(mac) {
		return fmt.Errorf("%s: no Mac named %s in %s", file, mac, File)
	}
	return nil
}

// readEditable reads the list file named file for editing: a file that
// doesn't read as a list isn't edited.
func (c *Config) readEditable(file string) (string, error) {
	content, err := c.readFile(file)
	if err != nil {
		return "", err
	}
	if _, err := parseList(file, strings.NewReader(content)); err != nil {
		return "", fmt.Errorf("%s needs fixing before kit edits it: %w", file, err)
	}
	return content, nil
}

// readFile reads the file named name in the config: "" when there's none.
func (c *Config) readFile(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(c.Dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	return string(data), nil
}

// writeAtomic writes data to path whole or not at all: beside it, synced,
// then renamed into place, keeping the mode of what was there.
func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
