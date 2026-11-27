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
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
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

var at = rendertest.At

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
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), rendertest.StatusRun())
	golden(t, "pretty.golden", out.String())
}

func TestPrettyColour(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.TrueColor}, 80, false), rendertest.StatusRun())
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

// What a check printed comes through whole, whatever it holds, as dig's
// semicolons; kit's own parts of a summary show apart, after dots.
func TestPrettyShowsWhatACheckPrintedWhole(t *testing.T) {
	dig := ";; connection timed out; no servers could be reached"
	steps := []event.Step{{Name: "dns", Title: "dns", Area: "Steps"}, {Name: "db", Title: "db", Area: "Steps"}}
	printed := check.Result{State: check.Attention, Summary: dig, Items: []check.Item{{ID: "dns:dns", Name: "dns", State: "problem", Detail: "kit apply dns"}}}
	parts := check.Result{State: check.OK}.Saying("declared in laptop", "it fails now: ;; timed out; no servers")
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Steps: steps},
		event.StepFinished{Time: at, Step: "dns", Result: printed},
		event.StepFinished{Time: at, Step: "db", Result: parts},
		event.RunFinished{Time: at, Duration: time.Second, Counts: map[check.State]int{check.OK: 1, check.Attention: 1}},
	})
	for _, want := range []string{"  ▲ dns  " + dig + "\n", "  ● db  declared in laptop · it fails now: ;; timed out; no servers\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("printed\n%s\nwant a row\n%s", out.String(), want)
		}
	}
}

