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
// what to do, and its options, then the steps to do it, each a line of its
// own indented under it.
const ManualKind = "manual"

// StepsKind names the section of steps of the user's own: a name, what the
// step does, and its options; its script and data are in its folder
// (StepFolder).
const StepsKind = "steps"

// StepFolder is the folder of scope's step called name, by its path in the
// config repository, as in laptop/steps/fonts: its run script, and its
// data.
func StepFolder(scope, name string) string {
	return scope + "/steps/" + name
}

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
// name: the parts with nothing to list, such as apps' settings.
const FeaturesKind = "features"

// BackupsKind names the section of the backup tools a Mac uses: a line
// each, the tool's name, then the options placing its check.
const BackupsKind = "backups"

// Form is how a section's lines read.
type Form int

const (
	// Names: a name, then an optional note.
	Names Form = iota
	// Paths: a path, the whole line, spaces and all, then an optional note.
	Paths
	// Commands: a name, then a command's options, its words as a shell
	// splits them, then an optional note.
	Commands
	// Settings: a setting's key, then its value, as a shell splits words,
	// then an optional note, as in git config --global's form.
	Settings
	// Defaults: a macOS setting in defaults write's form, its words as a
	// shell splits them: -currentHost for one of this Mac's host, the
	// domain, the key, and the value (-bool, -int, -float or -string and
	// the value; -dict-add, an entry's key and its value as XML; or a value
	// as XML), then an optional note.
	Defaults
	// Secrets: a secret, what it fills (an environment variable's name, or a
	// file's path) then where a secrets store keeps its value and any
	// options, words as a shell splits them, then an optional note.
	Secrets
	// Power: a power setting in pmset's form, its words as a shell splits
	// them: the power source (-a every one, -b the battery, -c the charger,
	// -u a UPS), the setting and its value, then an optional note.
	Power
	// Options: a name, then its options, if it has any, its words as a
	// shell splits them, then an optional note.
	Options
)

// Section is a kind's section of a declarations file, as the kind says:
// its header, and how its lines read. The config reads the kinds' sections
// it's loaded with (Load), each kind's own.
type Section struct {
	// Header is the section's header, between its brackets, as in homebrew
	// formulae.
	Header string
	// Kind names the kind it declares, as in brew.
	Kind string
	Form Form
	// Folders is whether the kind has a section for each project folder,
	// the folder after the header, as in [claude mcp ~/Code/site].
	Folders bool
	// Items, for a kind with a section for each item of a store's, the item
	// after the header, as in [secrets op://vault/item], says whether what
	// follows the header is an item: nil when it is, else why not, naming
	// the items the kind knows. Its lines' references are short for the
	// item's fields, their names unprefixed; and the section may go by a
	// name (--as), which stands for its item in every file. Nil for a kind
	// without.
	Items func(ref string) error
	// Line, when set, says why one of its lines, as the config reads it,
	// doesn't read as the kind reads it: nil when it does. kit never writes
	// a line it refuses (Config.Declare).
	Line func(Entry) error
	// Held is whether it's a list whose sections can hold their lines back,
	// options after the header saying what for: --after a step, or one of a
	// step's things; --secret a secret they need, as in [homebrew formulae
	// --secret NAME].
	Held bool
	// Sorted is whether kit keeps the section sorted, each line with its
	// notes.
	Sorted bool
}

// sectionDef is a section a declarations file may hold: one kind's list, or
// one of the config's own, such as kit's own PATH.
type sectionDef struct {
	// header is the section's header, between its brackets.
	header string
	kind   string
	form   Form
	// isKind is whether it's a kind's: one a Mac's run takes up only while
	// the config has a section of it (List.Declared).
	isKind bool
	// folders is whether the kind has a section for each project folder,
	// the folder after the header, as in [claude mcp ~/Code/site].
	folders bool
	// items, for a kind with a section for each item of a store's, the item
	// after the header, as in [secrets op://vault/item], says whether what
	// follows the header is one (Section.Items).
	items func(ref string) error
	// line is the kind's own check of a line (Section.Line).
	line func(Entry) error
	// holds are the options the kind's sections take after the header,
	// holding their lines back, what for (its hold): --after a step, or one
	// of a step's things; --secret a secret they need, as in [homebrew
	// formulae --secret NAME]. A list's take both, [features] --after alone,
	// others none. Their names are unprefixed.
	holds []string
	// sorted is whether kit keeps the section sorted, each line with its
	// notes: a list of things or settings. Others keep their order: a
	// PATH's is the order it's searched in; commands' are as written, a new
	// one in its place by name.
	sorted bool
	// steps is whether its lines take steps, each a line of its own
	// indented under the line: a step by hand's.
	steps bool
}

