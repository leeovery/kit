package look

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"rsc.io/qr"
)

// Boot is the boot's look: in Terminal, before kit hands over to the
// terminal it's set up, kit is the raw machine. One colour, amber, in three
// strengths: full for names and what's live, mid for what they say, dim for
// brackets, dots and what's still to fill; red only for a failure. A boot
// log's brackets take the marks' place, and the wordmark has no gradient.
type Boot struct {
	full, mid, dim color.Color
	// hot is the splash's scan line: amber, hotter.
	hot color.Color
	// on is a reversed tag's text, on amber.
	on color.Color
	// dark is whether the terminal's background is dark.
	dark bool
}

// NewBoot is the boot's look for a terminal with a dark background, or a
// light one: amber bright on dark, deep on light, so each strength reads
// on either.
func NewBoot(dark bool) Boot {
	if dark {
		return Boot{full: lipgloss.Color("#FFB000"), mid: lipgloss.Color("#C18A0F"), dim: lipgloss.Color("#7A5E1F"), hot: lipgloss.Color("#FFDB8C"), on: lipgloss.Color("#212734"), dark: true}
	}
	return Boot{full: lipgloss.Color("#8F5300"), mid: lipgloss.Color("#A86400"), dim: lipgloss.Color("#C9A26A"), hot: lipgloss.Color("#5C3500"), on: lipgloss.Color("#FFFFFF")}
}

// Full is what's live: a key, a step at work, what needs you.
func (b Boot) Full(s string) string { return fg(b.full).Render(s) }

// Strong is a name.
func (b Boot) Strong(s string) string { return fg(b.full).Bold(true).Render(s) }

// Mid is what a line says.
func (b Boot) Mid(s string) string { return fg(b.mid).Render(s) }

// Dim is a bracket, a dot, what's still to fill.
func (b Boot) Dim(s string) string { return fg(b.dim).Render(s) }

// Rev is words reversed out of amber, as a code to type, or a tag that
// needs you.
func (b Boot) Rev(s string) string {
	return lipgloss.NewStyle().Foreground(b.on).Background(b.full).Bold(true).Render(s)
}

// Says joins what a line says, its extras after a dim dot.
func (b Boot) Says(parts ...string) string { return strings.Join(parts, b.Dim(" · ")) }

// Tag is a state's bracket: OK done, ** working, blank waiting, -- not
// here, !! reversed for needs you, FAIL red.
func (b Boot) Tag(s State) string {
	in := "    "
	switch s {
	case Done:
		in = b.Strong(" OK ")
	case Running:
		in = b.Full(" ** ")
	case NeedsYou:
		in = b.Rev(" !! ")
	case Failed:
		in = fg(red).Bold(true).Render("FAIL")
	case Skipped:
		in = b.Dim(" -- ")
	}
	return b.Dim("[") + in + b.Dim("]")
}

// Words is text in a state's strength: full for what's at work or needs
// you, red for a failure, mid for the rest.
func (b Boot) Words(s State, text string) string {
	switch s {
	case Running, NeedsYou:
		return b.Full(text)
	case Failed:
		return Red(text)
	}
	return b.Mid(text)
}

// BootUnder is where what belongs to a boot log's line starts: past the
// margin, the tag and its space, under the name.
const BootUnder = "         "

// Rows are a boot log's lines: each its tag, its name, two spaces, what it
// says; what belongs to it under its name. Every line is cut at w.
func (b Boot) Rows(w int, rs ...Row) []string {
	var out []string
	for _, r := range rs {
		name := b.Strong(r.Name)
		if r.State == Queued || r.State == Skipped {
			name = b.Full(r.Name)
		}
		l := "  " + b.Tag(r.State) + " " + name
		if r.Says != "" {
			l += "  " + r.Says
		}
		out = append(out, Cut(l, w))
		for _, u := range r.Under {
			out = append(out, Cut(BootUnder+u, w))
		}
	}
	return out
}

// Header is a block's label: capitals, mid.
func (b Boot) Header(label string) string { return "  " + b.Mid(strings.ToUpper(label)) }

// Keys are the keys at the foot.
func (b Boot) Keys(ks ...Key) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = b.Full(k.Key) + " " + b.Mid(k.Does)
	}
	return "  " + strings.Join(parts, b.Dim(" · "))
}

