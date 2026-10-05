package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ToBeSorted heads the group a name goes in when no group is named, to be
// filed by hand later.
const ToBeSorted = "To be sorted"

// noteGap is what separates an entry from the note after it.
const noteGap = "   # "

// lineKind is what a line of a section is.
type lineKind int

const (
	blankLine lineKind = iota
	// headingLine is a comment that starts a group, after a blank line or at
	// the section's start, or carries on its heading.
	headingLine
	// noteLine is a comment directly after an entry or another note: a note
	// on the entry after it.
	noteLine
	entryLine
)

// body is a section's lines, each with what it is and, for an entry, its
// name as the line has it, for editing in place: what isn't edited is kept
// as it is.
type body struct {
	form  form
	lines []string
	kinds []lineKind
	names []string
}

func splitBody(f form, lines []string) body {
	b := body{form: f}
	prev := blankLine
	for _, line := range lines {
		kind, name := entryLine, ""
		switch trimmed := strings.TrimSpace(line); {
		case trimmed == "":
			kind = blankLine
		case strings.HasPrefix(trimmed, "#"):
			kind = headingLine
			if prev == entryLine || prev == noteLine {
				kind = noteLine
			}
		case f == commands:
			name, _, _, _ = splitCommand(line)
		default:
			name, _ = splitComment(line)
		}
		b.lines = append(b.lines, line)
		b.kinds = append(b.kinds, kind)
		b.names = append(b.names, name)
		prev = kind
	}
	return b
}

// group is a group's span of lines: from its heading to the next heading,
// or the section's end.
type group struct {
	heading    string
	start, end int
}

// groups are the section's groups, in order.
func (b body) groups() []group {
	var gs []group
	for i, kind := range b.kinds {
		if kind != headingLine || (i > 0 && b.kinds[i-1] != blankLine) {
			continue
		}
		if len(gs) > 0 {
			gs[len(gs)-1].end = i
		}
		heading := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(b.lines[i]), "#"))
		gs = append(gs, group{heading: heading, start: i, end: len(b.lines)})
	}
	return gs
}

func (b body) find(heading string) (group, bool) {
	for _, g := range b.groups() {
		if g.heading == heading {
			return g, true
		}
	}
	return group{}, false
}

// entries are the indexes of the entry lines in [from, to).
func (b body) entries(from, to int) []int {
	var es []int
	for i := from; i < to; i++ {
		if b.kinds[i] == entryLine {
			es = append(es, i)
		}
	}
	return es
}

// notesStart is where entry i's block starts: the first of the notes
// directly above it, or i.
func (b body) notesStart(i int) int {
	for i > 0 && b.kinds[i-1] == noteLine {
		i--
	}
	return i
}

// headingEnd is the first line after g's heading.
func (b body) headingEnd(g group) int {
	i := g.start
	for i < g.end && b.kinds[i] == headingLine {
		i++
	}
	return i
}

// lastEntryEnd is where a line goes after the last entry in [from, to), or
// at from when there's none.
func (b body) lastEntryEnd(from, to int) int {
	if es := b.entries(from, to); len(es) > 0 {
		return es[len(es)-1] + 1
	}
	return from
}

// sortedPlace is where name goes among the entries in [from, to): before
// the first, with its notes, that sorts after it, else after the last.
func (b body) sortedPlace(from, to int, name string) int {
	key := sortKey(name)
	for _, i := range b.entries(from, to) {
		if sortKey(b.names[i]) > key {
			return b.notesStart(i)
		}
	}
	return b.lastEntryEnd(from, to)
}

// groupAt is the group line i is in, if any: lines before the first heading
// are in none.
func (b body) groupAt(i int) (group, bool) {
	for _, g := range b.groups() {
		if g.start <= i && i < g.end {
			return g, true
		}
	}
	return group{}, false
}

func (b *body) insert(at int, lines ...string) {
	b.lines = slices.Insert(b.lines, at, lines...)
	*b = splitBody(b.form, b.lines)
}

// remove takes out lines [from, to), and a blank line left beside another,
// or at the section's start or end.
func (b *body) remove(from, to int) {
	lines := slices.Delete(slices.Clone(b.lines), from, to)
	*b = splitBody(b.form, lines)
	switch {
	case from < len(b.lines) && b.kinds[from] == blankLine && (from == 0 || b.kinds[from-1] == blankLine):
		lines = slices.Delete(lines, from, from+1)
	case from == len(b.lines) && from > 0 && b.kinds[from-1] == blankLine && from < len(lines):
		lines = slices.Delete(lines, from-1, from)
	}
	*b = splitBody(b.form, lines)
}

