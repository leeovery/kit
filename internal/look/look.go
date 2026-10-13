// Package look is kit's look at a terminal: the components every screen is
// drawn from, so each rule lives in one place.
//
// The rules, in brief:
//   - content starts at column 3, and no line passes Width;
//   - a row is its mark, a space, its name, two spaces, then what it says,
//     extras after a dim " · ": nothing padded into columns, nothing pushed
//     to an edge;
//   - what belongs to a row (a tool's output, a typed field, a choice, what
//     to do) goes under its name, on a timeline's line when it has one;
//   - a cursor takes a mark's place, and the chosen item's label becomes a
//     pill, its words where the others' are;
//   - what to do starts "→ ", the command in it white;
//   - one blank line between blocks, none inside one;
//   - the states: done a pink ●, needs you an orange ▲, failed a red ✗,
//     running a cyan ◐, queued a cyan ○, skipped a grey –. Grey means only
//     "didn't run", and colour is never the only sign.
package look

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Width is the widest a line goes.
const Width = 80

// The colours: pink to orange, kit's gradient, with white, two greys, red
// and cyan; dark is a pill's text.
var (
	pink   = lipgloss.Color("#FF4F9A")
	orange = lipgloss.Color("#FFA24C")
	white  = lipgloss.Color("#F4F4F6")
	muted  = lipgloss.Color("#9CA3B4")
	dim    = lipgloss.Color("#596173")
	red    = lipgloss.Color("#FF5C5C")
	cyan   = lipgloss.Color("#64D2FF")
	dark   = lipgloss.Color("#1B1D24")
)

func fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Muted is words that say something: a row's text, a note.
func Muted(s string) string { return fg(muted).Render(s) }

// Dim is what's there to be found, not read: a command run, a time, a dot.
func Dim(s string) string { return fg(dim).Render(s) }

// White is what's typed or counted: a command to run, a figure.
func White(s string) string { return fg(white).Render(s) }

// Strong is a name.
func Strong(s string) string { return fg(white).Bold(true).Render(s) }

// Pink, Orange, Red and Cyan are words in a state's colour: done, needs
// you, failed, running.
func Pink(s string) string   { return fg(pink).Render(s) }
func Orange(s string) string { return fg(orange).Render(s) }
func Red(s string) string    { return fg(red).Render(s) }
func Cyan(s string) string   { return fg(cyan).Render(s) }

// Cmd is a command in words saying what to do: white, as it's typed.
func Cmd(s string) string { return White(s) }

var dot = Dim(" · ")

// Says joins what a row says, its extras after a dim dot.
func Says(parts ...string) string { return strings.Join(parts, dot) }

// State is how a thing stands, as its mark shows it.
type State int

const (
	Done State = iota
	NeedsYou
	Failed
	Running
	Queued
	Skipped
)

// marks are the states' marks, uncoloured.
var marks = map[State]string{Done: "●", NeedsYou: "▲", Failed: "✗", Running: "◐", Queued: "○", Skipped: "–"}

// Mark is a state's mark, in its colour.
func Mark(s State) string {
	switch s {
	case NeedsYou:
		return Orange(marks[s])
	case Failed:
		return Red(marks[s])
	case Running, Queued:
		return Cyan(marks[s])
	case Skipped:
		return Muted(marks[s])
	}
	return Pink(marks[Done])
}

// Words is text in a state's colour: what a row says about how it stands.
func Words(s State, text string) string {
	switch s {
	case NeedsYou:
		return Orange(text)
	case Failed:
		return Red(text)
	case Running:
		return Cyan(text)
	}
	return Muted(text)
}

// Row is a thing's line, and what belongs to it, drawn under its name.
type Row struct {
	State State
	Name  string
	Says  string
	Under []string
}

// Rows draws rows after indent, what belongs to each under its name, every
// line cut at w.
func Rows(indent string, w int, rs ...Row) []string {
	var out []string
	for _, r := range rs {
		out = append(out, Cut(line(indent, r), w))
		for _, u := range r.Under {
			out = append(out, Cut(indent+"  "+u, w))
		}
	}
	return out
}

// Timeline draws rows on a line: each row's mark a point on it, and what
// belongs to the row on the line too, its words where the names are.
func Timeline(indent string, w int, rs ...Row) []string {
	var out []string
	for _, r := range rs {
		out = append(out, Cut(line(indent, r), w))
		for _, u := range r.Under {
			out = append(out, Cut(indent+Dim("│")+" "+u, w))
		}
	}
	return out
}

