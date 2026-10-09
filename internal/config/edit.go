package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// noteGap is what separates an entry from the note after it.
const noteGap = "   # "

// lineKind is what a line of a section is.
type lineKind int

const (
	blankLine lineKind = iota
	// commentLine is a comment: those directly above an entry are its
	// notes, moving with it.
	commentLine
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
	for _, line := range lines {
		kind, name := entryLine, ""
		switch trimmed := strings.TrimSpace(line); {
		case trimmed == "":
			kind = blankLine
		case strings.HasPrefix(trimmed, "#"):
			kind = commentLine
		case f == commands || f == settings || f == secrets:
			name, _, _, _ = splitCommand(line)
		case f == defaults:
			name = defaultsName(line)
		case f == power:
			name = powerName(line)
		default:
			name, _ = splitComment(line)
		}
		b.lines = append(b.lines, line)
		b.kinds = append(b.kinds, kind)
		b.names = append(b.names, name)
	}
	return b
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

// notesStart is where entry i's block starts: the first of the comments
// directly above it, or i.
func (b body) notesStart(i int) int {
	for i > 0 && b.kinds[i-1] == commentLine {
		i--
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
	for _, i := range b.entries(from, to) {
		if compareNames(b.form, b.names[i], name) > 0 {
			return b.notesStart(i)
		}
	}
	return b.lastEntryEnd(from, to)
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

// sortedLines are a sorted section's lines as kit keeps them, with added,
// entry lines, among them: the comments opening the section, before a blank
// line; each entry in order, the comments above it with it; any comments
// after the last; then the blank lines ending the section, as they were. A
// comment apart from any entry is a note on the entry after it, and the
// blank lines between entries go.
func sortedLines(f form, lines []string, added ...string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	b := splitBody(f, lines[:end])
	es := b.entries(0, end)
	start := end
	if len(es) > 0 {
		start = b.notesStart(es[0])
	}
	type block struct {
		name  string
		lines []string
	}
	var blocks []block
	var notes []string
	for i := start; i < end; i++ {
		switch b.kinds[i] {
		case commentLine:
			notes = append(notes, b.lines[i])
		case entryLine:
			blocks = append(blocks, block{b.names[i], append(notes, b.lines[i])})
			notes = nil
		}
	}
	for _, line := range added {
		blocks = append(blocks, block{splitBody(f, []string{line}).names[0], []string{line}})
	}
	slices.SortStableFunc(blocks, func(x, y block) int { return compareNames(f, x.name, y.name) })
	var out []string
	if lead := trimBlank(lines[:start]); len(lead) > 0 {
		out = append(slices.Clone(lead), "")
	}
	for _, bl := range blocks {
		out = append(out, bl.lines...)
	}
	out = append(out, notes...)
	return append(out, lines[end:]...)
}

// trimBlank is lines without the blank lines at their start and end.
func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// compareNames orders two names of a section as kit places them: a package
// by its name (packageName), a macOS setting kept for this Mac's host among
// its domain's, anything else as written; case aside and numbers by their
// value (28 before 184); then as written.
func compareNames(f form, a, b string) int {
	ka, kb := a, b
	switch f {
	case names:
		ka, kb = packageName(a), packageName(b)
	case defaults:
		ka, kb = strings.TrimPrefix(a, currentHostName), strings.TrimPrefix(b, currentHostName)
	}
	return cmp.Or(natural(ka, kb), natural(a, b), strings.Compare(a, b))
}

// natural compares a and b case aside, a run of digits by its value.
func natural(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), strings.Compare(na, nb)); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

// digits is how many digits s starts with.
func digits(s string) int {
	n := 0
	for n < len(s) && '0' <= s[n] && s[n] <= '9' {
		n++
	}
	return n
}

// packageName is what a package sorts by: what's before a version or a
// source after @ (php for php@8.5, revdiff for revdiff@umputun/revdiff),
// then its last part (bun for oven-sh/bun/bun).
func packageName(name string) string {
	if i := strings.Index(name, "@"); i > 0 {
		name = name[:i]
	}
	return name[strings.LastIndex(name, "/")+1:]
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

// holding is the section of d declaring name, and the name as its line has
// it: in a kind with sections for items, whichever item's section has it.
func (f *declFile) holding(d sectionDef, name string) (*section, string) {
	folder, line := splitName(d, name)
	for _, s := range f.sections {
		if s.def.kind == d.kind && (d.items || s.arg == folder) && slices.Contains(splitBody(d.form, s.body).names, line) {
			return s, line
		}
	}
	return nil, line
}

// Items are the 1Password items kind's sections in scope's declarations are
// for, in order, as in op://vault/item: none for its plain section.
func (c *Config) Items(kind, scope string) ([]string, error) {
	d, f, err := c.editable(kind, scope)
	if err != nil {
		return nil, err
	}
	var items []string
	for _, s := range f.sections {
		if s.def.kind == d.kind && d.items && s.arg != "" {
			items = append(items, s.arg)
		}
	}
	return items, nil
}

// Declare declares e in kind's section of scope's declarations, shared or a
// Mac's, as in laptop, with its note after it: in a sorted section, which
// stays sorted (sortedLines); in another, a name in its sorted place, and a
// path last, as paths are searched in order. The section is made, in its
// place among the file's, when there's none, and the file and its folder
// too; a kind with sections for items declares it in e.Item's. The rest of
// the file is kept as it is.
func (c *Config) Declare(kind, scope string, e Entry) error {
	d, f, err := c.editable(kind, scope)
	if err != nil {
		return err
	}
	arg, name := splitName(d, e.Name)
	if d.items {
		arg = strings.TrimSuffix(e.Item, "/")
		if arg != "" && !ItemRef(arg) {
			return fmt.Errorf("%q isn't a 1Password item's reference, as in op://vault/item", e.Item)
		}
	}
	line, err := entryText(d, name, e)
	if err != nil {
		return err
	}
	if s, _ := f.holding(d, e.Name); s != nil {
		return fmt.Errorf("%s is in %s already", e.Name, DeclFile(scope))
	}
	s := f.find(d, arg)
	if s == nil {
		s = f.add(d, arg)
	}
	switch b := splitBody(d.form, s.body); {
	case d.sorted:
		s.body = sortedLines(d.form, s.body, line)
	case d.form == paths:
		b.insert(b.lastEntryEnd(0, len(b.lines)), line)
		s.body = b.lines
	default:
		b.insert(b.sortedPlace(0, len(b.lines), name), line)
		s.body = b.lines
	}
	return c.writeDecl(f, d, e.Name, true)
}

// Undeclare takes name out of kind's sections of scope's declarations, with
// the notes directly above it, keeping a sorted section sorted; a section it
// leaves empty goes. The rest of the file is kept as it is.
func (c *Config) Undeclare(kind, scope, name string) error {
	d, f, err := c.editable(kind, scope)
	if err != nil {
		return err
	}
	s, line := f.holding(d, name)
	var b body
	i := -1
	if s != nil {
		if d.sorted {
			s.body = sortedLines(d.form, s.body)
		}
		b = splitBody(d.form, s.body)
		i = slices.Index(b.names, line)
	}
	if i < 0 {
		return fmt.Errorf("%s isn't in %s", name, DeclFile(scope))
	}
	b.remove(b.notesStart(i), i+1)
	s.body = b.lines
	if len(b.entries(0, len(b.lines))) == 0 {
		f.drop(s)
	}
	return c.writeDecl(f, d, name, false)
}

// Replace rewrites the line declaring e.Name in kind's sections of scope's
// declarations, with e's value and note; the notes above it are kept, and a
// sorted section sorted.
func (c *Config) Replace(kind, scope string, e Entry) error {
	d, f, err := c.editable(kind, scope)
	if err != nil {
		return err
	}
	s, name := f.holding(d, e.Name)
	if s == nil {
		return fmt.Errorf("%s isn't in %s", e.Name, DeclFile(scope))
	}
	b := splitBody(d.form, s.body)
	i := slices.Index(b.names, name)
	if i < 0 {
		return fmt.Errorf("%s isn't in %s", e.Name, DeclFile(scope))
	}
	line, err := entryText(d, name, e)
	if err != nil {
		return err
	}
	s.body[i] = line
	if d.sorted {
		s.body = sortedLines(d.form, s.body)
	}
	return c.writeDecl(f, d, e.Name, true)
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
	case commands, settings, secrets:
		if d.form == commands && !commandName.MatchString(name) || d.form == settings && !settingKey.MatchString(name) || d.form == secrets && !secretName.MatchString(name) {
			return "", fmt.Errorf("%q isn't a name", name)
		}
		if strings.TrimSpace(e.Value) == "" {
			return "", fmt.Errorf("%s needs its %s", name, map[form]string{commands: "options", settings: "value", secrets: "1Password reference"}[d.form])
		}
		line += " " + strings.TrimSpace(e.Value)
	case paths:
		if err := checkDir(name); err != nil {
			return "", err
		}
	case defaults:
		text, err := defaultsText(name, e.Value)
		if err != nil {
			return "", err
		}
		line = text
	case power:
		text, err := powerText(name, e.Value)
		if err != nil {
			return "", err
		}
		line = text
	}
	if note := strings.TrimSpace(strings.ReplaceAll(e.Note, "\n", " ")); note != "" {
		line += noteGap + note
	}
	return line, nil
}

// add adds a section of d for arg (a project folder, or an item), empty, in
// its place among the file's: after the sections before it in the order kit
// writes them, and a kind's folders' or items' sections after its own, by
// folder or item.
func (f *declFile) add(d sectionDef, arg string) *section {
	order := func(s *section) (int, string) {
		return slices.IndexFunc(sectionDefs, func(x sectionDef) bool { return x.kind == s.def.kind }), s.arg
	}
	s := &section{def: d, arg: arg, header: "[" + strings.TrimSpace(d.header+" "+arg) + "]"}
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

// editable reads scope's declarations file for editing kind's sections: a
// file that doesn't read isn't edited.
func (c *Config) editable(kind, scope string) (sectionDef, *declFile, error) {
	d, err := defFor(kind)
	if err != nil {
		return d, nil, err
	}
	if scope != Shared && !c.Knows(scope) {
		return d, nil, fmt.Errorf("%s is neither shared nor a Mac's (%s)", scope, c.macList())
	}
	f, err := c.readDecl(scope)
	if err != nil {
		return d, nil, fmt.Errorf("%s needs fixing before kit edits it: %w", DeclFile(scope), err)
	}
	return d, f, nil
}

// writeDecl writes f, whole or not at all, having checked it reads back,
// with name declared in d's sections, or not, as in says: a name another
// kind's section has too, as a step can share a formula's, is that kind's.
func (c *Config) writeDecl(f *declFile, d sectionDef, name string, in bool) error {
	content := f.String()
	file := DeclFile(f.scope)
	back, err := parseFile(f.scope, content)
	if err != nil {
		return fmt.Errorf("the edit would leave %s unreadable: %w", file, err)
	}
	found := false
	for _, s := range back.sections {
		if s.def.kind != d.kind {
			continue
		}
		entries, _ := s.entries(f.scope)
		found = found || slices.ContainsFunc(entries, func(e Entry) bool { return e.Name == name })
	}
	if found != in {
		return fmt.Errorf("the edit didn't leave %s as it should in %s", name, file)
	}
	path := filepath.Join(c.Dir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("make %s's folder: %w", file, err)
	}
	return writeAtomic(path, []byte(content))
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
