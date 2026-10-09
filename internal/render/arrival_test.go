package render_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// wordmarkCells is how many cells the wordmark takes, big: four by two a
// pixel.
func wordmarkCells() int {
	n := 0
	for y := range 6 {
		for x := range 17 {
			if look.WordmarkPixel(x, y) {
				n += 8
			}
		}
	}
	return n
}

// stripped are lines without their colours, trailing spaces trimmed.
func stripped(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return strings.Join(out, "\n")
}

// The arrival greets you, quietly, in amber, till a key.
func TestArrivalGreets(t *testing.T) {
	a := render.NewArrival(true, "bootstrap", "laptop", at)
	got := stripped(a.Full(120, 30))
	if !strings.Contains(got, "hello again") || !strings.Contains(got, "press any key") || strings.Contains(got, "█") {
		t.Errorf("the greeting is\n%s\nwant hello again, press any key, and no wordmark yet", got)
	}
	if a.Key(ask.Key{Text: "x"}) || a.Stopped() {
		t.Error("a key at the greeting ended it, or stopped it: want it to start the decode")
	}
	if stop := render.NewArrival(true, "bootstrap", "laptop", at); !stop.Key(ask.Key{Name: "ctrl+c"}) || !stop.Stopped() {
		t.Error("ctrl+c didn't stop it")
	}
}

// The decode starts in amber and turns to kit's colours as the wordmark
// locks in, in kit's gradient.
func TestArrivalDecodesIntoColour(t *testing.T) {
	a := render.NewArrival(true, "bootstrap", "laptop", at)
	pink := "38;2;255;79;154"
	if first := strings.Join(a.DecodeAt(120, 30, 0), "\n"); strings.Contains(first, pink) {
		t.Error("the first frame has kit's pink in it: want amber alone")
	}
	last := a.DecodeAt(120, 30, 63)
	if raw := strings.Join(last, "\n"); !strings.Contains(raw, pink) || !strings.Contains(raw, "38;2;255;162;76") {
		t.Error("the last frame isn't in kit's gradient, pink to orange")
	}
	if n, want := strings.Count(stripped(last), "█"), wordmarkCells(); n != want {
		t.Errorf("the last frame has %d of the wordmark's cells: want it whole, %d", n, want)
	}
}

// The wordmark shrinks into the header's place: at the last step, the
// header's own wordmark, a blank line above it, two cells in.
func TestArrivalShrinksIntoTheHeader(t *testing.T) {
	a := render.NewArrival(true, "bootstrap", "laptop", at)
	got := strings.Split(stripped(a.ShrinkAt(120, 30, 13)), "\n")
	want := strings.Split(stripped(look.Head()), "\n")
	for i, w := range want {
		w = strings.TrimSuffix(strings.TrimRight(w, " "), "│")
		if strings.TrimRight(got[i+1], " ") != strings.TrimRight(w, " ") {
			t.Errorf("line %d of the last step is %q, want %q", i+1, got[i+1], w)
		}
	}
	if first := stripped(a.ShrinkAt(120, 30, 0)); strings.Count(first, "█") != wordmarkCells() {
		t.Errorf("the first step isn't the big wordmark:\n%s", first)
	}
}

// Played through, a key skipping to the end, the arrival lands on the
// header, as the real screen has it, and leaves it in the window when it
// keeps it.
func TestArrivalLands(t *testing.T) {
	in, keys := io.Pipe()
	defer func() { _ = keys.Close() }()
	var out syncBuffer
	a := render.NewArrival(true, "bootstrap", "laptop", at).KeepHeader()
	done := make(chan error, 1)
	go func() {
		done <- a.Play(t.Context(), ask.Terminal{In: in, Out: &out, Size: func() (int, int) { return 120, 30 }})
	}()
	_, _ = keys.Write([]byte("x"))
	time.Sleep(100 * time.Millisecond)
	_, _ = keys.Write([]byte("y"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("it didn't land")
	}
	header := append([]string{""}, look.Head(look.Meta("bootstrap", "laptop", "Fri 2 Jan · 03:04")...)...)
	if !a.Landed() || stripped(a.Leaves(120)) != stripped(header) {
		t.Errorf("it leaves\n%s\nwant the header\n%s", stripped(a.Leaves(120)), stripped(header))
	}
	_, after, ok := strings.Cut(out.String(), "\x1b[?1049l")
	if !ok || !strings.Contains(ansi.Strip(after), "│  bootstrap\r\n") {
		t.Errorf("after the window's own screen came back, it wrote %q: want the header", after)
	}
	if !render.NewArrival(true, "bootstrap", "laptop", at).Landed() {
		return
	}
	t.Error("an arrival not played says it landed")
}

// Not keeping the header, as kit splash doesn't, it leaves nothing.
func TestArrivalLeavesNothingUnlessKept(t *testing.T) {
	a := render.NewArrival(true, "splash", "laptop", at)
	if a.Leaves(120) != nil || a.View(120, 30) != nil {
		t.Error("it drew something in place")
	}
}
