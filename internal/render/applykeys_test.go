package render_test

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// keyboard is the terminal's keys as a test presses them, read while a
// face listens, the terminal in its raw mode meanwhile; and the run, as
// they stop it. stopped is what the face had written to out when it last
// stopped reading them.
type keyboard struct {
	mu      sync.Mutex
	keys    chan ask.Key
	raw     bool
	started int
	halted  int
	out     *syncBuffer
	stopped string
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
		if k.out != nil {
			k.stopped = k.out.String()
		}
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
	keyQ        = ask.Key{Text: "q"}
	keyUp       = ask.Key{Name: "up"}
	keyDown     = ask.Key{Name: "down"}
	keyEnter    = ask.Key{Name: "enter"}
	keyEsc      = ask.Key{Name: "esc"}
	keyCtrlC    = ask.Key{Name: "ctrl+c"}
	keyTab      = ask.Key{Name: "tab"}
	keyShiftTab = ask.Key{Name: "shift+tab"}
	keyA        = ask.Key{Text: "a"}
	keyO        = ask.Key{Text: "o"}
	keyL        = ask.Key{Text: "l"}
)

// click is the mouse's left button pressed at column x of line y; wheel the
// wheel turned up or down at line y.
func click(x, y int) ask.Key { return ask.Key{Name: "click", X: x, Y: y} }

func wheel(dir string, y int) ask.Key { return ask.Key{Name: "wheel " + dir, X: 40, Y: y} }

// The mouse's buttons and wheel reported, in SGR's form, and no longer.
const (
	mouseOn  = "\x1b[?1000h\x1b[?1006h"
	mouseOff = "\x1b[?1006l\x1b[?1000l"
)

// mouseReported is whether, after raw, the terminal reports the mouse.
func mouseReported(raw string) bool {
	return strings.LastIndex(raw, mouseOn) > strings.LastIndex(raw, mouseOff)
}

// mouseOffFirst is whether the face stopped the mouse being reported before
// it last stopped reading keys, the terminal leaving its raw mode.
func mouseOffFirst(k *keyboard) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.stopped != "" && !mouseReported(k.stopped)
}

