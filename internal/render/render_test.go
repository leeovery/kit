package render_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/render"
)

var update = flag.Bool("update", false, "rewrite the golden files with what the faces print")

// golden compares got with the golden file name, in testdata, or rewrites it
// with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run with -update to write it): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("%s differs (run with -update to accept, then review the diff)\ngot:\n%s\nwant:\n%s", name, got, want)
	}
}

var at = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// run is a run's events: steps finishing out of order, one of each state,
// and items of each sort.
func run() []event.Event {
	steps := []event.Step{
		{Name: "homebrew", Title: "Homebrew"},
		{Name: "brew", Title: "Formulae"},
		{Name: "cask", Title: "Casks"},
		{Name: "kit-config", Title: "kit-config"},
		{Name: "mas", Title: "App Store"},
	}
	brew := check.Result{
		State: check.Attention, Summary: "6 declared, 4 installed", Counts: map[string]int{"declared": 6, "installed": 4},
		Items: []check.Item{
			{ID: "brew:jq", Name: "jq", State: "missing"},
			{ID: "brew:owner/tap/tool", Name: "owner/tap/tool", State: "missing"},
			{ID: "brew:ffmpeg", Name: "ffmpeg", State: "extra"},
			{ID: "brew:node@20", Name: "node@20", State: "unused-dependency"},
		},
	}
	return []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Version: "0.1.0", Steps: steps},
		event.StepStarted{Time: at, Step: "homebrew"},
		event.StepFinished{Time: at, Step: "homebrew", Result: check.Result{State: check.OK, Summary: "/opt/homebrew"}},
		event.StepStarted{Time: at, Step: "brew"},
		event.StepStarted{Time: at, Step: "cask"},
		event.StepStarted{Time: at, Step: "kit-config"},
		event.StepFinished{Time: at, Step: "cask", Result: check.Result{State: check.OK, Summary: "3 declared, all installed"}},
		event.StepFinished{Time: at, Step: "kit-config", Result: check.Result{State: check.Failed, Reason: "gh repo view exited 1: not signed in"}},
		event.StepFinished{Time: at, Step: "brew", Result: brew},
		event.StepFinished{Time: at, Step: "mas", Result: check.Result{State: check.Deferred, Reason: "needs Homebrew"}},
		event.RunFinished{Time: at, Duration: time.Second, Counts: map[check.State]int{check.OK: 2, check.Attention: 1, check.Failed: 1, check.Deferred: 1}},
	}
}