// Head is the wordmark before colour, with lines stacked beside it after a
// dim rule: what's running, the Mac and when.
func (b Boot) Head(beside ...string) []string {
	out := make([]string, len(wordmark))
	for r, k := range wordmark {
		l := "  " + b.Full(k) + "  " + b.Dim("│")
		if r < len(beside) {
			l += "  " + beside[r]
		}
		out[r] = l
	}
	return out
}

// Meta is what Head stacks beside the wordmark: what's running, the Mac and
// when.
func (b Boot) Meta(what, mac, when string) []string {
	return []string{b.Strong(what), b.Mid(mac), b.Mid(when)}
}

// bootBarWidth is the boot's bar, in cells.
const bootBarWidth = 40

// Bar is the boot's progress: its cells lit in proportion to the steps
// done.
func (b Boot) Bar(done, of int) string {
	lit := 0
	if of > 0 {
		lit = bootBarWidth * min(done, of) / of
	}
	return b.Full(strings.Repeat("█", lit)) + b.Dim(strings.Repeat("░", bootBarWidth-lit))
}

// Answers are a question's choices, under its line: the cursor before the
// chosen one, its label reversed, what each does two spaces after it.
func (b Boot) Answers(cs []Choice, chosen int) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		if i == chosen {
			out[i] = b.Full("❯") + b.Rev(" "+c.Label+" ")
			if c.Does != "" {
				out[i] += " " + b.Full(c.Does)
			}
			continue
		}
		out[i] = "  " + b.Strong(c.Label)
		if c.Does != "" {
			out[i] += "  " + b.Mid(c.Does)
		}
	}
	return out
}

// Field is a typed answer: an amber ❯, a dot for each character typed,
// hidden, and the cursor.
func (b Boot) Field(typed int) string {
	return b.Full("❯") + " " + b.Full(strings.Repeat("•", typed)) + b.Rev(" ")
}

// Todo is what to do: an arrow, then the words.
func (b Boot) Todo(text string) string { return b.Mid("→ ") + b.Full(text) }

// Output is a tool's lines, as it printed them.
func (b Boot) Output(lines ...string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = b.Mid(l)
	}
	return out
}

// QR is text as a QR code in half blocks, a cell two modules high, dark on
// light as a camera reads it: on a dark background its light modules and a
// quiet zone of two are amber, the dark ones the background; on a light
// background its dark modules are amber, the light ones the background.
func (b Boot) QR(text string) ([]string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	const quiet = 2
	n := code.Size + 2*quiet
	// filled is whether the module at x, y is drawn in amber.
	filled := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		dark := x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
		return dark != b.dark
	}
	var out []string
	for y := 0; y < n; y += 2 {
		var row strings.Builder
		for x := range n {
			top, bottom := filled(x, y), y+1 < n && filled(x, y+1)
			switch {
			case top && bottom:
				row.WriteString("█")
			case top:
				row.WriteString("▀")
			case bottom:
				row.WriteString("▄")
			default:
				row.WriteString(" ")
			}
		}
		out = append(out, b.Full(row.String()))
	}
	return out, nil
}

// Strength is one of the boot's strengths of amber, for what draws a cell
// at a time, as the splash does.
type Strength int

const (
	Blank Strength = iota
	Dimmed
	Middle
	Bright
	Glowing
)

// Paint is text in a strength of amber; blank is the terminal's own.
func (b Boot) Paint(s Strength, text string) string {
	switch s {
	case Dimmed:
		return b.Dim(text)
	case Middle:
		return b.Mid(text)
	case Bright:
		return b.Full(text)
	case Glowing:
		return fg(b.hot).Render(text)
	}
	return text
}

// WordmarkPixel is whether the wordmark's pixel at x, y is lit: 17 wide, 6
// high.
func WordmarkPixel(x, y int) bool {
	if x < 0 || y < 0 || x >= 17 || y >= 6 {
		return false
	}
	ch := []rune(wordmark[y/2])[x]
	if y%2 == 0 {
		return ch == '█' || ch == '▀'
	}
	return ch == '█' || ch == '▄'
}
