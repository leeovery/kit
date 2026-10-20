package render_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/colorprofile"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
)

var keyR = ask.Key{Text: "r"}

// printed is the step called step's command printing lines, from sec
// seconds in, a second apart.
func printed(step string, sec int, lines ...string) []event.Event {
	var events []event.Event
	for i, l := range lines {
		events = append(events, event.Output{Time: after(sec + i), Step: step, Command: "brew install", Line: l})
	}
	return events
}

// printing is sixMinutesIn, Homebrew's formulae and Claude Code's set-up
// printing as they go.
func printing() []event.Event {
	return slices.Concat(sixMinutesIn(),
		printed("brew", 351, "==> Fetching ffmpeg", "==> Pouring ffmpeg--7.1.arm64_sequoia.bottle.tar.gz"),
		printed("claude-code", 353, "Installing Claude Code native build"))
}

// The raw view of Homebrew's formulae in printing, as it shows them.
var formulaeRaw = []string{
	"  03:09:56  │ ==> Fetching ffmpeg",
	"  03:09:57  │ ==> Pouring ffmpeg--7.1.arm64_sequoia.bottle.tar.gz",
}

// rawHeading is the right pane's heading in a screen w wide.
func rawHeading(screen string, w int) string {
	_, right := panes(screen, w)
	return right[1]
}

// rawShown is what the raw view shows in a screen w wide: the right pane's
// lines under its heading, to the last that isn't blank; none when it
// isn't showing.
func rawShown(screen string, w int) []string {
	_, right := panes(screen, w)
	if !strings.Contains(right[1], "OWN OUTPUT") {
		return nil
	}
	shown := right[3:]
	for len(shown) > 0 && shown[len(shown)-1] == "" {
		shown = shown[:len(shown)-1]
	}
	return shown
}

// With a row chosen whose commands have printed something, the log's
// heading has the key to its raw view at its right end, two cells in, in
// the tool's name where the row's kind runs one, Homebrew for its
// formulae and casks; with none chosen, or one that's printed nothing, the
// heading alone; in a pane too narrow for both, the heading alone.
func TestApplyingLogHeadingOffersTheRawView(t *testing.T) {
	for _, tt := range []struct {
		name   string
		w, h   int
		keys   func(string, int) []ask.Key
		events []event.Event
		want   string
		ends   string
		whole  int
	}{
		{name: "no row", w: 160, h: 64, want: "  LOG"},
		{name: "a row that printed nothing", w: 160, h: 64, keys: clickRow("Homebrew casks"), want: "  LOG · HOMEBREW CASKS"},
		{name: "Homebrew's", w: 160, h: 64, keys: clickRow("Homebrew formulae"), want: "  LOG · HOMEBREW FORMULAE", ends: "r Homebrew's own output", whole: 160 - 60 - 1 - 2},
		{name: "a row without a tool", w: 160, h: 64, keys: clickRow("Your steps"), want: "  LOG · YOUR STEPS", ends: "r its own output", whole: 160 - 60 - 1 - 2},
		{name: "a row whose kinds run tools of their own", w: 160, h: 64, keys: clickRow("Developer tools"), events: printed("npm", 354, "added 1 package"), want: "  LOG · DEVELOPER TOOLS", ends: "r its own output", whole: 160 - 60 - 1 - 2},
		{name: "too narrow for both", w: 100, h: 30, keys: clickRow("Homebrew formulae"), want: "  LOG · HOMEBREW FORMULAE"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			face, out, kb := applyAt(tt.w, tt.h, 6*time.Minute+12*time.Second, slices.Concat(printing(), tt.events))
			defer func() { _ = face.Close() }()
			if tt.keys != nil {
				kb.press(t, tt.keys(screen(out.String()), tt.w)...)
			}
			got := rawHeading(screen(out.String()), tt.w)
			if tt.ends == "" && got != tt.want {
				t.Fatalf("the log's heading is %q; want %q", got, tt.want)
			}
			if tt.ends != "" && (!strings.HasPrefix(got, tt.want+"  ") || !strings.HasSuffix(got, "  "+tt.ends) || utf8.RuneCountInString(got) != tt.whole) {
				t.Errorf("the log's heading is %q; want %q, then %q ending two cells from the pane's edge", got, tt.want, tt.ends)
			}
		})
	}
}