// Status's details under a row are what's acted on, so they wrap, never
// cut, what to do under its words past the arrow, and so do the rows of
// what needs attention in Config, what to run on its own line, each
// thing's state in its kind's words, its part's without; the lines a
// failed step printed are a log, cut.
func TestPrettyStatusWrapsDetails(t *testing.T) {
	var out bytes.Buffer
	refused := check.Result{State: check.Attention, Summary: "needs your permission", Items: []check.Item{{
		ID: "login-item:allowed", Name: "macOS hasn't let the terminal control System Events", State: check.NotAllowed,
		Detail: "System Settings › Privacy & Security › Automation › the terminal › System Events",
	}}}
	failed := check.Result{State: check.Failed, Phase: check.Applying, Reason: "couldn't install ripgrap: no formula of that name in any tap Homebrew knows of, nor any cask of that name in them"}
	since := at.Add(-48 * time.Hour)
	drifted := check.Result{State: check.Attention, Summary: "2 declared, 1 excluded", Items: []check.Item{
		{ID: "backup-exclusion:long", Name: "~/Library/Application Support/Some Long App Name/Default/Service Worker/Cache_Data", State: "extra", Since: since},
	}}
	unset := check.Result{State: check.Attention, Summary: "3 declared, 2 set", Items: []check.Item{
		{ID: "defaults:agent", Name: "com.example.agent", State: "missing", Detail: "not installed: install the app, or open the app it comes with", Since: since},
		{ID: "defaults:com.apple.dock:autohide", Name: "com.apple.dock:autohide", State: "removed", Since: since},
	}}
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false)
	steps := []event.Step{
		{Name: "login-item", Title: "macOS login items", Area: "Drift", Row: "Login items", Part: render.PartSettings},
		{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "backup-exclusion", Title: "Backup exclusions", Area: "Drift", Row: "Mac settings", Part: render.PartSettings, Verb: "excluded"},
		{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings", Part: render.PartSettings},
	}
	show(t, face, []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Steps: steps},
		event.StepFinished{Time: at, Step: "backup-exclusion", Result: drifted},
		event.StepFinished{Time: at, Step: "defaults", Result: unset},
		event.StepFinished{Time: at, Step: "login-item", Result: refused},
		event.Output{Step: "brew", Command: "brew install --formula ripgrap", Line: "Warning: No available formula with the name ripgrap, nor any cask of that name either"},
		event.StepFinished{Time: at, Step: "brew", Result: failed},
		event.RunFinished{Time: at, Duration: time.Second, Counts: map[check.State]int{check.Failed: 1, check.Attention: 3}},
	})
	for _, want := range []string{
		"  ▲ ~/Library/Application Support/Some Long App Name/Default/Service Worker/\n" +
			"    Cache_Data\n" +
			"    excluded, not declared · 2 days\n" +
			"    → kit reconcile\n",
		"  ▲ com.example.agent\n" +
			"    declared, not set · not installed: install the app, or open the app it comes\n" +
			"    with · 2 days\n" +
			"    → kit apply\n",
		"  ▲ com.apple.dock:autohide  removed, not declared · 2 days · → kit reconcile\n",
		"  ▲ macOS login items  needs your permission\n" +
			"    macOS hasn't let the terminal control System Events\n" +
			"    → System Settings › Privacy & Security › Automation › the terminal › System\n" +
			"      Events\n",
		"    no formula of that name in any tap Homebrew knows of, nor any cask of that\n" +
			"    name in them\n" +
			"    Warning: No available formula with the name ripgrap, nor any cask of that n…\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("printed\n%s\nwant\n%s", out.String(), want)
		}
	}
	for l := range strings.Lines(out.String()) {
		if w := ansi.StringWidth(strings.TrimSuffix(l, "\n")); w > look.Width {
			t.Errorf("%q is %d columns, want at most %d", l, w, look.Width)
		}
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

// Left to itself, as kit leaves it, the spinner turns by the clock: a
// frame on, then halted as the face closes.
func TestPrettySpinsByItself(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true)
	face.Emit(event.RunStarted{Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae"}}})
	face.Emit(event.StepStarted{Step: "brew"})
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "◓") {
		if time.Now().After(deadline) {
			t.Fatalf("the spinner never turned: %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrettySpinsWhileStepsRun(t *testing.T) {
	var out bytes.Buffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, true).Turned()
	steps := []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages}, {Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Part: render.PartPackages}}
	face.Emit(event.RunStarted{Command: "status", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "brew"})
	face.Emit(event.StepStarted{Step: "cask"})
	face.Turn()
	face.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "2 declared, all installed"}})
	face.Emit(event.StepFinished{Step: "cask", Result: check.Result{State: check.OK, Summary: "1 declared, all installed"}})
	face.Emit(event.RunFinished{Counts: map[check.State]int{check.OK: 2}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "◐ ▮▮  0 of 2") || !strings.Contains(got, "◓ ▮▮  1 of 2") || strings.Contains(got, "· checking") {
		t.Errorf("printed %q, want the loader turning: the bar and the steps counted, none named, as several run at once and a list of them doesn't fit", got)
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

// An app installed by hand that a cask installs is drift: its row says
// what it's installed as, once, for how long, and to run kit reconcile;
// those left by hand are counted with the rest that's quiet.
func TestAppsInstalledByHand(t *testing.T) {
	events := []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Part: render.PartPackages}}},
		event.StepFinished{Time: at, Step: "cask", Result: check.Result{State: check.Attention, Summary: "3 declared, all installed", Counts: map[string]int{"installed": 3, "by_hand": 3}, Items: []check.Item{
			{ID: "cask:google-chrome", Name: "google-chrome", State: "by-hand", Detail: "installed by hand as Google Chrome.app", Since: at.Add(-50 * time.Hour)},
			{ID: "cask:spaceman", Name: "spaceman", State: "by-hand", Detail: "installed by hand as Spaceman.app", Quiet: "left"},
			{ID: "cask:zoom", Name: "zoom", State: "by-hand", Detail: "installed by hand as zoom.us.app", Quiet: "new"},
		}}},
		event.RunFinished{Time: at, Counts: map[check.State]int{check.Attention: 1}},
	}
	var plain, pretty bytes.Buffer
	show(t, render.NewPlain(&plain), events)
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &pretty, Profile: colorprofile.NoTTY}, 80, false), events)
	wantPlain := "kit status · laptop\ncask attention 3 declared, all installed\ncask by-hand google-chrome (installed by hand as Google Chrome.app)\n" +
		"cask by-hand:left spaceman (installed by hand as Spaceman.app)\ncask by-hand:new zoom (installed by hand as zoom.us.app)\n1 needs attention\n"
	if plain.String() != wantPlain {
		t.Errorf("plain printed\n%s\nwant\n%s", plain.String(), wantPlain)
	}
	golden(t, "pretty-by-hand.golden", pretty.String())
}

