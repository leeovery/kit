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
	keys, rest := parse([]byte("\x1b[A\x1bOB\rjé\x03\x7f\x1b[3~\x1b[200~ab\ncd\x1b[201~\x1b\r"))
	want := []key{{name: "up"}, {name: "down"}, {name: "enter"}, {text: "j"}, {text: "é"}, {name: "ctrl+c"}, {name: "backspace"}, {text: "ab\ncd", paste: true}, {name: "esc"}, {name: "enter"}}
	if !slices.Equal(keys, want) || len(rest) != 0 {
		t.Errorf("parse = %+v, rest %q; want %+v", keys, rest, want)
	}
	if keys, rest = parse([]byte("q\x1b")); !slices.Equal(keys, []key{{text: "q"}}) || string(rest) != "\x1b" {
		t.Errorf("an escape last: %+v, rest %q; want it kept, as it may start a sequence", keys, rest)
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

// The terminal's replies to kit's questions of it, come late among the
// keys: its background's colour, an OSC ended by BEL or by ESC \, a DCS,
// and its device attributes; none is a key, its ESC not escape and its
// letters not text, the keys around it kept; a reply cut short is kept
// for the rest.
func TestParseSwallowsTerminalReplies(t *testing.T) {
	for _, tt := range []struct {
		name, sent string
		want       []key
	}{
		{"the background, ended by BEL", "\x1b]11;rgb:1e1e/1e1e/2e2e\x07", nil},
		{"the background, ended by ST", "\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\", nil},
		{"a DCS", "\x1bP1$r0m\x1b\\", nil},
		{"the device attributes", "\x1b[?1;2c", nil},
		{"replies among keys", "j\x1b]11;rgb:0/0/0\x07\x1b[?62;22ck\r", []key{{text: "j"}, {text: "k"}, {name: "enter"}}},
		{"escape after a reply", "\x1b]11;rgb:0/0/0\x1b\\\x1b\r", []key{{name: "esc"}, {name: "enter"}}},
	} {
		if keys, rest := parse([]byte(tt.sent)); !slices.Equal(keys, tt.want) || len(rest) != 0 {
			t.Errorf("%s: parse(%q) = %+v, rest %q; want %+v", tt.name, tt.sent, keys, rest, tt.want)
		}
	}
	for _, cut := range []string{"\x1b]11;rgb:1e1e/1e", "\x1b]11;rgb:0/0/0\x1b", "\x1bP1$r"} {
		if keys, rest := parse([]byte(cut)); len(keys) != 0 || string(rest) != cut {
			t.Errorf("a reply cut short, %q: %+v, rest %q; want it kept", cut, keys, rest)
		}
	}
	_, rest := parse([]byte("\x1b]11;rgb:0/0/0"))
	if keys, rest := parse(append(rest, "\x07q"...)); !slices.Equal(keys, []key{{text: "q"}}) || len(rest) != 0 {
		t.Errorf("a reply in two reads, then q: %+v, rest %q; want q alone", keys, rest)
	}
}

// Escape then ] or P, typed fast or as Alt+], starts as a reply would, but
// a reply is printable text: a control after it, or more than any reply
// has, makes it escape and what was typed, each key as it comes, never
// held.
func TestParseTakesWhatIsntAReplyAsKeys(t *testing.T) {
	var keys, rest []key
	var pending []byte
	for _, read := range []string{"\x1b]", "q", "\r", "\x03", "\x1b", "\x1b[A"} {
		rest, pending = parse(append(pending, read...))
		keys = append(keys, rest...)
	}
	want := []key{{name: "esc"}, {text: "]"}, {text: "q"}, {name: "enter"}, {name: "ctrl+c"}, {name: "esc"}, {name: "up"}}
	if !slices.Equal(keys, want) || len(pending) != 0 {
		t.Errorf("escape, ], q, enter, Ctrl+C, escape, up, a read at a time = %+v, rest %q; want %+v", keys, pending, want)
	}
	for _, sent := range []string{"\x1bP\x1b[A", "\x1b]a\x1bb"} {
		if keys, _ := parse([]byte(sent)); len(keys) == 0 || keys[0] != (key{name: "esc"}) {
			t.Errorf("parse(%q) = %+v; want escape first", sent, keys)
		}
	}
	long := "\x1b]" + strings.Repeat("a", 600)
	keys, pending = parse([]byte(long))
	if len(keys) != 602 || keys[0] != (key{name: "esc"}) || keys[1] != (key{text: "]"}) || len(pending) != 0 {
		t.Errorf("an unended reply of 600 = %d keys, first %+v, rest %d bytes; want escape, ], then each a", len(keys), keys[:min(2, len(keys))], len(pending))
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
// answer, the one asked with its answers, those to come waiting, each
// item's name on its line and what it is under it; q stops, keeping what's
// answered; they leave nothing.
func TestWalk(t *testing.T) {
	w := newWalk("Reconcile", []Question{question("ffmpeg"), question("zoom"), question("wget")})
	press(w, enter)
	equal(t, "view", plain(w.view(80, 40)), []string{
		"  RECONCILE  2 of 3", "  ● ffmpeg", "  │ adopt", "  ▲ zoom", "  │ cask · installed, not declared",
		"  │ ❯ Adopt  declare it", "  │   Remove  uninstall it", "  │   Skip  not now", "  ○ wget", "  │ cask · installed, not declared",
		"", "  ↑↓ choose · enter decide · q stop · esc cancel",
	})
	kept := newWalk("Reconcile", []Question{{About: look.Row{Name: "~/.zshrc"}, Answers: []look.Choice{{Label: "Keep the Mac's"}}}, question("zoom")})
	press(kept, enter)
	if got := plain(kept.view(80, 40))[1:3]; !slices.Equal(got, []string{"  ● ~/.zshrc", "  │ keep the Mac's"}) {
		t.Errorf("answered: %q; want its label in lower case but for the Mac", got)
	}
	if !press(w, down, enter, q) || !w.stopped || !slices.Equal(w.answers, []int{0, 1}) || w.leaves(80) != nil {
		t.Errorf("stopped: %+v", w)
	}
	if w := newWalk("Reconcile", []Question{question("zoom")}); !press(w, esc) || w.leaves(80) != nil {
		t.Error("a walk of one question, cancelled: want it to leave nothing")
	}
}

// Through a walk, nothing of an item is cut, as it's what's decided
// from: names too long for the line wrap, after a / in a path, and what
// each is wraps under it, every line within 80 columns.
func TestWalkWrapsWhatsAsked(t *testing.T) {
	long := "~/Library/Application Support/Some Long App Name/Default/Service Worker/CacheStorage/Cache_Data"
	asked := question(long)
	asked.About.Says = look.Says(look.Muted("backup-exclusion"), look.Orange("installed, not declared"), look.Muted("a detail long enough to go past the end of the line"), look.Muted("under a day"))
	w := newWalk("Reconcile", []Question{asked, question("~/Library/Application Support/Another App/Default/Service Worker/Cache_Data")})
	equal(t, "view", plain(w.view(80, 40)), []string{
		"  RECONCILE  1 of 2",
		"  ▲ ~/Library/Application Support/Some Long App Name/Default/Service Worker/",
		"  │ CacheStorage/Cache_Data",
		"  │ backup-exclusion · installed, not declared · a detail long enough to go past",
		"  │ the end of the line · under a day",
		"  │ ❯ Adopt  declare it", "  │   Remove  uninstall it", "  │   Skip  not now",
		"  ○ ~/Library/Application Support/Another App/Default/Service Worker/Cache_Data",
		"  │ cask · installed, not declared",
		"", "  ↑↓ choose · enter decide · q stop · esc cancel",
	})
	for _, l := range w.view(80, 40) {
		if ansi.StringWidth(l) > 80 {
			t.Errorf("%q is %d columns, past 80", ansi.Strip(l), ansi.StringWidth(l))
		}
	}
}

// What more there is to see of a question is never cut: a line too wide
// wraps, its words where they were; one that fits is as it is.
func TestQuestionsMoreWraps(t *testing.T) {
	q := question("example-app")
	q.More = []string{"~/Library/Application Support/Example App/Some Folder With A Long Name/And Deeper Still", "~/.example   kept"}
	w := newWalk("", []Question{q})
	equal(t, "view", plain(w.view(60, 40))[1:5], []string{
		"  ▲ example-app  cask · installed, not declared",
		"  │ ~/Library/Application Support/Example App/Some Folder",
		"  │ With A Long Name/And Deeper Still",
		"  │ ~/.example   kept",
	})
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

// The field's row says what needs the password whole, however many names
// it carries: too long for its line, the names wrap on theirs, what's
// needed under them.
func TestSecretFieldSaysWhatItNeedsWhole(t *testing.T) {
	f := &field{about: look.Row{State: look.NeedsYou, Name: "charger:hibernatemode, charger:networkoversleep, charger:womp", Says: look.Orange("need an administrator's password")}}
	equal(t, "view", plain(f.view(80, 40))[:3], []string{"  ▲ charger:hibernatemode, charger:networkoversleep, charger:womp", "  │ need an administrator's password", "  │ ❯  "})
}

// A question asked alone is never cut, nor what it leaves: one that fits
// is on its line; too long for it, its name and what's asked stack,
// wrapped.
func TestQuestionSaysWhatItAsksWhole(t *testing.T) {
	w := newWalk("", []Question{question("visual-studio-code-insiders-with-a-long-name")})
	equal(t, "row", plain(w.view(60, 40))[1:3], []string{"  ▲ visual-studio-code-insiders-with-a-long-name", "  │ cask · installed, not declared"})
	press(w, enter)
	equal(t, "leaves", plain(w.leaves(40)), []string{"", "  ● visual-studio-code-insiders-with-a-", "    long-name", "    adopt"})
	short := newWalk("", []Question{question("zoom")})
	press(short, enter)
	equal(t, "a short one's leaves", plain(short.leaves(40)), []string{"", "  ● zoom  adopt"})
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

// An arrow split across two reads, as over a slow connection, is the
// arrow, not escape and then text; an escape read alone, with nothing after
// it for a moment, is escape, as it is when the keys end.
func TestKeysWaitForTheRestOfAnEscape(t *testing.T) {
	in, typing := io.Pipe()
	keys, stop := Terminal{In: in}.keys()
	defer stop()
	go func() {
		for _, read := range []string{"\x1b", "[A"} {
			_, _ = typing.Write([]byte(read))
		}
		_, _ = typing.Write([]byte("\x1b"))
		time.Sleep(2 * escapeWait)
		_, _ = typing.Write([]byte("q\x1b"))
		_ = typing.Close()
	}()
	var got []key
	for k := range keys {
		got = append(got, k)
	}
	if want := []key{{name: "up"}, {name: "esc"}, {text: "q"}, {name: "esc"}}; !slices.Equal(got, want) {
		t.Errorf("keys = %+v, want %+v", got, want)
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

// An answer taken is its label with its first letter lowered, whatever
// the letter, the rest as it was; an empty label stays empty.
func TestTaken(t *testing.T) {
	for label, want := range map[string]string{"Keep the Mac's": "keep the Mac's", "Écrire": "écrire", "": ""} {
		if got := taken(look.Choice{Label: label}); got != want {
			t.Errorf("taken(%q) = %q, want %q", label, got, want)
		}
	}
}
