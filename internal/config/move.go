package config

import (
	"fmt"
	"slices"
	"strings"
)

// LineDiffers is a shared declare's refusal of a Mac's line it would move,
// as the shared line wouldn't say what that line says.
type LineDiffers struct {
	// Line is the Mac's line, as the config reads it.
	Line Entry
	// Theirs is what the Mac's line says that the shared line wouldn't,
	// and Ours what the shared line would say that the Mac's doesn't.
	Theirs, Ours []string
	// FromMac is whether the line's value is read from this Mac as it's
	// set (Section.FromMac): set the same here, not given.
	FromMac bool
}

func (l LineDiffers) Error() string {
	advice := ": give the same, or take that line out first"
	if l.FromMac {
		advice = ": set it the same here, or take that line out first"
	}
	ours := strings.Join(l.Ours, " and ")
	if len(l.Theirs) == 0 {
		return fmt.Sprintf("%s has %s without %s%s", l.Line.Pos(), l.Line.Name, ours, advice)
	}
	where := "where this doesn't"
	if ours != "" {
		where = "where this gives " + ours
	}
	return fmt.Sprintf("%s has %s with %s, %s%s", l.Line.Pos(), l.Line.Name, strings.Join(l.Theirs, " and "), where, advice)
}

// reading is what e's line says, as its kind reads it, in parts: the
// kind's own reading (Section.Says), else its words, quoted, as one.
func (d sectionDef) reading(e Entry) ([]string, error) {
	if d.lineSays != nil {
		return d.lineSays(e)
	}
	words, err := Words(e.Value)
	if err != nil || len(words) == 0 {
		return nil, err
	}
	for i, w := range words {
		words[i] = Quote(w)
	}
	return []string{strings.Join(words, " ")}, nil
}

// says is what e's line says, its notes aside, as a set of parts, so that
// two lines saying the same, however written, have the same: its reading,
// with its section's hold and its steps.
func (d sectionDef) says(e Entry) ([]string, error) {
	parts, err := d.reading(e)
	if err != nil {
		return nil, err
	}
	if e.Hold != "" {
		parts = append(parts, "its section's "+e.Hold)
	}
	if len(e.Steps) > 0 {
		parts = append(parts, "the steps "+strings.Join(e.Steps, "; "))
	}
	return slices.Compact(slices.Sorted(slices.Values(parts))), nil
}

// movedLine is a Mac's line a shared declare takes out: its entry, as the
// config reads it, and the comments of the block taking it out takes
// (lineBlock), as written.
type movedLine struct {
	Entry
	comments []string
}

// movedLines are the lines declaring name in the files of the Macs from,
// those a shared declare takes out.
func (c *Config) movedLines(kind, name string, from []string) ([]movedLine, error) {
	var lines []movedLine
	for _, mac := range from {
		d, f, err := c.editable(kind, mac)
		if err != nil {
			return nil, err
		}
		e, ok, err := f.entryOf(d, name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		_, b, i := f.lineBlock(d, name)
		lines = append(lines, movedLine{e, slices.Clone(b.lines[b.notesStart(i):i])})
	}
	return lines, nil
}

// entryOf is the entry declaring name in f's sections of d, and whether
// there's one.
func (f *declFile) entryOf(d sectionDef, name string) (Entry, bool, error) {
	s, _ := f.holding(d, name)
	if s == nil {
		return Entry{}, false, nil
	}
	entries, err := s.entries(f.scope)
	if err != nil {
		return Entry{}, false, err
	}
	i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == name })
	if i < 0 {
		return Entry{}, false, nil
	}
	return entries[i], true, nil
}

// sameAdvice is what to do about moved lines that differ in what their
// command has no way to give.
const sameAdvice = "make them the same, or take those lines out first"

// carry is e as a shared line takes from the lines it moves what e doesn't
// give, the comments to go above it, and what it says of a name it didn't
// carry. Of what a command has no way to say, it takes the hold of their
// section, a step by hand's steps and the options of d.carries, each the
// same in every line moved; and their note, the comments above them,
// compared by their words, and, for an item's block, the name it goes by,
// one between them, the name only when no file gives it to another item.
// Moved lines that differ in any are refused, naming each. Given no note,
// and none moved, e takes its DefaultNote.
func (c *Config) carry(d sectionDef, e Entry, moved []movedLine) (Entry, []string, string, error) {
	var err error
	if e.Hold == "" {
		if e.Hold, err = agreed(moved, "sections", sameAdvice, true, func(m movedLine) string { return m.Hold }); err != nil {
			return e, nil, "", err
		}
	}
	if len(e.Steps) == 0 && d.steps {
		steps, err := agreed(moved, "steps under them", sameAdvice, true, func(m movedLine) string { return strings.Join(m.Steps, "\n") })
		if err != nil {
			return e, nil, "", err
		}
		if steps != "" {
			e.Steps = strings.Split(steps, "\n")
		}
	}
	if e.Value, err = d.carryOptions(e, moved); err != nil {
		return e, nil, "", err
	}
	if e.Note == "" {
		if e.Note, err = agreed(moved, "notes", "say which with --note", false, func(m movedLine) string { return m.Note }); err != nil {
			return e, nil, "", err
		}
	}
	if e.Note == "" {
		e.Note = e.DefaultNote
	}
	var said string
	if e.As == "" && d.items != nil {
		if e.As, said, err = c.carriedName(d, e.Item, moved); err != nil {
			return e, nil, "", err
		}
	}
	words, err := agreed(moved, "comments above them", sameAdvice, false, func(m movedLine) string { return noteWords(m.comments) })
	if err != nil || words == "" {
		return e, nil, said, err
	}
	i := slices.IndexFunc(moved, func(m movedLine) bool { return noteWords(m.comments) == words })
	return e, moved[i].comments, said, nil
}