// When which apps are installed by hand can't be told, the casks' check
// stands as ever, and a greyed row says why, once: nothing to settle.
func TestAppsByHandThatCantBeTold(t *testing.T) {
	unseen := "kit can't tell which apps are installed by hand: no catalogue of casks in Homebrew's cache"
	events := []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Part: render.PartPackages}}},
		event.StepFinished{Time: at, Step: "cask", Result: check.Result{State: check.OK, Summary: "3 declared, all installed", Counts: map[string]int{"installed": 3}, HandLook: &check.Look{Unseen: unseen}}},
		event.RunFinished{Time: at, Counts: map[check.State]int{check.OK: 1}},
	}
	var plain, pretty bytes.Buffer
	show(t, render.NewPlain(&plain), events)
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &pretty, Profile: colorprofile.NoTTY}, 80, false), events)
	if want := "kit status · laptop\ncask ok 3 declared, all installed\ncask unseen " + unseen + "\nNothing needs attention\n"; plain.String() != want {
		t.Errorf("plain printed\n%s\nwant\n%s", plain.String(), want)
	}
	golden(t, "pretty-by-hand-unseen.golden", pretty.String())
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
		event.StepFinished{Step: "jq", Result: check.Result{State: check.OK}.Saying("already installed", "declared already, in laptop")},
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

// A row in the record is never cut: too long for the line, its name goes on
// a line of its own, wrapped at a / or a space when it's longer still, and
// what it says under it, wrapped, as the terminal narrows; plain prints
// each whole on one line, whatever the width.
func TestPrettyRecordWrapsWhatARowSays(t *testing.T) {
	for _, c := range []struct {
		name, path string
		width      int
	}{
		{"pretty-record-wraps-80.golden", "~/Library/Application Support/Google/Chrome", 80},
		{"pretty-record-wraps-52.golden", "~/Library/Application Support/Google/Chrome", 52},
		{"pretty-record-wraps-long-name.golden", "~/Library/Application Support/Some Long App Name/Default/Service Worker", 52},
	} {
		t.Run(c.name, func(t *testing.T) {
			events := []event.Event{
				event.RunStarted{Command: "backup-exclusion remove", Machine: "laptop", Steps: []event.Step{{Name: c.path, Title: c.path}, {Name: "kit-config", Title: "kit-config"}}},
				event.StepFinished{Step: c.path, Result: check.Result{State: check.OK}.Saying("no longer excluded", "out of shared/declarations and laptop/declarations")},
				event.StepFinished{Step: "kit-config", Result: check.Result{State: check.OK}.Saying("committed and pushed shared/declarations, laptop/declarations")},
				event.RunFinished{Counts: map[check.State]int{check.OK: 2}},
			}
			var plain, pretty bytes.Buffer
			show(t, render.NewPlain(&plain), events)
			show(t, render.NewPretty(&colorprofile.Writer{Forward: &pretty, Profile: colorprofile.NoTTY}, c.width, false), events)
			for l := range strings.SplitSeq(pretty.String(), "\n") {
				if strings.Contains(l, "…") || ansi.StringWidth(l) > c.width {
					t.Errorf("printed %q at %d columns: want it wrapped, never cut", l, c.width)
				}
			}
			if want := c.path + " ok no longer excluded; out of shared/declarations and laptop/declarations\n"; !strings.Contains(plain.String(), want) {
				t.Errorf("plain printed\n%s\nwant the row whole on a line:\n%s", plain.String(), want)
			}
			golden(t, c.name, pretty.String())
		})
	}
}

// Reconciling looks at the whole Mac: the wordmark, its things on a
// timeline, then how many were settled; run for steps named, a command is
// aimed at those, without the wordmark.
func TestPrettyWholeOrNamed(t *testing.T) {
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Time: at, Command: "reconcile", Machine: "laptop", Steps: []event.Step{{Name: "brew:ffmpeg", Title: "ffmpeg"}, {Name: "kit-config", Title: "kit-config"}}},
		event.StepFinished{Step: "brew:ffmpeg", Result: check.Result{State: check.OK}.Saying("already installed", "declared in laptop")},
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

