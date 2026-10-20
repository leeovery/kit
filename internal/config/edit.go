package config

import (
	"cmp"
	"errors"
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
	// stepLine is a step of the entry above it, indented under it, as a
	// step by hand's are: they move with it.
	stepLine
)

// body is a section's lines, each with what it is and, for an entry, its
// name as the line has it, for editing in place: what isn't edited is kept
// as it is.
type body struct {
	form  Form
	lines []string
	kinds []lineKind
	names []string
}

func splitBody(f Form, lines []string) body {
	b := body{form: f}
	for _, line := range lines {
		kind, name := entryLine, ""
		switch trimmed := strings.TrimSpace(line); {
		case trimmed == "":
			kind = blankLine
		case strings.HasPrefix(trimmed, "#"):
			kind = commentLine
		case line[0] == ' ' || line[0] == '\t':
			kind = stepLine
		case f == Commands || f == Settings || f == Secrets || f == Options:
			name, _, _, _ = splitCommand(line)
		case f == Defaults:
			name = defaultsName(line)
		case f == Power:
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

// notes are entry i's notes, the comments directly above it, as one
// paragraph (noteWords).
func (b body) notes(i int) string {
	return noteWords(b.lines[b.notesStart(i):i])
}

// noteWords are comments as one paragraph: each without its # and the
// space after that.
func noteWords(comments []string) string {
	var words []string
	for _, line := range comments {
		line = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(line), "#"), " ")
		if line != "" {
			words = append(words, line)
		}
	}
	return strings.Join(words, " ")
}

// end is where entry i's block ends: after the steps indented under it.
func (b body) end(i int) int {
	i++
	for i < len(b.kinds) && b.kinds[i] == stepLine {
		i++
	}
	return i
}

// lastEntryEnd is where a line goes after the last entry in [from, to), and
// its steps, or at from when there's none.
func (b body) lastEntryEnd(from, to int) int {
	if es := b.entries(from, to); len(es) > 0 {
		return b.end(es[len(es)-1])
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
// entry lines, each with the comments above it, among them: the comments
// opening the section, before a blank line; each entry in order, the
// comments above it with it; any comments after the last; then the blank
// lines ending the section, as they were. A comment apart from any entry is
// a note on the entry after it, and the blank lines between entries go.
func sortedLines(f Form, lines []string, added ...string) []string {
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
	var addedNotes []string
	for _, line := range added {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			addedNotes = append(addedNotes, line)
			continue
		}
		blocks = append(blocks, block{splitBody(f, []string{line}).names[0], append(addedNotes, line)})
		addedNotes = nil
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
func compareNames(f Form, a, b string) int {
	ka, kb := a, b
	switch f {
	case Names:
		ka, kb = packageName(a), packageName(b)
	case Defaults:
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
		if s.def.kind == d.kind && (d.items != nil || s.arg == folder) && slices.Contains(splitBody(d.form, s.body).names, line) {
			return s, line
		}
	}
	return nil, line
}

// AddSection adds kind's section, empty, to scope's declarations, shared or
// a Mac's, in its place, unless the file has one of the kind's already: the
// kind's looked after from then on (List.Declared). It reports whether it
// added one.
func (c *Config) AddSection(kind, scope string) (bool, error) {
	d, f, err := c.editable(kind, scope)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(f.sections, func(s *section) bool { return s.def.kind == d.kind }) {
		return false, nil
	}
	f.add(&section{def: d})
	return true, c.write(f, func(*declFile) error { return nil })
}

// Declare declares e in kind's section of scope's declarations, shared or a
// Mac's, as in laptop, with its note after it, and a step by hand's steps
// indented under it: in a sorted section, which stays sorted (sortedLines);
// in another, a name in its sorted place, and a path last, as paths are
// searched in order. The section is made, in its place among the file's,
// when there's none, and the file and its folder too; a kind with sections
// for items declares it in e.Item's, whatever name it goes by, or makes one
// going by e.As; and a list in the section holding its lines back for
// e.Hold, when it says. The rest of the file is kept as it is. A line that
// wouldn't read back, as the config and the kind read it (Section.Line),
// is never written.
func (c *Config) Declare(kind, scope string, e Entry) error {
	d, files, err := c.declaring(kind, scope, e)
	if err != nil {
		return err
	}
	return c.writeDecl(files[0], d, e.Name, true)
}

// DeclareShared declares e in the shared declarations, as Declare does,
// taking it out of the files of each of the Macs from, as Undeclare does:
// one edit, judged as a whole before any of it's written. Nothing a line it
// moves says is lost: what its command can say must be the same
// (LineDiffers), and what none can, its notes and comments are carried
// (sectionDef.carry). It returns the scopes whose files it wrote, those
// written before one failed too.
func (c *Config) DeclareShared(kind string, e Entry, from []string) ([]string, error) {
	d, files, err := c.declaring(kind, Shared, e, from...)
	if err != nil {
		return nil, err
	}
	var written []string
	for i, f := range files {
		if err := c.writeDecl(f, d, e.Name, i == 0); err != nil {
			return written, err
		}
		written = append(written, f.scope)
	}
	return written, nil
}

// CheckDeclare says why Declare would refuse e, writing nothing: nil when
// it would declare it.
func (c *Config) CheckDeclare(kind, scope string, e Entry) error {
	_, _, err := c.declaring(kind, scope, e)
	return err
}

// CheckDeclareShared says why DeclareShared would refuse e, taking it out
// of the files of the Macs from, writing nothing: nil when it would declare
// it.
func (c *Config) CheckDeclareShared(kind string, e Entry, from []string) error {
	_, _, err := c.declaring(kind, Shared, e, from...)
	return err
}

// declaring is scope's declarations file with e declared, as Declare
// writes it, then each of the files of the Macs from with it taken out, none
// yet written, once the config reads as kit reads it with them all
// (readsAfter).
func (c *Config) declaring(kind, scope string, e Entry, from ...string) (sectionDef, []*declFile, error) {
	d, f, err := c.editable(kind, scope)
	if err != nil {
		return d, nil, err
	}
	arg, name := splitName(d, e.Name)
	if d.items != nil {
		arg = strings.TrimSuffix(e.Item, "/")
		if arg != "" {
			if err := d.items(arg); err != nil {
				return d, nil, err
			}
		}
	}
	if e.As != "" && arg == "" {
		return d, nil, fmt.Errorf("%s %s names an item's section: say which item", asOption, e.As)
	}
	if e.Hold != "" && len(d.holds) == 0 {
		return d, nil, fmt.Errorf("[%s] holds no lines back: only a list's sections, and [features], do", d.header)
	}
	if _, err := readHold(d, e.Hold); err != nil {
		return d, nil, err
	}
	if s, _ := f.holding(d, e.Name); s != nil {
		return d, nil, fmt.Errorf("%s is in %s already", e.Name, DeclFile(scope))
	}
	if scope != Shared {
		shared, err := c.readDecl(Shared)
		if err != nil {
			return d, nil, err
		}
		if s, _ := shared.holding(d, e.Name); s != nil {
			return d, nil, fmt.Errorf("%s is in the shared declarations already (%s), for every Mac", e.Name, DeclFile(Shared))
		}
	}
	moved, err := c.movedLines(kind, e.Name, from)
	if err != nil {
		return d, nil, err
	}
	e, notes, err := d.carry(e, moved)
	if err != nil {
		return d, nil, err
	}
	hold, err := readHold(d, e.Hold)
	if err != nil {
		return d, nil, err
	}
	line, err := entryText(d, name, e)
	if err != nil {
		return d, nil, err
	}
	steps, err := stepLines(d, e.Steps)
	if err != nil {
		return d, nil, err
	}
	s := f.find(d, arg, hold)
	if s == nil {
		s = f.add(&section{def: d, arg: arg, as: e.As, hold: hold})
	}
	block := slices.Concat(notes, []string{line}, steps)
	switch b := splitBody(d.form, s.body); {
	case d.sorted:
		s.body = sortedLines(d.form, s.body, slices.Concat(notes, []string{line})...)
	case d.form == Paths:
		b.insert(b.lastEntryEnd(0, len(b.lines)), block...)
		s.body = b.lines
	default:
		b.insert(b.sortedPlace(0, len(b.lines), name), block...)
		s.body = b.lines
	}
	if err := sameAsMoved(d, f, e.Name, moved); err != nil {
		return d, nil, err
	}
	files := []*declFile{f}
	// Taken out as the config reads with the shared file edited: whether a
	// Mac's section left empty stays (keeps) turns on the shared file.
	withShared := c.with(files)
	for _, mac := range from {
		_, g, err := withShared.undeclaring(kind, mac, e.Name)
		if err != nil {
			return d, nil, err
		}
		files = append(files, g)
	}
	return d, files, c.readsAfter(d, e.Name, files)
}

// readsAfter says why the config, with files written, the first declaring
// name, wouldn't read as kit reads it: that entry, as the kind reads it;
// and each Mac's list of d's kind that reads one of them, as it loads now,
// the rules between the shared file and a Mac's (a name in one of them, a
// section's --as naming one item) and all. A Mac's list that didn't load
// before isn't the edit's to refuse.
func (c *Config) readsAfter(d sectionDef, name string, files []*declFile) error {
	after := c.with(files)
	entries, _, err := after.declared(d.kind, files[0].scope)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == name }); i >= 0 && d.line != nil {
		if err := d.line(entries[i]); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	_, shared := after.edited[Shared]
	for _, mac := range c.MacNames() {
		if _, edited := after.edited[mac]; !edited && !shared {
			continue
		}
		if _, err := c.List(d.kind, mac); err != nil {
			continue
		}
		if _, err := after.List(d.kind, mac); err != nil {
			return fmt.Errorf("the config wouldn't read with it: %w", err)
		}
	}
	return nil
}

// with is the config as it'd read with files written in place of theirs.
func (c *Config) with(files []*declFile) *Config {
	after := *c
	after.edited = map[string]string{}
	for _, f := range files {
		after.edited[f.scope] = f.String()
	}
	return &after
}

// Undeclare takes name out of kind's sections of scope's declarations, with
// the notes directly above it and the steps under it, keeping a sorted
// section sorted; a section it leaves empty goes, unless it's the last
// declaring its kind for a Mac (keeps), which stays, header and all, empty,
// so the kind's still looked after (List.Declared). The rest of the file is
// kept as it is.
func (c *Config) Undeclare(kind, scope, name string) error {
	d, f, err := c.undeclaring(kind, scope, name)
	if err != nil {
		return err
	}
	return c.writeDecl(f, d, name, false)
}

// undeclaring is scope's declarations file with name taken out of kind's
// sections, as Undeclare writes it, not yet written.
func (c *Config) undeclaring(kind, scope, name string) (sectionDef, *declFile, error) {
	d, f, err := c.editable(kind, scope)
	if err != nil {
		return d, nil, err
	}
	s, b, i := f.lineBlock(d, name)
	if s == nil {
		return d, nil, fmt.Errorf("%s isn't in %s", name, DeclFile(scope))
	}
	b.remove(b.notesStart(i), b.end(i))
	s.body = b.lines
	switch {
	case len(b.entries(0, len(b.lines))) > 0:
	case c.keeps(f, s):
		f.empty(s)
	default:
		f.drop(s)
	}
	return d, f, nil
}

// lineBlock is the block removing the line declaring name in f's sections
// of d takes: its section, sorted first when kit keeps it sorted, so a
// comment standing apart is the next line's (sortedLines); the section's
// lines; and the line's index among them, its block running from the
// comments above it (notesStart) to the end of the steps under it (end).
// Nil when f doesn't declare it.
func (f *declFile) lineBlock(d sectionDef, name string) (*section, body, int) {
	s, line := f.holding(d, name)
	if s == nil {
		return nil, body{}, -1
	}
	if d.sorted {
		s.body = sortedLines(d.form, s.body)
	}
	b := splitBody(d.form, s.body)
	i := slices.Index(b.names, line)
	if i < 0 {
		return nil, body{}, -1
	}
	return s, b, i
}

// Replace rewrites the line declaring e.Name in kind's sections of scope's
// declarations, with e's value and note; the notes above it and the steps
// under it are kept, and a sorted section sorted.
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
	case Names:
		if !lineName.MatchString(name) {
			return "", fmt.Errorf("%q isn't a name", name)
		}
	case Commands, Settings, Secrets:
		if d.form == Commands && !commandName.MatchString(name) || d.form == Settings && !settingKey.MatchString(name) || d.form == Secrets && !secretName.MatchString(name) {
			return "", fmt.Errorf("%q isn't a name", name)
		}
		if strings.TrimSpace(e.Value) == "" {
			return "", fmt.Errorf("%s needs its %s", name, map[Form]string{Commands: "options", Settings: "value", Secrets: "reference"}[d.form])
		}
		line += " " + strings.TrimSpace(e.Value)
	case Options:
		if !commandName.MatchString(name) {
			return "", fmt.Errorf("%q isn't a name", name)
		}
		if value := strings.TrimSpace(e.Value); value != "" {
			line += " " + value
		}
	case Paths:
		if err := checkDir(name); err != nil {
			return "", err
		}
	case Defaults:
		text, err := defaultsText(name, e.Value)
		if err != nil {
			return "", err
		}
		line = text
	case Power:
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

// stepIndent is what a step's line starts with, under its entry.
const stepIndent = "  "

// stepsUnderManual says which lines take steps, for a line that has them
// and doesn't.
const stepsUnderManual = "only a [manual] line takes steps, indented under it"

// stepLines are steps, in order, as lines under their entry in d's
// sections: only a step by hand's take them, and each is a line of what to
// do.
func stepLines(d sectionDef, steps []string) ([]string, error) {
	if len(steps) > 0 && !d.steps {
		return nil, errors.New(stepsUnderManual)
	}
	lines := make([]string, len(steps))
	for i, step := range steps {
		step = strings.TrimSpace(step)
		if step == "" || strings.HasPrefix(step, "#") || strings.ContainsAny(step, "\r\n") {
			return nil, fmt.Errorf("%q isn't a step: a step is a line of what to do, not starting with #", step)
		}
		lines[i] = stepIndent + step
	}
	return lines, nil
}

// add adds s, a section empty but for its definition, arg (a project
// folder, or an item, and the name it goes by) and hold, in its place among
// the file's: after the sections before it in the order kit writes them,
// and a kind's folders', items' or held lines' sections after its own, by
// folder, item or hold.
func (f *declFile) add(s *section) *section {
	order := func(s *section) (int, string) {
		return slices.IndexFunc(f.defs, func(x sectionDef) bool { return x.kind == s.def.kind }), s.arg + s.hold
	}
	s.header = "[" + s.name() + "]"
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

// keeps reports whether f keeps its section s once it's empty: a kind's
// last section in the shared file, or in a Mac's while the shared file has
// none of the kind.
func (c *Config) keeps(f *declFile, s *section) bool {
	ofKind := func(sections []*section) bool {
		return slices.ContainsFunc(sections, func(other *section) bool { return other != s && other.def.kind == s.def.kind })
	}
	if !s.def.isKind || ofKind(f.sections) {
		return false
	}
	if f.scope == Shared {
		return true
	}
	shared, err := c.readDecl(Shared)
	return err != nil || !ofKind(shared.sections)
}

// empty leaves section s, with nothing in it, as it is, header and all, a
// blank line after it when another follows.
func (f *declFile) empty(s *section) {
	last := s == f.sections[len(f.sections)-1]
	if n := len(s.body); !last && (n == 0 || strings.TrimSpace(s.body[n-1]) != "") {
		s.body = append(s.body, "")
	}
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
	d, err := c.defFor(kind)
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
	return c.write(f, func(back *declFile) error {
		found := false
		for _, s := range back.sections {
			if s.def.kind != d.kind {
				continue
			}
			entries, _ := s.entries(f.scope)
			found = found || slices.ContainsFunc(entries, func(e Entry) bool { return e.Name == name })
		}
		if found != in {
			return fmt.Errorf("the edit didn't leave %s as it should in %s", name, DeclFile(f.scope))
		}
		return nil
	})
}

// write writes f, whole or not at all, having checked it reads back, as
// check says too.
func (c *Config) write(f *declFile, check func(back *declFile) error) error {
	content := f.String()
	file := DeclFile(f.scope)
	back, err := parseFile(f.defs, f.scope, content)
	if err != nil {
		return fmt.Errorf("the edit would leave %s unreadable: %w", file, err)
	}
	if err := check(back); err != nil {
		return err
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
