package ask

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/look"
)

// press presses keys on m, saying whether the last ended it.
func press(m model, keys ...key) bool {
	done := false
	for _, k := range keys {
		done = m.update(k)
	}
	return done
}

// plain is lines without their colours.
func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}

var (
	down  = key{name: "down"}
	up    = key{name: "up"}
	j     = key{text: "j"}
	q     = key{text: "q"}
	enter = key{name: "enter"}
	esc   = key{name: "esc"}
)

func question(name string) Question {
	return Question{
		About:   look.Row{State: look.NeedsYou, Name: name, Says: look.Says(look.Muted("cask"), look.Orange("installed, not declared"))},
		Answers: []look.Choice{{Label: "Adopt", Does: "declare it"}, {Label: "Remove", Does: "uninstall it"}, {Label: "Skip", Does: "not now"}},
	}
}

func equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s =\n%s\nwant\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// What the terminal sends, as keys: the arrows in either form, enter, tab
// and shift+tab, escape alone, Ctrl+C, backspace, text, a paste whole even
// when it comes in parts; other sequences dropped, and one cut short kept
// for the rest.
func TestParse(t *testing.T) {
	keys, rest := parse([]byte("\x1b[A\x1bOB\rjé\x03\x7f\x1b[3~\x1b[200~ab\ncd\x1b[201~\x1b"))
	want := []key{{name: "up"}, {name: "down"}, {name: "enter"}, {text: "j"}, {text: "é"}, {name: "ctrl+c"}, {name: "backspace"}, {text: "ab\ncd", paste: true}, {name: "esc"}}
	if !slices.Equal(keys, want) || len(rest) != 0 {
		t.Errorf("parse = %+v, rest %q; want %+v", keys, rest, want)
	}
	keys, rest = parse([]byte("x\x1b[200~par"))
	if !slices.Equal(keys, []key{{text: "x"}}) || string(rest) != "\x1b[200~par" {
		t.Errorf("a paste still coming: %+v, rest %q", keys, rest)
	}
	if keys, rest = parse(append(rest, "t\x1b[201~"...)); !slices.Equal(keys, []key{{text: "part", paste: true}}) || len(rest) != 0 {
		t.Errorf("the paste whole: %+v, rest %q", keys, rest)
	}
	if keys, rest = parse([]byte("\x1b[1;5")); len(keys) != 0 || string(rest) != "\x1b[1;5" {
		t.Errorf("a sequence cut short: %+v, rest %q; want it kept", keys, rest)
	}
	keys, rest = parse([]byte("\t\x1b[Zq"))
	if want := []key{{name: "tab"}, {name: "shift+tab"}, {text: "q"}}; !slices.Equal(keys, want) || len(rest) != 0 {
		t.Errorf("tab, shift+tab, q: %+v, rest %q; want %+v", keys, rest, want)
	}
}

// Home and end, in each form a terminal sends them, and page up and down,
// as keys; the window coming into focus and leaving it, as a terminal
// reports them once asked to, as keys of their own, never text typed.
func TestParseMovesAndFocus(t *testing.T) {
	for _, tt := range []struct {
		sent string
		want key
	}{
		{"\x1b[H", key{name: "home"}},
		{"\x1bOH", key{name: "home"}},
		{"\x1b[1~", key{name: "home"}},
		{"\x1b[7~", key{name: "home"}},
		{"\x1b[1;5H", key{name: "home"}},
		{"\x1b[F", key{name: "end"}},
		{"\x1bOF", key{name: "end"}},
		{"\x1b[4~", key{name: "end"}},
		{"\x1b[8~", key{name: "end"}},
		{"\x1b[5~", key{name: "pgup"}},
		{"\x1b[6~", key{name: "pgdown"}},
		{"\x1b[I", key{name: "focus in"}},
		{"\x1b[O", key{name: "focus out"}},
	} {
		if keys, rest := parse([]byte(tt.sent)); !slices.Equal(keys, []key{tt.want}) || len(rest) != 0 {
			t.Errorf("parse(%q) = %+v, rest %q; want %+v", tt.sent, keys, rest, tt.want)
		}
	}
	keys, rest := parse([]byte("\x1b[O\x1b[<0;3;4M\x1b[I\x1bOA"))
	if want := []key{{name: "focus out"}, {name: "click", x: 2, y: 3}, {name: "focus in"}, {name: "up"}}; !slices.Equal(keys, want) || len(rest) != 0 {
		t.Errorf("focus out, a click, focus in, up: %+v, rest %q; want %+v", keys, rest, want)
	}
}

