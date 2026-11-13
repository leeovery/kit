package look

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// palette is what the components shared by kit's own look and the boot's
// are drawn in: a name, bold; a key, or what's live; what something says; a
// dot or a rule; a block's label; the cursor before what's chosen, the
// chosen label's tag and a typed answer's cursor; and how the wordmark's
// lines are painted.
type palette struct {
	name, key, says, dim, label lipgloss.Style
	cursor, chosen, field       lipgloss.Style
	wordmark                    func(string) string
}

// kit is kit's own look's palette: white names, muted words, pink for
// what's chosen, the wordmark along the gradient.
var kit = palette{
	name: fg(white).Bold(true), key: fg(white), says: fg(muted), dim: fg(dim), label: fg(pink).Bold(true),
	cursor: fg(pink).Bold(true), chosen: lipgloss.NewStyle().Foreground(dark).Background(pink).Bold(true),
	field:    lipgloss.NewStyle().Foreground(dark).Background(pink),
	wordmark: func(k string) string { return along(k, 0, ansi.StringWidth(k)) },
}

// Says joins what a row says, its extras after a dim dot.
func (p palette) Says(parts ...string) string { return strings.Join(parts, p.dim.Render(" · ")) }

// Output is a tool's lines, as it printed them.
func (p palette) Output(lines ...string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = p.says.Render(l)
	}
	return out
}

// todo is what to do: an arrow, then the words.
func (p palette) todo(parts ...string) string {
	return p.says.Render("→ ") + strings.Join(parts, p.dim.Render(" · "))
}

// Field is a typed answer: the cursor, a dot for each character typed,
// hidden, and the typing cursor.
func (p palette) Field(typed int) string {
	return p.cursor.Render("❯") + " " + p.key.Render(strings.Repeat("•", typed)) + p.field.Render(" ")
}

// Answers are a question's choices: the cursor in the marks' column, the
// chosen one's label a tag, its words where the others' are; what each does
// two spaces after it, and the command it runs after a dot.
func (p palette) Answers(cs []Choice, chosen int) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		var l string
		if i == chosen {
			l = p.cursor.Render("❯") + p.chosen.Render(" "+c.Label+" ")
			if c.Does != "" {
				l += " " + p.key.Render(c.Does)
			}
		} else {
			l = "  " + p.name.Render(c.Label)
			if c.Does != "" {
				l += "  " + p.says.Render(c.Does)
			}
		}
		if c.Cmd != "" {
			l += p.dim.Render(" · ") + p.dim.Render(c.Cmd)
		}
		out[i] = l
	}
	return out
}

// Keys are the keys at the foot of a screen.
func (p palette) Keys(ks ...Key) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = p.key.Render(k.Key) + " " + p.says.Render(k.Does)
	}
	return "  " + strings.Join(parts, p.dim.Render(" · "))
}

// header is a block's label in capitals, and a note two spaces on.
func (p palette) header(label, note string) string {
	s := "  " + p.label.Render(strings.ToUpper(label))
	if note != "" {
		s += "  " + p.says.Render(note)
	}
	return s
}

// Head is the wordmark, with lines stacked beside it after a dim rule: what
// ran, the Mac and when.
func (p palette) Head(beside ...string) []string {
	out := make([]string, len(wordmark))
	for r, k := range wordmark {
		l := "  " + p.wordmark(k) + "  " + p.dim.Render("│")
		if r < len(beside) {
			l += "  " + beside[r]
		}
		out[r] = l
	}
	return out
}

// Meta is what Head stacks beside the wordmark: what ran, when there's a
// command, then the Mac and when.
func (p palette) Meta(what, mac, when string) []string {
	if what == "" {
		return []string{p.name.Render(mac), p.says.Render(when)}
	}
	return []string{p.name.Render(what), p.says.Render(mac), p.says.Render(when)}
}
