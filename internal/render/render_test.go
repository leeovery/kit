package render_test

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/boot"
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
		{Name: "homebrew", Title: "Homebrew", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings", Part: render.PartSettings},
		{Name: "config-private", Title: "Privacy", Area: "Config", Part: render.PartConfig},
		{Name: "config-sync", Title: "Sync", Area: "Config", Part: render.PartConfig},
		{Name: "mas", Title: "App Store", Area: "Drift", Row: "App Store apps", Part: render.PartPackages},
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
		event.StepFinished{Time: at, Step: "config-sync", Result: check.Result{State: check.Failed, Reason: "not a git repository"}},
		event.StepFinished{Time: at, Step: "brew", Result: brew},
		event.StepFinished{Time: at, Step: "mas", Result: check.Result{State: check.Deferred, Reason: "needs Homebrew"}},
		event.StepFinished{Time: at, Step: "time-machine", Result: ok("last backup 02:35", nil)},
		event.StepFinished{Time: at, Step: "disk", Result: check.Result{State: check.Attention, Summary: "8% free", Items: []check.Item{
			{ID: "disk:low", Name: "only 8% free on the startup disk", State: "problem", Detail: "free some space: kit status says where it went"},
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

// A step by hand come up shows without a terminal as its title, then each
// of its steps, a line each, led by its step's name; the JSON face's
// document is as it would be without it.
func TestFacesWithoutATerminalShowACard(t *testing.T) {
	events := []event.Event{
		event.RunStarted{Command: "apply", Machine: "laptop", Steps: []event.Step{{Name: "1password", Title: "1password", Area: "Manual"}}},
		event.StepStarted{Step: "1password", Doing: "applying"},
		event.NeedsYou{Step: "1password", Item: "1password", Title: "Sign in to 1Password", Why: "Secrets come from it.", Steps: []string{"Scan the QR code it shows", "Turn on Integrate with 1Password CLI"}, Tells: true},
		event.StepFinished{Step: "1password", Result: check.Result{State: check.OK, Summary: "signed in"}},
		event.RunFinished{Counts: map[check.State]int{check.OK: 1}},
	}
	var plain bytes.Buffer
	show(t, render.NewPlain(&plain), events)
	want := "kit apply · laptop\n1password Sign in to 1Password\n1password Scan the QR code it shows\n1password Turn on Integrate with 1Password CLI\n1password ok signed in\nNothing needs attention\n"
	if plain.String() != want {
		t.Errorf("plain printed\n%s\nwant\n%s", plain.String(), want)
	}
	var with, without bytes.Buffer
	show(t, render.NewJSON(&with), events)
	show(t, render.NewJSON(&without), slices.Delete(slices.Clone(events), 2, 3))
	if with.String() != without.String() {
		t.Errorf("with a card, JSON printed\n%s\nwant it as without\n%s", with.String(), without.String())
	}
}

// What a step tells the run's log changes nothing a face shows yet: plain
// says what a step installed as it finishes, as it did; the JSON face's
// document, and what the pretty face prints, are as they would be without
// it.
func TestFacesShowNothingOfTheLogsLines(t *testing.T) {
	installed := check.Result{State: check.OK, Summary: "2 declared, all installed", Done: []check.Item{
		{ID: "brew:jq", Name: "jq", Action: "install"}, {ID: "brew:owner/tap/tool", Name: "owner/tap/tool", Action: "install"},
	}}
	events := []event.Event{
		event.RunStarted{Command: "apply", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae"}}},
		event.StepStarted{Step: "brew", Doing: "applying"},
		event.Logged{Step: "brew", Line: "Installed jq"},
		event.Logged{Step: "brew", Line: "Installed tool"},
		event.StepFinished{Step: "brew", Result: installed},
		event.RunFinished{Counts: map[check.State]int{check.OK: 1}},
	}
	without := slices.Delete(slices.Clone(events), 2, 4)
	var plain bytes.Buffer
	show(t, render.NewPlain(&plain), events)
	if want := "kit apply · laptop\nbrew ok 2 declared, all installed\nbrew installed jq, owner/tap/tool\nNothing needs attention\n"; plain.String() != want {
		t.Errorf("plain printed\n%s\nwant\n%s", plain.String(), want)
	}
	faces := map[string]func(w io.Writer) render.Face{
		"json": func(w io.Writer) render.Face { return render.NewJSON(w) },
		"pretty": func(w io.Writer) render.Face {
			return render.NewPretty(&colorprofile.Writer{Forward: w, Profile: colorprofile.NoTTY}, 80, false)
		},
	}
	for name, face := range faces {
		var with, wout bytes.Buffer
		show(t, face(&with), events)
		show(t, face(&wout), without)
		if with.String() != wout.String() {
			t.Errorf("with the log's lines, %s printed\n%s\nwant it as without\n%s", name, with.String(), wout.String())
		}
	}
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

// A failed step's row is headed by the phase it failed in, as its result
// says, when its reason doesn't say what couldn't be done: couldn't apply,
// couldn't check, or, where nothing says, couldn't check; why under it.
func TestPrettyFailedRowNamesItsPhase(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Command: "apply a", Machine: "laptop", Only: []string{"a", "b", "c", "d"}, Steps: []event.Step{{Name: "a", Title: "a"}, {Name: "b", Title: "b"}, {Name: "c", Title: "c"}, {Name: "d", Title: "d"}}},
		event.StepFinished{Step: "a", Result: check.Result{State: check.Failed, Reason: "the link points elsewhere", Phase: check.Applying}},
		event.StepFinished{Step: "b", Result: check.Result{State: check.Failed, Reason: "not a git repository", Phase: check.Checking}},
		event.StepFinished{Step: "c", Result: check.Result{State: check.Failed, Reason: "not a git repository"}},
		event.StepFinished{Step: "d", Result: check.Result{State: check.Failed, Reason: "couldn't install jq: no network", Phase: check.Applying}},
	})
	want := "\n" +
		"  ✗ a  couldn't apply\n  │ the link points elsewhere\n" +
		"  ✗ b  couldn't check\n  │ not a git repository\n" +
		"  ✗ c  couldn't check\n  │ not a git repository\n" +
		"  ✗ d  couldn't install jq\n  │ no network\n"
	if out.String() != want {
		t.Errorf("printed\n%s\nwant\n%s", out.String(), want)
	}
}

// A kind macOS hasn't let look needs you: status's Config shows its row,
// saying so, what isn't allowed and where to allow it under it, and counts
// nothing of it.
func TestPrettyStatusSaysWhatsNotAllowed(t *testing.T) {
	var out bytes.Buffer
	refused := check.Result{State: check.Attention, Summary: "needs your permission", Items: []check.Item{{
		ID: "login-item:allowed", Name: "macOS hasn't let the terminal control System Events", State: check.NotAllowed,
		Detail: "System Settings › Privacy & Security › Automation",
	}}}
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Steps: []event.Step{
			{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings", Part: render.PartSettings},
			{Name: "login-item", Title: "macOS login items", Area: "Drift", Row: "Login items", Part: render.PartSettings},
		}},
		event.StepFinished{Time: at, Step: "defaults", Result: check.Result{State: check.OK, Summary: "9 declared, all set", Counts: map[string]int{"installed": 9}}},
		event.StepFinished{Time: at, Step: "login-item", Result: refused},
		event.RunFinished{Time: at, Duration: time.Second, Counts: map[check.State]int{check.OK: 1, check.Attention: 1}},
	})
	want := "  CONFIG\n" +
		"  ▲ macOS login items  needs your permission\n" +
		"    macOS hasn't let the terminal control System Events\n" +
		"    → System Settings › Privacy & Security › Automation\n" +
		"  ● Settings  9 set\n"
	if !strings.Contains(out.String(), want) {
		t.Errorf("printed\n%s\nwant\n%s", out.String(), want)
	}
}

func TestJSON(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewJSON(&out), run())
	golden(t, "status.json.golden", out.String())
}