// What the mouse reports, in SGR's form, as keys: the left button pressed
// a click, the wheel turned up or down, with or without shift, alt or ctrl
// held, each at its cell counted from 0; the button let go, the other
// buttons, a drag and the old form of report are none, and none leaks its
// numbers or letters as text typed; a report cut short is kept for the
// rest.
func TestParseMouse(t *testing.T) {
	for _, tt := range []struct {
		name, sent string
		want       []key
	}{
		{"a click", "\x1b[<0;12;5M", []key{{name: "click", x: 11, y: 4}}},
		{"a click let go", "\x1b[<0;12;5m", nil},
		{"the wheel up", "\x1b[<64;80;30M", []key{{name: "wheel up", x: 79, y: 29}}},
		{"the wheel down", "\x1b[<65;1;1M", []key{{name: "wheel down"}}},
		{"the wheel with ctrl held", "\x1b[<81;3;4M", []key{{name: "wheel down", x: 2, y: 3}}},
		{"a click with shift held", "\x1b[<4;3;4M", []key{{name: "click", x: 2, y: 3}}},
		{"the middle button", "\x1b[<1;3;4M", nil},
		{"the right button", "\x1b[<2;3;4M", nil},
		{"a drag", "\x1b[<32;3;4M", nil},
		{"the wheel sideways", "\x1b[<66;3;4M", nil},
		{"the old form", "\x1b[M q!", nil},
		{"a report then keys", "\x1b[<0;113;5Mq\x1b[A", []key{{name: "click", x: 112, y: 4}, {text: "q"}, {name: "up"}}},
	} {
		if keys, rest := parse([]byte(tt.sent)); !slices.Equal(keys, tt.want) || len(rest) != 0 {
			t.Errorf("%s: parse(%q) = %+v, rest %q; want %+v", tt.name, tt.sent, keys, rest, tt.want)
		}
	}
	for _, cut := range []string{"\x1b[<0;12", "\x1b[M q"} {
		if keys, rest := parse([]byte(cut)); len(keys) != 0 || string(rest) != cut {
			t.Errorf("a report cut short, %q: %+v, rest %q; want it kept", cut, keys, rest)
		}
	}
}

// One question: a blank line, its row, its answers under it, the cursor
// moving among them, round from the last to the first and back; taken, it
// leaves its row with the answer.
func TestChooseMovesAndTakes(t *testing.T) {
	w := newWalk("", []Question{question("zoom")})
	if press(w, down, j); w.cursor != 2 {
		t.Fatalf("down, j: cursor %d, want the last", w.cursor)
	}
	if press(w, down); w.cursor != 0 {
		t.Errorf("down on the last: cursor %d, want the first", w.cursor)
	}
	if press(w, up, up) {
		t.Fatal("moving ended it")
	}
	equal(t, "view", plain(w.view(80, 40)), []string{
		"", "  ▲ zoom  cask · installed, not declared", "  │   Adopt  declare it", "  │ ❯ Remove  uninstall it", "  │   Skip  not now",
		"", "  ↑↓ choose · enter decide · esc cancel",
	})
	if !press(w, enter) || !slices.Equal(w.answers, []int{1}) {
		t.Errorf("enter: answers %v; want Remove taken, and the end", w.answers)
	}
	equal(t, "leaves", plain(w.leaves(80)), []string{"", "  ● zoom  remove"})
}

func TestChooseCancels(t *testing.T) {
	for _, k := range []key{esc, q, {name: "ctrl+c"}} {
		w := newWalk("", []Question{question("zoom")})
		if !press(w, k) || !w.cancelled {
			t.Errorf("%v: want it cancelled", k)
		}
		equal(t, "leaves", plain(w.leaves(80)), []string{"", "  – zoom  cancelled"})
	}
}

// Several questions: the header counting them, those answered with their
// answer, the one asked with its answers, those to come waiting; q stops,
// keeping what's answered; they leave nothing.
func TestWalk(t *testing.T) {
	w := newWalk("Reconcile", []Question{question("ffmpeg"), question("zoom"), question("wget")})
	press(w, enter)
	equal(t, "view", plain(w.view(80, 40)), []string{
		"  RECONCILE  2 of 3", "  ● ffmpeg  adopt", "  ▲ zoom  cask · installed, not declared",
		"  │ ❯ Adopt  declare it", "  │   Remove  uninstall it", "  │   Skip  not now", "  ○ wget  cask · installed, not declared",
		"", "  ↑↓ choose · enter decide · q stop · esc cancel",
	})
	if !press(w, down, enter, q) || !w.stopped || !slices.Equal(w.answers, []int{0, 1}) || w.leaves(80) != nil {
		t.Errorf("stopped: %+v", w)
	}
	if w := newWalk("Reconcile", []Question{question("zoom")}); !press(w, esc) || w.leaves(80) != nil {
		t.Error("a walk of one question, cancelled: want it to leave nothing")
	}
}

