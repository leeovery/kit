package rendertest

import (
	"fmt"
	"strings"
)

// Screen is what's left on a terminal once raw is written to it: text
// written over what's there, a carriage return, a new line, the cursor
// moved up or down or put in place, a line cleared to its end or whole, the screen cleared
// below; other controls change nothing on it. The alternate screen is a
// screen of its own, the main one kept beneath it: what's left is the one
// showing.
func Screen(raw string) string {
	return written(raw).text(false)
}

// Styled is what's left on a terminal once raw is written to it, as Screen
// has it, each cell with its style: its glyph's colours, and whether it's
// bold, faint, italic, underlined or reversed, as the terminal shows it. A
// line says each style once, where it starts, as one sequence, whichever
// sequences set it, and ends with the style reset, so two screens are the
// same text only when they're the same cell for cell.
func Styled(raw string) string {
	return written(raw).text(true)
}

// written is the screen showing once raw is written to a terminal.
func written(raw string) *term {
	main := &term{lines: [][]cell{nil}}
	t := main
	var st style
	for len(raw) > 0 {
		switch {
		case raw[0] == '\n':
			t.row++
			t.col = 0
			t.grow()
			raw = raw[1:]
		case raw[0] == '\r':
			t.col = 0
			raw = raw[1:]
		case strings.HasPrefix(raw, "\x1b]"):
			raw = raw[strings.IndexByte(raw, '\a')+1:]
		case strings.HasPrefix(raw, "\x1b["):
			end := 2
			for end < len(raw) && (raw[end] < 0x40 || raw[end] > 0x7e) {
				end++
			}
			params, final := raw[2:end], raw[end]
			raw = raw[end+1:]
			if final == 'm' {
				st = st.set(params)
				continue
			}
			n := 1
			if _, err := fmt.Sscanf(params, "%d", &n); err != nil {
				n = 1
			}
			switch {
			case params == "?1049" && final == 'h':
				t = &term{lines: [][]cell{nil}}
			case params == "?1049" && final == 'l':
				t = main
			case final == 'A':
				t.row = max(t.row-n, 0)
			case final == 'B':
				t.row += n
				t.grow()
			case final == 'H':
				row, col := 1, 1
				_, _ = fmt.Sscanf(params, "%d;%d", &row, &col)
				t.row, t.col = row-1, col-1
				t.grow()
			case final == 'K' && params == "2":
				t.lines[t.row] = nil
			case final == 'K':
				t.lines[t.row] = t.lines[t.row][:min(t.col, len(t.lines[t.row]))]
			case final == 'J' && params == "2":
				t.lines, t.row, t.col = [][]cell{nil}, 0, 0
			case final == 'J':
				t.lines[t.row] = t.lines[t.row][:min(t.col, len(t.lines[t.row]))]
				t.lines = t.lines[:t.row+1]
			}
		default:
			i := strings.IndexAny(raw, "\n\r\x1b")
			if i < 0 {
				i = len(raw)
			}
			t.put(raw[:i], st)
			raw = raw[i:]
		}
	}
	return t
}

// term is a screen's lines, a cell at a time, and where the cursor is.
type term struct {
	lines    [][]cell
	row, col int
}

// cell is a glyph, and the style it's drawn in.
type cell struct {
	r     rune
	style style
}

// grow has the screen as many lines as the cursor's down to.
func (t *term) grow() {
	for t.row >= len(t.lines) {
		t.lines = append(t.lines, nil)
	}
}

// put writes text at the cursor, in st, over what's there.
func (t *term) put(text string, st style) {
	line := t.lines[t.row]
	for len(line) < t.col {
		line = append(line, cell{r: ' '})
	}
	for _, r := range text {
		if t.col < len(line) {
			line[t.col] = cell{r, st}
		} else {
			line = append(line, cell{r, st})
		}
		t.col++
	}
	t.lines[t.row] = line
}

// text is the screen's lines, each style where it starts when styled.
func (t *term) text(styled bool) string {
	out := make([]string, len(t.lines))
	for i, line := range t.lines {
		var b strings.Builder
		var at style
		for _, c := range line {
			if styled && c.style != at {
				b.WriteString(c.style.sgr())
				at = c.style
			}
			b.WriteRune(c.r)
		}
		if at != (style{}) {
			b.WriteString(style{}.sgr())
		}
		out[i] = b.String()
	}
	return strings.Join(out, "\n")
}

// style is how a cell's drawn: its colours, each as the parameters that
// set it, and its attributes.
type style struct {
	fg, bg                                   string
	bold, faint, italic, underline, reversed bool
}

// set is st with an SGR sequence's parameters applied.
func (st style) set(params string) style {
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		switch p := ps[i]; p {
		case "", "0":
			st = style{}
		case "1":
			st.bold = true
		case "2":
			st.faint = true
		case "3":
			st.italic = true
		case "4":
			st.underline = true
		case "7":
			st.reversed = true
		case "22":
			st.bold, st.faint = false, false
		case "23":
			st.italic = false
		case "24":
			st.underline = false
		case "27":
			st.reversed = false
		case "39":
			st.fg = ""
		case "49":
			st.bg = ""
		case "38", "48":
			n := 3
			if i+1 < len(ps) && ps[i+1] == "2" {
				n = 5
			}
			colour := strings.Join(ps[i:min(i+n, len(ps))], ";")
			if p == "38" {
				st.fg = colour
			} else {
				st.bg = colour
			}
			i += n - 1
		default:
			switch n := len(p); {
			case n == 2 && (p[0] == '3' || p[0] == '9') && p[1] <= '7':
				st.fg = p
			case n == 2 && p[0] == '4' && p[1] <= '7', n == 3 && strings.HasPrefix(p, "10") && p[2] <= '7':
				st.bg = p
			}
		}
	}
	return st
}

// sgr is the one sequence that sets st from nothing.
func (st style) sgr() string {
	ps := []string{"0"}
	for _, a := range []struct {
		on bool
		p  string
	}{{st.bold, "1"}, {st.faint, "2"}, {st.italic, "3"}, {st.underline, "4"}, {st.reversed, "7"}} {
		if a.on {
			ps = append(ps, a.p)
		}
	}
	for _, colour := range []string{st.fg, st.bg} {
		if colour != "" {
			ps = append(ps, colour)
		}
	}
	return "\x1b[" + strings.Join(ps, ";") + "m"
}