// inOrder is whether parts are all in s, each after the last.
func inOrder(s string, parts ...string) bool {
	for _, part := range parts {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return true
}

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
	if got := screen(out.String()); !strings.Contains(got, "\n       ==> Pouring formula 17\n") || !slices.Equal(foot(got, 6), asking) {
		t.Errorf("asking, the screen is\n%s\nwant the run going on, the question asked", got)
	}
	// The mouse is no answer: neither the wheel nor a click, on the grid or
	// where q would be typed, moves the cursor, decides, or pins.
	before := screen(out.String())
	kb.press(t, wheel("down", 40), wheel("up", 40), click(13, 9), click(112, 4))
	if got := screen(out.String()); got != before {
		t.Errorf("asking, the mouse changed the screen to\n%s\nwant it as it was\n%s", got, before)
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

// answered is what the keys answered a step by hand's card with.
type answered struct {
	step   string
	answer hand.Answer
}

// o, enter and l answer the card up, passing the answer to its step, each
// only when the card's keys offer it: o when kit opened an app for it,
// enter when kit can't tell by itself that it's done, l always. With no
// card up, or once its step's finished, they answer nothing.
func TestApplyingCardKeys(t *testing.T) {
	for _, tt := range []struct {
		name   string
		events []event.Event
		want   []answered
	}{
		{"kit opened it, and tells", cardUp(signIn), []answered{{signIn.Step, hand.Open}, {signIn.Step, hand.Later}}},
		{"kit opened it", cardUp(handCard), []answered{{handCard.Step, hand.Open}, {handCard.Step, hand.Done}, {handCard.Step, hand.Later}}},
		{"neither", cardUp(paperCard), []answered{{paperCard.Step, hand.Done}, {paperCard.Step, hand.Later}}},
		{"no card", casksFailed(), nil},
		{"its step finished", append(cardUp(handCard), event.StepFinished{Time: at, Step: handCard.Step, Result: check.Result{State: check.OK, Summary: "done"}}), nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			face, _, kb := applyAt(160, 64, time.Minute, tt.events)
			defer func() { _ = face.Close() }()
			var mu sync.Mutex
			var got []answered
			face.Hands(func(step string, a hand.Answer) bool {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, answered{step, a})
				return true
			})
			kb.press(t, keyO, keyEnter, keyL)
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(got, tt.want) {
				t.Errorf("o, enter and l answered %v; want %v", got, tt.want)
			}
			if n := kb.stops(); n != 0 {
				t.Errorf("answering the card stopped the run %d times", n)
			}
		})
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

// shows are lines numbered first to last, the number in place of #.
func shows(line string, first, last int) []string {
	var want []string
	for i := first; i <= last; i++ {
		want = append(want, strings.Replace(line, "#", strconv.Itoa(i), 1))
	}
	return want
}

const (
	formula = "==> Pouring formula #"
	cask    = "==> Installing Cask #"
)

// ↑ scrolls the panel with the focus back a line, its lines ending that
// many before its newest, or its first lines, when it has no more; ↓
// scrolls it forth, back to the newest; no other panel moves. Each panel
// keeps how far it's scrolled when the focus moves on.
func TestApplyingScrollsThePanelWithTheFocus(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, withHistory())
	defer func() { _ = face.Close() }()
	check := func(when string, formulae, casks []string) {
		t.Helper()
		got := screen(out.String())
		if _, f := panelOf(got, "Homebrew formulae"); !slices.Equal(f, formulae) {
			t.Errorf("%s, Homebrew formulae's panel shows\n%s\nwant\n%s", when, strings.Join(f, "\n"), strings.Join(formulae, "\n"))
		}
		if _, c := panelOf(got, "Homebrew casks"); !slices.Equal(c, casks) {
			t.Errorf("%s, Homebrew casks' panel shows\n%s\nwant\n%s", when, strings.Join(c, "\n"), strings.Join(casks, "\n"))
		}
	}
	check("at first", shows(formula, 34, 46), shows(cask, 18, 30))
	kb.press(t, keyUp, keyUp, keyUp)
	check("↑ three times", shows(formula, 31, 43), shows(cask, 18, 30))
	kb.press(t, slices.Repeat([]ask.Key{keyUp}, 40)...)
	check("↑ past the first", shows(formula, 1, 13), shows(cask, 18, 30))
	kb.press(t, keyDown)
	check("↓ once from there", shows(formula, 2, 14), shows(cask, 18, 30))
	kb.press(t, keyTab, keyUp)
	check("tab, then ↑", shows(formula, 2, 14), shows(cask, 17, 29))
	kb.press(t, append([]ask.Key{keyShiftTab}, slices.Repeat([]ask.Key{keyDown}, 40)...)...)
	check("back, then ↓ past the newest", shows(formula, 34, 46), shows(cask, 17, 29))
}

// The focus: on the first panel at first, its rule pink, every other's
// dim; tab moves it to the next, round from the last to the first, and
// shift+tab back.
func TestApplyingMovesTheFocus(t *testing.T) {
	face, out, kb := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, sixMinutesIn())
	defer func() { _ = face.Close() }()
	for _, tt := range []struct {
		keys []ask.Key
		want string
	}{
		{nil, "Homebrew formulae"},
		{[]ask.Key{keyTab}, "Homebrew casks"},
		{[]ask.Key{keyTab}, "claude-code"},
		{[]ask.Key{keyTab}, "Homebrew formulae"},
		{[]ask.Key{keyShiftTab}, "claude-code"},
		{[]ask.Key{keyShiftTab, keyShiftTab, keyShiftTab}, "claude-code"},
	} {
		kb.press(t, tt.keys...)
		if got := focused(out.String()); !slices.Equal(got, []string{tt.want}) {
			t.Errorf("after %v, the panels with the focus are %q; want %s's alone", tt.keys, got, tt.want)
		}
	}
}

// lastFrame is the last of applying's screens drawn in raw, a line a row,
// as it was styled.
func lastFrame(raw string) []string {
	frame := raw[strings.LastIndex(raw, "\x1b[H")+len("\x1b[H"):]
	frame, _, _ = strings.Cut(frame, "\x1b[?2026l")
	ls := strings.Split(frame, "\r\n")
	for i, l := range ls {
		ls[i] = strings.TrimSuffix(l, "\x1b[K")
	}
	return ls
}

// focused are the titles of the panels whose rule is pink in the last
// screen drawn in raw: the one with the focus.
func focused(raw string) []string {
	var titles []string
	for _, l := range lastFrame(raw) {
		if !strings.HasPrefix(l, "  "+look.Pink("── ")) {
			continue
		}
		if name, ok := panelTitle(ansi.Strip(l)); ok {
			titles = append(titles, name)
		}
	}
	return titles
}

// lineOf is the screen's line whose text starts as prefix.
func lineOf(screen, prefix string) int {
	return slices.IndexFunc(lines(screen), func(l string) bool { return strings.HasPrefix(l, prefix) })
}