// More questions than the terminal holds: the question asked stays in view,
// those above it cut from the top.
func TestWalkKeepsTheQuestionInView(t *testing.T) {
	var qs []Question
	for i := range 12 {
		qs = append(qs, question("cask"+strconv.Itoa(i)))
	}
	w := newWalk("Reconcile", qs)
	press(w, slices.Repeat([]key{enter}, 8)...)
	got := plain(w.view(80, 12))
	if len(got) != 11 || got[len(got)-1] != "  ↑↓ choose · enter decide · q stop · esc cancel" || !slices.Contains(got, "  │ ❯ Adopt  declare it") {
		t.Errorf("view at 12 lines =\n%s", strings.Join(got, "\n"))
	}
}

// A list: under its head, the cursor on the first line that can be picked,
// moving among those, past the rest; a fold opens in place, the cursor on
// its first; enter picks.
func TestPick(t *testing.T) {
	l := &list{head: []string{"", "  ── menu"}, keys: []look.Key{{Key: "enter", Does: "open"}}, lines: []Line{
		{Text: "  MON"},
		{Text: "  a", Chosen: "  ❯ a", Value: "A"},
		{Text: "  + 2 more", Chosen: "  ❯ 2 more", Folds: []Line{{Text: "  b", Chosen: "  ❯ b", Value: "B"}, {Text: "  c", Chosen: "  ❯ c", Value: "C"}}},
		{},
		{Text: "  SUN"},
		{Text: "  d", Chosen: "  ❯ d", Value: "D"},
	}}
	l.cursor = l.next(-1, 1)
	press(l, down)
	equal(t, "on the fold", plain(l.view(80, 40)), []string{"", "  ── menu", "  MON", "  a", "  ❯ 2 more", "", "  SUN", "  d", "", "  enter open"})
	if press(l, enter) {
		t.Fatal("opening the fold ended it")
	}
	equal(t, "opened", plain(l.view(80, 40))[2:6], []string{"  MON", "  a", "  ❯ b", "  c"})
	press(l, down, down)
	if press(l, down); l.lines[l.cursor].Value != "A" {
		t.Errorf("down on the last: on %q, want round to A", l.lines[l.cursor].Value)
	}
	if !press(l, up, enter) || l.lines[l.cursor].Value != "D" {
		t.Errorf("up on the first, then enter: picked %q; want round to D", l.lines[l.cursor].Value)
	}
	if l := (&list{lines: []Line{{Text: "a", Chosen: "❯ a"}}}); !press(l, q) || !l.cancelled {
		t.Error("q: want the list cancelled")
	}
}

// A list longer than the terminal scrolls, keeping the cursor in view.
func TestPickScrolls(t *testing.T) {
	l := &list{}
	for i := range 20 {
		s := strconv.Itoa(i)
		l.lines = append(l.lines, Line{Text: "  " + s, Chosen: "  ❯ " + s, Value: s})
	}
	press(l, slices.Repeat([]key{j}, 12)...)
	equal(t, "view", plain(l.view(80, 9)), []string{"  9", "  10", "  11", "  ❯ 12", "  13", "  14", "", "  "})
}

// A secret is typed or pasted, a dot a character, never shown.
func TestSecretField(t *testing.T) {
	f := &field{about: look.Row{State: look.NeedsYou, Name: "NPM_TOKEN", Says: look.Orange("needs its value")}}
	press(f, key{text: "a"}, key{text: "q"}, key{text: "bcd\n", paste: true}, key{name: "backspace"})
	got := plain(f.view(80, 40))
	equal(t, "view", got, []string{"  ▲ NPM_TOKEN  needs its value", "  │ ❯ •••• ", "", "  enter done · esc cancel"})
	if !press(f, enter) || string(f.typed) != "aqbc" || strings.Contains(strings.Join(got, ""), "aqbc") {
		t.Errorf("entered %q", string(f.typed))
	}
}