func line(indent string, r Row) string {
	name := Strong(r.Name)
	switch r.State {
	case Queued:
		name = White(r.Name)
	case Skipped:
		name = Muted(r.Name)
	}
	l := indent + Mark(r.State) + " " + name
	if r.Says != "" {
		l += "  " + r.Says
	}
	return l
}

// Output is a tool's lines, as it printed them.
func Output(lines ...string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = Muted(l)
	}
	return out
}

// Todo is what to do: an arrow, then words, commands in them white.
func Todo(parts ...string) string { return Muted("→ ") + strings.Join(parts, dot) }

func cursor() string { return fg(pink).Bold(true).Render("❯") }

// Pill is words made a tag: dark bold text on a state's colour, pink for
// done or chosen.
func Pill(text string, s State) string {
	bg := pink
	switch s {
	case NeedsYou:
		bg = orange
	case Failed:
		bg = red
	}
	return lipgloss.NewStyle().Foreground(dark).Background(bg).Bold(true).Render(" " + text + " ")
}

// Field is a typed answer: a pink ❯, a dot for each character typed,
// hidden, and a pink cursor.
func Field(typed int) string {
	return cursor() + " " + White(strings.Repeat("•", typed)) + lipgloss.NewStyle().Foreground(dark).Background(pink).Render(" ")
}

// Choice is one answer to a question: its label, what it does, and the
// command it runs, when it runs one.
type Choice struct {
	Label, Does, Cmd string
}

// Answers are a question's choices: the cursor in the marks' column, the
// chosen one's label a pill, its words where the others' are; what each does
// two spaces after it.
func Answers(cs []Choice, chosen int) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		var l string
		if i == chosen {
			l = cursor() + Pill(c.Label, Done)
			if c.Does != "" {
				l += " " + White(c.Does)
			}
		} else {
			l = "  " + Strong(c.Label)
			if c.Does != "" {
				l += "  " + Muted(c.Does)
			}
		}
		if c.Cmd != "" {
			l += dot + Dim(c.Cmd)
		}
		out[i] = l
	}
	return out
}

// Chosen is something chosen from a list: the cursor in the marks'
// column, and the thing a pill, its words where the others' are.
func Chosen(text string) string { return cursor() + Pill(text, Done) }

// Key is a key and what it does, as Keys lists them.
type Key struct{ Key, Does string }

// Keys are the keys at the foot of a screen.
func Keys(ks ...Key) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = White(k.Key) + " " + Muted(k.Does)
	}
	return "  " + strings.Join(parts, dot)
}

// Header is a block's label in pink capitals, and a note two spaces on.
func Header(label, note string) string {
	s := "  " + fg(pink).Bold(true).Render(strings.ToUpper(label))
	if note != "" {
		s += "  " + Muted(note)
	}
	return s
}

// Block is a report's block: its header, then its rows.
func Block(label string, w int, rs ...Row) []string {
	return append([]string{Header(label, "")}, Rows("  ", w, rs...)...)
}

// Summary is how a run ended, its parts after dim dots.
func Summary(parts ...string) string { return "  " + Says(parts...) }

// wordmark is kit's name in block letters.
var wordmark = []string{
	"█  ▄▀  ▀█▀  ▀▀█▀▀",
	"█▀▀▄    █     █  ",
	"█   █  ▄█▄    █  ",
}

// Head is the wordmark, along kit's gradient, with lines stacked beside it
// after a dim rule: what ran, the Mac and when.
func Head(beside ...string) []string {
	out := make([]string, len(wordmark))
	for r, k := range wordmark {
		l := "  " + along(k, 0, ansi.StringWidth(k)) + "  " + Dim("│")
		if r < len(beside) {
			l += "  " + beside[r]
		}
		out[r] = l
	}
	return out
}

// Meta is what Head stacks beside the wordmark: what ran, when there's a
// command, then the Mac and when.
func Meta(what, mac, when string) []string {
	if what == "" {
		return []string{Strong(mac), Muted(when)}
	}
	return []string{Strong(what), Muted(mac), Muted(when)}
}

// Lamp is one of a run's lights: an area, how it stands, and a count once
// it's done.
type Lamp struct {
	Name  string
	State State
	Count string
}