func TestEverythingOK(t *testing.T) {
	events := []event.Event{
		event.RunStarted{Command: "status", Machine: "studio", Steps: []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages}}},
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
		event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages}}},
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
	steps := []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages}, {Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Part: render.PartPackages}}
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
		event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages}}},
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
			{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages},
			{Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Part: render.PartPackages},
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
// order whatever order they finish in, no wordmark and no summary; a step
// that failed says what went wrong, and why on the line under it; a commit
// with nothing to commit was skipped.
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
		"  ✗ ripgrap  couldn't install\n" +
		"  │ No available formula with the name \"ripgrap\"\n" +
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
	face.Emit(event.StepFinished{Step: "ripgrap", Result: check.Result{State: check.Failed, Reason: "couldn't install: Warning: No available formula with the name \"ripgrap\"."}})
	face.Emit(event.RunFinished{Counts: map[check.State]int{check.OK: 1, check.Failed: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	want = "\n  ● jq  installed · declared in laptop\n  ✗ ripgrap  couldn't install · brew install --formula ripgrap\n  │ Warning: No available formula with the name \"ripgrap\".\n"
	if got := screen(out.String()); got != want {
		t.Errorf("once done, the screen is\n%s\nwant\n%s", got, want)
	}
}

// While applying, its live part is under the wordmark: the rows' lights,
// the bar, and what's happening and what's wrong, not what finished well
// or hasn't started; at the end, its record in their place, the wordmark
// once: what ran, the bar and the tally beside it, then the rows done.
func TestPrettyApplyingNow(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	steps := []event.Step{
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
	}
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop", Doing: "checking what will need an administrator's password", Steps: steps})
	if got := screen(out.String()); !strings.Contains(got, "│  apply\n") || !strings.Contains(got, "  ○ HOMEBREW FORMULAE  ○ HOMEBREW CASKS  ○ YOUR STEPS  ○ BACKUPS\n\n  ◐ checking what will need an administrator's password") {
		t.Errorf("while preparing, the screen is\n%s\nwant the heading, the lights, and what kit's doing where the bar will be", got)
	}
	before := out.String()
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	if started := strings.TrimPrefix(out.String(), before); !strings.Contains(started, "▮") {
		t.Errorf("as the run started, it drew %q; want the bar drawn over what kit was doing, in place", started)
	}
	face.Emit(event.StepFinished{Step: "time-machine", Result: check.Result{State: check.OK, Summary: "last backup 02:35"}})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	face.Emit(event.Output{Step: "brew", Command: "brew install --formula jq", Line: "==> Pouring jq"})
	time.Sleep(300 * time.Millisecond)
	got := turned(screen(out.String()))
	if want := "  ◐ HOMEBREW FORMULAE  ○ HOMEBREW CASKS  ○ YOUR STEPS  ● BACKUPS 1\n\n  ▮▮▮▮  1 of 4 · 1 running · "; !strings.Contains(got, want) {
		t.Errorf("while applying, the screen is\n%s\nwant it to hold\n%s", got, want)
	}
	if want := "s\n\n  ◐ Formulae  applying · brew install --formula jq\n  │ ==> Pouring jq"; !strings.HasSuffix(got, want) {
		t.Errorf("while applying, the screen is\n%s\nwant it to end with what's running\n%s", got, want)
	}
	for _, gone := range []string{"Time Machine", "Casks", "fonts"} {
		if strings.Contains(got, gone) {
			t.Errorf("while applying, the screen is\n%s\nwant no row for %s, done well or not started", got, gone)
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
	want := "\n  █  ▄▀  ▀█▀  ▀▀█▀▀  │  apply · laptop · Fri 2 Jan · 03:04\n  █▀▀▄    █     █    │  ▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮\n  █   █  ▄█▄    █    │  4 fine · 4.2s\n\n" +
		"  DONE  4\n  ● Homebrew formulae  installed jq\n  ● Homebrew casks  none declared\n" +
		"  ● Your steps  16 installed\n  ● Backups  last backup 02:35\n"
	if got != want {
		t.Errorf("once applied, the screen is\n%s\nwant\n%s", got, want)
	}
}

// Applying's live part is never taller than the terminal: on a short one,
// the heading goes, as it would have scrolled away; what finished unwell
// that won't fit is counted, where it would have been, and what's running
// stays; once applied, the record lists every step that failed.
func TestPrettyApplyingFitsTheTerminal(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 12 })
	var steps []event.Step
	for i := range 20 {
		steps = append(steps, event.Step{Name: fmt.Sprintf("s%02d", i), Title: fmt.Sprintf("Step %02d", i), Area: "Drift"})
	}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	for i := range 14 {
		face.Emit(event.StepFinished{Step: fmt.Sprintf("s%02d", i), Result: check.Result{State: check.Failed, Reason: "couldn't apply: it broke"}})
	}
	face.Emit(event.StepStarted{Step: "s14", Doing: "applying"})
	time.Sleep(300 * time.Millisecond)
	got := screen(out.String())
	// The blank line before the live part is the run's, printed.
	live := strings.Split(strings.TrimRight(got, "\n"), "\n")[1:]
	if len(live) > 11 || strings.Contains(got, "█") || !strings.Contains(got, "✗ Step 04  couldn't apply\n  and 9 more\n  ◐ Step 14  applying") || strings.Contains(got, "Step 05") {
		t.Errorf("at 12 lines, the live part is %d lines:\n%s\nwant at most 11, no heading: five that failed, the other nine counted, then Step 14", len(live), got)
	}
	for i := 14; i < 20; i++ {
		face.Emit(event.StepFinished{Step: fmt.Sprintf("s%02d", i), Result: check.Result{State: check.OK, Summary: "fine"}})
	}
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 6, check.Failed: 14}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	if got := screen(out.String()); !strings.Contains(got, "\n  FAILED  14\n  ✗ This Mac · Step 00  couldn't apply\n") || !strings.Contains(got, "✗ This Mac · Step 13  couldn't apply") {
		t.Errorf("once applied, the screen is\n%s\nwant every step that failed", got)
	}
}

// macOS asking something while applying is a row of its own, first, saying
// what it's asking and what to do; answered, it goes.
func TestPrettyApplyingWhileMacOSAsks(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	steps := []event.Step{{Name: "1password", Title: "1password", Area: "Manual"}, {Name: "cask", Title: "Homebrew casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew"}}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "cask", Doing: "applying"})
	face.Emit(event.StepStarted{Step: "1password", Doing: "checking"})
	face.Emit(event.Asking{Time: at, Says: "asking whether to open a program from the internet"})
	got := turned(screen(out.String()))
	if want := "s\n\n  ▲ macOS  asking whether to open a program from the internet\n  │ → answer it in the window macOS shows\n  ◐ Homebrew casks  applying\n"; !strings.Contains(got, want) {
		t.Errorf("while macOS asks, the screen is\n%s\nwant it to hold\n%s", got, want)
	}
	face.Emit(event.Asking{Time: at})
	if got := turned(screen(out.String())); strings.Contains(got, "macOS") {
		t.Errorf("once answered, the screen is\n%s\nwant macOS's row gone", got)
	}
	face.Emit(event.StepFinished{Step: "cask", Result: check.Result{State: check.OK, Summary: "fine"}})
	face.Emit(event.StepFinished{Step: "1password", Result: check.Result{State: check.OK, Summary: "fine"}})
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 2}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
}