// The wheel scrolls the panel under the mouse, a few lines a turn, whichever
// has the focus; off the panels it scrolls nothing.
func TestApplyingWheelScrollsThePanelUnderIt(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, sixMinutesInWith(16, 30))
	defer func() { _ = face.Close() }()
	got := screen(out.String())
	casks := lineOf(got, "  ── ◐ Homebrew casks")
	kb.press(t, wheel("up", casks+4), wheel("up", casks))
	got = screen(out.String())
	if _, f := panelOf(got, "Homebrew formulae"); !slices.Equal(f, shows(formula, 4, 16)) {
		t.Errorf("the wheel over Homebrew casks moved Homebrew formulae's panel to\n%s", strings.Join(f, "\n"))
	}
	if _, c := panelOf(got, "Homebrew casks"); !slices.Equal(c, shows(cask, 12, 24)) {
		t.Errorf("turned up twice over it, Homebrew casks' panel shows\n%s\nwant it six lines back", strings.Join(c, "\n"))
	}
	kb.press(t, wheel("down", casks+9))
	if _, c := panelOf(screen(out.String()), "Homebrew casks"); !slices.Equal(c, shows(cask, 15, 27)) {
		t.Errorf("turned down once over its last line, Homebrew casks' panel shows\n%s\nwant it three lines forth", strings.Join(c, "\n"))
	}
	before := screen(out.String())
	kb.press(t, wheel("up", 2), wheel("up", lineOf(before, "  CONFIG")), wheel("down", 63))
	if after := screen(out.String()); after != before {
		t.Errorf("the wheel off the panels changed the screen to\n%s", after)
	}
}

// cellOf is where the step titled title's name starts in a screen's grid:
// its column and its line.
func cellOf(t *testing.T, screen, title string) (x, y int) {
	t.Helper()
	cell := regexp.MustCompile(`[●◐○▲✗–△] ` + regexp.QuoteMeta(title) + `( |$)`)
	for y, l := range lines(screen) {
		if strings.HasPrefix(l, "──") {
			break
		}
		if at := cell.FindStringIndex(l); at != nil {
			return utf8.RuneCountInString(l[:at[0]]) + 2, y
		}
	}
	t.Fatalf("no cell for %s in\n%s", title, screen)
	return 0, 0
}

// pin clicks the cells of the steps titled titles in the screen's grid,
// as it's drawn.
func pin(t *testing.T, out *syncBuffer, kb *keyboard, titles ...string) {
	t.Helper()
	for _, title := range titles {
		kb.press(t, click(cellOf(t, screen(out.String()), title)))
	}
}

// A click on a step's cell in the grid pins it: pinned steps take the
// log's panels in the order pinned, their names in the grid pink, each
// newly pinned panel taking the focus; a click on a pinned step unpins it;
// with four pinned, a fifth takes the place of the one pinned first. With
// none pinned, the panels follow what's running again. The keys say a click
// unpins, and a unpins them all, once something's pinned.
func TestApplyingPinsSteps(t *testing.T) {
	face, out, kb := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, casksFailed())
	defer func() { _ = face.Close() }()
	plain := func() string { return screen(uncoloured(out.String())) }
	following := panelTitles(plain())
	for _, tt := range []struct {
		name  string
		click []string
		want  []string
	}{
		{"one", []string{"mise"}, []string{"mise"}},
		{"two", []string{"Homebrew casks"}, []string{"mise", "Homebrew casks"}},
		{"four", []string{"Load", "claude-code"}, []string{"mise", "Homebrew casks", "Load", "claude-code"}},
		{"a fifth", []string{"fonts"}, []string{"Homebrew casks", "Load", "claude-code", "fonts"}},
		{"one unpinned", []string{"Load"}, []string{"Homebrew casks", "claude-code", "fonts"}},
	} {
		pin(t, out, kb, tt.click...)
		if got := panelTitles(plain()); !slices.Equal(got, tt.want) {
			t.Errorf("%s: the panels are %q, want %q", tt.name, got, tt.want)
		}
		if got := focused(out.String()); tt.name != "one unpinned" && !slices.Equal(got, tt.want[len(tt.want)-1:]) {
			t.Errorf("%s: the panel with the focus is %q, want the one just pinned, %s", tt.name, got, tt.want[len(tt.want)-1])
		}
		if f := foot(plain(), 1)[0]; f != pinnedFoot {
			t.Errorf("%s: the keys are %q, want %q", tt.name, f, pinnedFoot)
		}
	}
	frame := strings.Join(lastFrame(out.String()), "\n")
	for _, cell := range []string{look.Red("✗") + " " + look.Pink("Homebrew casks"), look.Pink("●") + " " + look.Pink("fonts")} {
		if !strings.Contains(frame, cell) {
			t.Errorf("the grid has no pinned cell %q", cell)
		}
	}
	if strings.Contains(frame, " "+look.Pink("Load")) || strings.Contains(frame, " "+look.Pink("mise")) {
		t.Error("a step no longer pinned is still pink")
	}
	if strings.Contains(frame, look.Pill("fonts", look.Done)) {
		t.Error("a pinned step's name is a pill; want it pink text")
	}
	pin(t, out, kb, "Homebrew casks", "claude-code", "fonts")
	if got := panelTitles(plain()); !slices.Equal(got, following) || foot(plain(), 1)[0] != runningFoot {
		t.Errorf("all unpinned, the panels are %q, the keys %q; want them following what's running again, %q", got, foot(plain(), 1)[0], following)
	}
}

