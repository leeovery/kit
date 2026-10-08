package ask

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"
)

// Terminal is where kit asks: the keys come from In, what's drawn goes to
// Out, and Size says how many columns and lines it has.
type Terminal struct {
	In   io.Reader
	Out  io.Writer
	Size func() (width, height int)
}

// The terminal's controls asking uses: the cursor hidden and shown, an
// update shown whole where the terminal can, and pastes marked.
const (
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
	syncStart  = "\x1b[?2026h"
	syncEnd    = "\x1b[?2026l"
	pasteOn    = "\x1b[?2004h"
	pasteOff   = "\x1b[?2004l"
)

// model is something asked in place: its lines at a size, never more than
// the terminal's lines, so it can always be drawn over; what a key does to
// it, and whether that's the end; and what it leaves on screen then.
type model interface {
	view(width, height int) []string
	update(k key) (done bool)
	leaves(width int) []string
}

// show asks m at t, under what's on screen: drawn, then drawn over after
// each key and when the terminal's resized, till it's done, when its lines
// are cleared and what it leaves is written in their place. It reads keys
// one at a time, in the terminal's raw mode, which it puts back after.
func show(ctx context.Context, t Terminal, m model) error {
	restore := t.raw()
	defer restore()
	keys, stop := t.keys()
	defer stop()
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	defer signal.Stop(resized)
	d := &drawer{out: t.Out}
	d.write(hideCursor + pasteOn)
	defer d.write(pasteOff + showCursor)
	draw := func() {
		w, h := t.size()
		d.draw(m.view(w, h))
	}
	draw()
	for {
		select {
		case <-ctx.Done():
			d.clear()
			return ctx.Err()
		case <-resized:
			draw()
		case k, ok := <-keys:
			if !ok {
				d.clear()
				return io.ErrUnexpectedEOF
			}
			if !m.update(k) {
				draw()
				continue
			}
			d.clear()
			w, _ := t.size()
			d.print(m.leaves(w))
			return nil
		}
	}
}

// size is the terminal's columns, up to kit's width, and its lines.
func (t Terminal) size() (width, height int) {
	width, height = 80, 24
	if t.Size != nil {
		width, height = t.Size()
	}
	return min(max(width, 40), 80), max(height, 8)
}

// raw puts the terminal in raw mode, when In is one, and returns what puts
// it back.
func (t Terminal) raw() (restore func()) {
	f, ok := t.In.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return func() {}
	}
	state, err := term.MakeRaw(f.Fd())
	if err != nil {
		return func() {}
	}
	return func() { _ = term.Restore(f.Fd(), state) }
}

// keys reads keys from In as they come, and returns them, and what stops
// reading: a read waiting for a key is cancelled, so the next question's
// keys aren't taken.
func (t Terminal) keys() (<-chan key, func()) {
	in := t.In
	var cancel func()
	if f, ok := t.In.(*os.File); ok {
		if r, err := cancelreader.NewReader(f); err == nil {
			in, cancel = r, func() { r.Cancel(); _ = r.Close() }
		}
	}
	keys, done := make(chan key), make(chan struct{})
	go func() {
		defer close(keys)
		buf := make([]byte, 1024)
		var pending []byte
		for {
			n, err := in.Read(buf)
			var ks []key
			ks, pending = parse(append(pending, buf[:n]...))
			for _, k := range ks {
				select {
				case keys <- k:
				case <-done:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return keys, func() {
		close(done)
		if cancel != nil {
			cancel()
		}
	}
}

// key is a key pressed: its name, as up, down, enter, esc, ctrl+c or
// backspace; or text typed, or pasted.
type key struct {
	name  string
	text  string
	paste bool
}

// parse turns what the terminal sent into keys, and returns what's left of
// a paste still coming: the arrows, as either form sends them, enter,
// escape alone, Ctrl+C, backspace, a paste whole, and text; other controls
// and sequences are dropped.
func parse(b []byte) (keys []key, rest []byte) {
	for len(b) > 0 {
		switch {
		case strings.HasPrefix(string(b), "\x1b[200~"):
			end := strings.Index(string(b), "\x1b[201~")
			if end < 0 {
				return keys, b
			}
			keys = append(keys, key{text: string(b[6:end]), paste: true})
			b = b[end+6:]
		case b[0] == 0x1b && len(b) == 1:
			keys, b = append(keys, key{name: "esc"}), nil
		case b[0] == 0x1b && (b[1] == '[' || b[1] == 'O'):
			i := 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++
			}
			if i == len(b) {
				return keys, b
			}
			switch b[i] {
			case 'A':
				keys = append(keys, key{name: "up"})
			case 'B':
				keys = append(keys, key{name: "down"})
			}
			b = b[i+1:]
		case b[0] == 0x1b:
			keys, b = append(keys, key{name: "esc"}), b[1:]
		case b[0] == '\r' || b[0] == '\n':
			keys, b = append(keys, key{name: "enter"}), b[1:]
		case b[0] == 0x03:
			keys, b = append(keys, key{name: "ctrl+c"}), b[1:]
		case b[0] == 0x7f || b[0] == 0x08:
			keys, b = append(keys, key{name: "backspace"}), b[1:]
		case b[0] < 0x20:
			b = b[1:]
		default:
			r, n := utf8.DecodeRune(b)
			if r == utf8.RuneError && !utf8.FullRune(b) {
				return keys, b
			}
			keys, b = append(keys, key{text: string(r)}), b[n:]
		}
	}
	return keys, nil
}

// is is whether k is any of names, by name or, for a letter, as typed.
func (k key) is(names ...string) bool {
	for _, n := range names {
		if k.name == n || !k.paste && k.name == "" && k.text == n {
			return true
		}
	}
	return false
}

// drawer draws lines in place, under what's on screen: drawn over each time,
// the cursor left on the last.
type drawer struct {
	out   io.Writer
	lines int
}

func (d *drawer) write(s string) {
	_, _ = io.WriteString(d.out, s)
}

// draw draws lines over the last: back to the first line, each written over
// in place, and what's left of the last cleared.
func (d *drawer) draw(lines []string) {
	var b strings.Builder
	b.WriteString(syncStart + "\r")
	if d.lines > 1 {
		fmt.Fprintf(&b, "\x1b[%dA", d.lines-1)
	}
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(l + "\x1b[K")
	}
	if len(lines) < d.lines {
		b.WriteString("\x1b[J")
	}
	b.WriteString(syncEnd)
	d.lines = len(lines)
	d.write(b.String())
}

// clear takes the lines drawn down, leaving the cursor where the first was.
func (d *drawer) clear() {
	if d.lines == 0 {
		return
	}
	s := "\r"
	if d.lines > 1 {
		s += fmt.Sprintf("\x1b[%dA", d.lines-1)
	}
	d.write(s + "\x1b[J")
	d.lines = 0
}

// print writes lines where they'll stay, each ended.
func (d *drawer) print(lines []string) {
	for _, l := range lines {
		d.write(l + "\r\n")
	}
}
