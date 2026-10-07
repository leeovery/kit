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
