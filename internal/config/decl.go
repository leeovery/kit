package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Shared is the declarations file every Mac reads; each Mac's own is named
// after it, as in laptop.
const Shared = "shared"

// PathsKind names the section of kit's own PATH, which isn't a kind's.
const PathsKind = "paths"

// form is how a section's lines read.
type form int

const (
	// names: a name, then an optional note.
	names form = iota
	// paths: a path, the whole line, spaces and all, then an optional note.
	paths
	// commands: a name, then a command's options, its words as a shell
	// splits them, then an optional note.
	commands
)

// sectionDef is a section a declarations file may hold: one kind's list, or
// kit's own PATH.
type sectionDef struct {
	// header is the section's header, between its brackets.
	header string
	kind   string
	form   form
	// folders is whether the kind has a section for each project folder,
	// the folder after the header, as in [claude mcp ~/Code/site].
	folders bool
}

// sectionDefs are the sections a declarations file may hold, in the order
// kit writes them.
var sectionDefs = []sectionDef{
	{header: "paths", kind: PathsKind, form: paths},
	{header: "homebrew formulae", kind: "brew", form: names},
	{header: "homebrew casks", kind: "cask", form: names},
	{header: "app store apps", kind: "app", form: names},
	{header: "npm packages", kind: "npm", form: names},
	{header: "composer packages", kind: "composer", form: names},
	{header: "go tools", kind: "go", form: names},
	{header: "github extensions", kind: "gh", form: names},
	{header: "macos login items", kind: "login", form: names},
	{header: "claude mcp", kind: "claude-mcp", form: commands, folders: true},
}

// defFor is the section kind's declarations go in.
func defFor(kind string) (sectionDef, error) {
	i := slices.IndexFunc(sectionDefs, func(d sectionDef) bool { return d.kind == kind })
	if i < 0 {
		return sectionDef{}, fmt.Errorf("no section declares %s", kind)
	}
	return sectionDefs[i], nil
}

// Header is the header of kind's section, as in homebrew formulae.
func Header(kind string) string {
	d, err := defFor(kind)
	if err != nil {
		return kind
	}
	return d.header
}

// Grouped reports whether kind's declarations are filed in groups: a name
// list's are; a command's or a path's aren't.
func Grouped(kind string) bool {
	d, err := defFor(kind)
	return err == nil && d.form == names
}

// List is what one kind declares for one Mac: the shared file's entries, then
// the Mac's own file's.
type List struct {
	Kind    string
	Entries []Entry
}

// Names are the list's names, in order.
func (l List) Names() []string {
	out := make([]string, len(l.Entries))
	for i, e := range l.Entries {
		out[i] = e.Name
	}
	return out
}

// Entry is a thing a declarations file declares, and where.
type Entry struct {
	// Name is what kit calls it: the name a line declares, or, in a project
	// folder's section, the folder and the name, as in ~/Code/site:mail.
	Name string
	// Value is what follows the name on a command's line: its options.
	Value string `json:",omitempty"`
	// File is the file that declares it: shared, or a Mac's.
	File string
	// Section is its section's header, as in homebrew formulae.
	Section string
	// Folder is the project folder whose section it's in, if any.
	Folder string `json:",omitempty"`
	// Line is its line in that file.
	Line int
	// Group is the comment heading the group it's in, if any.
	Group string
	// Note is the comment after it, saying why it's there, if any.
	Note string
	// Off is whether it's declared, but not to be installed: never installed
	// by kit, and left alone when it is. Kinds whose lines say so set it.
	Off bool `json:",omitempty"`
}

// lineName is what a name in a list may be: no spaces, and nothing a
// package's name never holds, so a typo shows as an error. A version or a
// constraint may follow the name, as in typescript@5 or laravel/valet:^4.0.
var lineName = regexp.MustCompile(`^[A-Za-z0-9@._+/-][A-Za-z0-9@._+/:^~*<>=|,-]*$`)

// commandName is what a command's name may be, as Claude Code's MCP servers'
// names may.
var commandName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// headerLine is a section's header: words between brackets, alone on their
// line but for a comment.
var headerLine = regexp.MustCompile(`^\[([^\]]*)\]\s*(#.*)?$`)

// declFile is a declarations file, split into its sections for reading and
// editing in place: what's before the first section, and each section's
// header and lines.
type declFile struct {
	name     string
	preamble []string
	sections []*section
}

// section is a section of a declarations file: its header, its definition,
// the folder it's for, if any, and its lines, which start at line start.
type section struct {
	def    sectionDef
	folder string
	header string
	start  int
	body   []string
}

