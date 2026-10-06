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

// Shared names the folder of what every Mac declares; each Mac's folder is
// named after it, as in laptop.
const Shared = "shared"

// declName names the declarations file in each folder.
const declName = "declarations"

// DeclFile is the declarations file of scope, shared or a Mac's, by its path
// in the config repository, as in laptop/declarations.
func DeclFile(scope string) string {
	return scope + "/" + declName
}

// PathsKind names the section of kit's own PATH, which isn't a kind's.
const PathsKind = "paths"

// ChecksKind names the section of checks of the user's own, each a command.
const ChecksKind = "checks"

// HourlyKind and NightlyKind name the sections of jobs of the user's own,
// each a command: run every hour, and in the nightly run.
const (
	HourlyKind  = "hourly"
	NightlyKind = "nightly"
)

// ManualKind names the section of the steps a person does by hand: a name,
// what to do, and, after --, a command saying whether it's done.
const ManualKind = "manual"

// The sections of kit prefs's lists, which aren't kinds: the settings files
// it saves besides apps' preferences; the domains it never saves; the apps
// that domains belong to when their names don't say, a domain's pattern then
// the app's bundle id; and the settings that belong to one Mac, never
// copied to another.
const (
	PrefsFilesKind        = "prefs-files"
	PrefsDenyKind         = "prefs-deny"
	PrefsAppsKind         = "prefs-apps"
	PrefsMachineBoundKind = "prefs-machine-bound"
)

// FeaturesKind names the section of the pieces switched on for a Mac, by
// name: the parts with nothing to list, such as a backup tool.
const FeaturesKind = "features"

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
	// settings: a setting's key, then its value, as a shell splits words,
	// then an optional note, as in git config --global's form.
	settings
	// defaults: a macOS setting in defaults write's form, its words as a
	// shell splits them: -currentHost for one of this Mac's host, the
	// domain, the key, and the value (-bool, -int, -float or -string and
	// the value; -dict-add, an entry's key and its value as XML; or a value
	// as XML), then an optional note.
	defaults
	// secrets: a secret, what it fills (an environment variable's name, or a
	// file's path) then where 1Password keeps its value and any options,
	// words as a shell splits them, then an optional note.
	secrets
	// power: a power setting in pmset's form, its words as a shell splits
	// them: the power source (-a every one, -b the battery, -c the charger,
	// -u a UPS), the setting and its value, then an optional note.
	power
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
	// items is whether the kind has a section for each 1Password item, the
	// item after the header, as in [secrets op://vault/item]: its lines'
	// references are short for the item's fields, their names unprefixed.
	items bool
	// grouped is whether its names are filed in groups under comment
	// headings, a new one going under "To be sorted".
	grouped bool
}