// statusRun is a run of kit status as the pretty face shows it: steps in
// their areas, Config's with their parts, one of each state, and items of
// each sort.
func statusRun() []event.Event {
	steps := []event.Step{
		{Name: "homebrew", Title: "Homebrew", Area: "Drift", Part: render.PartPackages},
		{Name: "brew", Title: "Formulae", Area: "Drift", Part: render.PartPackages},
		{Name: "cask", Title: "Casks", Area: "Drift", Part: render.PartPackages},
		{Name: "defaults", Title: "macOS settings", Area: "Drift", Part: render.PartSettings},
		{Name: "config-private", Title: "Privacy", Area: "Config", Part: render.PartConfig},
		{Name: "config-sync", Title: "Sync", Area: "Config", Part: render.PartConfig},
		{Name: "mas", Title: "App Store", Area: "Drift", Part: render.PartPackages},
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "disk", Title: "Disk space", Area: "Mac"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
	}
	brew := check.Result{
		State: check.Attention, Summary: "6 declared, 4 installed", Counts: map[string]int{"declared": 6, "installed": 4},
		Items: []check.Item{
			{ID: "brew:jq", Name: "jq", State: "missing", Action: "install"},
			{ID: "brew:ffmpeg", Name: "ffmpeg", State: "extra", Since: at.Add(-50 * time.Hour)},
			{ID: "brew:hello", Name: "hello", State: "extra", Quiet: "new"},
		},
	}
	ok := func(summary string, counts map[string]int) check.Result {
		return check.Result{State: check.OK, Summary: summary, Counts: counts}
	}
	return []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Version: "0.1.0", Steps: steps},
		event.StepFinished{Time: at, Step: "homebrew", Result: ok("/opt/homebrew", nil)},
		event.StepFinished{Time: at, Step: "cask", Result: ok("3 declared, all installed", map[string]int{"installed": 3})},
		event.StepFinished{Time: at, Step: "defaults", Result: ok("9 declared, all set", map[string]int{"installed": 9})},
		event.StepFinished{Time: at, Step: "config-private", Result: check.Result{State: check.OK, Summary: "private on GitHub", Glance: "private"}},
		event.StepFinished{Time: at, Step: "config-sync", Result: check.Result{State: check.Failed, Reason: "git status exited 128: not a git repository"}},
		event.StepFinished{Time: at, Step: "brew", Result: brew},
		event.StepFinished{Time: at, Step: "mas", Result: check.Result{State: check.Deferred, Reason: "needs Homebrew"}},
		event.StepFinished{Time: at, Step: "time-machine", Result: ok("last backup 02:35", nil)},
		event.StepFinished{Time: at, Step: "disk", Result: check.Result{State: check.Attention, Summary: "8% free", Items: []check.Item{
			{ID: "disk:disk", Name: "disk", State: "problem", Detail: "free some space: kit status says where it went"},
		}}},
		event.StepFinished{Time: at, Step: "fonts", Result: ok("16 installed", nil)},
		event.RunFinished{Time: at, Duration: 1800 * time.Millisecond, Counts: map[check.State]int{check.OK: 6, check.Attention: 2, check.Failed: 1, check.Deferred: 1}},
	}
}

func show(t *testing.T, face render.Face, events []event.Event) {
	t.Helper()
	for _, e := range events {
		face.Emit(e)
	}
	if err := face.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestPlain(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPlain(&out), run())
	golden(t, "plain.golden", out.String())
}

func TestPrettyLayout(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), statusRun())
	golden(t, "pretty.golden", out.String())
}

func TestPrettyColour(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.TrueColor}, 80, false), statusRun())
	golden(t, "pretty-colour.golden", out.String())
}

func TestJSON(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewJSON(&out), run())
	golden(t, "status.json.golden", out.String())
}

func TestEverythingOK(t *testing.T) {
	events := []event.Event{
		event.RunStarted{Command: "status", Machine: "studio", Steps: []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Part: render.PartPackages}}},
		event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "2 declared, all installed", Counts: map[string]int{"installed": 2}}},
		event.RunFinished{Counts: map[check.State]int{check.OK: 1}},
	}
	var plain, pretty bytes.Buffer
	show(t, render.NewPlain(&plain), events)
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &pretty, Profile: colorprofile.NoTTY}, 80, false), events)
	if want := "kit status · studio\nbrew ok 2 declared, all installed\nNothing needs attention\n"; plain.String() != want {
		t.Errorf("plain printed\n%s\nwant\n%s", plain.String(), want)
	}
	golden(t, "pretty-ok.golden", pretty.String())
}

// Each thing that needs attention is a row of its own, and no line passes
// the width, cut when it would.
func TestPrettyKeepsToItsWidth(t *testing.T) {
	var names []check.Item
	for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliett"} {
		names = append(names, check.Item{ID: "brew:" + n, Name: n, State: "extra"})
	}
	events := []event.Event{
		event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Part: render.PartPackages}}},
		event.StepFinished{Step: "brew", Result: check.Result{State: check.Attention, Summary: "10 not declared", Items: names}},
		event.RunFinished{Counts: map[check.State]int{check.Attention: 1}},
	}
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 50, false), events)
	golden(t, "pretty-narrow.golden", out.String())
	for line := range strings.Lines(out.String()) {
		if w := ansi.StringWidth(strings.TrimRight(line, "\n")); w > 50 {
			t.Errorf("line %q is %d columns, want at most 50", line, w)
		}
	}
}