// String is the file's text.
func (f *declFile) String() string {
	lines := slices.Clone(f.preamble)
	for _, s := range f.sections {
		lines = append(lines, s.header)
		lines = append(lines, s.body...)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// matchHeader finds the section a header names: a kind's own, or a kind's
// for a project folder.
func matchHeader(text string) (sectionDef, string, bool) {
	text = strings.Join(strings.Fields(text), " ")
	for _, d := range sectionDefs {
		if text == d.header {
			return d, "", true
		}
		if rest, ok := strings.CutPrefix(text, d.header+" "); ok && d.folders && (strings.HasPrefix(rest, "~/") || strings.HasPrefix(rest, "/")) {
			return d, rest, true
		}
	}
	return sectionDef{}, "", false
}

// parseFile splits the declarations file named name into its sections, and
// checks every line reads as its section's lines do: a section kit doesn't
// know, or one twice, is refused.
func parseFile(name, content string) (*declFile, error) {
	f := &declFile{name: name}
	if content == "" {
		return f, nil
	}
	var cur *section
	for i, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		m := headerLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			if cur == nil {
				if text, _ := splitComment(line); text != "" {
					return nil, fmt.Errorf("%s:%d: %q is before any section: a section's header comes first, as in [homebrew formulae]", name, i+1, text)
				}
				f.preamble = append(f.preamble, line)
			} else {
				cur.body = append(cur.body, line)
			}
			continue
		}
		d, folder, ok := matchHeader(m[1])
		if !ok {
			return nil, fmt.Errorf("%s:%d: kit doesn't know the section [%s]", name, i+1, m[1])
		}
		if prev := f.find(d, folder); prev != nil {
			return nil, fmt.Errorf("%s:%d: [%s] is at line %d too", name, i+1, m[1], prev.start)
		}
		cur = &section{def: d, folder: folder, header: line, start: i + 1}
		f.sections = append(f.sections, cur)
	}
	for _, s := range f.sections {
		if _, err := s.entries(name); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// find is the file's section of d, for folder, or nil.
func (f *declFile) find(d sectionDef, folder string) *section {
	for _, s := range f.sections {
		if s.def.kind == d.kind && s.folder == folder {
			return s
		}
	}
	return nil
}

// entries are what the section declares, in the file named file.
func (s *section) entries(file string) ([]Entry, error) {
	var out []Entry
	seen := make(map[string]int)
	b := splitBody(s.def.form, s.body)
	group := ""
	for i, line := range s.body {
		n := s.start + 1 + i
		switch b.kinds[i] {
		case headingLine:
			if i == 0 || b.kinds[i-1] == blankLine {
				group = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
			}
			continue
		case entryLine:
		default:
			continue
		}
		e, err := s.entry(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", file, n, err)
		}
		if first, ok := seen[e.Name]; ok {
			return nil, fmt.Errorf("%s:%d: %s is already at line %d", file, n, e.Name, first)
		}
		seen[e.Name] = n
		e.File, e.Line, e.Group = file, n, group
		out = append(out, e)
	}
	return out, nil
}

// entry reads one of the section's entry lines.
func (s *section) entry(line string) (Entry, error) {
	e := Entry{Section: s.def.header, Folder: s.folder}
	if s.folder != "" {
		e.Section += " " + s.folder
	}
	switch s.def.form {
	case names:
		text, note := splitComment(line)
		if !lineName.MatchString(text) {
			return e, fmt.Errorf("%q isn't a name: one name a line, a note after a space and #", text)
		}
		e.Name, e.Note = text, note
	case paths:
		e.Name, e.Note = splitComment(line)
	case commands:
		name, value, note, err := splitCommand(line)
		if err != nil {
			return e, err
		}
		if !commandName.MatchString(name) {
			return e, fmt.Errorf("%q isn't a name: letters, digits, dots, hyphens and underscores", name)
		}
		if value == "" {
			return e, fmt.Errorf("%s has nothing after its name: its options follow it", name)
		}
		e.Name, e.Value, e.Note = name, value, note
	}
	if s.folder != "" {
		e.Name = s.folder + ":" + e.Name
	}
	return e, nil
}

// Files are the declarations files a Mac may read: the shared one, then each
// Mac's, by name.
func (c *Config) Files() []string {
	return append([]string{Shared}, c.MacNames()...)
}

// readDecl reads the declarations file named name: empty, when there's none.
func (c *Config) readDecl(name string) (*declFile, error) {
	data, err := os.ReadFile(filepath.Join(c.Dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return &declFile{name: name}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return parseFile(name, string(data))
}

// declared is what the file named file declares in kind's sections.
func (c *Config) declared(kind, file string) ([]Entry, error) {
	d, err := defFor(kind)
	if err != nil {
		return nil, err
	}
	f, err := c.readDecl(file)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, s := range f.sections {
		if s.def.kind != d.kind {
			continue
		}
		entries, err := s.entries(file)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
	return out, nil
}

// List reads what kind declares for the Mac named mac: the shared file's,
// then the Mac's own. A thing may be declared in one of them, once.
func (c *Config) List(kind, mac string) (List, error) {
	if !c.Knows(mac) {
		return List{}, fmt.Errorf("no Mac named %s in %s: one of %s", mac, File, c.macList())
	}
	shared, err := c.declared(kind, Shared)
	if err != nil {
		return List{}, err
	}
	own, err := c.declared(kind, mac)
	if err != nil {
		return List{}, err
	}
	where := make(map[string]Entry, len(shared))
	for _, e := range shared {
		where[e.Name] = e
	}
	for _, e := range own {
		if s, ok := where[e.Name]; ok {
			return List{}, fmt.Errorf("%s:%d: %s is in %s too (line %d): a thing goes in the shared file or a Mac's, not both", e.File, e.Line, e.Name, s.File, s.Line)
		}
	}
	return List{Kind: kind, Entries: append(shared, own...)}, nil
}

// Where are the entries for name in kind's sections: the shared file's,
// then each Mac's, by the Macs' names.
func (c *Config) Where(kind, name string) ([]Entry, error) {
	var found []Entry
	for _, file := range c.Files() {
		entries, err := c.declared(kind, file)
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

// checkFiles finds a declarations file for a Mac the config doesn't know: a
// file whose first section's header is a kit section's, named neither
// shared nor one of the Macs, so a misnamed one is never silently ignored.
func (c *Config) checkFiles() error {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return fmt.Errorf("read the config repository: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || strings.ContainsAny(name, ".") || name == Shared || c.Knows(name) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.Dir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		for line := range strings.Lines(string(data)) {
			text, _ := splitComment(line)
			if text == "" {
				continue
			}
			if m := headerLine.FindStringSubmatch(text); m != nil {
				if _, _, ok := matchHeader(m[1]); ok {
					return fmt.Errorf("%s declares for a Mac %s doesn't name (one of %s): rename the file, or add the Mac", name, File, c.macList())
				}
			}
			break
		}
	}
	return nil
}