// Asked at a terminal: drawn under what's on screen, drawn over in place as
// keys come, then cleared, what it leaves written where it was.
func TestShow(t *testing.T) {
	var out bytes.Buffer
	term := Terminal{In: strings.NewReader("\x1b[B\r"), Out: &out, Size: func() (int, int) { return 80, 24 }}
	i, err := Choose(context.Background(), term, question("zoom"))
	if err != nil || i != 1 {
		t.Fatalf("Choose = %d, %v; want Remove", i, err)
	}
	s := out.String()
	for _, want := range []string{hideCursor, "\x1b[6A", "\x1b[2K", "  ● zoom  remove\r\n", showCursor} {
		if !strings.Contains(ansi.Strip(s), ansi.Strip(want)) && !strings.Contains(s, want) {
			t.Errorf("drew %q; want it to hold %q", s, want)
		}
	}
	if strings.LastIndex(s, "remove") < strings.LastIndex(s, "\x1b[2K") {
		t.Error("the answer was written before the question was cleared")
	}
}

// screen is a Screen a test changes from outside: a count drawn.
type screen struct {
	mu      sync.Mutex
	count   int
	changed chan struct{}
}

func (s *screen) add() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
}

func (s *screen) line(what string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []string{"  " + what + " " + strconv.Itoa(s.count)}
}

func (s *screen) View(int, int) []string   { return s.line("count") }
func (s *screen) Key(k Key) bool           { return k.Is("q") }
func (s *screen) Leaves(int) []string      { return s.line("ended at") }
func (s *screen) Changes() <-chan struct{} { return s.changed }

// A live screen is drawn over as it changes, and as keys come; closing its
// changes ends it, what it leaves written where it was.
func TestLive(t *testing.T) {
	in, keys := io.Pipe()
	defer func() { _ = keys.Close() }()
	var out bytes.Buffer
	s := &screen{changed: make(chan struct{})}
	done := make(chan error)
	go func() {
		done <- Live(context.Background(), Terminal{In: in, Out: &out, Size: func() (int, int) { return 80, 24 }}, s)
	}()
	_, _ = keys.Write([]byte("x"))
	for range 2 {
		s.add()
		s.changed <- struct{}{}
	}
	close(s.changed)
	if err := <-done; err != nil {
		t.Fatalf("Live = %v", err)
	}
	got := ansi.Strip(out.String())
	for _, want := range []string{"count 2", "ended at 2\r\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("drew %q; want it to hold %q", got, want)
		}
	}
}

// Keys read for something drawn by another, as they're pressed: what the
// terminal sends, as keys, ctrl+c among them, and the mouse's clicks, with
// where, till stop, which ends them and can be called again.
func TestListen(t *testing.T) {
	in, typing := io.Pipe()
	defer func() { _ = typing.Close() }()
	keys, stop := Listen(Terminal{In: in})
	go func() { _, _ = typing.Write([]byte("q\x03\x1b[A\x1b[<0;5;6M")) }()
	var got []Key
	for range 4 {
		got = append(got, <-keys)
	}
	if want := []Key{{Text: "q"}, {Name: "ctrl+c"}, {Name: "up"}, {Name: "click", X: 4, Y: 5}}; !slices.Equal(got, want) {
		t.Errorf("Listen gave %+v, want %+v", got, want)
	}
	stop()
	stop()
	if k, ok := <-keys; ok {
		t.Errorf("after stop, Listen gave %+v; want its keys ended", k)
	}
}

// fullScreen is a screen that takes the whole of the terminal.
type fullScreen struct{ screen }

func (s *fullScreen) Full(int, int) []string { return s.line("whole") }

// A screen that took the whole of the terminal gives it back however it
// ends: stopped, as well as ended.
func TestLiveGivesTheWholeScreenBack(t *testing.T) {
	in, keys := io.Pipe()
	defer func() { _ = keys.Close() }()
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		done <- Live(ctx, Terminal{In: in, Out: &out, Size: func() (int, int) { return 80, 24 }}, &fullScreen{screen{changed: make(chan struct{})}})
	}()
	for !strings.Contains(out.String(), altOn) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Error("stopped, Live returned no error")
	}
	if s := out.String(); strings.LastIndex(s, altOff) < strings.LastIndex(s, altOn) {
		t.Errorf("stopped, Live left %q; want the terminal's own screen back", s)
	}
}

// syncBuffer is a buffer written by one goroutine as another reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