// A row's light says at once when a step in it fails, not only once the
// row's done, and the counts say what failed; a step waiting its turn for
// another's thing isn't counted running, nor lights its row.
func TestPrettyLightsSayAtOnce(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	steps := []event.Step{
		{Name: "arq", Title: "Arq", Area: "Backups"},
		{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"},
		{Name: "cask", Title: "Homebrew casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
	}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "arq", Doing: "waiting for arq"})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	face.Emit(event.StepFinished{Step: "cask", Result: check.Result{State: check.Failed, Reason: "couldn't install: it broke"}})
	got := turned(screen(out.String()))
	if want := "  ◐ HOMEBREW FORMULAE   ✗ HOMEBREW CASKS   ○ YOUR STEPS  ○ BACKUPS\n\n  ▮▮▮▮  1 of 4 · 1 failed · 1 running · "; !strings.Contains(got, want) {
		t.Errorf("with Homebrew casks failed and formulae running, the screen is\n%s\nwant it to hold\n%s", got, want)
	}
	for _, s := range []string{"arq", "brew", "fonts"} {
		face.Emit(event.StepFinished{Step: s, Result: check.Result{State: check.OK, Summary: "fine"}})
	}
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 3, check.Failed: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
}

// The face draws to the terminal's width as it is when it draws, not as it
// was when kit started: Ghostty starts kit before it's sized its window.
func TestPrettyFollowsTheTerminalsWidth(t *testing.T) {
	var out syncBuffer
	width := 45
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, width, true).Sized(func() (int, int) { return 80, 40 })
	steps := []event.Step{
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
		{Name: "by-hand", Title: "By hand", Area: "Manual"},
	}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	got := turned(screen(out.String()))
	if want := "  ◐ HOMEBREW FORMULAE  ○ YOUR STEPS  ○ BACKUPS\n"; !strings.Contains(got, want) {
		t.Errorf("at 80 columns, started at 45, the screen is\n%s\nwant the lights whole\n%s", got, want)
	}
	for _, s := range steps {
		face.Emit(event.StepFinished{Step: s.Name, Result: check.Result{State: check.OK, Summary: "fine"}})
	}
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 4}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
}