// The keys at the screen's foot while it reads them, once something's
// pinned: a goes back to the steps running.
const pinnedFoot = "  tab panel · ↑↓ scroll · click a step to see it · a back to running · q stop"

// a unpins every step at once: the panels follow what's running again, the
// grid's names their own colours, and the keys lose a. With nothing
// pinned, a does nothing.
func TestApplyingGoesBackToRunning(t *testing.T) {
	face, out, kb := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, casksFailed())
	defer func() { _ = face.Close() }()
	plain := func() string { return screen(uncoloured(out.String())) }
	following := plain()
	kb.press(t, keyA)
	if got := plain(); got != following {
		t.Errorf("a with nothing pinned changed the screen to\n%s", got)
	}
	pin(t, out, kb, "mise", "Homebrew casks", "fonts")
	if got := panelTitles(plain()); !slices.Equal(got, []string{"mise", "Homebrew casks", "fonts"}) {
		t.Fatalf("pinned three, the panels are %q", got)
	}
	kb.press(t, keyA)
	if got := plain(); got != following {
		t.Errorf("a with three pinned left the screen\n%s\nwant it following what's running again\n%s", got, following)
	}
	if frame := strings.Join(lastFrame(out.String()), "\n"); strings.Contains(frame, " "+look.Pink("Homebrew casks")) || strings.Contains(frame, " "+look.Pink("mise")) {
		t.Error("a step unpinned by a is still pink in the grid")
	}
}

// withHistory is the run six minutes in, Homebrew having printed enough to
// scroll back through.
func withHistory() []event.Event { return sixMinutesInWith(46, 30) }

// The screen with steps pinned, one filling the log, two sharing it, four
// with one failed; and with a panel scrolled back, saying how many lines
// are below what it shows.
func TestApplyingPinnedScreens(t *testing.T) {
	for _, tt := range []struct {
		golden string
		events func() []event.Event
		pin    []string
		keys   []ask.Key
		panels []string
	}{
		{"apply-screen-one.golden", withHistory, []string{"Homebrew formulae"}, nil, []string{"Homebrew formulae"}},
		{"apply-screen-two.golden", withHistory, []string{"Homebrew formulae", "Homebrew casks"}, nil, []string{"Homebrew formulae", "Homebrew casks"}},
		{"apply-screen-four.golden", casksFailed, []string{"Homebrew formulae", "Homebrew casks", "claude-code", "App settings"}, nil, []string{"Homebrew formulae", "Homebrew casks", "claude-code", "App settings"}},
		{"apply-screen-scrolled.golden", withHistory, nil, append([]ask.Key{keyTab}, slices.Repeat([]ask.Key{keyUp}, 12)...), []string{"Homebrew formulae", "Homebrew casks", "claude-code"}},
	} {
		t.Run(tt.golden, func(t *testing.T) {
			face, out, kb := applyAt(160, 64, 6*time.Minute+12*time.Second, tt.events())
			defer func() { _ = face.Close() }()
			pin(t, out, kb, tt.pin...)
			kb.press(t, tt.keys...)
			got := screen(out.String())
			golden(t, tt.golden, got+"\n")
			if titles := panelTitles(got); !slices.Equal(titles, tt.panels) {
				t.Errorf("the panels are %q, want %q", titles, tt.panels)
			}
		})
	}
	face, out, kb := applyAt(160, 64, time.Minute, withHistory())
	defer func() { _ = face.Close() }()
	pin(t, out, kb, "Homebrew formulae")
	got := screen(out.String())
	rules := edges(lines(got))
	if _, under := panelOf(got, "Homebrew formulae"); len(rules) != 2 || len(under) != rules[1]-rules[0]-2 || under[len(under)-1] != "==> Pouring formula 46" {
		t.Errorf("pinned alone, Homebrew formulae's panel shows %d lines, its newest last, between rules at %v; want it filling the log", len(under), rules)
	}
	kb.press(t, keyTab, keyUp)
	if rule, _ := panelOf(screen(out.String()), "Homebrew formulae"); !strings.HasSuffix(rule, " ↑ 1 more ──") {
		t.Errorf("scrolled back a line, the panel's rule is %q; want it to say one line is below", rule)
	}
}