// carriedName is the name the moved lines' blocks for item go by, for the
// shared one, should it be made: none, saying why, when a file gives it to
// another item, as it'd stand for two.
func (c *Config) carriedName(d sectionDef, item string, moved []movedLine) (string, string, error) {
	item = strings.TrimSuffix(item, "/")
	as, err := agreed(moved, "names for their block", sameAdvice, false, func(m movedLine) string {
		if m.Item != item {
			return ""
		}
		return m.As
	})
	if err != nil || as == "" {
		return "", "", err
	}
	for _, scope := range c.Scopes() {
		f, err := c.readDecl(scope)
		if err != nil {
			return "", "", err
		}
		for _, s := range f.sections {
			if s.def.kind == d.kind && s.as == as && s.arg != item {
				return "", fmt.Sprintf("its block's name, %s, not carried: %s:%d gives it to %s", as, DeclFile(scope), s.start, s.arg), nil
			}
		}
	}
	return as, "", nil
}

// carryOptions is e's value with the options of d.carries the lines moved
// give, when it gives none of them, after its first word.
func (d sectionDef) carryOptions(e Entry, moved []movedLine) (string, error) {
	if len(d.carries) == 0 {
		return e.Value, nil
	}
	given, err := d.carried(e)
	if err != nil || len(given) > 0 {
		return e.Value, err
	}
	options, err := agreed(moved, "options", sameAdvice, true, func(m movedLine) string {
		parts, err := d.carried(m.Entry)
		if err != nil {
			return m.Value
		}
		return strings.Join(parts, " ")
	})
	if err != nil || options == "" {
		return e.Value, err
	}
	words, err := Words(e.Value)
	if err != nil || len(words) == 0 {
		return e.Value, err
	}
	rest := make([]string, len(words)-1)
	for i, w := range words[1:] {
		rest[i] = Quote(w)
	}
	return strings.Join(slices.Concat([]string{Quote(words[0]), options}, rest), " "), nil
}

// carried are the parts of e's reading that are options of d.carries, as
// a set: sorted, each once.
func (d sectionDef) carried(e Entry) ([]string, error) {
	parts, err := d.reading(e)
	parts = slices.DeleteFunc(parts, func(p string) bool {
		return !slices.ContainsFunc(d.carries, func(flag string) bool { return strings.HasPrefix(p, flag+" ") })
	})
	return slices.Compact(slices.Sorted(slices.Values(parts))), err
}

// agreed is what the lines moved have, as of says: the one thing they all
// have, or, unless all, those having any; refused when they differ, naming
// each line and what it has, and saying advice.
func agreed(moved []movedLine, what, advice string, all bool, of func(movedLine) string) (string, error) {
	var with []movedLine
	var have []string
	for _, m := range moved {
		if v := of(m); v != "" || all {
			with = append(with, m)
			have = append(have, v)
		}
	}
	if len(slices.Compact(slices.Sorted(slices.Values(have)))) > 1 {
		said := make([]string, len(with))
		for i, m := range with {
			said[i] = fmt.Sprintf("%s (%s)", m.Pos(), quotedOrNone(have[i]))
		}
		return "", fmt.Errorf("the lines it moves have different %s, %s: %s", what, strings.Join(said, ", "), advice)
	}
	if len(have) == 0 {
		return "", nil
	}
	return have[0], nil
}

// quotedOrNone is what a moved line has, as a refusal names it: quoted, its
// lines joined, or none.
func quotedOrNone(v string) string {
	if v == "" {
		return "none"
	}
	return fmt.Sprintf("%q", strings.ReplaceAll(v, "\n", "; "))
}

// sameAsMoved says why the line declaring name in f, the shared file as the
// edit leaves it, doesn't say what each of moved says (LineDiffers): nil
// when it does.
func sameAsMoved(d sectionDef, f *declFile, name string, moved []movedLine) error {
	if len(moved) == 0 {
		return nil
	}
	shared, _, err := f.entryOf(d, name)
	if err != nil {
		return err
	}
	ours, err := d.says(shared)
	if err != nil {
		return err
	}
	for _, m := range moved {
		theirs, err := d.says(m.Entry)
		if err != nil {
			return fmt.Errorf("%s: %w", m.Pos(), err)
		}
		if !slices.Equal(theirs, ours) {
			return LineDiffers{Line: m.Entry, Theirs: without(theirs, ours), Ours: without(ours, theirs), FromMac: d.fromMac}
		}
	}
	return nil
}

// without is what of parts isn't among others.
func without(parts, others []string) []string {
	return slices.DeleteFunc(slices.Clone(parts), func(p string) bool { return slices.Contains(others, p) })
}
