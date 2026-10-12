package render_test

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/colorprofile"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/render"
)

// keyboard is the terminal's keys as a test presses them, read while a
// face listens, the terminal in its raw mode meanwhile; and the run, as
// they stop it.
type keyboard struct {
	mu      sync.Mutex
	keys    chan ask.Key
	raw     bool
	started int
	halted  int
}

func (k *keyboard) listen() (<-chan ask.Key, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	keys := make(chan ask.Key)
	k.keys, k.raw = keys, true
	k.started++
	return keys, sync.OnceFunc(func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		k.raw = false
		close(keys)
	})
}

func (k *keyboard) halt() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.halted++
}

// isRaw is whether keys are being read, the terminal in its raw mode.
func (k *keyboard) isRaw() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.raw
}

// listens is how many times keys were read.
func (k *keyboard) listens() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.started
}

// stops is how many times the keys stopped the run.
func (k *keyboard) stops() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.halted
}

// press presses keys, one at a time: each taken, and what it changed drawn,
// before the next is, as a key that does nothing follows the last.
func (k *keyboard) press(t *testing.T, keys ...ask.Key) {
	t.Helper()
	k.mu.Lock()
	ch, raw := k.keys, k.raw
	k.mu.Unlock()
	if !raw {
		t.Fatal("keys pressed while the face isn't reading them")
	}
	for _, key := range append(keys, ask.Key{Name: "nothing"}) {
		ch <- key
	}
}

var (
	keyQ     = ask.Key{Text: "q"}
	keyUp    = ask.Key{Name: "up"}
	keyDown  = ask.Key{Name: "down"}
	keyEnter = ask.Key{Name: "enter"}
	keyEsc   = ask.Key{Name: "esc"}
	keyCtrlC = ask.Key{Name: "ctrl+c"}
)

// foot is a screen's foot: its last n lines, trailing spaces trimmed.
func foot(screen string, n int) []string {
	ls := lines(screen)
	out := ls[len(ls)-n:]
	for i, l := range out {
		out[i] = strings.TrimRight(l, " ")
	}
	return out
}

// q asks, at the screen's foot, whether to stop, in what needs you's place,
// naming the steps at work, and the keys answer it; the run goes on
// meanwhile. Keep going, or esc, puts the foot back, the run going on;
// Stop stops it, as ctrl+c does.
func TestApplyingAsksBeforeStopping(t *testing.T) {
	face, out, kb := applyAt(160, 64, 13*time.Minute+2*time.Second, waitingOnYou())
	defer func() { _ = face.Close() }()
	running := foot(screen(out.String()), 7)
	asking := []string{
		"",
		"  ▲ Stop applying?  Homebrew formulae and App settings stop where they are",
		"  │ ❯ Keep going",
		"  │   Stop",
		"",
		"  ↑↓ choose · enter decide · esc keep going",
	}
	kb.press(t, keyQ)
	got := screen(out.String())
	golden(t, "apply-screen-stopping.golden", got+"\n")
	if f := foot(got, 6); !slices.Equal(f, asking) {
		t.Errorf("q, the foot is\n%s\nwant\n%s", strings.Join(f, "\n"), strings.Join(asking, "\n"))
	}
	face.Emit(event.Output{Step: stepName("Drift", "Homebrew formulae"), Command: "brew install --formula ffmpeg", Line: "==> Pouring formula 17"})
	time.Sleep(300 * time.Millisecond)
	if got := screen(out.String()); !strings.Contains(got, "│ ==> Pouring formula 17") || !slices.Equal(foot(got, 6), asking) {
		t.Errorf("asking, the screen is\n%s\nwant the run going on, the question asked", got)
	}
	for _, tt := range []struct {
		name  string
		keys  []ask.Key
		stops int
	}{
		{"esc", []ask.Key{keyEsc}, 0},
		{"Keep going", []ask.Key{keyEnter}, 0},
		{"Stop", []ask.Key{keyDown, keyEnter}, 1},
		{"round to Keep going", []ask.Key{keyDown, keyUp, keyEnter}, 1},
	} {
		kb.press(t, append([]ask.Key{keyQ}, tt.keys...)...)
		if f := foot(screen(out.String()), 7); !slices.Equal(f, running) {
			t.Errorf("%s, the foot is\n%s\nwant it as it was\n%s", tt.name, strings.Join(f, "\n"), strings.Join(running, "\n"))
		}
		if n := kb.stops(); n != tt.stops {
			t.Errorf("after %s, the run was stopped %d times, want %d", tt.name, n, tt.stops)
		}
	}
}