// The raw view's key is styled as the screen's keys are: the key white,
// what it does muted.
func TestApplyingRawViewKeyIsStyled(t *testing.T) {
	face, out, kb := applyIn(colorprofile.TrueColor, 160, 64, 6*time.Minute+12*time.Second, printing())
	defer func() { _ = face.Close() }()
	kb.press(t, clickRow("Homebrew formulae")(screen(uncoloured(out.String())), 160)...)
	if frame := strings.Join(lastFrame(out.String()), "\n"); !strings.Contains(frame, look.White("r")+" "+look.Muted("Homebrew's own output")) {
		t.Errorf("the log's heading doesn't style its key as the screen's keys are:\n%q", frame)
	}
	kb.press(t, keyR)
	if frame := strings.Join(lastFrame(out.String()), "\n"); !strings.Contains(frame, look.Keys(look.Key{Key: "↑↓", Does: "scroll"}, look.Key{Key: "esc", Does: "back to the log"})) {
		t.Errorf("the raw view's heading doesn't style its keys as the screen's keys are:\n%q", frame)
	}
}

// r, with a row chosen, shows its commands' lines in the log's place,
// oldest first, each its time and a spine, under the row and its tool's
// own output, with the keys at the heading's end; lines keep coming while
// it's open. esc goes back to the log, the row still chosen, then to
// everything; r again goes back too. Choosing another row while it's open
// shows that row's lines.
func TestApplyingRawView(t *testing.T) {
	face, out, kb := applyAt(160, 64, 6*time.Minute+12*time.Second, printing())
	defer func() { _ = face.Close() }()
	shows := func() string { return screen(out.String()) }
	kb.press(t, clickRow("Homebrew formulae")(shows(), 160)...)
	kb.press(t, keyR)
	got := shows()
	if h := rawHeading(got, 160); !strings.HasPrefix(h, "  HOMEBREW FORMULAE · HOMEBREW'S OWN OUTPUT  ") || !strings.HasSuffix(h, "  ↑↓ scroll · esc back to the log") {
		t.Errorf("r, the right pane's heading is %q", h)
	}
	if raw := rawShown(got, 160); !slices.Equal(raw, formulaeRaw) {
		t.Errorf("r, the raw view is\n%s\nwant\n%s", strings.Join(raw, "\n"), strings.Join(formulaeRaw, "\n"))
	}
	face.Emit(event.Output{Time: after(380), Step: "brew", Command: "brew install", Line: "🍺  /opt/homebrew/Cellar/ffmpeg/7.1: 285 files"})
	time.Sleep(300 * time.Millisecond)
	if raw := rawShown(shows(), 160); len(raw) != 3 || raw[2] != "  03:10:25  │ 🍺  /opt/homebrew/Cellar/ffmpeg/7.1: 285 files" {
		t.Errorf("a line came while the raw view was open, it is\n%s\nwant it at its foot", strings.Join(raw, "\n"))
	}
	kb.press(t, keyEsc)
	if h := rawHeading(shows(), 160); !strings.HasPrefix(h, "  LOG · HOMEBREW FORMULAE  ") {
		t.Errorf("esc from the raw view, the heading is %q; want the log, Homebrew formulae still chosen", h)
	}
	kb.press(t, keyEsc)
	if h := rawHeading(shows(), 160); h != "  LOG" {
		t.Errorf("esc twice from the raw view, the heading is %q; want the log of everything", h)
	}
	kb.press(t, clickRow("Homebrew formulae")(shows(), 160)...)
	kb.press(t, keyR, keyR)
	if h := rawHeading(shows(), 160); !strings.HasPrefix(h, "  LOG · HOMEBREW FORMULAE  ") {
		t.Errorf("r twice, the heading is %q; want the log, Homebrew formulae still chosen", h)
	}
	kb.press(t, keyR)
	kb.press(t, clickRow("Your steps")(shows(), 160)...)
	got = shows()
	if h := rawHeading(got, 160); !strings.HasPrefix(h, "  YOUR STEPS · ITS OWN OUTPUT  ") {
		t.Errorf("Your steps chosen with the raw view open, the heading is %q", h)
	}
	if raw := rawShown(got, 160); !slices.Equal(raw, []string{"  03:09:58  │ Installing Claude Code native build"}) {
		t.Errorf("Your steps chosen with the raw view open, it is\n%s", strings.Join(raw, "\n"))
	}
	kb.press(t, keyTab, keyTab)
	if h := rawHeading(shows(), 160); h != "  LOG" {
		t.Errorf("everything chosen with the raw view open, the heading is %q; want the log of everything", h)
	}
}

