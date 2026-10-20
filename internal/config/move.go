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
}

func (l LineDiffers) Error() string {
	text := strings.TrimSpace(l.Line.Name + " " + l.Line.Value)
	if l.Line.Item != "" || l.Line.Hold != "" {
		text += " in [" + l.Line.Section + "]"
	}
	return fmt.Sprintf("%s has %s: give the same, or take that line out first", l.Line.Pos(), text)
}

// orderFree are the options a line may give in any order, each as often as
// wanted: two lines giving the same of them say the same.
var orderFree = []string{"--after", "--before", "--secret"}

// restOfLine are the words after which the rest of a line is a command, as
// it's written.
var restOfLine = []string{"--", "--test"}

// says is what e's line says, as d reads it, its notes aside, so that two
// lines saying the same, however written, are alike: the kind's own reading
// when it has one; else its words, those of orderFree in any order, with
// its section's item and hold, and its steps.
func (d sectionDef) says(e Entry) (string, error) {
	if d.lineSays != nil {
		return d.lineSays(e)
	}
	words, err := Words(e.Value)
	if err != nil {
		return "", err
	}
	var kept, options []string
	for i := 0; i < len(words); i++ {
		switch w := words[i]; {
		case slices.Contains(restOfLine, w):
			kept = append(kept, words[i:]...)
			i = len(words)
		case slices.Contains(orderFree, w) && i+1 < len(words):
			options = append(options, w+" "+Quote(words[i+1]))
			i++
		default:
			kept = append(kept, w)
		}
	}
	for i, w := range kept {
		kept[i] = Quote(w)
	}
	slices.Sort(options)
	parts := []string{e.Item, e.Hold, strings.Join(kept, " "), strings.Join(options, " ")}
	return strings.Join(append(parts, e.Steps...), "\n"), nil
}

// movedLine is a Mac's line a shared declare takes out: its entry, as the
// config reads it, and the comments directly above it, as written.
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
		if ok {
			lines = append(lines, movedLine{e, f.commentsAbove(d, name)})
		}
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

// commentsAbove are the comments directly above the line declaring name in
// f's sections of d, its notes, as written.
func (f *declFile) commentsAbove(d sectionDef, name string) []string {
	s, line := f.holding(d, name)
	if s == nil {
		return nil
	}
	b := splitBody(d.form, s.body)
	i := slices.Index(b.names, line)
	return slices.Clone(b.lines[b.notesStart(i):i])
}

// carriedNotes are the comments a shared line takes from above the lines it
// moves: theirs, when they have one block between them; refused, naming
// them, when theirs differ.
func carriedNotes(moved []movedLine) ([]string, error) {
	var with []movedLine
	for _, m := range moved {
		if len(m.comments) > 0 {
			with = append(with, m)
		}
	}
	for _, m := range with {
		if m.Notes != with[0].Notes {
			var said []string
			for _, w := range with {
				said = append(said, w.Pos())
			}
			return nil, fmt.Errorf("the lines it moves have different comments above them, %s: make them the same, or take those lines out first", strings.Join(said, ", "))
		}
	}
	if len(with) == 0 {
		return nil, nil
	}
	return with[0].comments, nil
}

// carriedNote is the note a shared line takes from the lines it moves,
// when it's given none: theirs, when they have one between them; refused,
// naming them, when they differ.
func carriedNote(moved []movedLine) (string, error) {
	var noted []movedLine
	var notes []string
	for _, e := range moved {
		if e.Note != "" {
			noted = append(noted, e)
			notes = append(notes, e.Note)
		}
	}
	slices.Sort(notes)
	switch notes = slices.Compact(notes); len(notes) {
	case 0:
		return "", nil
	case 1:
		return notes[0], nil
	}
	var said []string
	for _, e := range noted {
		said = append(said, fmt.Sprintf("%s (%q)", e.Pos(), e.Note))
	}
	return "", fmt.Errorf("the lines it moves have different notes, %s: say which with --note", strings.Join(said, ", "))
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
	says, err := d.says(shared)
	if err != nil {
		return err
	}
	for _, e := range moved {
		if other, err := d.says(e.Entry); err != nil || other != says {
			return LineDiffers{Line: e.Entry}
		}
	}
	return nil
}
