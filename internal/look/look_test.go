package look_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/look"
)

func text(lines ...string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}

func same(t *testing.T, got []string, want ...string) {
	t.Helper()
	if g := text(got...); !slices.Equal(g, want) {
		t.Errorf("drew\n%s\nwant\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
	}
}

// A row is its mark, its name, then what it says two spaces on, extras
// after dots; what belongs to it goes under its name, on a timeline's line
// when it has one.
func TestRowsAndTimelines(t *testing.T) {
	same(t, look.Rows("  ", look.Width,
		look.Row{State: look.Done, Name: "jq", Says: look.Says(look.Muted("installed 1.8.1"), look.Muted("declared in laptop"))},
		look.Row{State: look.NeedsYou, Name: "ffmpeg", Says: look.Orange("installed, not declared"), Under: []string{look.Todo(look.Cmd("kit reconcile"))}},
		look.Row{State: look.Skipped, Name: "kit-config", Says: look.Muted("skipped")},
	),
		"  ● jq  installed 1.8.1 · declared in laptop",
		"  ▲ ffmpeg  installed, not declared",
		"    → kit reconcile",
		"  – kit-config  skipped",
	)
	same(t, look.Timeline("  ", look.Width,
		look.Row{State: look.Running, Name: "jq", Says: look.Says(look.Cyan("installing"), look.Dim("brew install --formula jq")), Under: look.Output("==> Pouring jq", "✔ jq")},
		look.Row{State: look.Queued, Name: "kit-config", Says: look.Muted("waiting")},
	),
		"  ◐ jq  installing · brew install --formula jq",
		"  │ ==> Pouring jq",
		"  │ ✔ jq",
		"  ○ kit-config  waiting",
	)
}

// No line passes the width it's given: a long one is cut, an ellipsis
// ending it.
func TestRowsAreCut(t *testing.T) {
	got := look.Timeline("  ", 30, look.Row{State: look.Failed, Name: "ripgrap", Says: look.Red("couldn't install"), Under: look.Output(`No available formula with the name "ripgrap"`)})
	same(t, got, "  ✗ ripgrap  couldn't install", `  │ No available formula with…`)
	for _, l := range got {
		if w := ansi.StringWidth(l); w > 30 {
			t.Errorf("%q is %d wide", ansi.Strip(l), w)
		}
	}
}

func TestStatesMarks(t *testing.T) {
	for s, want := range map[look.State]string{look.Done: "●", look.NeedsYou: "▲", look.Failed: "✗", look.Running: "◐", look.Queued: "○", look.Skipped: "–", look.ToDo: "△"} {
		if got := ansi.Strip(look.Mark(s)); got != want {
			t.Errorf("Mark(%d) = %q, want %q", s, got, want)
		}
	}
	if look.Mark(look.Done) == look.Mark(look.Failed) || ansi.Strip(look.Words(look.Failed, "x")) != "x" {
		t.Error("marks and words: want each state its colour, the words kept")
	}
	if look.Mark(look.ToDo) != look.White("△") || look.Words(look.ToDo, "no backup disk") != look.White("no backup disk") {
		t.Error("what's left to do: want its mark and its words white, never orange")
	}
}

// A question's answers: the cursor in the marks' column, the chosen label a
// pill whose words line up with the others'.
func TestAnswers(t *testing.T) {
	same(t, look.Answers([]look.Choice{
		{Label: "Reconcile", Does: "decide on ffmpeg", Cmd: "kit reconcile"},
		{Label: "Apply", Does: "install what's missing", Cmd: "kit apply"},
		{Label: "More"},
	}, 0),
		"❯ Reconcile  decide on ffmpeg · kit reconcile",
		"  Apply  install what's missing · kit apply",
		"  More",
	)
}

func TestFieldAndKeys(t *testing.T) {
	if got := ansi.Strip(look.Field(3)); got != "❯ ••• " {
		t.Errorf("Field(3) = %q", got)
	}
	if got := ansi.Strip(look.Keys(look.Key{Key: "enter", Does: "done"}, look.Key{Key: "esc", Does: "cancel"})); got != "  enter done · esc cancel" {
		t.Errorf("Keys() = %q", got)
	}
}

// The wordmark, with what ran, the Mac and when stacked beside it.
func TestHead(t *testing.T) {
	same(t, look.Head(look.Meta("status", "laptop", "Wed 7 Oct · 14:36")...),
		"  █  ▄▀  ▀█▀  ▀▀█▀▀  │  status",
		"  █▀▀▄    █     █    │  laptop",
		"  █   █  ▄█▄    █    │  Wed 7 Oct · 14:36",
	)
	same(t, look.Head(look.Meta("", "laptop", "Wed 7 Oct · 11:20")...),
		"  █  ▄▀  ▀█▀  ▀▀█▀▀  │  laptop",
		"  █▀▀▄    █     █    │  Wed 7 Oct · 11:20",
		"  █   █  ▄█▄    █    │",
	)
}

func TestBlocksAndSummaries(t *testing.T) {
	same(t, look.Block("Mac", look.Width, look.Row{State: look.Done, Name: "Disk", Says: look.Muted("48% free")}),
		"  MAC",
		"  ● Disk  48% free",
	)
	same(t, []string{look.Header("Reconcile", "3 of 5"), look.Summary(look.Orange("1 needs you"), look.White("23 fine"), look.Muted("1.8s"))},
		"  RECONCILE  3 of 5",
		"  1 needs you · 23 fine · 1.8s",
	)
}

// A run's lights: each row led by its mark, two spaces between, as many
// to a line as fit; one that needs you, or failed, a pill.
func TestLights(t *testing.T) {
	lamps := []look.Lamp{
		{Name: "Backups", State: look.Done, Count: "5"},
		{Name: "Git", State: look.NeedsYou},
		{Name: "Your steps", State: look.Running},
		{Name: "This Mac", State: look.Queued},
		{Name: "Login items", State: look.Skipped},
		{Name: "Secrets", State: look.ToDo},
	}
	got := look.Lights(80, lamps...)
	same(t, got, "  ● BACKUPS 5   ▲ GIT   ◐ YOUR STEPS  ○ THIS MAC  – LOGIN ITEMS  △ SECRETS")
	if !strings.Contains(got[0], look.Muted("– LOGIN ITEMS")) {
		t.Errorf("Lights() = %q, want a row a stopped run didn't finish grey, mark and all", got[0])
	}
	if !strings.HasSuffix(got[0], look.White("△")+" "+look.White("SECRETS")) {
		t.Errorf("Lights() = %q, want a row with something left to do its white mark, its name white", got[0])
	}
	same(t, look.Lights(40, lamps...),
		"  ● BACKUPS 5   ▲ GIT   ◐ YOUR STEPS",
		"  ○ THIS MAC  – LOGIN ITEMS  △ SECRETS",
	)
}

// A card: an orange rule across, w wide with the margins, carrying the
// needs-you mark, the title strong and what it says; a blank line; its
// steps numbered, the numbers orange, each wrapped under its own words and
// a blank line after it; then what's said after them, muted, wrapped, and
// a blank line. A title too long for its rule is cut, some rule kept.
func TestCard(t *testing.T) {
	steps := []string{"1Password is open: kit opened it for you", "In 1Password, Settings › Developer › turn on Integrate with 1Password CLI"}
	got := look.Card(70, "Sign in to 1Password", look.Says(look.Orange("needs you"), look.Muted("2 more after this")), steps, "kit carries on by itself once it's done")
	same(t, got,
		"  ── ▲ Sign in to 1Password  needs you · 2 more after this ─────────",
		"",
		"       1  1Password is open: kit opened it for you",
		"",
		"       2  In 1Password, Settings › Developer › turn on Integrate",
		"          with 1Password CLI",
		"",
		"       kit carries on by itself once it's done",
		"",
	)
	if !strings.HasPrefix(got[0], "  "+look.Orange("── ")+look.Orange("▲")+" "+look.Strong("Sign in to 1Password")) || !strings.HasSuffix(got[0], " "+look.Orange("─────────")) {
		t.Errorf("the card's rule is %q; want it orange, the mark orange, the title strong", got[0])
	}
	for _, l := range got {
		if w := ansi.StringWidth(l); w > 68 {
			t.Errorf("%q is %d wide; want it to end two cells from the edge", ansi.Strip(l), w)
		}
	}
	same(t, look.Card(30, "Allow Karabiner-Elements", look.Orange("needs you"), []string{"Allow its driver"}, ""),
		"  ── ▲ Allow Karabiner-… ───",
		"",
		"       1  Allow its driver",
		"",
	)
}

// What's left to do, numbered: the to-do mark, the number and the title,
// then how, each thing under the title after an arrow, muted, wrapped
// under its own words; a title too long cut.
func TestTasks(t *testing.T) {
	got := look.Tasks(50,
		look.Task{Title: "Add a Time Machine disk", How: []string{"System Settings › General › Time Machine › Add Backup Disk"}},
		look.Task{Title: "Install Paper", How: []string{"Download it from paper.example.com", "Drag it to Applications"}},
		look.Task{Title: "A title far too long for a list fifty cells wide"},
	)
	same(t, got,
		"  △ 1  Add a Time Machine disk",
		"       → System Settings › General › Time Machine",
		"         › Add Backup Disk",
		"  △ 2  Install Paper",
		"       → Download it from paper.example.com",
		"       → Drag it to Applications",
		"  △ 3  A title far too long for a list fifty cell…",
	)
	if !strings.HasPrefix(got[0], "  "+look.White("△")+" "+look.White("1")+"  "+look.Strong("Add a Time Machine disk")) {
		t.Errorf("a task's line is %q; want its mark and number white, its title strong", got[0])
	}
}

// Wrapping cuts at spaces, no line wider than asked but a word too wide on
// its own.
func TestWrap(t *testing.T) {
	if got := look.Wrap("turn on Integrate with 1Password CLI", 16); !slices.Equal(got, []string{"turn on", "Integrate with", "1Password CLI"}) {
		t.Errorf("Wrap() = %q", got)
	}
	if got := look.Wrap("a karabiner_grabber b", 5); !slices.Equal(got, []string{"a", "karabiner_grabber", "b"}) {
		t.Errorf("Wrap() = %q, want the long word on its own", got)
	}
}

// The bar: a segment a step, the last one in the colour the run ended in.
func TestBar(t *testing.T) {
	running := look.Bar(2, 1, 5, look.Done)
	if ansi.Strip(running) != "▮▮▮▮▮" || !strings.Contains(running, look.Cyan("▮")) {
		t.Errorf("Bar(2, 1, 5) = %q", running)
	}
	if long := look.Bar(40, 0, 80, look.Done); ansi.StringWidth(long) != 32 || strings.Count(long, look.Dim("▮")) != 16 {
		t.Errorf("Bar(40, 0, 80) = %q, want 32 segments, half done", ansi.Strip(long))
	}
	ended := look.Bar(5, 0, 5, look.Failed)
	if !strings.HasSuffix(ended, look.Red("▮")) {
		t.Errorf("Bar(5, 0, 5, Failed) = %q, want the last segment red", ended)
	}
}

// Progress: frac of n cells along the gradient, rounded, the rest dim;
// ended failed or needing you, the last lit cell in that state's colour.
func TestProgress(t *testing.T) {
	half := look.Progress(0.5, 10, look.Done)
	if ansi.Strip(half) != "▮▮▮▮▮▮▮▮▮▮" || strings.Count(half, look.Dim("▮")) != 5 {
		t.Errorf("Progress(0.5, 10) = %q, want 5 of 10 lit", half)
	}
	if got := look.Progress(0.04, 10, look.Done); strings.Count(got, look.Dim("▮")) != 10 {
		t.Errorf("Progress(0.04, 10) = %q, want none lit, rounded down", got)
	}
	if got := look.Progress(1, 32, look.Failed); ansi.StringWidth(got) != 32 || !strings.HasSuffix(got, look.Red("▮")) {
		t.Errorf("Progress(1, 32, Failed) = %q, want 32 cells, the last red", got)
	}
	if got := look.Progress(1, 4, look.Done); strings.Contains(got, look.Red("▮")) || strings.Contains(got, look.Dim("▮")) {
		t.Errorf("Progress(1, 4, Done) = %q, want every cell along the gradient", got)
	}
}

// A junction: a dim rule across, w wide, the joint where it meets another.
func TestJunction(t *testing.T) {
	if got := look.Junction(8, 3, "┬"); got != look.Dim("───┬────") {
		t.Errorf("Junction(8, 3, ┬) = %q", got)
	}
	if got := look.Junction(5, 0, "├"); got != look.Dim("├────") {
		t.Errorf("Junction(5, 0, ├) = %q", got)
	}
}

// Hints: a screen's keys, dim, after the margin.
func TestHints(t *testing.T) {
	if got := look.Hints(look.Key{Key: "tab", Does: "next row"}, look.Key{Key: "q", Does: "stop"}); got != "  "+look.Dim("tab next row · q stop") {
		t.Errorf("Hints() = %q", got)
	}
}

// A command too long for its room is cut in the middle, so how it ended
// still shows.
func TestRan(t *testing.T) {
	if got := ansi.Strip(look.Ran("brew list --formula --full-name -1", 0, "0.4s", 60)); got != "brew list --formula --full-name -1 · exit 0 · 0.4s" {
		t.Errorf("Ran() = %q", got)
	}
	got := ansi.Strip(look.Ran("~/.config/kit/shared/steps/remote-session/run apply", 1, "0.3s", 40))
	if got != "~/.config…sion/run apply · exit 1 · 0.3s" || ansi.StringWidth(got) > 40 {
		t.Errorf("Ran(), cut = %q", got)
	}
}

// A command of many lines, as a script given with -e, shows on one line,
// cut in the middle to fit.
func TestRanOnOneLine(t *testing.T) {
	script := "osascript -l JavaScript -e 'function run() {\n  var se = Application(1);\n  return 2;\n}'"
	got := ansi.Strip(look.Ran(script, 1, "2m00s", 60))
	if strings.Contains(got, "\n") || ansi.StringWidth(got) > 60 || !strings.HasPrefix(got, "osascript -l") || !strings.HasSuffix(got, "return 2; }' · exit 1 · 2m00s") {
		t.Errorf("Ran() = %q, want it on one line, cut in the middle to 60", got)
	}
	if got := look.OneLine("a\n  b\tc  "); got != "a b c" {
		t.Errorf("OneLine() = %q, want %q", got, "a b c")
	}
}

func TestRule(t *testing.T) {
	if got := ansi.Strip(look.Rule(10)); got != "  ────────" {
		t.Errorf("Rule(10) = %q", got)
	}
}

// The boot's log: a state's bracket in the mark's place, the name, then what
// it says; what belongs to a line under its name; the wordmark plain, with
// what's running, the Mac and when beside it.
func TestBootRows(t *testing.T) {
	b := look.NewBoot(true)
	same(t, b.Rows(look.Width,
		look.Row{State: look.Done, Name: "GitHub", Says: b.Says(b.Mid("someone"), b.Mid("kit-config has laptop and studio"))},
		look.Row{State: look.NeedsYou, Name: "This Mac", Says: b.Words(look.NeedsYou, "which of your Macs is this?"), Under: b.Answers([]look.Choice{{Label: "laptop"}, {Label: "studio"}, {Label: "new", Does: "a Mac kit-config doesn't have yet"}}, 0)},
		look.Row{State: look.Running, Name: "Homebrew", Says: b.Says(b.Words(look.Running, "installing"), b.Mid("2m14s")), Under: b.Output("==> Installing Command Line Tools")},
		look.Row{State: look.Queued, Name: "Password", Says: b.Says(b.Mid("once"), b.Mid("Touch ID after"))},
		look.Row{State: look.Skipped, Name: "Not here", Says: b.Says(b.Mid("Homebrew"), b.Mid("1Password"))},
		look.Row{State: look.Failed, Name: "kit-config", Says: b.Words(look.Failed, "couldn't clone")},
	),
		"  [ OK ] GitHub  someone · kit-config has laptop and studio",
		"  [ !! ] This Mac  which of your Macs is this?",
		"         ❯ laptop ",
		"           studio",
		"           new  a Mac kit-config doesn't have yet",
		"  [*   ] Homebrew  installing · 2m14s",
		"         ==> Installing Command Line Tools",
		"  [    ] Password  once · Touch ID after",
		"  [ -- ] Not here  Homebrew · 1Password",
		"  [FAIL] kit-config  couldn't clone",
	)
	same(t, b.Head(b.Meta("bootstrap", "Some-MacBook", "Thu 8 Oct · 18:02")...),
		"  █  ▄▀  ▀█▀  ▀▀█▀▀  │  bootstrap",
		"  █▀▀▄    █     █    │  Some-MacBook",
		"  █   █  ▄█▄    █    │  Thu 8 Oct · 18:02",
	)
	same(t, []string{b.Bar(3, 8), b.Keys(look.Key{Key: "enter", Does: "boot"}, look.Key{Key: "esc", Does: "stop"}), b.Field(3)},
		strings.Repeat("█", 15)+strings.Repeat("░", 25), "  enter boot · esc stop", "❯ ••• ")
}

// GitHub's QR code: square, a quiet zone round it, and drawn the other way
// round on a light background, so it's dark on light either way.
func TestBootQR(t *testing.T) {
	dark, err := look.NewBoot(true).QR("https://github.com/login/device")
	if err != nil {
		t.Fatal(err)
	}
	light, err := look.NewBoot(false).QR("https://github.com/login/device")
	if err != nil {
		t.Fatal(err)
	}
	d, l := text(dark...), text(light...)
	// 29 modules and a quiet zone of two each side, two modules a line.
	if len(d) != 17 || len([]rune(d[0])) != 33 {
		t.Fatalf("the code is %d lines of %d cells; want 17 of 33", len(d), len([]rune(d[0])))
	}
	if d[0] != strings.Repeat("█", 33) || strings.TrimSpace(l[0]) != "" {
		t.Errorf("first lines: dark %q, light %q; want the quiet zone lit on dark, blank on light", d[0], l[0])
	}
	swap := strings.NewReplacer("█", " ", " ", "█", "▀", "▄", "▄", "▀")
	for i := 1; i < len(d)-1; i++ {
		if got, want := []rune(swap.Replace(l[i])), []rune(d[i]); string(got[2:len(got)-2]) != string(want[2:len(want)-2]) {
			t.Errorf("line %d: light isn't dark the other way round:\n%s\n%s", i, d[i], l[i])
		}
	}
}

// The wordmark's pixels, as what draws it a cell at a time reads them.
func TestWordmarkPixel(t *testing.T) {
	var rows []string
	for y := range 6 {
		var row strings.Builder
		for x := range 17 {
			if look.WordmarkPixel(x, y) {
				row.WriteString("#")
			} else {
				row.WriteString(".")
			}
		}
		rows = append(rows, row.String())
	}
	want := []string{
		"#...#..###..#####",
		"#..#....#.....#..",
		"###.....#.....#..",
		"#..#....#.....#..",
		"#...#...#.....#..",
		"#...#..###....#..",
	}
	if !slices.Equal(rows, want) {
		t.Errorf("pixels:\n%s\nwant\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
}

// What's at work: a light bouncing back and forth in its brackets.
func TestBootScanner(t *testing.T) {
	var got []string
	for f := range 7 {
		got = append(got, ansi.Strip(look.NewBoot(true).At(f).Tag(look.Running)))
	}
	want := []string{"[*   ]", "[ *  ]", "[  * ]", "[   *]", "[  * ]", "[ *  ]", "[*   ]"}
	if !slices.Equal(got, want) {
		t.Errorf("scanner %q; want %q", got, want)
	}
}