// colours are the controls that colour text.
var colours = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// uncoloured is raw without its colours, its other controls kept.
func uncoloured(raw string) string { return colours.ReplaceAllString(raw, "") }

// A click anywhere but on a step's cell does nothing: the header, an
// area's name, the room past a line's last cell, a rule, a panel, the
// foot.
func TestApplyingClicksOffTheGrid(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, sixMinutesIn())
	defer func() { _ = face.Close() }()
	before := screen(out.String())
	ls := lines(before)
	manual := lineOf(before, "  MANUAL")
	rule := edges(ls)[0]
	kb.press(t, click(10, 1), click(4, lineOf(before, "  CONFIG")), click(len([]rune(ls[manual]))+30, manual),
		click(80, rule), click(30, rule+1), click(30, rule+3), click(10, 63), click(1000, 1000))
	if after := screen(out.String()); after != before {
		t.Errorf("clicks off the grid changed the screen to\n%s", after)
	}
	x, y := cellOf(t, before, "Homebrew casks")
	kb.press(t, click(x+look.GridCell-4, y))
	if got := panelTitles(screen(out.String())); !slices.Equal(got, []string{"Homebrew casks"}) {
		t.Errorf("a click in the room after a cell's name, before the next, left the panels %q; want that step pinned", got)
	}
}

// The mouse is reported while the screen's up and reads keys, and no
// longer, before the keys stop being read, however the screen goes: the
// run ending, stopped by q's Stop or by ctrl+c, or kit panicking.
func TestApplyingReportsTheMouse(t *testing.T) {
	ended := event.RunFinished{Time: at, Duration: time.Minute, Counts: map[check.State]int{check.OK: 51}}
	for _, tt := range []struct {
		name  string
		leave func(*render.Pretty, *keyboard)
	}{
		{"the run ending", func(face *render.Pretty, _ *keyboard) { face.Emit(ended) }},
		{"q's Stop", func(face *render.Pretty, kb *keyboard) {
			kb.press(t, keyQ, keyDown, keyEnter)
			face.Emit(ended)
		}},
		{"ctrl+c", func(face *render.Pretty, kb *keyboard) {
			kb.press(t, keyCtrlC)
			face.Emit(ended)
		}},
		{"kit panicking", func(face *render.Pretty, _ *keyboard) { face.Unwind() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			face, out, kb := applyAt(160, 64, time.Minute, sixMinutesIn())
			defer func() { _ = face.Close() }()
			if raw := out.String(); !inOrder(raw, "\x1b[?1049h", mouseOn) || !mouseReported(raw) {
				t.Error("with the screen up, the mouse isn't reported")
			}
			tt.leave(face, kb)
			if raw := out.String(); mouseReported(raw) || !inOrder(raw[strings.LastIndex(raw, mouseOn):], mouseOff, "\x1b[?1049l") {
				t.Errorf("after %s, the face ended %q; want the mouse no longer reported, then the terminal's own screen", tt.name, raw[max(len(raw)-60, 0):])
			}
			if !mouseOffFirst(kb) {
				t.Errorf("after %s, the keys stopped being read before the mouse stopped being reported", tt.name)
			}
		})
	}
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 160, 64 })
	for _, e := range sixMinutesIn() {
		face.Emit(e)
	}
	_ = face.Close()
	if strings.Contains(out.String(), mouseOn) {
		t.Error("without keys to read, the screen had the mouse reported")
	}
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
// at once: the mouse no longer reported, its own screen, the cursor shown,
// its keys no longer read, so the terminal's out of its raw mode; and
// draws nothing after.
func TestApplyingUnwinds(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, sixMinutesIn())
	face.Unwind()
	raw := out.String()
	if !strings.HasSuffix(raw, mouseOff+"\x1b[?1049l\x1b[?25h") || kb.isRaw() {
		t.Errorf("unwound, the face ended %q, raw: %v; want the mouse no longer reported, the terminal's own screen and the cursor back, not raw", raw[max(len(raw)-40, 0):], kb.isRaw())
	}
	time.Sleep(300 * time.Millisecond)
	if out.String() != raw {
		t.Error("unwound, the face drew again")
	}
}