// ownLeading and ownTrailing are the config's own sections, which aren't
// kinds': those kit writes before the kinds', and those it writes after.
var (
	ownLeading = []sectionDef{
		{header: "features", kind: FeaturesKind, form: Names, holds: []string{afterOption}},
		{header: "backups", kind: BackupsKind, form: Options},
		{header: "paths", kind: PathsKind, form: Paths},
	}
	ownTrailing = []sectionDef{
		{header: "prefs files", kind: PrefsFilesKind, form: Paths},
		{header: "prefs deny", kind: PrefsDenyKind, form: Paths},
		{header: "prefs apps", kind: PrefsAppsKind, form: Settings},
		{header: "prefs machine-bound", kind: PrefsMachineBoundKind, form: Paths},
		{header: "steps", kind: StepsKind, form: Commands},
		{header: "manual", kind: ManualKind, form: Commands, steps: true},
		{header: "checks", kind: ChecksKind, form: Commands},
		{header: "hourly", kind: HourlyKind, form: Commands},
		{header: "nightly", kind: NightlyKind, form: Commands},
	}
)

// sectionDefs are the sections a declarations file may hold, in the order
// kit writes them: the config's own first ones, the kinds', in the order
// given, then the config's own others.
func sectionDefs(kinds []Section) []sectionDef {
	defs := slices.Clone(ownLeading)
	for _, k := range kinds {
		d := sectionDef{header: k.Header, kind: k.Kind, form: k.Form, isKind: true, folders: k.Folders, items: k.Items, line: k.Line, sorted: k.Sorted}
		if k.Held {
			d.holds = listOptions
		}
		defs = append(defs, d)
	}
	return append(defs, ownTrailing...)
}

// defFor is the section kind's declarations go in.
func (c *Config) defFor(kind string) (sectionDef, error) {
	i := slices.IndexFunc(c.defs, func(d sectionDef) bool { return d.kind == kind })
	if i < 0 {
		return sectionDef{}, fmt.Errorf("no section declares %s", kind)
	}
	return c.defs[i], nil
}