// A question asked as the run gets ready, sudo's password, shows the cursor
// as it ends: the live part hides it again.
func TestPrettyHidesTheCursorAfterAQuestion(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	steps := []event.Step{{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"}}
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop", Doing: "checking what will need an administrator's password", Steps: steps})
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop"})
	_, _ = out.Write([]byte("\x1b[?25h"))
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	got := out.String()
	if after := got[strings.LastIndex(got, "\x1b[?25h"):]; !strings.Contains(after, "\x1b[?25l") {
		t.Errorf("after the question, the face wrote %q; want the cursor hidden again", after)
	}
	face.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "fine"}})
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got[strings.LastIndex(got, "\x1b[?25l"):], "\x1b[?25h") {
		t.Errorf("once applied, the cursor isn't shown again: %q", got)
	}
}

// While a question's asked under the heading, as sudo's password is, the
// heading holds still: a turn of the spinner would draw it again from where
// the question left the cursor, over the question.
func TestPrettyHoldsStillForAQuestion(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	steps := []event.Step{{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"}}
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop", Doing: "checking what will need an administrator's password", Steps: steps})
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop"})
	asked := out.String()
	time.Sleep(400 * time.Millisecond)
	if drawn := strings.TrimPrefix(out.String(), asked); drawn != "" {
		t.Errorf("while the question was up, the face drew %q; want nothing", drawn)
	}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	before := out.String()
	time.Sleep(400 * time.Millisecond)
	if out.String() == before {
		t.Error("once the run started, the spinner didn't turn again")
	}
	face.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "fine"}})
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
}