// sortKey is what a name sorts by in its group: what's before a version
// or a source after @ (php for php@8.5, revdiff for revdiff@umputun/revdiff),
// then its last part (bun for oven-sh/bun/bun), in any case.
func sortKey(name string) string {
	if i := strings.Index(name, "@"); i > 0 {
		name = name[:i]
	}
	return strings.ToLower(name[strings.LastIndex(name, "/")+1:])
}

// splitName splits name into the project folder whose section declares it,
// if its kind has those, and the name as its line has it.
func splitName(d sectionDef, name string) (folder, line string) {
	if d.folders && (strings.HasPrefix(name, "~/") || strings.HasPrefix(name, "/")) {
		if i := strings.LastIndex(name, ":"); i > 0 {
			return name[:i], name[i+1:]
		}
	}
	return "", name
}

// Groups are the headings of the groups in kind's section of the file named
// file, in order: none when there's no section.
func (c *Config) Groups(kind, file string) ([]string, error) {
	d, f, err := c.editable(kind, file)
	if err != nil {
		return nil, err
	}
	var headings []string
	if s := f.find(d, ""); s != nil {
		for _, g := range splitBody(d.form, s.body).groups() {
			headings = append(headings, g.heading)
		}
	}
	return headings, nil
}

// Declare declares e in kind's section of the file named file, as in
// shared or laptop, with its note after it. A name goes into the group
// headed group, in its sorted place, made when the section has none such;
// or, when group is "", at the end of "To be sorted", made at the section's
// bottom when it has none. A command's line, its name then e.Value, goes in
// its sorted place. The section is made, in its place among the file's,
// when there's none, and the file too. The rest of the file is kept as it
// is.
func (c *Config) Declare(kind, file string, e Entry, group string) error {
	d, f, err := c.editable(kind, file)
	if err != nil {
		return err
	}
	folder, name := splitName(d, e.Name)
	line, err := entryText(d, name, e)
	if err != nil {
		return err
	}
	s := f.find(d, folder)
	if s == nil {
		s = f.add(d, folder)
	}
	b := splitBody(d.form, s.body)
	if slices.Contains(b.names, name) {
		return fmt.Errorf("%s is in %s already", e.Name, file)
	}
	switch {
	case d.form != names:
		b.insert(b.sortedPlace(0, len(b.lines), name), line)
	default:
		if group == "" {
			group = ToBeSorted
		}
		g, ok := b.find(group)
		switch {
		case ok && group == ToBeSorted:
			b.insert(max(b.lastEntryEnd(g.start, g.end), b.headingEnd(g)), line)
		case ok:
			b.insert(max(b.sortedPlace(g.start, g.end, name), b.headingEnd(g)), line)
		default:
			at := b.lastEntryEnd(0, len(b.lines))
			if tbs, ok := b.find(ToBeSorted); ok && group != ToBeSorted {
				b.insert(tbs.start, "# "+group, line, "")
			} else {
				block := []string{"# " + group, line}
				if at > 0 {
					block = append([]string{""}, block...)
				}
				b.insert(at, block...)
			}
		}
	}
	s.body = b.lines
	return c.writeDecl(f, e.Name, true)
}

// Undeclare takes name out of kind's sections of the file named file, with
// the notes directly above it. A group it leaves empty loses its heading,
// and a section it leaves empty goes. The rest of the file is kept as it is.
func (c *Config) Undeclare(kind, file, name string) error {
	d, f, err := c.editable(kind, file)
	if err != nil {
		return err
	}
	folder, line := splitName(d, name)
	s := f.find(d, folder)
	var b body
	i := -1
	if s != nil {
		b = splitBody(d.form, s.body)
		i = slices.Index(b.names, line)
	}
	if i < 0 {
		return fmt.Errorf("%s isn't in %s", name, file)
	}
	owner, grouped := b.groupAt(i)
	b.remove(b.notesStart(i), i+1)
	if grouped {
		// The group starts where it did: only lines after its heading went.
		for _, g := range b.groups() {
			if g.start == owner.start && len(b.entries(g.start, g.end)) == 0 {
				b.remove(g.start, g.end)
				break
			}
		}
	}
	s.body = b.lines
	if len(b.entries(0, len(b.lines))) == 0 {
		f.drop(s)
	}
	return c.writeDecl(f, name, false)
}