// screen is what's left on a terminal once raw is written to it.
var screen = rendertest.Screen

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
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned()
	face.Emit(event.RunStarted{Command: "brew add", Machine: "laptop", Steps: []event.Step{{Name: "jq", Title: "jq"}, {Name: "ripgrap", Title: "ripgrap"}}})
	face.Emit(event.StepStarted{Step: "jq", Doing: "changing"})
	for i := range 7 {
		face.Emit(event.Output{Step: "jq", Command: "brew install --formula jq", Line: fmt.Sprintf("==> line %d", i+1)})
	}
	// What a command prints is drawn at the spinner's next turn, at most.
	face.Turn()
	want := "\n  ◐ jq  changing · brew install --formula jq\n  │ ==> line 3\n  │ ==> line 4\n  │ ==> line 5\n  │ ==> line 6\n  │ ==> line 7"
	if got := turned(screen(out.String())); got != want {
		t.Errorf("while jq installs, the screen is\n%s\nwant\n%s", got, want)
	}
	face.Emit(event.StepFinished{Step: "jq", Result: check.Result{State: check.OK}.Saying("installed", "declared in laptop")})
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

// On a timeline, a step waiting on you says what you're to do under its
// row while it runs, as the plain face does.
func TestPrettyTimelineShowsWhatYoureToDo(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned()
	face.Emit(event.RunStarted{Command: "secret sync", Machine: "laptop", Steps: []event.Step{{Name: "secret", Title: "secret"}}})
	face.Emit(event.StepStarted{Step: "secret", Doing: "syncing"})
	face.Emit(event.Doing{Step: "secret", Says: "waiting for 1Password", Todo: "1Password asks whether kit may read your secrets: approve it"})
	face.Turn()
	want := "\n  ▲ secret  waiting for 1Password\n  │ → 1Password asks whether kit may read your secrets: approve it"
	if got := screen(out.String()); got != want {
		t.Errorf("while 1Password asks, the screen is\n%s\nwant\n%s", got, want)
	}
	face.Emit(event.StepFinished{Step: "secret", Result: check.Result{State: check.OK, Summary: "2 synced"}})
	face.Emit(event.RunFinished{Counts: map[check.State]int{check.OK: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
}

// While applying gets ready, the rows' lights are under the wordmark, and
// what kit's doing; below 100×30, the run stays in place, never on a
// screen of its own; at the end, its record in their place, the wordmark
// once: what ran, the bar and the tally beside it, then the rows done.
func TestPrettyApplyingNow(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned().Sized(func() (int, int) { return 80, 40 })
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
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepFinished{Step: "time-machine", Result: check.Result{State: check.OK, Summary: "last backup 02:35"}})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	face.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "1 declared, all installed", Done: []check.Item{{ID: "brew:jq", Name: "jq", Action: "install"}}}})
	face.Emit(event.StepFinished{Step: "cask", Result: check.Result{State: check.OK, Summary: "none declared"}})
	face.Emit(event.StepFinished{Step: "fonts", Result: check.Result{State: check.OK, Summary: "16 installed"}})
	face.Emit(event.RunFinished{Duration: 4200 * time.Millisecond, Counts: map[check.State]int{check.OK: 4}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[?1049") {
		t.Error("applying below 100×30 took a screen of its own: want it in place, under its heading")
	}
	got := screen(out.String())
	want := "\n  █  ▄▀  ▀█▀  ▀▀█▀▀  │  apply · laptop · Fri 2 Jan · 03:04\n  █▀▀▄    █     █    │  ▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮\n  █   █  ▄█▄    █    │  4 fine · 4.2s\n\n" +
		"  DONE  4\n  ● Homebrew formulae  installed jq\n  ● Homebrew casks  none declared\n" +
		"  ● Your steps  16 installed\n  ● Backups  last backup 02:35\n"
	if got != want {
		t.Errorf("once applied, the screen is\n%s\nwant\n%s", got, want)
	}
}

// A row's mark says at once when a step in it fails, not only once the
// row's done, and the header says what failed; a step waiting its turn for
// another's thing isn't counted running, nor marks its row running.
func TestPrettyLightsSayAtOnce(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned().Sized(func() (int, int) { return 160, 40 })
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
	face.Turn()
	got := turned(screen(out.String()))
	for _, want := range []string{"│  ◐ working · nothing needs you · 1 failed\n", "\n  ◐ Homebrew formulae  applying ", "\n  ✗ Homebrew casks  couldn't install ", "\n  ○ Backups  waits for arq "} {
		if !strings.Contains(got, want) {
			t.Errorf("with Homebrew casks failed and formulae running, the screen is\n%s\nwant it to hold\n%s", got, want)
		}
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
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, width, true).Turned().Sized(func() (int, int) { return 80, 40 })
	steps := []event.Step{
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
		{Name: "by-hand", Title: "By hand", Area: "Manual"},
	}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	face.Turn()
	got := turned(screen(out.String()))
	if want := "  ◐ HOMEBREW FORMULAE  ○ YOUR STEPS  ○ BACKUPS\n"; !strings.Contains(got, want) {
		t.Errorf("at 80 columns, started at 45, the window is\n%s\nwant the lights whole\n%s", got, want)
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
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned().Sized(func() (int, int) { return 160, 40 })
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
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned().Sized(func() (int, int) { return 160, 40 })
	steps := []event.Step{{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"}}
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop", Doing: "checking what will need an administrator's password", Steps: steps})
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop"})
	asked := out.String()
	face.Turn()
	if drawn := strings.TrimPrefix(out.String(), asked); drawn != "" {
		t.Errorf("while the question was up, the face drew %q; want nothing", drawn)
	}
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	before := out.String()
	face.Turn()
	if out.String() == before {
		t.Error("once the run started, the spinner didn't turn again")
	}
	face.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "fine"}})
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
}

// Held still while a question's up under the heading, as sudo's password
// is as the run goes on, the face draws nothing of the run meanwhile, the
// question left as it is, though another's asked and answered in turn,
// then, once it's answered, the run as it stands.
func TestPrettyHoldsStillWhileTheRunGoesOn(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned().Sized(func() (int, int) { return 160, 40 })
	steps := []event.Step{{Name: "brew", Title: "Homebrew formulae", Area: "Drift"}}
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop", Doing: "checking what will need an administrator's password", Steps: steps})
	face.Emit(event.Preparing{Time: at, Command: "apply", Machine: "laptop"})
	back := face.HoldStill()
	asked := out.String()
	face.Emit(event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps})
	face.Emit(event.StepStarted{Step: "brew", Doing: "applying"})
	face.Emit(event.Doing{Step: "brew", Says: "installing jq", Of: 1})
	// A program's sudo asking meanwhile has the face step aside, and back,
	// as kit's own question's still up: it stays as it is.
	aside := face.StepAside()
	aside()
	aside()
	face.Turn()
	if drawn := strings.TrimPrefix(out.String(), asked); drawn != "" {
		t.Errorf("while the question was up, the face drew %q; want nothing", drawn)
	}
	back()
	if drawn := strings.TrimPrefix(out.String(), asked); !strings.Contains(drawn, "installing jq") {
		t.Errorf("once it was answered, the face drew %q; want the run as it stands", drawn)
	}
	face.Emit(event.StepFinished{Step: "brew", Result: check.Result{State: check.OK, Summary: "fine"}})
	face.Emit(event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
}

// Once the boot's in the terminal, it's kit apply: the boot's steps in
// This Mac's row; a step waiting on you says what for in the header, the
// row it holds up asking.
func TestPrettyBootstrapInTheTerminal(t *testing.T) {
	var out syncBuffer
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned().Sized(func() (int, int) { return 160, 40 })
	steps := []event.Step{
		{Name: "boot-github", Title: "GitHub", Area: boot.AreaBooted},
		{Name: "boot-access", Title: "Full Disk Access", Area: boot.AreaBooted, Waits: []string{"boot-github"}},
		{Name: "1password", Title: "1Password", Area: "Manual"},
		{Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew"},
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"1password"}},
		{Name: "gpg-key", Title: "gpg-key", Area: "Steps", Waits: []string{"secret"}},
	}
	face.Emit(event.RunStarted{Time: at, Command: "bootstrap", Machine: "laptop", Steps: steps})
	face.Emit(event.StepFinished{Step: "boot-github", Result: check.Result{State: check.OK, Summary: "someone · kit-config has laptop"}})
	face.Emit(event.StepFinished{Step: "boot-access", Result: check.Result{State: check.OK, Summary: "granted to Ghostty"}})
	face.Emit(event.StepStarted{Step: "1password", Doing: "applying"})
	face.Emit(event.Doing{Step: "1password", Says: "not signed in", Todo: "sign in with your phone's 1Password, then turn on\nSettings › Developer › Integrate with 1Password CLI"})
	face.Emit(event.StepStarted{Step: "cask", Doing: "applying"})
	face.Turn()
	got := turned(screen(out.String()))
	for _, want := range []string{"│  bootstrap · laptop", "│  ▲ waiting for you · 1Password not signed in\n",
		"\n  ● This Mac  all 2 done ", "\n  ▲ Secrets  not signed in ", "\n  ◐ Homebrew casks  applying ", "\n  ○ Your steps  waits for Secrets "} {
		if !strings.Contains(got, want) {
			t.Errorf("waiting on you, the screen is\n%s\nwant it to hold\n%s", got, want)
		}
	}
	if strings.Contains(got, "\n  ● GitHub") || strings.Contains(got, "\n  ● Full Disk Access") {
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
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Turned().Sized(func() (int, int) { return 160, 40 })
	face.Emit(event.RunStarted{Time: at, Command: "bootstrap", Machine: "laptop", Steps: []event.Step{
		{Name: "boot-github", Title: "GitHub", Area: boot.AreaBooted},
		{Name: "boot-access", Title: "Full Disk Access", Area: boot.AreaBooted},
	}})
	face.Emit(event.StepFinished{Step: "boot-github", Result: check.Result{State: check.OK, Summary: "someone"}})
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

// A repository nested in kit-config, which git can't keep, is drift all
// the same, for kit reconcile, saying what it is, then what more there is
// to say.
func TestPrettyStatusShowsEditsGitKeepsApart(t *testing.T) {
	edits := check.Result{State: check.Attention, Summary: "1 file not committed", Items: []check.Item{
		{ID: "config:vendor/tool/", Name: "vendor/tool/", State: "repository", Detail: "kit-config can't keep it", Since: at.Add(-50 * time.Hour)},
	}}
	for _, it := range edits.Items {
		if !render.IsDrift(it) {
			t.Errorf("%s isn't drift", it.State)
		}
	}
	var out bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
		event.RunStarted{Time: at, Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "config", Title: "kit-config edits", Area: "Drift"}}},
		event.StepFinished{Time: at, Step: "config", Result: edits},
		event.RunFinished{Time: at, Duration: time.Second, Counts: map[check.State]int{check.Attention: 1}},
	})
	want := "  CONFIG\n" +
		"  ▲ vendor/tool/  a repository of its own · kit-config can't keep it · 2 days\n" +
		"    → kit reconcile\n"
	if !strings.Contains(out.String(), want) {
		t.Errorf("printed\n%s\nwant\n%s", out.String(), want)
	}
}

