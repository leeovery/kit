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
	main := &term{lines: []string{""}}
	t := main
	for len(raw) > 0 {
		switch {
		case raw[0] == '\n':
			t.row++
			t.col = 0
			for t.row >= len(t.lines) {
				t.lines = append(t.lines, "")
			}
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
			n := 1
			if _, err := fmt.Sscanf(params, "%d", &n); err != nil {
				n = 1
			}
			switch {
			case params == "?1049" && final == 'h':
				t = &term{lines: []string{""}}
			case params == "?1049" && final == 'l':
				t = main
			case final == 'A':
				t.row = max(t.row-n, 0)
			case final == 'B':
				t.row += n
				for t.row >= len(t.lines) {
					t.lines = append(t.lines, "")
				}
			case final == 'H':
				row, col := 1, 1
				_, _ = fmt.Sscanf(params, "%d;%d", &row, &col)
				t.row, t.col = row-1, col-1
				for t.row >= len(t.lines) {
					t.lines = append(t.lines, "")
				}
			case final == 'K' && params == "2":
				t.lines[t.row] = ""
			case final == 'K':
				t.lines[t.row] = cells(t.lines[t.row], t.col)
			case final == 'J' && params == "2":
				t.lines, t.row, t.col = []string{""}, 0, 0
			case final == 'J':
				t.lines[t.row] = cells(t.lines[t.row], t.col)
				t.lines = t.lines[:t.row+1]
			}
		default:
			i := strings.IndexAny(raw, "\n\r\x1b")
			if i < 0 {
				i = len(raw)
			}
			t.put(raw[:i])
			raw = raw[i:]
		}
	}
	return strings.Join(t.lines, "\n")
}

// term is a screen's lines, and where the cursor is.
type term struct {
	lines    []string
	row, col int
}

// put writes text at the cursor, over what's there.
func (t *term) put(text string) {
	line := []rune(t.lines[t.row])
	for len(line) < t.col {
		line = append(line, ' ')
	}
	for _, r := range text {
		if t.col < len(line) {
			line[t.col] = r
		} else {
			line = append(line, r)
		}
		t.col++
	}
	t.lines[t.row] = string(line)
}

// cells is line's first n cells.
func cells(line string, n int) string {
	r := []rune(line)
	return string(r[:min(n, len(r))])
}