// Replace rewrites the line declaring e.Name in kind's sections of the file
// named file, with e's value and note; the notes above it are kept.
func (c *Config) Replace(kind, file string, e Entry) error {
	d, f, err := c.editable(kind, file)
	if err != nil {
		return err
	}
	folder, name := splitName(d, e.Name)
	s := f.find(d, folder)
	if s == nil {
		return fmt.Errorf("%s isn't in %s", e.Name, file)
	}
	b := splitBody(d.form, s.body)
	i := slices.Index(b.names, name)
	if i < 0 {
		return fmt.Errorf("%s isn't in %s", e.Name, file)
	}
	line, err := entryText(d, name, e)
	if err != nil {
		return err
	}
	s.body[i] = line
	return c.writeDecl(f, e.Name, true)
}

// entryText is the line declaring name, as d's lines read: the name, a
// command's options, and the note.
func entryText(d sectionDef, name string, e Entry) (string, error) {
	line := name
	switch d.form {
	case names:
		if !lineName.MatchString(name) {
			return "", fmt.Errorf("%q isn't a name", name)
		}
	case commands:
		if !commandName.MatchString(name) {
			return "", fmt.Errorf("%q isn't a name", name)
		}
		if strings.TrimSpace(e.Value) == "" {
			return "", fmt.Errorf("%s needs its options", name)
		}
		line += " " + strings.TrimSpace(e.Value)
	case paths:
		return "", fmt.Errorf("kit doesn't declare paths: they're edited by hand")
	}
	if note := strings.TrimSpace(strings.ReplaceAll(e.Note, "\n", " ")); note != "" {
		line += noteGap + note
	}
	return line, nil
}

// add adds a section of d for folder, empty, in its place among the file's:
// after the sections before it in the order kit writes them, and a kind's
// folders' sections after its own, by folder.
func (f *declFile) add(d sectionDef, folder string) *section {
	order := func(s *section) (int, string) {
		return slices.IndexFunc(sectionDefs, func(x sectionDef) bool { return x.kind == s.def.kind }), s.folder
	}
	s := &section{def: d, folder: folder, header: "[" + strings.TrimSpace(d.header+" "+folder) + "]"}
	at := len(f.sections)
	for i, other := range f.sections {
		oi, of := order(other)
		si, sf := order(s)
		if oi > si || oi == si && of > sf {
			at = i
			break
		}
	}
	// A blank line before the header, and after the section when another
	// follows.
	if at > 0 {
		prev := f.sections[at-1]
		if n := len(prev.body); n == 0 || strings.TrimSpace(prev.body[n-1]) != "" {
			prev.body = append(prev.body, "")
		}
	} else if n := len(f.preamble); n > 0 && strings.TrimSpace(f.preamble[n-1]) != "" {
		f.preamble = append(f.preamble, "")
	}
	if at < len(f.sections) {
		s.body = []string{""}
	}
	f.sections = slices.Insert(f.sections, at, s)
	return s
}

// drop takes section s out of the file, and the blank line before it when
// it was the last.
func (f *declFile) drop(s *section) {
	i := slices.Index(f.sections, s)
	f.sections = slices.Delete(f.sections, i, i+1)
	if i < len(f.sections) {
		return
	}
	trim := func(lines []string) []string {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		return lines
	}
	if i > 0 {
		f.sections[i-1].body = trim(f.sections[i-1].body)
	} else {
		f.preamble = trim(f.preamble)
	}
}

// editable reads the declarations file named file for editing kind's
// sections: a file that doesn't read isn't edited.
func (c *Config) editable(kind, file string) (sectionDef, *declFile, error) {
	d, err := defFor(kind)
	if err != nil {
		return d, nil, err
	}
	if file != Shared && !c.Knows(file) {
		return d, nil, fmt.Errorf("%s is neither the shared file nor a Mac's (%s)", file, c.macList())
	}
	f, err := c.readDecl(file)
	if err != nil {
		return d, nil, fmt.Errorf("%s needs fixing before kit edits it: %w", file, err)
	}
	return d, f, nil
}

// writeDecl writes f, whole or not at all, having checked it reads back,
// with name declared in it, or not, as in says.
func (c *Config) writeDecl(f *declFile, name string, in bool) error {
	content := f.String()
	back, err := parseFile(f.name, content)
	if err != nil {
		return fmt.Errorf("the edit would leave %s unreadable: %w", f.name, err)
	}
	found := false
	for _, s := range back.sections {
		entries, _ := s.entries(f.name)
		found = found || slices.ContainsFunc(entries, func(e Entry) bool { return e.Name == name })
	}
	if found != in {
		return fmt.Errorf("the edit didn't leave %s as it should in %s", name, f.name)
	}
	return writeAtomic(filepath.Join(c.Dir, f.name), []byte(content))
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