// Lights are a run's areas, each led by its mark, two spaces between: an
// area that needs you or failed a pill; one that didn't finish, as a run
// stopped leaves it, grey.
func Lights(ls ...Lamp) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		n := strings.ToUpper(l.Name)
		switch l.State {
		case NeedsYou, Failed:
			parts[i] = Pill(marks[l.State]+" "+n, l.State)
		case Running:
			parts[i] = Mark(Running) + " " + White(n)
		case Queued:
			parts[i] = Mark(Queued) + " " + Muted(n)
		case Skipped:
			parts[i] = Muted(marks[Skipped] + " " + n)
		default:
			parts[i] = Mark(Done) + " " + Muted(n)
			if l.Count != "" {
				parts[i] += " " + Dim(l.Count)
			}
		}
	}
	return "  " + strings.Join(parts, "  ")
}

// GridCell is how wide a grid's cell is: a mark, a space and a name, and
// the room to the next.
const GridCell = 24

// Cell is a step in a grid: how it stands, its name, and whether it's
// pinned.
type Cell struct {
	State  State
	Name   string
	Pinned bool
}

// Area is a grid's area: its name, and its steps.
type Area struct {
	Name  string
	Cells []Cell
}

// gridLine is a line of a grid: the area it's in, by its place, and the
// first of that area's cells on it and how many.
type gridLine struct{ area, from, n int }

// gridLayout is how a grid w wide lays areas out: how wide the column of
// their names is, then its lines, as many of an area's cells on each as
// fit.
func gridLayout(w int, areas []Area) (label int, lines []gridLine) {
	for _, a := range areas {
		label = max(label, ansi.StringWidth(a.Name))
	}
	across := max((w-4-label)/GridCell, 1)
	for i, a := range areas {
		for from := 0; from < len(a.Cells); from += across {
			lines = append(lines, gridLine{area: i, from: from, n: min(across, len(a.Cells)-from)})
		}
	}
	return label, lines
}

// Grid draws areas w wide, every step a cell GridCell wide, so the marks
// line up in columns (a grid of marks is its own component: rule 3's "no
// columns" is for rows): an area's name in pink capitals at the left of its
// first line, padded to the longest, then as many of its cells as fit, the
// next line under the first cell.
func Grid(w int, areas ...Area) []string {
	label, lines := gridLayout(w, areas)
	out := make([]string, len(lines))
	for i, l := range lines {
		a := areas[l.area]
		lead := strings.Repeat(" ", label)
		if l.from == 0 {
			lead = fg(pink).Bold(true).Render(pad(strings.ToUpper(a.Name), label))
		}
		var line strings.Builder
		line.WriteString("  " + lead + "  ")
		for j, c := range a.Cells[l.from : l.from+l.n] {
			it := cell(c)
			if j < l.n-1 {
				it = pad(it, GridCell)
			}
			line.WriteString(it)
		}
		out[i] = Cut(line.String(), w)
	}
	return out
}

// GridHit is the cell a grid w wide of areas has at column x of its line y,
// both counted from 0: its area's place, and its own among the area's
// cells. A cell runs from its mark to the next cell's. ok is false off
// every cell: in the column of the areas' names, past a line's last cell,
// or off the grid.
func GridHit(w int, areas []Area, x, y int) (area, cell int, ok bool) {
	label, lines := gridLayout(w, areas)
	start := 2 + label + 2
	if y < 0 || y >= len(lines) || x < start {
		return 0, 0, false
	}
	l, j := lines[y], (x-start)/GridCell
	if j >= l.n {
		return 0, 0, false
	}
	return l.area, l.from + j, true
}

// cell is a step in a grid, its mark and its name, cut to fit: muted once
// done, bold while it runs, dim while it waits, in its state's colour when
// it's wrong, grey when it didn't run; a pinned step's name pink, its mark
// as it stands.
func cell(c Cell) string {
	name := Cut(c.Name, GridCell-4)
	switch {
	case c.Pinned:
		return Mark(c.State) + " " + Pink(name)
	case c.State == Running:
		return Mark(Running) + " " + Strong(name)
	case c.State == Queued:
		return Mark(Queued) + " " + Dim(name)
	case c.State == NeedsYou:
		return Mark(NeedsYou) + " " + Orange(name)
	case c.State == Failed:
		return Mark(Failed) + " " + Red(name)
	case c.State == Skipped:
		return Muted(marks[Skipped] + " " + name)
	}
	return Mark(Done) + " " + Muted(name)
}

// pad is s with spaces after it, n cells wide.
func pad(s string, n int) string { return s + strings.Repeat(" ", max(n-ansi.StringWidth(s), 0)) }

// barWidth is the most segments a bar has: past it, each stands for a
// share of the steps.
const barWidth = 32