// Once the boot's in the terminal, it's kit apply: the boot's steps among
// This Mac's; a step waiting on you says what for and what to do, the
// light of the row it holds up asking.
func TestPrettyBootstrapInTheTerminal(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	steps := []event.Step{
		{Name: "boot-github", Title: "GitHub", Area: boot.AreaBooted},
		{Name: "boot-access", Title: "Full Disk Access", Area: boot.AreaBooted, Waits: []string{"boot-github"}},
		{Name: "1password", Title: "1Password", Area: "Manual"},
		{Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew"},
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"1password"}},
		{Name: "gpg-key", Title: "gpg-key", Area: "Steps", Waits: []string{"secret"}},
	}
	face.Emit(event.RunStarted{Time: at, Command: "bootstrap", Machine: "laptop", Steps: steps})
	face.Emit(event.StepFinished{Step: "boot-github", Result: check.Result{State: check.OK, Summary: "lee · kit-config has laptop"}})
	face.Emit(event.StepFinished{Step: "boot-access", Result: check.Result{State: check.OK, Summary: "granted to Ghostty"}})
	face.Emit(event.StepStarted{Step: "1password", Doing: "applying"})
	face.Emit(event.Doing{Step: "1password", Says: "not signed in", Todo: "sign in with your phone's 1Password, then turn on\nSettings › Developer › Integrate with 1Password CLI"})
	face.Emit(event.StepStarted{Step: "cask", Doing: "applying"})
	time.Sleep(300 * time.Millisecond)
	got := turned(screen(out.String()))
	for _, want := range []string{"│  bootstrap\n", "  ● THIS MAC 2   ▲ SECRETS   ◐ HOMEBREW CASKS  ○ YOUR STEPS\n\n  ▮▮▮▮▮▮  2 of 6 · 1 running · 1 needs you",
		"s\n\n  ◐ Casks  applying\n  ▲ 1Password  not signed in\n  │ → sign in with your phone's 1Password, then turn on\n  │   Settings › Developer › Integrate with 1Password CLI"} {
		if !strings.Contains(got, want) {
			t.Errorf("waiting on you, the screen is\n%s\nwant it to hold\n%s", got, want)
		}
	}
	if strings.Contains(got, "GitHub") {
		t.Errorf("the boot's steps are all well, yet listed:\n%s", got)
	}
	face.Emit(event.StepFinished{Step: "1password", Result: check.Result{State: check.OK, Summary: "signed in"}})
	face.Emit(event.StepFinished{Step: "cask", Result: check.Result{State: check.OK, Summary: "1 declared, all installed"}})
	face.Emit(event.StepFinished{Step: "secret", Result: check.Result{State: check.OK, Summary: "2 synced"}})
	face.Emit(event.StepFinished{Step: "gpg-key", Result: check.Result{State: check.OK, Summary: "imported"}})
	face.Emit(event.RunFinished{Duration: 4200 * time.Millisecond, Counts: map[check.State]int{check.OK: 6}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	got = screen(out.String())
	if want := "│  4 fine · 4.2s\n\n  DONE  4\n  ● This Mac  all 2 done\n  ● Secrets  2 synced\n"; !strings.Contains(got, want) {
		t.Errorf("once done, the screen is\n%s\nwant it to hold\n%s", got, want)
	}
}

// A boot step that isn't well once the boot's in the terminal is in the
// record's steps needing you, named by its row and by itself.
func TestPrettyBootstrapShowsABootStepNotWell(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	face.Emit(event.RunStarted{Time: at, Command: "bootstrap", Machine: "laptop", Steps: []event.Step{
		{Name: "boot-github", Title: "GitHub", Area: boot.AreaBooted},
		{Name: "boot-access", Title: "Full Disk Access", Area: boot.AreaBooted},
	}})
	face.Emit(event.StepFinished{Step: "boot-github", Result: check.Result{State: check.OK, Summary: "lee"}})
	face.Emit(event.StepFinished{Step: "boot-access", Result: check.Result{State: check.Attention, Summary: "not granted to Ghostty"}})
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 1, check.Attention: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	got := screen(out.String())
	if want := "\n  NEEDS YOU  1\n  ▲ This Mac · Full Disk Access  not granted to Ghostty\n"; !strings.Contains(got, want) || strings.Contains(got, "GitHub") {
		t.Errorf("the screen is\n%s\nwant the boot's step not well, alone, as\n%s", got, want)
	}
}
