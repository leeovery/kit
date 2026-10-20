package config

import (
	"fmt"
	"slices"
	"strings"
)

// LineDiffers is a shared declare's refusal of a Mac's line it would move,
// as the shared line wouldn't say what that line says, of what its command
// can say.
type LineDiffers struct {
	// Line is the Mac's line, as the config reads it.
	Line Entry
	// Theirs is what the Mac's line says that the shared line wouldn't,
	// and Ours what the shared line would say that the Mac's doesn't.
	Theirs, Ours []string
}

func (l LineDiffers) Error() string {
	const advice = ": give the same, or take that line out first"
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

// orderFree are the options a line may give in any order, each as often as
// wanted: two lines giving the same of them say the same.
var orderFree = []string{afterOption, beforeOption, secretOption}

// restOfLine are the words after which the rest of a line is a command, as
// it's written.
var restOfLine = []string{"--", "--test"}

// lineOptions splits a line's value, each word quoted, into its words
// before restOfLine but the options of flags; those options, each flag and
// its value, sorted; and the words from restOfLine on.
func lineOptions(value string, flags []string) (words, options, rest []string, err error) {
	all, err := Words(value)
	if err != nil {
		return nil, nil, nil, err
	}
	for i := 0; i < len(all); i++ {
		switch w := all[i]; {
		case slices.Contains(restOfLine, w):
			for _, r := range all[i:] {
				rest = append(rest, Quote(r))
			}
			i = len(all)
		case slices.Contains(flags, w) && i+1 < len(all):
			options = append(options, w+" "+Quote(all[i+1]))
			i++
		default:
			words = append(words, Quote(w))
		}
	}
	slices.Sort(options)
	return words, options, rest, nil
}

// says is what e's line says, as d reads it, its notes aside, in parts, so
// that two lines saying the same, however written, have the same, sorted:
// the kind's own reading when it has one; else its words, one part, and
// those of orderFree, a part each, with its section's item and hold, and
// its steps.
func (d sectionDef) says(e Entry) ([]string, error) {
	if d.lineSays != nil {
		parts, err := d.lineSays(e)
		return slices.Sorted(slices.Values(parts)), err
	}
	words, options, rest, err := lineOptions(e.Value, orderFree)
	if err != nil {
		return nil, err
	}
	parts := options
	if text := strings.Join(slices.Concat(words, rest), " "); text != "" {
		parts = append(parts, text)
	}
	if e.Item != "" {
		parts = append(parts, "in "+e.Item)
	}
	if e.Hold != "" {
		parts = append(parts, "its section's "+e.Hold)
	}
	if len(e.Steps) > 0 {
		parts = append(parts, "the steps "+strings.Join(e.Steps, "; "))
	}
	slices.Sort(parts)
	return parts, nil
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
// give, and the comments to go above it. Of what a command has no way to
// say, it takes the hold of their section, a step by hand's steps and the
// options of d.carries, each the same in every line moved; and their note,
// the comments above them, compared by their words, and, for an item's
// block, the name it goes by, one between them. Moved lines that differ in
// any are refused, naming each. Given no note, and none moved, e takes its
// DefaultNote.
func (d sectionDef) carry(e Entry, moved []movedLine) (Entry, []string, error) {
	var err error
	if e.Hold == "" {
		if e.Hold, err = agreed(moved, "sections", sameAdvice, true, func(m movedLine) string { return m.Hold }); err != nil {
			return e, nil, err
		}
	}
	if len(e.Steps) == 0 && d.steps {
		steps, err := agreed(moved, "steps under them", sameAdvice, true, func(m movedLine) string { return strings.Join(m.Steps, "\n") })
		if err != nil {
			return e, nil, err
		}
		if steps != "" {
			e.Steps = strings.Split(steps, "\n")
		}
	}
	if e.Value, err = d.carryOptions(e.Value, moved); err != nil {
		return e, nil, err
	}
	if e.Note == "" {
		if e.Note, err = agreed(moved, "notes", "say which with --note", false, func(m movedLine) string { return m.Note }); err != nil {
			return e, nil, err
		}
	}
	if e.Note == "" {
		e.Note = e.DefaultNote
	}
	if e.As == "" && d.items != nil {
		item := strings.TrimSuffix(e.Item, "/")
		if e.As, err = agreed(moved, "names for their block", sameAdvice, false, func(m movedLine) string {
			if m.Item != item {
				return ""
			}
			return m.As
		}); err != nil {
			return e, nil, err
		}
	}
	words, err := agreed(moved, "comments above them", sameAdvice, false, func(m movedLine) string { return noteWords(m.comments) })
	if err != nil || words == "" {
		return e, nil, err
	}
	i := slices.IndexFunc(moved, func(m movedLine) bool { return noteWords(m.comments) == words })
	return e, moved[i].comments, nil
}

// carryOptions is value with the options of d.carries the lines moved
// give, when it gives none of them, before what's from restOfLine on.
func (d sectionDef) carryOptions(value string, moved []movedLine) (string, error) {
	if len(d.carries) == 0 {
		return value, nil
	}
	words, given, rest, err := lineOptions(value, d.carries)
	if err != nil || len(given) > 0 {
		return value, err
	}
	options, err := agreed(moved, "options", sameAdvice, true, func(m movedLine) string {
		_, options, _, err := lineOptions(m.Value, d.carries)
		if err != nil {
			return m.Value
		}
		return strings.Join(options, " ")
	})
	if err != nil || options == "" {
		return value, err
	}
	return strings.Join(slices.Concat(words, []string{options}, rest), " "), nil
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
			return LineDiffers{Line: m.Entry, Theirs: without(theirs, ours), Ours: without(ours, theirs)}
		}
	}
	return nil
}

// without is what of parts isn't among others.
func without(parts, others []string) []string {
	return slices.DeleteFunc(slices.Clone(parts), func(p string) bool { return slices.Contains(others, p) })
}