// sectionDefs are the sections a declarations file may hold, in the order
// kit writes them.
var sectionDefs = []sectionDef{
	{header: "features", kind: FeaturesKind, form: names},
	{header: "paths", kind: PathsKind, form: paths},
	{header: "git config", kind: "git-config", form: settings, grouped: true},
	{header: "macos settings", kind: "defaults", form: defaults, grouped: true},
	{header: "power settings", kind: "power", form: power, grouped: true},
	{header: "backup exclusions", kind: "backup-exclusion", form: paths, grouped: true},
	{header: "spotlight exclusions", kind: "spotlight-exclusion", form: paths, grouped: true},
	{header: "homebrew formulae", kind: "brew", form: names, grouped: true},
	{header: "homebrew casks", kind: "cask", form: names, grouped: true},
	{header: "app store apps", kind: "mas", form: names, grouped: true},
	{header: "npm packages", kind: "npm", form: names, grouped: true},
	{header: "composer packages", kind: "composer", form: names, grouped: true},
	{header: "go tools", kind: "go", form: names, grouped: true},
	{header: "github extensions", kind: "gh", form: names, grouped: true},
	{header: "macos login items", kind: "login-item", form: names, grouped: true},
	{header: "claude mcp", kind: "claude-mcp", form: commands, folders: true},
	{header: "claude plugins", kind: "claude-plugin", form: names, grouped: true},
	{header: "secrets", kind: "secret", form: secrets, grouped: true, items: true},
	{header: "prefs files", kind: PrefsFilesKind, form: paths},
	{header: "prefs deny", kind: PrefsDenyKind, form: paths},
	{header: "prefs apps", kind: PrefsAppsKind, form: settings},
	{header: "prefs machine-bound", kind: PrefsMachineBoundKind, form: paths},
	{header: "manual", kind: ManualKind, form: commands},
	{header: "checks", kind: ChecksKind, form: commands},
	{header: "hourly", kind: HourlyKind, form: commands},
	{header: "nightly", kind: NightlyKind, form: commands},
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

// Grouped reports whether kind's declarations are filed in groups, under
// comment headings: a package list's are; a command's, a path's or a
// switch's aren't.
func Grouped(kind string) bool {
	d, err := defFor(kind)
	return err == nil && d.grouped
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
	// Scope is whose declarations file declares it: shared, or a Mac's; ""
	// for a thing declared in a file of its own (File).
	Scope string `json:",omitempty"`
	// File is the file declaring it when that isn't a declarations file, as
	// tmux's config declares its plugins: as it's shown, ~ and all.
	File string `json:",omitempty"`
	// Section is its section's header, as in homebrew formulae.
	Section string
	// Folder is the project folder whose section it's in, if any.
	Folder string `json:",omitempty"`
	// Item is the 1Password item whose section it's in, if any, as in
	// op://vault/item.
	Item string `json:",omitempty"`
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

// Path is the file declaring the entry: its scope's declarations file, as in
// laptop/declarations, or a file of its own.
func (e Entry) Path() string {
	if e.File != "" {
		return e.File
	}
	return DeclFile(e.Scope)
}

// Pos is where the entry is, for messages: its file and line, as in
// laptop/declarations:12.
func (e Entry) Pos() string {
	return fmt.Sprintf("%s:%d", e.Path(), e.Line)
}

// lineName is what a name in a list may be: no spaces, and nothing a
// package's name never holds, so a typo shows as an error. A version or a
// constraint may follow the name, as in typescript@5 or laravel/valet:^4.0.
var lineName = regexp.MustCompile(`^[A-Za-z0-9@._+/-][A-Za-z0-9@._+/:^~*<>=|,-]*$`)

// commandName is what a command's name may be, as Claude Code's MCP servers'
// names may.
var commandName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// secretName is what a secret fills: an environment variable's name, or a
// file's path, from ~ or /.
var secretName = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*|(~/|/)[^\s"'#]+)$`)

// settingKey is what a setting's key may be: no spaces, quotes, # or
// backslashes, which a key such as credential.https://github.com.helper
// never needs.
var settingKey = regexp.MustCompile(`^[A-Za-z0-9][^\s"'#\\]*$`)

// headerLine is a section's header: words between brackets, alone on their
// line but for a comment.
var headerLine = regexp.MustCompile(`^\[([^\]]*)\]\s*(#.*)?$`)

// declFile is a declarations file, split into its sections for reading and
// editing in place: what's before the first section, and each section's
// header and lines.
type declFile struct {
	scope    string
	preamble []string
	sections []*section
}

// section is a section of a declarations file: its header, its definition,
// what follows the header's name, if anything (a project folder, or a
// 1Password item), and its lines, which start at line start.
type section struct {
	def    sectionDef
	arg    string
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
// for a project folder or a 1Password item.
func matchHeader(text string) (sectionDef, string, bool) {
	text = strings.Join(strings.Fields(text), " ")
	for _, d := range sectionDefs {
		if text == d.header {
			return d, "", true
		}
		rest, ok := strings.CutPrefix(text, d.header+" ")
		switch {
		case ok && d.folders && (strings.HasPrefix(rest, "~/") || strings.HasPrefix(rest, "/")):
			return d, rest, true
		case ok && d.items && strings.HasPrefix(rest, "op://"):
			return d, strings.TrimSuffix(rest, "/"), true
		}
	}
	return sectionDef{}, "", false
}

// ItemRef reports whether ref is a 1Password item's reference: "op://", a
// vault and an item, as in op://vault/item.
func ItemRef(ref string) bool {
	vault, item, ok := strings.Cut(strings.TrimPrefix(ref, "op://"), "/")
	return strings.HasPrefix(ref, "op://") && ok && vault != "" && item != "" && !strings.Contains(item, "/")
}

// parseFile splits scope's declarations file into its sections, and checks
// every line reads as its section's lines do: a section kit doesn't know, or
// one twice, is refused.
func parseFile(scope, content string) (*declFile, error) {
	f := &declFile{scope: scope}
	name := DeclFile(scope)
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
		d, arg, ok := matchHeader(m[1])
		switch {
		case !ok:
			return nil, fmt.Errorf("%s:%d: kit doesn't know the section [%s]", name, i+1, m[1])
		case d.items && arg != "" && !ItemRef(arg):
			return nil, fmt.Errorf("%s:%d: [%s]: %q isn't a 1Password item's reference, as in op://vault/item", name, i+1, m[1], arg)
		}
		if prev := f.find(d, arg); prev != nil {
			return nil, fmt.Errorf("%s:%d: [%s] is at line %d too", name, i+1, m[1], prev.start)
		}
		cur = &section{def: d, arg: arg, header: line, start: i + 1}
		f.sections = append(f.sections, cur)
	}
	seen := map[string]Entry{}
	for _, s := range f.sections {
		entries, err := s.entries(scope)
		if err != nil {
			return nil, err
		}
		// A kind's sections for items share their names: one each.
		for _, e := range entries {
			key := s.def.kind + "\x00" + e.Name
			if first, ok := seen[key]; ok {
				return nil, fmt.Errorf("%s: %s is in [%s] at line %d too", e.Pos(), e.Name, first.Section, first.Line)
			}
			seen[key] = e
		}
	}
	return f, nil
}

// find is the file's section of d, for arg (a project folder, or an item),
// or nil.
func (f *declFile) find(d sectionDef, arg string) *section {
	for _, s := range f.sections {
		if s.def.kind == d.kind && s.arg == arg {
			return s
		}
	}
	return nil
}

// entries are what the section declares, in scope's declarations file.
func (s *section) entries(scope string) ([]Entry, error) {
	file := DeclFile(scope)
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
		e.Scope, e.Line, e.Group = scope, n, group
		out = append(out, e)
	}
	return out, nil
}

// entry reads one of the section's entry lines.
func (s *section) entry(line string) (Entry, error) {
	e := Entry{Section: strings.TrimSpace(s.def.header + " " + s.arg)}
	if s.def.items {
		e.Item = s.arg
	} else {
		e.Folder = s.arg
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
	case commands, settings, secrets:
		name, value, note, err := splitCommand(line)
		if err != nil {
			return e, err
		}
		switch {
		case s.def.form == commands && !commandName.MatchString(name):
			return e, fmt.Errorf("%q isn't a name: letters, digits, dots, hyphens and underscores", name)
		case s.def.form == settings && !settingKey.MatchString(name):
			return e, fmt.Errorf("%q isn't a setting's key", name)
		case s.def.form == secrets && !secretName.MatchString(name):
			return e, fmt.Errorf("%q isn't what a secret fills: an environment variable's name, or a file's path from ~ or /", name)
		case value == "" && s.def.form == commands:
			return e, fmt.Errorf("%s has nothing after its name: its options follow it", name)
		case value == "":
			return e, fmt.Errorf("%s has no value after it", name)
		}
		e.Name, e.Value, e.Note = name, value, note
	case defaults:
		text, note := splitNote(line)
		words, err := Words(text)
		if err != nil {
			return e, err
		}
		setting, value, err := defaultsLine(words)
		if err != nil {
			return e, err
		}
		e.Name, e.Value, e.Note = setting.Name(), value, note
	case power:
		text, note := splitNote(line)
		words, err := Words(text)
		if err != nil {
			return e, err
		}
		name, value, err := powerLine(words)
		if err != nil {
			return e, err
		}
		e.Name, e.Value, e.Note = name, value, note
	}
	if e.Folder != "" {
		e.Name = e.Folder + ":" + e.Name
	}
	return e, nil
}

// Scopes are the folders whose declarations a Mac may read: the shared one,
// then each Mac's, by name.
func (c *Config) Scopes() []string {
	return append([]string{Shared}, c.MacNames()...)
}

// readDecl reads scope's declarations file: empty, when there's none.
func (c *Config) readDecl(scope string) (*declFile, error) {
	data, err := os.ReadFile(filepath.Join(c.Dir, DeclFile(scope)))
	if errors.Is(err, fs.ErrNotExist) {
		return &declFile{scope: scope}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", DeclFile(scope), err)
	}
	return parseFile(scope, string(data))
}

// declared is what scope's declarations file declares in kind's sections.
func (c *Config) declared(kind, scope string) ([]Entry, error) {
	d, err := defFor(kind)
	if err != nil {
		return nil, err
	}
	f, err := c.readDecl(scope)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, s := range f.sections {
		if s.def.kind != d.kind {
			continue
		}
		entries, err := s.entries(scope)
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
			return List{}, fmt.Errorf("%s: %s is in %s too (line %d): a thing goes in the shared declarations or a Mac's, not both", e.Pos(), e.Name, DeclFile(s.Scope), s.Line)
		}
	}
	return List{Kind: kind, Entries: append(shared, own...)}, nil
}

// Where are the entries for name in kind's sections: the shared file's,
// then each Mac's, by the Macs' names.
func (c *Config) Where(kind, name string) ([]Entry, error) {
	var found []Entry
	for _, scope := range c.Scopes() {
		entries, err := c.declared(kind, scope)
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

// checkFiles finds a folder declaring for a Mac the config doesn't know: one
// holding a declarations file, named neither shared nor one of the Macs, so
// a misnamed one is never silently ignored.
func (c *Config) checkFiles() error {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return fmt.Errorf("read the config repository: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || name == Shared || c.Knows(name) {
			continue
		}
		_, err := os.Stat(filepath.Join(c.Dir, DeclFile(name)))
		switch {
		case err == nil:
			return fmt.Errorf("%s declares for a Mac %s doesn't name (one of %s): rename the folder, or add the Mac", DeclFile(name), File, c.macList())
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("read %s: %w", DeclFile(name), err)
		}
	}
	return nil
}