// ctrl+c, a key in the terminal's raw mode, not an interrupt, stops the
// run at once, asking or not.
func TestApplyingStopsAtCtrlC(t *testing.T) {
	face, _, kb := applyAt(160, 64, time.Minute, sixMinutesIn())
	defer func() { _ = face.Close() }()
	kb.press(t, keyCtrlC)
	if n := kb.stops(); n != 1 {
		t.Errorf("ctrl+c stopped the run %d times, want once", n)
	}
	kb.press(t, keyQ, keyCtrlC)
	if n := kb.stops(); n != 2 {
		t.Errorf("ctrl+c, asked whether to stop, stopped the run %d times in all, want twice", n)
	}
}

// slotLines are the lines a step's slot in the log shows, under its row.
func slotLines(screen, title string) []string {
	ls := lines(screen)
	for i, l := range ls {
		mark, name, ok := strings.Cut(strings.TrimPrefix(l, "  "), " ")
		if !strings.HasPrefix(l, "  ") || !ok || utf8.RuneCountInString(mark) != 1 || !strings.HasPrefix(name, title+"  ") {
			continue
		}
		var out []string
		for _, u := range ls[i+1:] {
			text, ok := strings.CutPrefix(u, "  │ ")
			if !ok {
				break
			}
			out = append(out, text)
		}
		return out
	}
	return nil
}

// ↑ scrolls the log back a line, every slot showing its lines ending that
// many lines before its newest, or its first lines, when it has no more;
// no further back than the slot with the most lines goes. ↓ scrolls
// forth, back to the newest.
func TestApplyingScrollsItsLog(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, sixMinutesIn())
	defer func() { _ = face.Close() }()
	shows := func(title string, first, last int) []string {
		var want []string
		for i := first; i <= last; i++ {
			want = append(want, strings.Replace(title, "#", strconv.Itoa(i), 1))
		}
		return want
	}
	check := func(when string, formulae, casks []string) {
		t.Helper()
		got := screen(out.String())
		if f := slotLines(got, "Homebrew formulae"); !slices.Equal(f, formulae) {
			t.Errorf("%s, Homebrew formulae's slot shows\n%s\nwant\n%s", when, strings.Join(f, "\n"), strings.Join(formulae, "\n"))
		}
		if c := slotLines(got, "Homebrew casks"); !slices.Equal(c, casks) {
			t.Errorf("%s, Homebrew casks' slot shows\n%s\nwant\n%s", when, strings.Join(c, "\n"), strings.Join(casks, "\n"))
		}
	}
	formula, cask := "==> Pouring formula #", "==> Installing Cask #"
	check("at first", shows(formula, 8, 16), shows(cask, 2, 10))
	kb.press(t, keyUp, keyUp, keyUp)
	check("↑ three times", shows(formula, 5, 13), shows(cask, 1, 9))
	kb.press(t, slices.Repeat([]ask.Key{keyUp}, 20)...)
	check("↑ past the first", shows(formula, 1, 9), shows(cask, 1, 9))
	kb.press(t, keyDown)
	check("↓ once from there", shows(formula, 2, 10), shows(cask, 1, 9))
	kb.press(t, slices.Repeat([]ask.Key{keyDown}, 20)...)
	check("↓ past the newest", shows(formula, 8, 16), shows(cask, 2, 10))
}

// Without keys to read, the screen's foot says what stops the run: the
// terminal's interrupt.
func TestApplyingWithoutKeys(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 160, 64 })
	for _, e := range sixMinutesIn() {
		face.Emit(e)
	}
	got := screen(out.String())
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	if f := foot(got, 1); f[0] != "  ctrl+c stop" {
		t.Errorf("without keys, the screen's foot is %q, want ctrl+c stop", f[0])
	}
}

// Kit panicking with applying's screen up, the face puts the terminal back
// at once: its own screen, the cursor shown, its keys no longer read, so
// the terminal's out of its raw mode; and draws nothing after.
func TestApplyingUnwinds(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, sixMinutesIn())
	face.Unwind()
	raw := out.String()
	if !strings.HasSuffix(raw, "\x1b[?1049l\x1b[?25h") || kb.isRaw() {
		t.Errorf("unwound, the face ended %q, raw: %v; want the terminal's own screen and the cursor back, not raw", raw[max(len(raw)-40, 0):], kb.isRaw())
	}
	time.Sleep(300 * time.Millisecond)
	if out.String() != raw {
		t.Error("unwound, the face drew again")
	}
}
