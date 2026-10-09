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
	for s, want := range map[look.State]string{look.Done: "●", look.NeedsYou: "▲", look.Failed: "✗", look.Running: "◐", look.Queued: "○", look.Skipped: "–"} {
		if got := ansi.Strip(look.Mark(s)); got != want {
			t.Errorf("Mark(%d) = %q, want %q", s, got, want)
		}
	}
	if look.Mark(look.Done) == look.Mark(look.Failed) || ansi.Strip(look.Words(look.Failed, "x")) != "x" {
		t.Error("marks and words: want each state its colour, the words kept")
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

// A run's lights: each area led by its mark; one that needs you, or failed,
// a pill.
func TestLights(t *testing.T) {
	got := look.Lights(
		look.Lamp{Name: "Backups", State: look.Done, Count: "5"},
		look.Lamp{Name: "Config", State: look.NeedsYou},
		look.Lamp{Name: "Steps", State: look.Running},
		look.Lamp{Name: "Mac", State: look.Queued},
	)
	if want := "  ● BACKUPS 5   ▲ CONFIG   ◐ STEPS  ○ MAC"; ansi.Strip(got) != want {
		t.Errorf("Lights() = %q, want %q", ansi.Strip(got), want)
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