// r with no row chosen, or with one whose commands have printed nothing,
// does nothing.
func TestApplyingRawViewNeedsARowThatPrinted(t *testing.T) {
	for _, tt := range []struct {
		name string
		keys func(string, int) []ask.Key
	}{
		{"no row", nil},
		{"a row that printed nothing", clickRow("Homebrew casks")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			face, out, kb := applyAt(160, 64, 6*time.Minute+12*time.Second, printing())
			defer func() { _ = face.Close() }()
			if tt.keys != nil {
				kb.press(t, tt.keys(screen(out.String()), 160)...)
			}
			before := screen(out.String())
			kb.press(t, keyR)
			if after := screen(out.String()); after != before {
				t.Errorf("r changed the screen from\n%s\nto\n%s", before, after)
			}
		})
	}
}

// The raw view's spine is in the row's colour: cyan while it runs, red
// once it's failed, else done's.
func TestApplyingRawViewSpine(t *testing.T) {
	brewDone := append(printing(), fine(360, "brew", "129 installed"))
	for _, tt := range []struct {
		name   string
		events []event.Event
		row    string
		at     time.Time
		line   string
		spine  string
	}{
		{"running", printing(), "Homebrew formulae", after(351), "==> Fetching ffmpeg", look.Cyan("│")},
		{"failed", casksFailed(), "Homebrew casks", after(355), "==> Installing Cask tailscale-app", look.Red("│")},
		{"done", brewDone, "Homebrew formulae", after(351), "==> Fetching ffmpeg", look.Pink("│")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			face, out, kb := applyIn(colorprofile.TrueColor, 160, 64, 6*time.Minute+12*time.Second, tt.events)
			defer func() { _ = face.Close() }()
			kb.press(t, clickRow(tt.row)(screen(uncoloured(out.String())), 160)...)
			kb.press(t, keyR)
			want := "  " + look.Dim(tt.at.Format("15:04:05")) + "  " + tt.spine + " " + look.Muted(tt.line)
			if frame := strings.Join(lastFrame(out.String()), "\n"); !strings.Contains(frame, want) {
				t.Errorf("the raw view doesn't hold %q:\n%q", want, frame)
			}
		})
	}
}

// The raw view follows the newest line, and scrolls back as the log does,
// a rule and how many lines are below at its foot.
func TestApplyingRawViewScrolls(t *testing.T) {
	var many []string
	for i := range 80 {
		many = append(many, fmt.Sprintf("line %d", i+1))
	}
	face, out, kb := applyAt(160, 64, 7*time.Minute, append(sixMinutesIn(), printed("brew", 351, many...)...))
	defer func() { _ = face.Close() }()
	kb.press(t, clickRow("Homebrew formulae")(screen(out.String()), 160)...)
	kb.press(t, keyR)
	if raw := rawShown(screen(out.String()), 160); !strings.HasSuffix(raw[len(raw)-1], "│ line 80") {
		t.Errorf("the raw view ends %q; want it following the newest", raw[len(raw)-1])
	}
	kb.press(t, keyUp)
	_, right := panes(screen(out.String()), 160)
	if foot := right[len(right)-1]; foot != "  ↓ 3 new lines · end or scroll down to follow again" || strings.Trim(right[len(right)-2], "─") != "" {
		t.Errorf("↑ in the raw view, its foot is\n%s\nwant a rule, then how many lines are below", strings.Join(right[len(right)-2:], "\n"))
	}
	kb.press(t, keyHome)
	if raw := rawShown(screen(out.String()), 160); !strings.HasSuffix(raw[0], "│ line 1") {
		t.Errorf("home in the raw view, it starts %q; want its first line", raw[0])
	}
}

// A step's raw lines are kept to its newest 5000, as far back as the raw
// view goes.
func TestApplyingRawViewKeepsTheNewest(t *testing.T) {
	var lines []string
	for i := range 5003 {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	events := append(sixMinutesIn(), printed("brew", 351, lines...)...)
	events = append(events, finished(6000, "brew", check.Result{State: check.OK, Summary: "fine"}))
	face, out, kb := applyAt(160, 64, 2*time.Hour, events)
	defer func() { _ = face.Close() }()
	kb.press(t, clickRow("Homebrew formulae")(screen(out.String()), 160)...)
	kb.press(t, keyR, keyHome)
	if raw := rawShown(screen(out.String()), 160); !strings.HasSuffix(raw[0], "│ line 4") {
		t.Errorf("home in the raw view, it starts %q; want the first of the newest 5000, line 4", raw[0])
	}
}