func TestPrettySpinsWhileStepsRun(t *testing.T) {
	var out bytes.Buffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, true)
	steps := []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Part: render.PartPackages}, {Name: "cask", Title: "Casks", Area: "Drift", Part: render.PartPackages}}
	face.Emit(event.RunStarted{Command: "status", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "brew"})
	face.Emit(event.StepStarted{Step: "cask"})
	time.Sleep(200 * time.Millisecond)
	face.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "2 declared, all installed"}})
	face.Emit(event.StepFinished{Step: "cask", Result: check.Result{State: check.OK, Summary: "1 declared, all installed"}})
	face.Emit(event.RunFinished{Counts: map[check.State]int{check.OK: 2}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "◐ ▮▮  0 of 2 · checking Formulae, Casks") || !strings.Contains(got, "◓ ▮▮  1 of 2 · checking Casks") {
		t.Errorf("printed %q, want the loader turning: the bar, the steps counted and those running named", got)
	}
	// What's left on screen, once each line has been drawn over, is the
	// report alone.
	var screen []string
	for line := range strings.Lines(got) {
		if i := strings.LastIndex(line, "\r"); i >= 0 {
			line = line[i+1:]
		}
		screen = append(screen, strings.TrimPrefix(line, "\x1b[2K"))
	}
	var still bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &still, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Command: "status", Machine: "laptop", Steps: steps},
		event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "2 declared, all installed"}},
		event.StepFinished{Step: "cask", Result: check.Result{State: check.OK, Summary: "1 declared, all installed"}},
		event.RunFinished{Counts: map[check.State]int{check.OK: 2}},
	})
	if want := still.String(); strings.Join(screen, "") != want {
		t.Errorf("left on screen\n%q\nwant\n%q", strings.Join(screen, ""), want)
	}
}

func TestQuietItems(t *testing.T) {
	events := []event.Event{
		event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Part: render.PartPackages}}},
		event.StepFinished{Step: "brew", Result: check.Result{State: check.Attention, Summary: "3 declared, all installed", Items: []check.Item{
			{ID: "brew:hello", Name: "hello", State: "extra", Quiet: "new"},
			{ID: "brew:ffmpeg", Name: "ffmpeg", State: "extra"},
			{ID: "brew:wget", Name: "wget", State: "extra", Quiet: "snoozed"},
			{ID: "brew:cowsay", Name: "cowsay", State: "extra", Quiet: "temporary"},
		}}},
		event.RunFinished{Counts: map[check.State]int{check.Attention: 1}},
	}
	var plain, pretty bytes.Buffer
	show(t, render.NewPlain(&plain), events)
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &pretty, Profile: colorprofile.NoTTY}, 80, false), events)
	wantPlain := "kit status · laptop\nbrew attention 3 declared, all installed\nbrew extra ffmpeg\nbrew extra:new hello\nbrew extra:snoozed wget\nbrew extra:temporary cowsay\n1 needs attention\n"
	if plain.String() != wantPlain {
		t.Errorf("plain printed\n%s\nwant\n%s", plain.String(), wantPlain)
	}
	golden(t, "pretty-quiet.golden", pretty.String())
}

