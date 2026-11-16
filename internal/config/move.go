package config

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

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

// moving is e as a shared declare writes it in d's section, taking out the
// lines moved, and the comments to go above it. Moved, their block goes as
// written, its comments, line, steps and section, when every Mac's is the
// same (alike); what e gives of its value, item and section's hold must be
// the block's, else it's refused, naming both (given). A note or name e
// gives wins, and with no note given or moved, e takes its DefaultNote; a
// name the shared file's block for the item hasn't is refused (declaring).
func moving(d sectionDef, e Entry, moved []movedLine) (Entry, []string, error) {
	if len(moved) == 0 {
		e.Note = cmp.Or(e.Note, e.DefaultNote)
		return e, nil, nil
	}
	if err := alike(e, moved); err != nil {
		return e, nil, err
	}
	m := moved[0]
	if err := m.given(d, e); err != nil {
		return e, nil, err
	}
	e.Value, e.Item, e.Hold, e.Steps = m.Value, m.Item, m.Hold, m.Steps
	e.Note, e.As = cmp.Or(e.Note, m.Note, e.DefaultNote), cmp.Or(e.As, m.As)
	return e, m.comments, nil
}

// sameAdvice is what to do about moved lines that differ.
const sameAdvice = "make them the same, or take those lines out first"

// alike says why the blocks of the lines moved aren't the same, naming each
// line and what it has: nil when they are. Two are the same with the same
// words in their line, however quoted and spaced; the same note; comments
// above them of the same words; the same steps; and sections of the same
// item, name and hold. A note or name e gives, which wins over theirs,
// isn't compared.
func alike(e Entry, moved []movedLine) error {
	for _, part := range []struct {
		what, given, advice string
		of, shown           func(movedLine) string
	}{
		{"words", "", sameAdvice, func(m movedLine) string { return quotedWords(m.Value) }, func(m movedLine) string { return m.Name + " " + m.Value }},
		{"notes", e.Note, "make them the same, give one with --note, or take those lines out first", func(m movedLine) string { return m.Note }, nil},
		{"comments above them", "", sameAdvice, func(m movedLine) string { return noteWords(m.comments) }, nil},
		{"steps under them", "", sameAdvice, func(m movedLine) string { return strings.Join(m.Steps, "; ") }, nil},
		{"items", "", sameAdvice, func(m movedLine) string { return m.Item }, nil},
		{"names for their block", e.As, sameAdvice, func(m movedLine) string { return m.As }, nil},
		{"holds", "", sameAdvice, func(m movedLine) string { return m.Hold }, nil},
	} {
		if part.given != "" || agreed(moved, part.of) {
			continue
		}
		said := make([]string, len(moved))
		for i, m := range moved {
			shown := part.of(m)
			if part.shown != nil {
				shown = part.shown(m)
			}
			said[i] = fmt.Sprintf("%s (%s)", m.Pos(), cmp.Or(shown, "none"))
		}
		return fmt.Errorf("the lines it moves have different %s, %s: %s", part.what, strings.Join(said, ", "), part.advice)
	}
	return nil
}

// given says why what e gives of its value, item and section's hold, to go
// in d's section, isn't what m has, naming both: nil when it is, or e gives
// none.
func (m movedLine) given(d sectionDef, e Entry) error {
	if e.Value != "" && quotedWords(e.Value) != quotedWords(m.Value) {
		return fmt.Errorf("%s has %s %s, where this gives %s %s: %s", m.Pos(), m.Name, m.Value, m.Name, e.Value, d.givenAdvice())
	}
	if item, hold := cmp.Or(strings.TrimSuffix(e.Item, "/"), m.Item), cmp.Or(e.Hold, m.Hold); item != m.Item || hold != m.Hold {
		return fmt.Errorf("%s has %s in [%s], where this gives [%s]: move that line to the same section, or take it out first", m.Pos(), m.Name, sectionSaid(d, m.Item, m.Hold), sectionSaid(d, item, hold))
	}
	return nil
}

// givenAdvice is what to do about a value given that isn't a Mac's line's:
// a value kit reads from the Mac is set the same there; a secret's, which
// its command always gives, given the same; another left out, so the line
// moves as written.
func (d sectionDef) givenAdvice() string {
	switch {
	case d.fromMac:
		return "set it the same here, or edit the line"
	case d.items != nil:
		return "give the same, or take that line out first"
	}
	return "give only its name to move it as written, or take that line out first"
}

// sectionSaid is the header of d's section for item, holding its lines back
// for hold, as a refusal names it.
func sectionSaid(d sectionDef, item, hold string) string {
	return strings.Join(slices.DeleteFunc([]string{d.header, item, hold}, func(s string) bool { return s == "" }), " ")
}

// quotedWords is value's words, each quoted as kit writes it, so two values
// quoted or spaced otherwise compare alike; value as it is when it doesn't
// read as words.
func quotedWords(value string) string {
	words, err := Words(value)
	if err != nil {
		return value
	}
	for i, w := range words {
		words[i] = Quote(w)
	}
	return strings.Join(words, " ")
}

// agreed is whether the lines moved all have the same, as of says.
func agreed(moved []movedLine, of func(movedLine) string) bool {
	return !slices.ContainsFunc(moved, func(m movedLine) bool { return of(m) != of(moved[0]) })
}