// Bar is a run's progress, a segment a step, scaled down to barWidth
// segments when there are more: those done along kit's gradient, those
// running cyan, the rest dim. When the run has ended needing you or failed,
// the last segment takes that state's colour.
func Bar(done, running, of int, ended State) string {
	if of > barWidth {
		done, running = done*barWidth/of, (done+running)*barWidth/of-done*barWidth/of
		of = barWidth
	}
	var b strings.Builder
	for i := range of {
		switch {
		case i == done-1 && (ended == NeedsYou || ended == Failed):
			b.WriteString(Words(ended, "▮"))
		case i < done:
			b.WriteString(fg(gradient(float64(i) / float64(max(of-1, 1)))).Render("▮"))
		case i < done+running:
			b.WriteString(Cyan("▮"))
		default:
			b.WriteString(Dim("▮"))
		}
	}
	return b.String()
}

// Edge is a dim line across the whole window, w wide: it parts a screen's
// regions.
func Edge(w int) string { return Dim(strings.Repeat("─", w)) }

// A panel's rule before its row, after the margin; and how much of the
// rule a row cut to fit it leaves after it.
const (
	panelLead  = "── "
	panelLeast = 3
)

// Panel is a part of a screen given to a row: a rule across, w wide with
// the margins, carrying the row, pink when the panel has the focus and dim
// when it hasn't, then what belongs to the row under its name, without a
// line. A panel scrolled back says how many lines are below what it shows
// at its rule's end. A row too long for its rule is cut, some rule kept
// after it.
func Panel(w int, focused bool, below int, r Row) []string {
	rule := Dim
	if focused {
		rule = Pink
	}
	lead, tail := "  "+rule(panelLead), ""
	if below > 0 {
		tail = " " + Muted(fmt.Sprintf("↑ %d more", below)) + " " + rule("──")
	}
	end := w - 2
	row := Cut(line("", r), end-ansi.StringWidth(lead+tail)-1-panelLeast)
	fill := end - ansi.StringWidth(lead+row+tail) - 1
	out := []string{lead + row + " " + rule(strings.Repeat("─", max(fill, 0))) + tail}
	under := strings.Repeat(" ", ansi.StringWidth(lead)+2)
	for _, u := range r.Under {
		out = append(out, Cut(under+u, end))
	}
	return out
}

// Rule is a line across, along kit's gradient, w wide with the margin.
func Rule(w int) string {
	n := max(w-2, 1)
	return "  " + along(strings.Repeat("─", n), 0, n)
}

// Ran is a command a step ran: the command as kit ran it, then how it ended
// and how long it took, red when it failed. A command too long for w is
// cut in the middle, so how it ended still shows.
func Ran(command string, exit int, took string, w int) string {
	code := fmt.Sprintf("exit %d", exit)
	command = Squeeze(command, w-ansi.StringWidth(" · "+code+" · "+took))
	if exit != 0 {
		return Red(command) + dot + Red(code) + dot + Red(took)
	}
	return Dim(command) + dot + Dim(code) + dot + Dim(took)
}

// Squeeze cuts s to n cells in the middle, keeping its start and its end.
func Squeeze(s string, n int) string {
	r := []rune(s)
	if len(r) <= n || n < 2 {
		return s
	}
	head := n * 2 / 5
	return string(r[:head]) + "…" + string(r[len(r)-(n-head-1):])
}

// Cut cuts a line to w cells, an ellipsis ending it when it's cut.
func Cut(s string, w int) string { return ansi.Truncate(s, w, "…") }

// Colours are kit's colours, as the boot's arrival in the terminal turns
// its noise from amber into them: pink, a purple, a blue, cyan and orange.
var Colours = []color.Color{pink, lipgloss.Color("#B57BFF"), lipgloss.Color("#5B8CFF"), cyan, orange}

// Gradient is the colour t along kit's gradient, from pink (0) to orange
// (1).
func Gradient(t float64) color.Color { return gradient(t) }

// Paint is text in the colour c.
func Paint(c color.Color, text string) string { return fg(c).Render(text) }

// gradient is the colour t along kit's gradient, from pink (0) to orange
// (1).
func gradient(t float64) color.Color {
	a, b := rgb(pink), rgb(orange)
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", m(a.R, b.R), m(a.G, b.G), m(a.B, b.B)))
}

func rgb(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
}

// along colours each cell of s by its place along kit's gradient, n cells
// long, starting at from; spaces stay plain.
func along(s string, from, n int) string {
	var b strings.Builder
	i := 0
	for _, r := range s {
		if r == ' ' {
			b.WriteRune(' ')
		} else {
			b.WriteString(fg(gradient(float64(from+i) / float64(max(n-1, 1)))).Render(string(r)))
		}
		i++
	}
	return b.String()
}