// List is what one kind declares for one Mac: the shared file's entries, then
// the Mac's own file's.
type List struct {
	Kind    string
	Entries []Entry
	// Declared is whether the kind is declared for the Mac at all: a section
	// of its, even an empty one, in the shared file or the Mac's. A kind is
	// looked after only then, an empty section saying to look after it with
	// nothing of it declared yet.
	Declared bool
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
	// Item is the store's item whose section it's in, if any, as in
	// op://vault/item; As is the name that section goes by, if it has one,
	// as its header says after --as.
	Item string `json:",omitempty"`
	As   string `json:",omitempty"`
	// Hold is what its section holds its lines back for, as its header says
	// after its name, if it does, as kit writes it: --after a step or one of
	// a step's things, --secret a secret they need (After, Secrets).
	Hold string `json:",omitempty"`
	// Line is its line in that file.
	Line int
	// Note is the comment after it, saying why it's there, if any.
	Note string
	// Notes are the comments directly above it, no blank line between, as
	// one paragraph: each without its # and the space after that.
	Notes string `json:",omitempty"`
	// Steps are what to do, in order, as the lines indented under it say: a
	// step by hand's.
	Steps []string `json:",omitempty"`
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

// After are what the entry's section has its lines installed after, as its
// Hold says: steps, or steps' things, as in cask:firefox.
func (e Entry) After() []string { return holdOption(e.Hold, afterOption) }

// Secrets are the secrets the entry's section's lines need, as its Hold
// says, each by what it fills.
func (e Entry) Secrets() []string { return holdOption(e.Hold, secretOption) }

// The options of a section holding its lines back.
const (
	afterOption  = "--after"
	secretOption = "--secret"
)

// listOptions are the options a list's sections take.
var listOptions = []string{afterOption, secretOption}

// holdOption are the values of hold's option flag, in order.
func holdOption(hold, flag string) []string {
	words, _ := Words(hold)
	var values []string
	for i := 0; i+1 < len(words); i += 2 {
		if words[i] == flag {
			values = append(values, words[i+1])
		}
	}
	return values
}

// readHold reads what a section of d holds its lines back for, its
// header's options, those d's take: --after a step, or one of a step's
// things, as in cask:firefox; --secret a secret, by what it fills; each as
// often as wanted. It returns them as kit writes them, sorted, so sections
// holding their lines for the same are one.
func readHold(d sectionDef, text string) (string, error) {
	words, err := Words(text)
	if err != nil {
		return "", err
	}
	var opts []string
	for i := 0; i < len(words); i += 2 {
		flag := words[i]
		if !slices.Contains(d.holds, flag) {
			return "", fmt.Errorf("%q isn't one of [%s]'s options: %s", flag, d.header, strings.Join(d.holds, " and "))
		}
		if i+1 == len(words) || strings.HasPrefix(words[i+1], "--") {
			return "", fmt.Errorf("%s needs %s after it", flag, map[string]string{afterOption: "a step's name", secretOption: "a secret's name"}[flag])
		}
		value := words[i+1]
		step, thing, isThing := strings.Cut(value, ":")
		switch {
		case flag == afterOption && (step == "" || isThing && thing == ""):
			return "", fmt.Errorf("--after %s: --after names a step, or one of its things as in cask:firefox", value)
		case flag == secretOption && !secretName.MatchString(value):
			return "", fmt.Errorf("--secret %s: a secret is named by what it fills: an environment variable's name, or a file's path from ~ or /", value)
		}
		opts = append(opts, flag+" "+Quote(value))
	}
	slices.Sort(opts)
	return strings.Join(slices.Compact(opts), " "), nil
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
	scope string
	// defs are the sections it may hold, in the order kit writes them.
	defs     []sectionDef
	preamble []string
	sections []*section
}

// section is a section of a declarations file: its header, its definition,
// what follows the header's name, if anything (a project folder, or a
// store's item, its arg, and the name the item's section goes by, as; or
// what a list's section holds its lines back for, its hold, as kit writes
// it), and its lines, which start at line start.
type section struct {
	def    sectionDef
	arg    string
	as     string
	hold   string
	header string
	start  int
	body   []string
}

// name is the section's name, as its lines' entries have it: its header's,
// then its folder, item (and the name it goes by) or hold.
func (s *section) name() string {
	name := s.def.header + " " + s.arg
	if s.as != "" {
		name += " " + asOption + " " + s.as
	}
	return strings.TrimSpace(name + s.hold)
}

// asOption names an item's section, after its item, as in [secrets
// op://vault/item --as tokens]: a name that stands for the item.
const asOption = "--as"

// placeName is what a section's name (--as) may be.
var placeName = commandName

// readAs reads what follows an item in its section's header: --as and the
// name the section goes by, or nothing.
func readAs(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	words, err := Words(text)
	if err != nil {
		return "", err
	}
	switch {
	case len(words) != 2 || words[0] != asOption:
		return "", fmt.Errorf("%q isn't what follows an item: --as and the name its section goes by, alone", text)
	case !placeName.MatchString(words[1]):
		return "", fmt.Errorf("--as %s: a name is letters, digits, dots, hyphens and underscores", words[1])
	}
	return words[1], nil
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
// for a project folder or a store's item (arg), or with options after its
// name (opts), as a list's holding its lines back, or an item's going by a
// name.
func matchHeader(defs []sectionDef, text string) (d sectionDef, arg, opts string, ok bool) {
	text = strings.Join(strings.Fields(text), " ")
	for _, d := range defs {
		if text == d.header {
			return d, "", "", true
		}
		rest, ok := strings.CutPrefix(text, d.header+" ")
		switch {
		case ok && d.folders && (strings.HasPrefix(rest, "~/") || strings.HasPrefix(rest, "/")):
			return d, rest, "", true
		case ok && strings.HasPrefix(rest, "--"):
			return d, "", rest, true
		case ok && d.items != nil:
			item, opts, _ := strings.Cut(rest, " --")
			if opts != "" {
				opts = "--" + opts
			}
			return d, strings.TrimSuffix(item, "/"), opts, true
		}
	}
	return sectionDef{}, "", "", false
}

// parseFile splits scope's declarations file into its sections, those defs
// define, and checks every line reads as its section's lines do: a section
// kit doesn't know, or one twice, is refused.
func parseFile(defs []sectionDef, scope, content string) (*declFile, error) {
	f := &declFile{scope: scope, defs: defs}
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
		d, arg, opts, ok := matchHeader(defs, m[1])
		if !ok {
			return nil, fmt.Errorf("%s:%d: kit doesn't know the section [%s]", name, i+1, m[1])
		}
		cur = &section{def: d, arg: arg, header: line, start: i + 1}
		if err := f.readOptions(cur, opts); err != nil {
			return nil, fmt.Errorf("%s:%d: [%s]: %w", name, i+1, m[1], err)
		}
		if prev := f.find(d, cur.arg, cur.hold); prev != nil {
			return nil, fmt.Errorf("%s:%d: [%s] is at line %d too", name, i+1, m[1], prev.start)
		}
		f.sections = append(f.sections, cur)
	}
	seen := map[string]Entry{}
	for _, s := range f.sections {
		entries, err := s.entries(scope)
		if err != nil {
			return nil, err
		}
		// A kind's sections share their names, its items' and those holding
		// their lines back: one each.
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

// readOptions reads what follows s's header's name, opts, into s, checking
// its item, should it be for one: an item's section may go by a name (--as),
// one an item's in the file; a list's may hold its lines back (hold).
func (f *declFile) readOptions(s *section, opts string) error {
	d := s.def
	var err error
	switch {
	case d.items != nil && s.arg != "":
		if err := d.items(s.arg); err != nil {
			return err
		}
		if s.as, err = readAs(opts); err != nil {
			return err
		}
	case d.items != nil && opts != "":
		return fmt.Errorf("%s names an item's section, after its item, as in [%s <item> %s <name>]", asOption, d.header, asOption)
	case opts != "" && len(d.holds) == 0:
		return errors.New("only a list's sections, and [features], take options, as in [homebrew formulae --after cask:firefox]")
	default:
		if s.hold, err = readHold(d, opts); err != nil {
			return err
		}
	}
	if prev := f.named(d, s.as); prev != nil {
		return fmt.Errorf("%s %s names [%s] at line %d too: a name stands for one item", asOption, s.as, prev.name(), prev.start)
	}
	return nil
}

// named is the file's section of d going by the name as, or nil: none for
// no name.
func (f *declFile) named(d sectionDef, as string) *section {
	for _, s := range f.sections {
		if as != "" && s.def.kind == d.kind && s.as == as {
			return s
		}
	}
	return nil
}

// find is the file's section of d, for arg (a project folder, or an item)
// and hold, or nil.
func (f *declFile) find(d sectionDef, arg, hold string) *section {
	for _, s := range f.sections {
		if s.def.kind == d.kind && s.arg == arg && s.hold == hold {
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
	for i, line := range s.body {
		n := s.start + 1 + i
		if b.kinds[i] == stepLine {
			if err := s.stepOf(b, i); err != nil {
				return nil, fmt.Errorf("%s:%d: %q is indented, but %w", file, n, strings.TrimSpace(line), err)
			}
			out[len(out)-1].Steps = append(out[len(out)-1].Steps, strings.TrimSpace(line))
			continue
		}
		if b.kinds[i] != entryLine {
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
		e.Scope, e.Line, e.Notes = scope, n, b.notes(i)
		out = append(out, e)
	}
	return out, nil
}

// stepOf says why line i of the section, indented, isn't a step of the
// entry above it: nil when it is.
func (s *section) stepOf(b body, i int) error {
	switch {
	case !s.def.steps:
		return errors.New(stepsUnderManual)
	case i == 0 || b.kinds[i-1] != entryLine && b.kinds[i-1] != stepLine:
		return errors.New("there's no line directly above it for it to be a step of")
	}
	return nil
}

// entry reads one of the section's entry lines.
func (s *section) entry(line string) (Entry, error) {
	e := Entry{Section: s.name(), Hold: s.hold}
	if s.def.items != nil {
		e.Item, e.As = s.arg, s.as
	} else {
		e.Folder = s.arg
	}
	switch s.def.form {
	case Names:
		text, note := splitComment(line)
		if !lineName.MatchString(text) {
			return e, fmt.Errorf("%q isn't a name: one name a line, a note after a space and #", text)
		}
		e.Name, e.Note = text, note
	case Paths:
		e.Name, e.Note = splitComment(line)
	case Commands, Settings, Secrets, Options:
		name, value, note, err := splitCommand(line)
		if err != nil {
			return e, err
		}
		switch {
		case (s.def.form == Commands || s.def.form == Options) && !commandName.MatchString(name):
			return e, fmt.Errorf("%q isn't a name: letters, digits, dots, hyphens and underscores", name)
		case s.def.form == Settings && !settingKey.MatchString(name):
			return e, fmt.Errorf("%q isn't a setting's key", name)
		case s.def.form == Secrets && !secretName.MatchString(name):
			return e, fmt.Errorf("%q isn't what a secret fills: an environment variable's name, or a file's path from ~ or /", name)
		case value == "" && s.def.form == Commands:
			return e, fmt.Errorf("%s has nothing after its name: its options follow it", name)
		case value == "" && s.def.form != Options:
			return e, fmt.Errorf("%s has no value after it", name)
		}
		e.Name, e.Value, e.Note = name, value, note
	case Defaults:
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
	case Power:
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
	if content, ok := c.edited[scope]; ok {
		return parseFile(c.defs, scope, content)
	}
	data, err := os.ReadFile(filepath.Join(c.Dir, DeclFile(scope)))
	if errors.Is(err, fs.ErrNotExist) {
		return &declFile{scope: scope, defs: c.defs}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", DeclFile(scope), err)
	}
	return parseFile(c.defs, scope, string(data))
}

// declared is what scope's declarations file declares in kind's sections,
// and whether it has any, empty or not.
func (c *Config) declared(kind, scope string) (entries []Entry, has bool, err error) {
	d, err := c.defFor(kind)
	if err != nil {
		return nil, false, err
	}
	f, err := c.readDecl(scope)
	if err != nil {
		return nil, false, err
	}
	for _, s := range f.sections {
		if s.def.kind != d.kind {
			continue
		}
		in, err := s.entries(scope)
		if err != nil {
			return nil, false, err
		}
		entries, has = append(entries, in...), true
	}
	return entries, has, nil
}

// List reads what kind declares for the Mac named mac: the shared file's,
// then the Mac's own. A thing may be declared in one of them, once.
func (c *Config) List(kind, mac string) (List, error) {
	if !c.Knows(mac) {
		return List{}, fmt.Errorf("no Mac named %s in %s: one of %s", mac, File, c.macList())
	}
	shared, inShared, err := c.declared(kind, Shared)
	if err != nil {
		return List{}, err
	}
	own, inOwn, err := c.declared(kind, mac)
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
	if _, err := c.Places(kind, mac); err != nil {
		return List{}, err
	}
	return List{Kind: kind, Entries: append(shared, own...), Declared: inShared || inOwn}, nil
}

// Place is one of the sections of a kind with sections for items
// (Section.Items), where its things are kept: the item it's for, "" for the
// plain section; the name it goes by (--as), if any; whose declarations
// file it's in, shared or a Mac's, and the line of its header; and how many
// things it declares.
type Place struct {
	Item, As string
	Scope    string
	Line     int
	Count    int
}

// Places are kind's sections in the shared file then the Mac named mac's,
// in order, when kind has sections for items: none, else. A name stands for
// one item, in both files.
func (c *Config) Places(kind, mac string) ([]Place, error) {
	d, err := c.defFor(kind)
	if err != nil || d.items == nil {
		return nil, err
	}
	var places []Place
	named := map[string]Place{}
	for _, scope := range []string{Shared, mac} {
		f, err := c.readDecl(scope)
		if err != nil {
			return nil, err
		}
		for _, s := range f.sections {
			if s.def.kind != d.kind {
				continue
			}
			entries, err := s.entries(scope)
			if err != nil {
				return nil, err
			}
			p := Place{Item: s.arg, As: s.as, Scope: scope, Line: s.start, Count: len(entries)}
			if other, ok := named[p.As]; ok && other.Item != p.Item {
				return nil, fmt.Errorf("%s:%d: [%s]: %s %s is %s in %s (line %d): a name stands for one item", DeclFile(scope), p.Line, s.name(), asOption, p.As, other.Item, DeclFile(other.Scope), other.Line)
			}
			if p.As != "" {
				named[p.As] = p
			}
			places = append(places, p)
		}
	}
	return places, nil
}

// Where are the entries for name in kind's sections: the shared file's,
// then each Mac's, by the Macs' names.
func (c *Config) Where(kind, name string) ([]Entry, error) {
	var found []Entry
	for _, scope := range c.Scopes() {
		entries, _, err := c.declared(kind, scope)
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