func TestActionsAndWhatWasDone(t *testing.T) {
	brew := check.Result{State: check.OK, Summary: "3 declared, all installed", Done: []check.Item{
		{ID: "brew:jq", Name: "jq", State: "missing", Action: "install"},
		{ID: "brew:ripgrep", Name: "ripgrep", State: "missing", Action: "install"},
	}}
	cask := check.Result{State: check.Attention, Summary: "2 declared, 0 installed", Items: []check.Item{
		{ID: "cask:ghostty", Name: "ghostty", State: "missing", Action: "install"},
		{ID: "cask:zoom", Name: "zoom", State: "missing", Detail: "needs an administrator's password"},
	}}
	var plain bytes.Buffer
	show(t, render.NewPlain(&plain), []event.Event{
		event.RunStarted{Command: "apply", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae"}, {Name: "cask", Title: "Casks"}}},
		event.StepFinished{Step: "brew", Result: brew},
		event.StepFinished{Step: "cask", Result: cask},
		event.RunFinished{Counts: map[check.State]int{check.OK: 1, check.Attention: 1}},
	})
	wantPlain := "kit apply · laptop\nbrew ok 3 declared, all installed\nbrew installed jq, ripgrep\n" +
		"cask attention 2 declared, 0 installed\ncask missing ghostty (to install)\ncask missing zoom (needs an administrator's password)\n1 needs attention\n"
	if plain.String() != wantPlain {
		t.Errorf("plain printed\n%s\nwant\n%s", plain.String(), wantPlain)
	}
	// Applying shows its lights, what it did and what needs attention, by
	// area, and its bar.
	var pretty bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &pretty, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Command: "apply", Machine: "laptop", Time: at, Steps: []event.Step{
			{Name: "brew", Title: "Formulae", Area: "Drift", Part: render.PartPackages},
			{Name: "cask", Title: "Casks", Area: "Drift", Part: render.PartPackages},
			{Name: "fonts", Title: "fonts", Area: "Steps"},
			{Name: "disk", Title: "Disk space", Area: "Mac"},
		}},
		event.StepFinished{Step: "brew", Result: brew},
		event.StepFinished{Step: "cask", Result: cask},
		event.StepFinished{Step: "fonts", Result: check.Result{State: check.OK, Summary: "16 installed", Done: []check.Item{{ID: "fonts:fonts", Name: "fonts", Action: "run"}}}},
		event.StepFinished{Step: "disk", Result: check.Result{State: check.OK, Summary: "48% free"}},
		event.RunFinished{Duration: 4200 * time.Millisecond, Counts: map[check.State]int{check.OK: 3, check.Attention: 1}},
	})
	golden(t, "pretty-done.golden", pretty.String())
}

func TestJSONWritesNothingWithoutARun(t *testing.T) {
	var out bytes.Buffer
	if err := render.NewJSON(&out).Close(); err != nil || out.Len() != 0 {
		t.Errorf("Close() with no run = %q, %v; want nothing", out.String(), err)
	}
}

// A command aimed at one thing is a timeline: a row a step, in the run's
// order whatever order they finish in, no wordmark and no summary; a
// commit with nothing to commit was skipped.
func TestPrettyTimeline(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Command: "brew add", Machine: "laptop", Steps: []event.Step{{Name: "jq", Title: "jq"}, {Name: "ripgrap", Title: "ripgrap"}, {Name: "kit-config", Title: "kit-config"}}},
		event.StepFinished{Step: "ripgrap", Result: check.Result{State: check.Failed, Reason: `couldn't install: No available formula with the name "ripgrap"`}},
		event.StepFinished{Step: "jq", Result: check.Result{State: check.OK, Summary: "already installed; declared already, in laptop"}},
		event.StepFinished{Step: "kit-config", Result: check.Result{State: check.OK, Summary: "nothing changed"}},
		event.RunFinished{Counts: map[check.State]int{check.OK: 2, check.Failed: 1}},
	})
	want := "\n" +
		"  ● jq  already installed · declared already, in laptop\n" +
		"  ✗ ripgrap  couldn't install: No available formula with the name \"ripgrap\"\n" +
		"  – kit-config  nothing to commit\n"
	if out.String() != want {
		t.Errorf("printed\n%s\nwant\n%s", out.String(), want)
	}
}

