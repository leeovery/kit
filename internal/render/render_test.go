package render_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

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
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), run())
	golden(t, "pretty.golden", out.String())
}

func TestPrettyColour(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.ANSI}, 80, false), run())
	golden(t, "pretty-colour.golden", out.String())
}

func TestJSON(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewJSON(&out), run())
	golden(t, "status.json.golden", out.String())
}

func TestEverythingOK(t *testing.T) {
	events := []event.Event{
		event.RunStarted{Command: "status", Machine: "studio", Steps: []event.Step{{Name: "brew", Title: "Formulae"}}},
		event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "2 declared, all installed"}},
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

func TestPrettyWrapsALongList(t *testing.T) {
	var names []check.Item
	for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliett"} {
		names = append(names, check.Item{ID: "brew:" + n, Name: n, State: "extra"})
	}
	events := []event.Event{
		event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae"}}},
		event.StepFinished{Step: "brew", Result: check.Result{State: check.Attention, Summary: "10 not declared", Items: names}},
		event.RunFinished{Counts: map[check.State]int{check.Attention: 1}},
	}
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 50, false), events)
	golden(t, "pretty-wrapped.golden", out.String())
	for line := range strings.Lines(out.String()) {
		if w := len([]rune(strings.TrimRight(line, "\n"))); w > 50 {
			t.Errorf("line %q is %d columns, want at most 50", line, w)
		}
	}
}

func TestPrettySpinsWhileStepsRun(t *testing.T) {
	var out bytes.Buffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, true)
	face.Emit(event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae"}, {Name: "cask", Title: "Casks"}}})
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
	if !strings.Contains(got, "⠋ 0 of 2 · checking Formulae, Casks") || !strings.Contains(got, "⠙") {
		t.Errorf("printed %q, want the spinner turning, counting the steps and naming those running", got)
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
		event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae"}, {Name: "cask", Title: "Casks"}}},
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
		event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae"}}},
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
	events := []event.Event{
		event.RunStarted{Command: "apply", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae"}, {Name: "cask", Title: "Casks"}}},
		event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "3 declared, all installed", Done: []check.Item{
			{ID: "brew:jq", Name: "jq", State: "missing", Action: "install"},
			{ID: "brew:ripgrep", Name: "ripgrep", State: "missing", Action: "install"},
		}}},
		event.StepFinished{Step: "cask", Result: check.Result{State: check.Attention, Summary: "2 declared, 0 installed", Items: []check.Item{
			{ID: "cask:ghostty", Name: "ghostty", State: "missing", Action: "install"},
			{ID: "cask:zoom", Name: "zoom", State: "missing", Detail: "needs an administrator's password"},
		}}},
		event.RunFinished{Counts: map[check.State]int{check.OK: 1, check.Attention: 1}},
	}
	var plain, pretty bytes.Buffer
	show(t, render.NewPlain(&plain), events)
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &pretty, Profile: colorprofile.NoTTY}, 80, false), events)
	wantPlain := "kit apply · laptop\nbrew ok 3 declared, all installed\nbrew installed jq, ripgrep\n" +
		"cask attention 2 declared, 0 installed\ncask missing ghostty (to install)\ncask missing zoom (needs an administrator's password)\n1 needs attention\n"
	if plain.String() != wantPlain {
		t.Errorf("plain printed\n%s\nwant\n%s", plain.String(), wantPlain)
	}
	golden(t, "pretty-done.golden", pretty.String())
}

func TestJSONWritesNothingWithoutARun(t *testing.T) {
	var out bytes.Buffer
	if err := render.NewJSON(&out).Close(); err != nil || out.Len() != 0 {
		t.Errorf("Close() with no run = %q, %v; want nothing", out.String(), err)
	}
}
