package render_test

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

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
	keyPgUp     = ask.Key{Name: "pgup"}
	keyPgDown   = ask.Key{Name: "pgdown"}
	keyHome     = ask.Key{Name: "home"}
	keyEnd      = ask.Key{Name: "end"}
	keyEnter    = ask.Key{Name: "enter"}
	keyEsc      = ask.Key{Name: "esc"}
	keyCtrlC    = ask.Key{Name: "ctrl+c"}
	keyTab      = ask.Key{Name: "tab"}
	keyShiftTab = ask.Key{Name: "shift+tab"}
	keyO        = ask.Key{Text: "o"}
	keyL        = ask.Key{Text: "l"}
	focusIn     = ask.Key{Name: "focus in"}
	focusOut    = ask.Key{Name: "focus out"}
)

// click is the mouse's left button pressed at column x of line y; wheel the
// wheel turned up or down at column x of line y.
func click(x, y int) ask.Key { return ask.Key{Name: "click", X: x, Y: y} }

func wheel(dir string, x, y int) ask.Key { return ask.Key{Name: "wheel " + dir, X: x, Y: y} }

// The mouse's buttons and wheel reported, in SGR's form, and no longer; the
// window's focus reported, and no longer.
const (
	mouseOn  = "\x1b[?1000h\x1b[?1006h"
	mouseOff = "\x1b[?1006l\x1b[?1000l"
	focusOn  = "\x1b[?1004h"
	focusOff = "\x1b[?1004l"
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

// leftFoot is a screen w wide's left pane's last n lines.
func leftFoot(screen string, w, n int) []string {
	left, _ := panes(screen, w)
	return left[len(left)-n:]
}

// The keys at the left pane's foot while it reads them.
const runningFoot = "  tab next row · ↑↓ scroll · q stop"

// q asks, at the left pane's foot, whether to stop, naming the steps at
// work, and the keys answer it; the run goes on meanwhile. Keep going, or
// esc, puts the foot back, the run going on; Stop stops it, as ctrl+c
// does. The mouse is no answer.
func TestApplyingAsksBeforeStopping(t *testing.T) {
	face, out, kb := applyAt(160, 64, 13*time.Minute+2*time.Second, waitingOnYou())
	defer func() { _ = face.Close() }()
	running := leftFoot(screen(out.String()), 160, 2)
	if running[1] != runningFoot {
		t.Errorf("the keys are %q; want %q", running[1], runningFoot)
	}
	asking := []string{
		"",
		"  ▲ Stop applying?  Homebrew casks, Homebrew formulae and …",
		"  │ ❯ Keep going",
		"  │   Stop",
		"",
		"  ↑↓ choose · enter decide · esc keep going",
	}
	kb.press(t, keyQ)
	got := screen(out.String())
	if f := leftFoot(got, 160, 6); !slices.Equal(f, asking) {
		t.Errorf("q, the left pane's foot is\n%s\nwant\n%s", strings.Join(f, "\n"), strings.Join(asking, "\n"))
	}
	face.Emit(event.Logged{Time: after(380), Step: "brew", Line: "Installed jq"})
	time.Sleep(300 * time.Millisecond)
	if got := screen(out.String()); !strings.Contains(strings.Join(logOf(got, 160), "\n"), "Installed jq") || !slices.Equal(leftFoot(got, 160, 6), asking) {
		t.Errorf("asking, the screen is\n%s\nwant the run going on, the question asked", got)
	}
	before := screen(out.String())
	kb.press(t, wheel("down", 100, 40), wheel("up", 100, 40), click(8, rowAt(before, 160, "Secrets")))
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
		if f := leftFoot(screen(out.String()), 160, 2); !slices.Equal(f, running) {
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

// karabiner and paper are steps by hand at the end come up: one kit opened
// System Settings for, the other kit neither opened anything for nor can
// tell is done.
var (
	karabiner = event.NeedsYou{Time: after(360), Step: "manual", Item: "karabiner", Title: "Allow Karabiner-Elements", Steps: []string{
		"Allow Karabiner-Elements' driver (the Allow button, near the bottom)",
	}, Opened: "System Settings", More: 2}
	paper = event.NeedsYou{Time: after(360), Step: "manual", Item: "editor", Title: "Install the editor from its site", Steps: []string{"Download it from editor.example.com"}}
)

// o and enter answer the card up, passing the answer to its step, each
// only when the card's keys offer it: o when kit opened an app for it,
// enter when kit can't tell by itself that it's done. l, once a card's
// putting off, answers nothing: a card's done, or the run's stopped. With
// no card up, or once its step's finished, they answer nothing.
func TestApplyingCardKeys(t *testing.T) {
	for _, tt := range []struct {
		name   string
		events []event.Event
		want   []answered
	}{
		{"kit opened it, and tells", cardUp(signIn), []answered{{signIn.Step, hand.Open}}},
		{"kit opened it", cardUp(karabiner), []answered{{karabiner.Step, hand.Open}, {karabiner.Step, hand.Done}}},
		{"neither", cardUp(paper), []answered{{paper.Step, hand.Done}}},
		{"no card", sixMinutesIn(), nil},
		{"its step finished", append(cardUp(karabiner), finished(400, karabiner.Step, check.Result{State: check.OK, Summary: "done"})), nil},
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

// installedAt is the log's line for formula-n, in sixMinutesInWith's run.
var installedAt = regexp.MustCompile(`\+ Installed formula-(\d+)$`)

// firstFormula is the first formula the log shows a line for.
func firstFormula(log []string) string {
	for _, l := range log {
		if m := installedAt.FindStringSubmatch(l); m != nil {
			return m[1]
		}
	}
	return ""
}

// ↑ scrolls the log back a line, the newest no longer followed: the view
// stays put as lines come, a rule and how many lines are below it at its
// foot; ↓ scrolls it forth, following again once it's back at the newest,
// as end does at once; the page keys a page; home to the first line. The
// wheel over the log scrolls it a few lines a turn; off it, nothing.
func TestApplyingScrollsTheLog(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, sixMinutesInWith(80, 12))
	defer func() { _ = face.Close() }()
	shows := func() ([]string, []string) {
		got := screen(out.String())
		_, right := panes(got, 160)
		return logOf(got, 160), right
	}
	following, _ := shows()
	kb.press(t, keyUp)
	back, right := shows()
	if back[0] == following[0] || right[len(right)-1] == "" || !strings.HasPrefix(right[len(right)-1], "  ↓ ") {
		t.Fatalf("↑ once, the log is\n%s\nwant it a line back, its foot saying what's below", strings.Join(right, "\n"))
	}
	if rule := right[len(right)-2]; strings.Trim(rule, "─") != "" {
		t.Errorf("over the log's foot is %q; want a rule", rule)
	}
	top := back[0]
	for i := range 5 {
		face.Emit(event.Logged{Time: after(400 + i), Step: "brew", Line: "Installed jq"})
	}
	time.Sleep(300 * time.Millisecond)
	if now, right := shows(); now[0] != top || !strings.Contains(right[len(right)-1], "new lines") {
		t.Errorf("scrolled back, five lines came, the log starts %q; want it where it was, %q", now[0], top)
	}
	_, right = shows()
	below := right[len(right)-1]
	kb.press(t, keyDown)
	if now, right := shows(); now[0] == top || right[len(right)-1] == below {
		t.Errorf("↓, the log starts %q, its foot %q; want it a line forth, a line fewer below", now[0], right[len(right)-1])
	}
	kb.press(t, keyEnd)
	if _, right := shows(); strings.Contains(strings.Join(right, "\n"), "new lines") || !strings.Contains(right[len(right)-1], "Installed jq") && !strings.Contains(strings.Join(right[len(right)-4:], "\n"), "Installed jq") {
		t.Errorf("end, the log is\n%s\nwant it following the newest again", strings.Join(right, "\n"))
	}
	kb.press(t, keyHome)
	if now, _ := shows(); now[0] != "  03:04:06  ● GitHub: fine" {
		t.Errorf("home, the log starts %q; want its first line", now[0])
	}
	kb.press(t, keyPgDown)
	paged, _ := shows()
	if firstFormula(paged) == "" {
		t.Errorf("a page down from the first line, the log is\n%s\nwant the formulae's lines", strings.Join(paged, "\n"))
	}
	kb.press(t, keyPgUp)
	if now, _ := shows(); now[0] != "  03:04:06  ● GitHub: fine" {
		t.Errorf("a page down and back up, the log starts %q; want its first line", now[0])
	}
	kb.press(t, keyEnd)
	for range 30 {
		kb.press(t, keyUp)
	}
	for range 30 {
		kb.press(t, keyDown)
	}
	if _, right := shows(); strings.Contains(strings.Join(right, "\n"), "new lines") {
		t.Errorf("as far down as it went up, the log is\n%s\nwant it following again", strings.Join(right, "\n"))
	}
	before := screen(out.String())
	kb.press(t, wheel("up", 30, 30), wheel("up", 100, 2))
	if after := screen(out.String()); after != before {
		t.Error("the wheel off the log scrolled it")
	}
	kb.press(t, wheel("up", 100, 40), wheel("up", 100, 40))
	if now, _ := shows(); now[0] == following[0] || firstFormula(now) == "" {
		t.Errorf("the wheel turned up twice over the log, it starts %q", now[0])
	}
}

// tab chooses the next row, round from the last to everything; shift+tab
// the one before; esc everything. Each shows its lines alone, its name a
// pill, from the newest.
func TestApplyingChoosesRows(t *testing.T) {
	face, out, kb := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, sixMinutesIn())
	defer func() { _ = face.Close() }()
	chosen := func() string {
		frame := strings.Join(lastFrame(out.String()), "\n")
		for _, name := range append([]string{"Everything"}, "This Mac", "Files and shell", "Mac settings", "Secrets", "Homebrew formulae",
			"Homebrew casks", "App Store apps", "Developer tools", "Git", "Login items", "Your steps", "Backups") {
			if strings.Contains(frame, look.Pill(name, look.Done)) {
				return name
			}
		}
		return ""
	}
	for _, tt := range []struct {
		keys []ask.Key
		want string
	}{
		{nil, "Everything"},
		{[]ask.Key{keyTab}, "This Mac"},
		{[]ask.Key{keyTab}, "Files and shell"},
		{[]ask.Key{keyShiftTab, keyShiftTab}, "Everything"},
		{[]ask.Key{keyShiftTab}, "Backups"},
		{[]ask.Key{keyTab}, "Everything"},
		{[]ask.Key{keyTab, keyTab, keyTab, keyEsc}, "Everything"},
	} {
		kb.press(t, tt.keys...)
		if got := chosen(); got != tt.want {
			t.Errorf("after %v, the chosen is %q; want %q", tt.keys, got, tt.want)
		}
	}
	kb.press(t, keyShiftTab)
	if _, right := panes(screen(uncoloured(out.String())), 160); right[1] != "  LOG · BACKUPS" {
		t.Errorf("Backups chosen, the log's heading is %q", right[1])
	}
}

// A click on a row chooses it; on the row chosen, everything; on
// Everything, everything. A click anywhere else does nothing: the header,
// a row's bar, the rows' foot, the log.
func TestApplyingClicksChooseRows(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, sixMinutesIn())
	defer func() { _ = face.Close() }()
	heading := func() string {
		_, right := panes(screen(out.String()), 160)
		return right[1]
	}
	kb.press(t, clickRow("Homebrew casks")(screen(out.String()), 160)...)
	if h := heading(); h != "  LOG · HOMEBREW CASKS" {
		t.Errorf("Homebrew casks clicked, the log's heading is %q", h)
	}
	kb.press(t, clickRow("Homebrew casks")(screen(out.String()), 160)...)
	if h := heading(); h != "  LOG" {
		t.Errorf("the row chosen clicked again, the log's heading is %q", h)
	}
	kb.press(t, clickRow("Secrets")(screen(out.String()), 160)...)
	kb.press(t, click(10, headLines+1))
	if h := heading(); h != "  LOG" {
		t.Errorf("Everything clicked, the log's heading is %q", h)
	}
	before := screen(out.String())
	formulae := rowAt(before, 160, "Homebrew formulae")
	kb.press(t, click(10, 1), click(10, formulae+1), click(10, 63), click(100, formulae), click(1000, 1000))
	if after := screen(out.String()); after != before {
		t.Errorf("clicks off the rows changed the screen to\n%s", after)
	}
}

// A click that brings the window forward chooses nothing: one while it's
// out of focus, and one just after it's come into focus; one a moment
// after does.
func TestApplyingClickBringingTheWindowForward(t *testing.T) {
	face, out, kb, c := applyClocked(colorprofile.Ascii, 160, 64, time.Minute, sixMinutesIn())
	defer func() { _ = face.Close() }()
	heading := func() string {
		_, right := panes(screen(out.String()), 160)
		return right[1]
	}
	casks := clickRow("Homebrew casks")(screen(out.String()), 160)
	kb.press(t, focusOut)
	kb.press(t, casks...)
	if h := heading(); h != "  LOG" {
		t.Errorf("clicked out of focus, the log's heading is %q; want everything still", h)
	}
	kb.press(t, focusIn)
	c.set(at.Add(time.Minute + 299*time.Millisecond))
	kb.press(t, casks...)
	if h := heading(); h != "  LOG" {
		t.Errorf("clicked just after coming into focus, the log's heading is %q; want everything still", h)
	}
	c.set(at.Add(time.Minute + 300*time.Millisecond))
	kb.press(t, casks...)
	if h := heading(); h != "  LOG · HOMEBREW CASKS" {
		t.Errorf("clicked once in focus, the log's heading is %q; want Homebrew casks", h)
	}
}

// The mouse and the window's focus are reported while the screen's up and
// reads keys, and no longer, before the keys stop being read, however the
// screen goes: the run ending, stopped by q's Stop or by ctrl+c, or kit
// panicking.
func TestApplyingReportsTheMouse(t *testing.T) {
	ended := event.RunFinished{Time: at, Duration: time.Minute, Counts: map[check.State]int{check.OK: 30}}
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
			if raw := out.String(); !inOrder(raw, "\x1b[?1049h", mouseOn+focusOn) || !mouseReported(raw) {
				t.Error("with the screen up, the mouse and the window's focus aren't reported")
			}
			tt.leave(face, kb)
			if raw := out.String(); mouseReported(raw) || !inOrder(raw[strings.LastIndex(raw, mouseOn):], focusOff+mouseOff, "\x1b[?1049l") {
				t.Errorf("after %s, the face ended %q; want the focus and the mouse no longer reported, then the terminal's own screen", tt.name, raw[max(len(raw)-60, 0):])
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
	if strings.Contains(out.String(), mouseOn) || strings.Contains(out.String(), focusOn) {
		t.Error("without keys to read, the screen had the mouse or the focus reported")
	}
}

// Without keys to read, the left pane's foot says what stops the run: the
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
	if f := leftFoot(got, 160, 1); f[0] != "  ctrl+c stop" {
		t.Errorf("without keys, the left pane's foot is %q, want ctrl+c stop", f[0])
	}
}

// Kit panicking with applying's screen up, the face puts the terminal back
// at once: the focus and the mouse no longer reported, its own screen, the
// cursor shown, its keys no longer read, so the terminal's out of its raw
// mode; and draws nothing after.
func TestApplyingUnwinds(t *testing.T) {
	face, out, kb := applyAt(160, 64, time.Minute, sixMinutesIn())
	face.Unwind()
	raw := out.String()
	if !strings.HasSuffix(raw, focusOff+mouseOff+"\x1b[?1049l\x1b[?25h") || kb.isRaw() {
		t.Errorf("unwound, the face ended %q, raw: %v; want the focus and the mouse no longer reported, the terminal's own screen and the cursor back, not raw", raw[max(len(raw)-40, 0):], kb.isRaw())
	}
	time.Sleep(300 * time.Millisecond)
	if out.String() != raw {
		t.Error("unwound, the face drew again")
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

// colours are the controls that colour text.
var colours = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// uncoloured is raw without its colours, its other controls kept.
func uncoloured(raw string) string { return colours.ReplaceAllString(raw, "") }