// Reconciling looks at the whole Mac: the wordmark, its things on a
// timeline, then how many were settled; run for steps named, a command is
// aimed at those, without the wordmark.
func TestPrettyWholeOrNamed(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Time: at, Command: "reconcile", Machine: "laptop", Steps: []event.Step{{Name: "brew:ffmpeg", Title: "ffmpeg"}, {Name: "kit-config", Title: "kit-config"}}},
		event.StepFinished{Step: "brew:ffmpeg", Result: check.Result{State: check.OK, Summary: "already installed; declared in laptop"}},
		event.StepFinished{Step: "kit-config", Result: check.Result{State: check.OK, Summary: "committed and pushed laptop/declarations"}},
		event.RunFinished{Duration: 6100 * time.Millisecond, Counts: map[check.State]int{check.OK: 2}},
	})
	if got := ansi.Strip(out.String()); !strings.Contains(got, "│  reconcile\n") || !strings.HasSuffix(got, "  ● ffmpeg  already installed · declared in laptop\n  ● kit-config  committed and pushed laptop/declarations\n\n  1 settled · 6.1s\n") {
		t.Errorf("printed\n%s", got)
	}
	out.Reset()
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Command: "apply", Machine: "laptop", Only: []string{"fonts"}, Steps: []event.Step{{Name: "fonts", Title: "fonts", Area: "Steps"}}},
		event.StepFinished{Step: "fonts", Result: check.Result{State: check.OK, Summary: "16 installed"}},
		event.RunFinished{Counts: map[check.State]int{check.OK: 1}},
	})
	if want := "\n  ● fonts  16 installed\n"; out.String() != want {
		t.Errorf("kit apply fonts printed %q, want %q", out.String(), want)
	}
}

// syncBuffer is a buffer a face animating in the background writes to while
// a test reads it.
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

// screen is what's left on a terminal once raw is written to it: text
// written over what's there, a carriage return, a new line, the cursor
// moved up or put in place, a line cleared to its end, the screen cleared
// below; other controls change nothing on it. The alternate screen is a
// screen of its own, the main one kept beneath it: what's left is the one
// showing.
func screen(raw string) string {
	main := &term{lines: []string{""}}
	t := main
	for len(raw) > 0 {
		switch {
		case raw[0] == '\n':
			t.row++
			t.col = 0
			for t.row >= len(t.lines) {
				t.lines = append(t.lines, "")
			}
			raw = raw[1:]
		case raw[0] == '\r':
			t.col = 0
			raw = raw[1:]
		case strings.HasPrefix(raw, "\x1b]"):
			raw = raw[strings.IndexByte(raw, '\a')+1:]
		case strings.HasPrefix(raw, "\x1b["):
			end := 2
			for end < len(raw) && (raw[end] < 0x40 || raw[end] > 0x7e) {
				end++
			}
			params, final := raw[2:end], raw[end]
			raw = raw[end+1:]
			n := 1
			if _, err := fmt.Sscanf(params, "%d", &n); err != nil {
				n = 1
			}
			switch {
			case params == "?1049" && final == 'h':
				t = &term{lines: []string{""}}
			case params == "?1049" && final == 'l':
				t = main
			case final == 'A':
				t.row = max(t.row-n, 0)
			case final == 'H':
				row, col := 1, 1
				_, _ = fmt.Sscanf(params, "%d;%d", &row, &col)
				t.row, t.col = row-1, col-1
				for t.row >= len(t.lines) {
					t.lines = append(t.lines, "")
				}
			case final == 'K':
				t.lines[t.row] = cells(t.lines[t.row], t.col)
			case final == 'J' && params == "2":
				t.lines, t.row, t.col = []string{""}, 0, 0
			case final == 'J':
				t.lines[t.row] = cells(t.lines[t.row], t.col)
				t.lines = t.lines[:t.row+1]
			}
		default:
			i := strings.IndexAny(raw, "\n\r\x1b")
			if i < 0 {
				i = len(raw)
			}
			t.put(raw[:i])
			raw = raw[i:]
		}
	}
	return strings.Join(t.lines, "\n")
}