// How bringing in kit-config's remote commits went is one row at most:
// those brought in, counted, under kit-config's own, which a long glance
// can't cut off; none brought in, quiet, a grey row there saying why,
// whole, as what didn't happen;
// none for a day, a row needing you, saying what to do, naming no kit
// command, and kit-config's row marked so.
func TestPrettyConfigPull(t *testing.T) {
	const why = "the remote's commits change what's not committed here, so nothing was touched or brought in: commit or undo the change to shared/home/.zshrc first"
	missed := check.Item{ID: "config-sync:pull", Name: "the remote's commits not brought in", State: check.Problem, Detail: why}
	for _, tt := range []struct {
		name   string
		result check.Result
		want   string
	}{
		{"brought in", check.Result{State: check.OK, Summary: "pushed; 2 commits brought in", Glance: "pushed", Counts: map[string]int{"brought_in": 2}},
			"  CONFIG\n  ● kit-config  pushed · private\n  ● the remote's commits brought in  2 commits\n\n"},
		{"missed, quiet", check.Result{State: check.OK, Summary: "pushed", Glance: "pushed", Items: []check.Item{func() check.Item { it := missed; it.Quiet = "new"; return it }()}},
			"  CONFIG\n  ● kit-config  pushed · private\n  – the remote's commits not brought in\n    the remote's commits change what's not committed here, so nothing was\n    touched or brought in: commit or undo the change to shared/home/.zshrc first\n\n"},
		{"missed for days", check.Result{State: check.Attention, Summary: "pushed", Glance: "pushed", Items: []check.Item{func() check.Item { it := missed; it.Since = at.Add(-50 * time.Hour); return it }()}},
			"  CONFIG\n  ▲ the remote's commits not brought in\n    the remote's commits change what's not committed here, so nothing was\n    touched or brought in: commit or undo the change to shared/home/.zshrc\n    first · 2 days\n  ▲ kit-config  pushed · private\n\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, false), []event.Event{
				event.RunStarted{Time: at, Command: "status", Machine: "laptop", Steps: []event.Step{
					{Name: "config-sync", Title: "Sync", Area: "Config", Part: render.PartConfig},
					{Name: "config-private", Title: "Privacy", Area: "Config", Part: render.PartConfig},
				}},
				event.StepFinished{Step: "config-sync", Result: tt.result},
				event.StepFinished{Step: "config-private", Result: check.Result{State: check.OK, Summary: "private on GitHub (owner/kit-config)", Glance: "private"}},
				event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 2}},
			})
			if got := ansi.Strip(out.String()); !strings.Contains(got, tt.want) || strings.Contains(got, "kit reconcile") {
				t.Errorf("printed\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// A kit-config that isn't private needs you, and no kit command makes it
// private: its row says what does, naming none, and kit-config's own row is
// marked as needing you, whatever else of it is well.
func TestPrettyConfigNotPrivate(t *testing.T) {
	for _, tt := range []struct{ state, detail, row string }{
		{"not-private", "make it private: gh repo edit owner/kit-config --visibility private",
			"  ▲ owner/kit-config\n    make it private: gh repo edit owner/kit-config --visibility private\n"},
		{"unproven", "gh isn't signed in", "  ▲ owner/kit-config  gh isn't signed in\n"},
	} {
		t.Run(tt.state, func(t *testing.T) {
			var out bytes.Buffer
			show(t, render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 120, false), []event.Event{
				event.RunStarted{Time: at, Command: "status", Machine: "laptop", Steps: []event.Step{
					{Name: "config-private", Title: "Privacy", Area: "Config", Part: render.PartConfig},
					{Name: "config-sync", Title: "Sync", Area: "Config", Part: render.PartConfig},
				}},
				event.StepFinished{Step: "config-private", Result: check.Result{State: check.Attention, Summary: "it must be private", Items: []check.Item{
					{ID: "config-private:owner/kit-config", Name: "owner/kit-config", State: tt.state, Detail: tt.detail},
				}}},
				event.StepFinished{Step: "config-sync", Result: check.Result{State: check.OK, Summary: "pushed", Glance: "pushed"}},
				event.RunFinished{Duration: time.Second, Counts: map[check.State]int{check.OK: 1, check.Attention: 1}},
			})
			got := ansi.Strip(out.String())
			if !strings.Contains(got, tt.row) || strings.Contains(got, "kit reconcile") || strings.Contains(got, "kit apply") {
				t.Errorf("printed\n%s\nwant\n%s\nand no kit command", got, tt.row)
			}
			if !strings.Contains(got, "  ▲ kit-config  it must be private · pushed\n") {
				t.Errorf("printed\n%s\nwant kit-config's row marked as needing you", got)
			}
		})
	}
}
