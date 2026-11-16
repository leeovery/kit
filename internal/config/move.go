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

// moving is e as a shared declare writes it, taking out the lines moved,
// and the comments to go above it. Moved, their block goes as written, its
// comments, line, steps and section, when every Mac's is the same (alike);
// what e gives of its value, item and section's hold must be the block's,
// else it's refused, naming both. A note or name e gives wins, and with no
// note given or moved, e takes its DefaultNote.
func moving(e Entry, moved []movedLine) (Entry, []string, error) {
	if len(moved) == 0 {
		e.Note = cmp.Or(e.Note, e.DefaultNote)
		return e, nil, nil
	}
	if err := alike(e, moved); err != nil {
		return e, nil, err
	}
	m := moved[0]
	if err := m.given(e); err != nil {
		return e, nil, err
	}
	e.Value, e.Item, e.Hold, e.Steps = m.Value, m.Item, m.Hold, m.Steps
	e.Note, e.As = cmp.Or(e.Note, m.Note, e.DefaultNote), cmp.Or(e.As, m.As)
	return e, m.comments, nil
}

// alike says why the blocks of the lines moved aren't the same, naming each
// line and what it has: nil when they are. Two are the same with the same
// words in their line, however quoted and spaced; the same note; comments
// above them of the same words; the same steps; and sections of the same
// item, name and hold. A note or name e gives, which wins over theirs,
// isn't compared.
func alike(e Entry, moved []movedLine) error {
	for _, part := range []struct {
		what  string
		given string
		of    func(movedLine) string
	}{
		{"words", "", func(m movedLine) string { return quotedWords(m.Value) }},
		{"notes", e.Note, func(m movedLine) string { return m.Note }},
		{"comments above them", "", func(m movedLine) string { return noteWords(m.comments) }},
		{"steps under them", "", func(m movedLine) string { return strings.Join(m.Steps, "\n") }},
		{"items", "", func(m movedLine) string { return m.Item }},
		{"names for their block", e.As, func(m movedLine) string { return m.As }},
		{"holds", "", func(m movedLine) string { return m.Hold }},
	} {
		if part.given != "" {
			continue
		}
		if err := agreed(moved, part.what, part.of); err != nil {
			return err
		}
	}
	return nil
}

// given says why what e gives of its value, item and section's hold isn't
// what m has: nil when it is, or e gives none.
func (m movedLine) given(e Entry) error {
	for _, part := range []struct{ ours, theirs, has, none string }{
		{quotedWords(e.Value), quotedWords(m.Value), "has %s with %s", "nothing after it"},
		{strings.TrimSuffix(e.Item, "/"), m.Item, "has %s in %s", "no item's section"},
		{e.Hold, m.Hold, "has %s in a section holding it %s", "for nothing"},
	} {
		if part.ours != "" && part.ours != part.theirs {
			has := fmt.Sprintf(part.has, m.Name, cmp.Or(part.theirs, part.none))
			return fmt.Errorf("%s %s, where this gives %s: give the same, or take that line out first", m.Pos(), has, part.ours)
		}
	}
	return nil
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

// agreed says why the lines moved don't have the same, as of says, naming
// each line and what it has: nil when they do.
func agreed(moved []movedLine, what string, of func(movedLine) string) error {
	have := make([]string, len(moved))
	for i, m := range moved {
		have[i] = of(m)
	}
	if len(slices.Compact(slices.Sorted(slices.Values(have)))) == 1 {
		return nil
	}
	said := make([]string, len(moved))
	for i, m := range moved {
		said[i] = fmt.Sprintf("%s (%s)", m.Pos(), quotedOrNone(have[i]))
	}
	return fmt.Errorf("the lines it moves have different %s, %s: make them the same, or take those lines out first", what, strings.Join(said, ", "))
}

// quotedOrNone is what a moved line has, as a refusal names it: quoted, its
// lines joined, or none.
func quotedOrNone(v string) string {
	if v == "" {
		return "none"
	}
	return fmt.Sprintf("%q", strings.ReplaceAll(v, "\n", "; "))
}