// term is a screen's lines, and where the cursor is.
type term struct {
	lines    []string
	row, col int
}

// put writes text at the cursor, over what's there.
func (t *term) put(text string) {
	line := []rune(t.lines[t.row])
	for len(line) < t.col {
		line = append(line, ' ')
	}
	for _, r := range text {
		if t.col < len(line) {
			line[t.col] = r
		} else {
			line = append(line, r)
		}
		t.col++
	}
	t.lines[t.row] = string(line)
}

// cells is line's first n cells.
func cells(line string, n int) string {
	r := []rune(line)
	return string(r[:min(n, len(r))])
}

// turned is a screen with the running mark as it starts, whichever way it
// has turned since.
func turned(screen string) string {
	return strings.NewReplacer("◓", "◐", "◑", "◐", "◒", "◐").Replace(screen)
}

// While a step changes things, its row says what it's running, and the last
// lines its command printed show on the line under it; done, they fold
// away; failed, they stay.
func TestPrettyShowsWhatACommandPrints(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true)
	face.Emit(event.RunStarted{Command: "brew add", Machine: "laptop", Steps: []event.Step{{Name: "jq", Title: "jq"}, {Name: "ripgrap", Title: "ripgrap"}}})
	face.Emit(event.StepStarted{Step: "jq", Doing: "changing"})
	for i := range 7 {
		face.Emit(event.Output{Step: "jq", Command: "brew install --formula jq", Line: fmt.Sprintf("==> line %d", i+1)})
	}
	// What a command prints is drawn at the spinner's next turn, at most.
	time.Sleep(300 * time.Millisecond)
	want := "\n  ◐ jq  changing · brew install --formula jq\n  │ ==> line 3\n  │ ==> line 4\n  │ ==> line 5\n  │ ==> line 6\n  │ ==> line 7"
	if got := turned(screen(out.String())); got != want {
		t.Errorf("while jq installs, the screen is\n%s\nwant\n%s", got, want)
	}
	face.Emit(event.StepFinished{Step: "jq", Result: check.Result{State: check.OK, Summary: "installed; declared in laptop"}})
	face.Emit(event.StepStarted{Step: "ripgrap", Doing: "changing"})
	face.Emit(event.Output{Step: "ripgrap", Command: "brew install --formula ripgrap", Line: "\x1b[31mWarning:\x1b[0m No available formula with the name \"ripgrap\"."})
	face.Emit(event.StepFinished{Step: "ripgrap", Result: check.Result{State: check.Failed, Reason: "couldn't install: brew install --formula ripgrap exited 1"}})
	face.Emit(event.RunFinished{Counts: map[check.State]int{check.OK: 1, check.Failed: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	want = "\n  ● jq  installed · declared in laptop\n  ✗ ripgrap  couldn't install · brew install --formula ripgrap\n  │ Warning: No available formula with the name \"ripgrap\".\n"
	if got := screen(out.String()); got != want {
		t.Errorf("once done, the screen is\n%s\nwant\n%s", got, want)
	}
}

// While applying, the whole screen is its live view: the wordmark, the
// lights, the bar, and the work list, a block an area, each step's row as it
// stands; at the end, the screen as it was, and the report under the
// heading: the lights, the bar, the whole list as it finished.
func TestPrettyApplyingNow(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop", Doing: "checking what will need an administrator's password"})
	if got := screen(out.String()); !strings.Contains(got, "│  apply\n") || !strings.Contains(got, "◐ checking what will need an administrator's password") {
		t.Errorf("while preparing, the screen is\n%s\nwant the heading and what kit's doing", got)
	}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: []event.Step{
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "brew", Title: "Formulae", Area: "Drift", Part: render.PartPackages},
		{Name: "cask", Title: "Casks", Area: "Drift", Part: render.PartPackages},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
	}})
	face.Emit(event.StepFinished{Step: "time-machine", Result: check.Result{State: check.OK, Summary: "last backup 02:35"}})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	face.Emit(event.Output{Step: "brew", Command: "brew install --formula jq", Line: "==> Pouring jq"})
	time.Sleep(300 * time.Millisecond)
	got := turned(screen(out.String()))
	for _, want := range []string{"  ● BACKUPS 1  ◐ CONFIG  ○ STEPS\n  ▮▮▮▮  1 of 4 · 1 running · ",
		"  BACKUPS  1 of 1\n  ● Time Machine  last backup 02:35\n\n  CONFIG  0 of 2\n  ◐ Formulae  applying · brew install --formula jq\n  │ ==> Pouring jq\n  ○ Casks  waiting\n\n  STEPS  0 of 1\n  ○ fonts  waiting"} {
		if !strings.Contains(got, want) {
			t.Errorf("while applying, the screen is\n%s\nwant it to hold\n%s", got, want)
		}
	}
	face.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "1 declared, all installed", Done: []check.Item{{ID: "brew:jq", Name: "jq", Action: "install"}}}})
	face.Emit(event.StepFinished{Step: "cask", Result: check.Result{State: check.OK, Summary: "none declared"}})
	face.Emit(event.StepFinished{Step: "fonts", Result: check.Result{State: check.OK, Summary: "16 installed"}})
	face.Emit(event.RunFinished{Duration: 4200 * time.Millisecond, Counts: map[check.State]int{check.OK: 4}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[?1049") {
		t.Error("applying took a screen of its own: want it in place, under its heading")
	}
	got = screen(out.String())
	want := "\n  █  ▄▀  ▀█▀  ▀▀█▀▀  │  apply\n  █▀▀▄    █     █    │  laptop\n  █   █  ▄█▄    █    │  Fri 2 Jan · 03:04\n\n" +
		"  ● BACKUPS 1  ● CONFIG 2  ● STEPS 1\n  ▮▮▮▮  1 done · 4.2s\n\n" +
		"  BACKUPS\n  ● Time Machine  last backup 02:35\n\n" +
		"  CONFIG\n  ● Formulae  installed jq · 1 declared, all installed\n  ● Casks  none declared\n\n" +
		"  STEPS\n  ● fonts  16 installed\n"
	if got != want {
		t.Errorf("once applied, the screen is\n%s\nwant\n%s", got, want)
	}
}

// On a short terminal, applying's live part is a window on its list, never
// taller than the terminal, kept around what's running; once applied, the
// whole list stays.
func TestPrettyApplyingFitsTheTerminal(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 12 })
	var steps []event.Step
	for i := range 20 {
		steps = append(steps, event.Step{Name: fmt.Sprintf("s%02d", i), Title: fmt.Sprintf("Step %02d", i), Area: "Drift"})
	}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	for i := range 14 {
		face.Emit(event.StepFinished{Step: fmt.Sprintf("s%02d", i), Result: check.Result{State: check.OK, Summary: "fine"}})
	}
	face.Emit(event.StepStarted{Step: "s14", Doing: "applying"})
	time.Sleep(300 * time.Millisecond)
	got := screen(out.String())
	// The heading is a blank line, the wordmark's three and a blank line.
	live := strings.Split(strings.TrimRight(got, "\n"), "\n")[5:]
	if len(live) > 11 || !strings.Contains(got, "◐ Step 14  applying") {
		t.Errorf("at 12 lines, the live part is %d lines:\n%s\nwant at most 11, Step 14 in it", len(live), got)
	}
	for i := 14; i < 20; i++ {
		face.Emit(event.StepFinished{Step: fmt.Sprintf("s%02d", i), Result: check.Result{State: check.OK, Summary: "fine"}})
	}
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 20}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	if got := screen(out.String()); !strings.Contains(got, "● Step 00  fine") || !strings.Contains(got, "● Step 19  fine") {
		t.Errorf("once applied, the screen is\n%s\nwant every step", got)
	}
}
