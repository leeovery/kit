package render_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// newMac are a new Mac's steps by area, as round 6 draws its run: the
// boot's eight, done before kit apply starts, then apply's.
var newMac = []struct {
	area   string
	titles []string
}{
	{boot.AreaBooted, []string{"GitHub", "This Mac", "Password", "Homebrew", "Apps", "kit-config", "Full Disk Access", "Ghostty"}},
	{"Backups", []string{"Time Machine", "Arq", "App settings", "Full Disk Access"}},
	{"Jobs", []string{"Scheduled runs"}},
	{"Mac", []string{"Scratch volume", "Disk space", "Memory", "File events", "Load"}},
	{"Drift", []string{"Homebrew", "Linked files", "Shell PATH", "Touch ID for sudo", "Remote Login", "File Sharing",
		"Homebrew formulae", "Homebrew casks", "App Store apps", "npm packages", "Composer packages", "Go tools",
		"GitHub extensions", "tmux plugins", "macOS login items", "Claude MCP", "macOS settings", "Power settings",
		"Backup exclusions", "Spotlight exclusions", "kit-config edits", "Secrets", "git config", "Sync", "Privacy"}},
	{"Steps", []string{"claude-code", "fonts", "mise", "remote-session", "valet", "gpg-key"}},
	{"Manual", []string{"By hand", "1password"}},
}

// stepName is the name of the step titled title, in area: titles repeat
// across areas.
func stepName(area, title string) string { return area + "/" + title }

func newMacSteps() []event.Step {
	var steps []event.Step
	for _, a := range newMac {
		for _, title := range a.titles {
			steps = append(steps, event.Step{Name: stepName(a.area, title), Title: title, Area: a.area})
		}
	}
	return steps
}

func finished(area, title string, r check.Result) event.Event {
	return event.StepFinished{Time: at, Step: stepName(area, title), Result: r}
}

func fine(area, title, summary string) event.Event {
	return finished(area, title, check.Result{State: check.OK, Summary: summary})
}

// applyingStep is a step starting to apply, running command, which prints
// lines.
func applyingStep(area, title, command string, lines ...string) []event.Event {
	name := stepName(area, title)
	events := []event.Event{event.StepStarted{Step: name, Doing: "checking"}, event.StepStarted{Step: name, Doing: "applying"}}
	for _, l := range lines {
		events = append(events, event.Output{Step: name, Command: command, Line: l})
	}
	return events
}

func numbered(what string, n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("==> %s %d", what, i+1)
	}
	return lines
}

// sixMinutesIn is the run six minutes in: the boot done, what's quick done
// too, Homebrew's formulae and casks installing, Claude Code setting itself
// up, the fonts done.
func sixMinutesIn() []event.Event { return sixMinutesInWith(16, 10) }

// sixMinutesInWith is sixMinutesIn, Homebrew's formulae having printed
// formulae lines, and its casks casks.
func sixMinutesInWith(formulae, casks int) []event.Event {
	events := []event.Event{event.RunStarted{Time: at, Command: "bootstrap", Machine: "laptop", Steps: newMacSteps()}}
	for _, title := range newMac[0].titles {
		events = append(events, fine(boot.AreaBooted, title, "fine"))
	}
	events = append(events, applyingStep("Drift", "Homebrew formulae", "brew install --formula ffmpeg", numbered("Pouring formula", formulae)...)...)
	events = append(events, event.Doing{Step: stepName("Drift", "Homebrew formulae"), Says: "installing ffmpeg", Done: 38, Of: 129})
	events = append(events, applyingStep("Drift", "Homebrew casks", "brew install --cask 1password", numbered("Installing Cask", casks)...)...)
	events = append(events, event.Doing{Step: stepName("Drift", "Homebrew casks"), Says: "installing 1password", Done: 4, Of: 42})
	events = append(events, applyingStep("Steps", "claude-code", "run apply", "Setting up Claude Code...", "Checking installation status...", "Downloading 2.1.296 for darwin-arm64", "Verifying checksum")...)
	events = append(events, applyingStep("Steps", "fonts", "run apply", numbered("copied font", 9)...)...)
	return append(events,
		finished("Backups", "Time Machine", check.Result{State: check.Attention, Summary: "no backup disk", Items: []check.Item{
			{ID: "time-machine:disk", Name: "no backup disk is set up", State: "problem", Detail: "System Settings › General › Time Machine › Add Backup Disk"}}}),
		fine("Backups", "Full Disk Access", "granted"),
		fine("Mac", "Scratch volume", "mounted; not indexed"),
		fine("Mac", "Disk space", "71% free"),
		fine("Mac", "Memory", "0.0 GB swap"),
		fine("Mac", "File events", "fseventsd 0.0 GB"),
		fine("Mac", "Load", "load 3.1 on 8 cores"),
		fine("Drift", "Homebrew", "/opt/homebrew"),
		fine("Drift", "Linked files", "54 linked"),
		fine("Drift", "Shell PATH", "15 directories"),
		fine("Drift", "Touch ID for sudo", "turned on"),
		fine("Drift", "Remote Login", "turned on"),
		fine("Drift", "File Sharing", "turned on"),
		fine("Drift", "Go tools", "none declared"),
		fine("Drift", "macOS settings", "99 set"),
		fine("Drift", "Power settings", "6 set"),
		fine("Drift", "Backup exclusions", "89 excluded"),
		fine("Drift", "Spotlight exclusions", "1 excluded"),
		fine("Drift", "kit-config edits", "all committed"),
		fine("Drift", "Sync", "pushed"),
		fine("Drift", "Privacy", "private on GitHub"),
		fine("Steps", "fonts", "16 installed"),
		fine("Steps", "remote-session", "applied"),
	)
}

// casksFailed is the run once Homebrew's casks have failed: one wouldn't
// install. Claude Code is done; App settings restores, taking the fonts'
// place in the log, done longest ago.
func casksFailed() []event.Event {
	events := sixMinutesIn()
	events = append(events, fine("Steps", "claude-code", "installed 2.1.296"))
	for _, l := range []string{
		"==> Installing Cask tailscale-app",
		"installer: Package name is Tailscale",
		"installer: The install failed. (The Installer encountered an error that caused the installation to fail.)",
	} {
		events = append(events, event.Output{Step: stepName("Drift", "Homebrew casks"), Command: "brew install --cask tailscale-app", Line: l})
	}
	events = append(events,
		finished("Drift", "Homebrew casks", check.Result{State: check.Failed, Reason: "couldn't install tailscale-app: brew install --cask tailscale-app exited 1"}),
		finished("Backups", "Arq", check.Result{State: check.Attention, Summary: "no backup plan", Items: []check.Item{
			{ID: "arq:plan", Name: "no backup plan is chosen", State: "problem", Detail: "open Arq and choose its backup plan"}}}),
		fine("Drift", "macOS login items", "14 added"),
	)
	return append(events, applyingStep("Backups", "App settings", "kit prefs restore", "com.example.editor restored", "com.example.terminal restored")...)
}

// waitingOnYou is the run once 1Password's sign-in has come due, a step by
// hand, while macOS asks for an administrator's password.
func waitingOnYou() []event.Event {
	name := stepName("Manual", "1password")
	return append(casksFailed(),
		event.StepStarted{Step: name, Doing: "applying"},
		event.Doing{Step: name, Says: "waiting on you", Todo: "Sign in to 1Password with your phone, then turn on Settings › Developer › Integrate with 1Password CLI"},
		event.Asking{Time: at, Says: "asking for an administrator's password"},
	)
}

// clock is a time a test sets and a face reads, as it draws.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// applyAt shows events on a face at a terminal w by h, reading keys from a
// keyboard, the run started at at and the clock then at took, and returns
// the face, what it wrote, once it's had a turn of its spinner to draw
// them, and the keyboard.
func applyAt(w, h int, took time.Duration, events []event.Event) (*render.Pretty, *syncBuffer, *keyboard) {
	return applyIn(colorprofile.Ascii, w, h, took, events)
}

// applyIn is applyAt at a terminal showing colour as profile does.
func applyIn(profile colorprofile.Profile, w, h int, took time.Duration, events []event.Event) (*render.Pretty, *syncBuffer, *keyboard) {
	var out syncBuffer
	c := &clock{t: at}
	kb := &keyboard{out: &out}
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: profile}, 80, true).
		Sized(func() (int, int) { return w, h }).Clocked(c.now).Keys(kb.listen, kb.halt)
	for _, e := range events {
		face.Emit(e)
	}
	c.set(at.Add(took))
	time.Sleep(300 * time.Millisecond)
	return face, &out, kb
}

// theRest are events finished, then each step they didn't finish, as end
// has it.
func theRest(events []event.Event, end func(event.Step) check.Result) []event.Event {
	done := map[string]bool{}
	for _, e := range events {
		if f, ok := e.(event.StepFinished); ok {
			done[f.Step] = true
		}
	}
	for _, s := range newMacSteps() {
		if !done[s.Name] {
			events = append(events, event.StepFinished{Time: at, Step: s.Name, Result: end(s)})
		}
	}
	return events
}

// appliedRun is casksFailed to its end: the rest done, but two steps by
// hand left to do, one saying what to run once it's done, one saying
// nothing more.
func appliedRun() []event.Event {
	events := theRest(casksFailed(), func(s event.Step) check.Result {
		if s.Title == "By hand" {
			return check.Result{State: check.Attention, Summary: "2 to do", Items: []check.Item{
				{ID: "manual:sharing", Name: "Allow remote users Full Disk Access: System Settings › General › Sharing", State: "manual", Detail: "then: kit manual done sharing"},
				{ID: "manual:editor", Name: "Install the editor from its site (no Homebrew cask)", State: "manual"}}}
		}
		return check.Result{State: check.OK, Summary: "fine"}
	})
	return append(events, event.RunFinished{Time: at, Duration: 24*time.Minute + 31*time.Second, Counts: map[check.State]int{check.OK: 47, check.Attention: 3, check.Failed: 1}})
}

// stoppedRun is waitingOnYou stopped by ctrl+c: what was running cut off,
// what hadn't started never run, both failing as the engine has them.
func stoppedRun() []event.Event {
	events := theRest(waitingOnYou(), func(event.Step) check.Result {
		return check.Result{State: check.Failed, Reason: event.ErrStopped.Error()}
	})
	return append(events, event.RunFinished{Time: at, Duration: 13*time.Minute + 2*time.Second, Counts: map[check.State]int{check.OK: 32, check.Attention: 2, check.Failed: 17}})
}

// panelRow is a row in the log, in its panel's rule: a mark, then a step's
// name.
var panelRow = regexp.MustCompile(`(?m)^  ── [●◐✗▲–○] (\S+)`)

// The keys at the screen's foot while it reads them, before anything's
// pinned.
const runningFoot = "  tab panel · ↑↓ scroll · click a step to pin · q stop · shift+drag selects"

// lines are a screen's lines.
func lines(screen string) []string { return strings.Split(screen, "\n") }

// edges are where a screen's lines are rules across the whole window.
func edges(ls []string) []int {
	var at []int
	for i, l := range ls {
		if l != "" && strings.Trim(l, "─") == "" {
			at = append(at, i)
		}
	}
	return at
}

// panelTitle is the title of the step whose row a panel's rule, line,
// carries: ok is false for any other line.
func panelTitle(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "  ── ")
	if !ok {
		return "", false
	}
	_, rest, _ = strings.Cut(rest, " ")
	name, _, _ := strings.Cut(rest, "  ")
	name, _, _ = strings.Cut(name, " ─")
	return name, true
}

// panelOf is the panel on screen carrying title's row: its rule, then the
// lines under it, without their indent.
func panelOf(screen, title string) (rule string, under []string) {
	ls := lines(screen)
	for i, l := range ls {
		if name, ok := panelTitle(l); !ok || name != title {
			continue
		}
		for _, u := range ls[i+1:] {
			text, ok := strings.CutPrefix(u, "       ")
			if !ok {
				break
			}
			under = append(under, text)
		}
		return l, under
	}
	return "", nil
}

// panelTitles are the titles of the steps in a screen's panels, in order.
func panelTitles(screen string) []string {
	var titles []string
	for _, l := range lines(screen) {
		if name, ok := panelTitle(l); ok {
			titles = append(titles, name)
		}
	}
	return titles
}

// While applying, at a terminal big enough, kit has a screen of its own,
// the window's whole size: the header with the bar and the counts, every
// step in a grid, a rule, the log's panels, a rule, what needs you, the
// keys; a blank line over the first rule and under the second, none
// between the panels.
func TestApplyingHasAScreenOfItsOwn(t *testing.T) {
	for _, tt := range []struct {
		golden, counts string
		took           time.Duration
		events         func() []event.Event
	}{
		{"apply-screen.golden", "31 of 51 · 3 running · 6m12s", 6*time.Minute + 12*time.Second, sixMinutesIn},
		{"apply-screen-failed.golden", "35 of 51 · 1 failed · 2 running · 10m48s", 10*time.Minute + 48*time.Second, casksFailed},
		{"apply-screen-you.golden", "35 of 51 · 1 failed · 2 running · 1 needs you · 12m40s", 12*time.Minute + 40*time.Second, waitingOnYou},
	} {
		t.Run(tt.golden, func(t *testing.T) {
			face, out, _ := applyAt(160, 64, tt.took, tt.events())
			got := screen(out.String())
			if err := face.Close(); err != nil {
				t.Fatal(err)
			}
			golden(t, tt.golden, got+"\n")
			ls := lines(got)
			if len(ls) != 64 || !strings.HasSuffix(ls[3], "│  "+tt.counts) || ls[63] != runningFoot {
				t.Errorf("the screen is %d lines, its counts and keys\n%s\n%s\nwant 64 lines, the counts %q, the keys at the foot", len(ls), ls[3], ls[len(ls)-1], tt.counts)
			}
			if !strings.Contains(out.String(), "\x1b[?1049h\x1b[?25l") {
				t.Error("applying didn't take the alternate screen, its cursor hidden")
			}
			rules := edges(ls)
			if len(rules) != 2 || ls[rules[0]-1] != "" || ls[rules[1]+1] != "" || ls[rules[0]+1] == "" || !panelRow.MatchString(ls[rules[0]+1]) {
				t.Errorf("the screen's rules are at lines %v; want two, a blank line over the first and under the second, the first panel straight under the first", rules)
			}
		})
	}
}

// The screen's header is the one the arrival lands on: a blank line, then
// the wordmark two cells in, what runs, the Mac and when beside it, so the
// hand-over moves nothing; the bar and the counts fill in under that line.
// As the run starts, nothing in the log yet, the keys are at the foot all
// the same.
func TestApplyingScreenTakesOverFromTheArrival(t *testing.T) {
	face, out, _ := applyAt(160, 64, time.Second, sixMinutesIn()[:9])
	got := lines(screen(out.String()))
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	landed := lines(stripped(render.NewArrival(true, "bootstrap", "laptop", at).HeaderAt(1 << 10)))
	for i, want := range landed {
		if !strings.HasPrefix(got[i], want) {
			t.Errorf("line %d of the screen is %q; want it to start as the arrival left it, %q", i, got[i], want)
		}
	}
	if got[1] != landed[1] {
		t.Errorf("the screen's first line is %q; want the arrival's, %q", got[1], landed[1])
	}
	if len(got) != 64 || got[63] != runningFoot {
		t.Errorf("as the run starts, the screen is %d lines, the last %q; want 64, the keys at the foot", len(got), got[len(got)-1])
	}
}

// The log's panels, with nothing pinned, follow its slots: a step takes
// one as it starts applying, an empty one first, and keeps it while it
// runs; once it's done, its lines stay till another needs the place, the
// one done longest ago giving way first, one that failed last. While every
// slot's step runs, a step starting has none.
func TestApplyingLogsSlots(t *testing.T) {
	var steps []event.Step
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		steps = append(steps, event.Step{Name: n, Title: n, Area: "Steps"})
	}
	events := []event.Event{event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps}}
	start := func(names ...string) {
		for _, n := range names {
			events = append(events, event.StepStarted{Step: n, Doing: "applying"}, event.Output{Step: n, Command: "run apply", Line: n + " at work"})
		}
	}
	finish := func(n string, s check.State) {
		events = append(events, event.StepFinished{Step: n, Result: check.Result{State: s, Summary: "done", Reason: "couldn't apply"}})
	}
	start("a", "b", "c", "d")
	finish("a", check.OK)
	finish("c", check.OK)
	finish("b", check.Failed)
	start("e", "f", "g")
	events = append(events, event.StepStarted{Step: "h", Doing: "applying"})
	finish("d", check.OK)
	face, out, _ := applyAt(160, 64, time.Second, events)
	got := screen(out.String())
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, m := range panelRow.FindAllStringSubmatch(got, -1) {
		rows = append(rows, m[1])
	}
	if want := []string{"e", "g", "f", "d"}; !slices.Equal(rows, want) {
		t.Errorf("the log's panels hold %q; want %q: a's place, done first, then the failed b's, then c's; h, starting with every slot's step running, none\n%s", rows, want, got)
	}
	if rule, under := panelOf(got, "d"); !strings.HasPrefix(rule, "  ── ● d  done ─") || !slices.Equal(under, []string{"d at work"}) {
		t.Errorf("d's panel, once it's done, is %q, then %q; want its row in the rule, its lines kept", rule, under)
	}
}

// A step waiting partway through applying, for a thing another step puts
// in place, gives its place in the log up to a step at work, and takes one
// again once it's back at work.
func TestApplyingSlotsAreForStepsAtWork(t *testing.T) {
	var steps []event.Step
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		steps = append(steps, event.Step{Name: n, Title: n, Area: "Steps"})
	}
	events := []event.Event{event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps}}
	for _, n := range []string{"a", "b", "c", "d"} {
		events = append(events, event.StepStarted{Step: n, Doing: "applying"}, event.Output{Step: n, Command: "run apply", Line: n + " at work"})
	}
	events = append(events,
		event.Doing{Step: "a", Says: "waiting for TOKEN: 2 to install"},
		event.StepStarted{Step: "e", Doing: "applying"}, event.Output{Step: "e", Command: "run apply", Line: "e at work"},
		event.StepFinished{Step: "b", Result: check.Result{State: check.OK, Summary: "done"}},
		event.Doing{Step: "a", Says: "installing owner/private/fetcher", Of: 2},
		event.Output{Step: "a", Command: "brew install", Line: "a at work again"},
	)
	face, out, _ := applyAt(160, 64, time.Second, events)
	got := screen(out.String())
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, m := range panelRow.FindAllStringSubmatch(got, -1) {
		rows = append(rows, m[1])
	}
	if want := []string{"e", "a", "c", "d"}; !slices.Equal(rows, want) {
		t.Errorf("the log's panels hold %q; want %q: a's place e's while a waited, then b's, done, a's\n%s", rows, want, got)
	}
}

// scratchLeft is a run in which the Scratch volume's step made the volume,
// printing as it did, but found two things still left once it checked
// again, one saying what to do, while Claude Code installs.
func scratchLeft() []event.Event {
	var steps []event.Step
	for _, a := range []struct{ area, title string }{{"Mac", "Scratch volume"}, {"Mac", "Disk space"}, {"Steps", "claude-code"}} {
		steps = append(steps, event.Step{Name: stepName(a.area, a.title), Title: a.title, Area: a.area})
	}
	events := []event.Event{event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps}}
	events = append(events, applyingStep("Mac", "Scratch volume", "mdutil -i off /Volumes/Scratch", append(numbered("Adding volume", 12),
		"mdutil disabling Spotlight", "/System/Volumes/Data/Volumes/Scratch:", "    Indexing disabled.")...)...)
	events = append(events, finished("Mac", "Scratch volume", check.Result{
		State: check.Attention, Summary: "not set up as it should be",
		Items: []check.Item{
			{ID: "scratch:tmpdir", Name: "Claude Code's settings don't send TMPDIR to it", State: "problem", Detail: "set it in Claude Code's settings"},
			{ID: "scratch:backups", Name: "Time Machine still backs it up", State: "problem"},
		},
		Done: []check.Item{{ID: "scratch:mounted", Name: "/Volumes/Scratch", Action: "create"}},
	}))
	events = append(events, fine("Mac", "Disk space", "71% free"))
	return append(events, applyingStep("Steps", "claude-code", "run apply", "Setting up Claude Code...", "Verifying checksum")...)
}

// A step that finished not well says in its panel what's left of it, as
// the record does, under the last lines it printed, which give way to it
// when the panel hasn't the room for both: each thing by name, what to do
// about it under it when it says, in orange, as it needs you.
func TestApplyingPanelSaysWhatsLeft(t *testing.T) {
	events := scratchLeft()
	face, out, kb := applyIn(colorprofile.TrueColor, 100, 30, time.Minute, events)
	got := screen(uncoloured(out.String()))
	golden(t, "apply-screen-left.golden", got+"\n")
	left := []string{"Claude Code's settings don't send TMPDIR to it", "→ set it in Claude Code's settings", "Time Machine still backs it up"}
	if _, under := panelOf(got, "Scratch volume"); len(under) < 4 || !slices.Equal(under[len(under)-4:], append([]string{"    Indexing disabled."}, left...)) {
		t.Errorf("the Scratch volume's panel shows\n%s\nwant its last line printed, then what's left: %q", strings.Join(under, "\n"), left)
	}
	if frame := strings.Join(lastFrame(out.String()), "\n"); !strings.Contains(frame, look.Orange(left[0])) || !strings.Contains(frame, look.Orange(left[2])) {
		t.Errorf("the things left aren't named in orange in\n%s", frame)
	}
	kb.press(t, slices.Repeat([]ask.Key{keyUp}, 20)...)
	if _, under := panelOf(screen(uncoloured(out.String())), "Scratch volume"); len(under) < 4 || under[0] != "==> Adding volume 1" || !slices.Equal(under[len(under)-3:], left) {
		t.Errorf("scrolled back as far as it goes, the Scratch volume's panel shows\n%s\nwant its first lines printed, then what's left", strings.Join(under, "\n"))
	}
	face.Emit(event.RunFinished{Time: at, Duration: time.Minute, Counts: map[check.State]int{check.OK: 2, check.Attention: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	if record := screen(uncoloured(out.String())); !strings.Contains(record, "  ▲ Scratch volume  created /Volumes/Scratch · not set up as it should be\n  │ "+strings.Join(left, "\n  │ ")+"\n") {
		t.Errorf("the record is\n%s\nwant the Scratch volume's row, what's left under it, as its slot had them", record)
	}
}

// A window too small for the screen keeps the run in place under its
// heading, as before; the screen takes a window wide and tall enough for
// its grid, its rules and the log.
func TestApplyingScreenNeedsTheRoom(t *testing.T) {
	for _, tt := range []struct {
		w, h   int
		screen bool
	}{{99, 64, false}, {100, 64, true}, {160, 29, false}, {160, 30, true}, {100, 37, false}, {100, 38, true}} {
		var out syncBuffer
		face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return tt.w, tt.h })
		for _, e := range sixMinutesIn()[:12] {
			face.Emit(e)
		}
		if got := strings.Contains(out.String(), "\x1b[?1049h"); got != tt.screen {
			t.Errorf("at %d×%d, a screen of its own: %v, want %v", tt.w, tt.h, got, tt.screen)
		}
		if err := face.Close(); err != nil {
			t.Fatal(err)
		}
	}
	face, out, _ := applyAt(99, 40, 6*time.Minute+12*time.Second, sixMinutesIn())
	got := screen(out.String())
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	golden(t, "apply-small.golden", got+"\n")
}

// A step that counts what it's doing says how far it's got after what it's
// doing, in its panel's row, and, cut off by the run stopping, in its row in
// the record; a step that doesn't count says what it did before.
func TestApplyingSaysHowFarAStepHasGot(t *testing.T) {
	for _, tt := range []struct {
		events func() []event.Event
		want   []string
	}{
		{sixMinutesIn, []string{
			"  ── ◐ Homebrew formulae  installing ffmpeg · 38 of 129 · brew install --formula ffmpeg ─",
			"  ── ◐ claude-code  applying · run apply ─",
		}},
		{stoppedRun, []string{
			"  – Homebrew formulae  stopped · 38 of 129\n",
			"  – App settings  stopped\n",
		}},
	} {
		face, out, _ := applyAt(160, 64, time.Minute, tt.events())
		got := screen(out.String())
		if err := face.Close(); err != nil {
			t.Fatal(err)
		}
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Errorf("the face shows\n%s\nwant a row %q", got, want)
			}
		}
	}
}

// When the run ends, the screen goes and the record is left under the
// run's heading, kept to column 80: the lights, the bar with how the run
// ended, then every step, what needs you with what to do under it. A run
// stopped greys what it cut off and what never ran, says so, nothing red
// but what failed, and says where to carry on.
func TestApplyingLeavesItsRecord(t *testing.T) {
	for _, tt := range []struct {
		golden string
		events func() []event.Event
	}{{"apply-record.golden", appliedRun}, {"apply-record-stopped.golden", stoppedRun}} {
		t.Run(tt.golden, func(t *testing.T) {
			face, out, kb := applyAt(160, 64, time.Minute, tt.events())
			ended := kb.isRaw()
			if err := face.Close(); err != nil {
				t.Fatal(err)
			}
			raw := out.String()
			if after := raw[strings.LastIndex(raw, "\x1b[?1049h"):]; !inOrder(after, mouseOff, "\x1b[?1049l") || !strings.Contains(after, "\x1b[?25h") || ended || kb.listens() != 1 {
				t.Errorf("after the screen, the face wrote %q, raw: %v; want the mouse no longer reported, the terminal's own screen back, the cursor shown, and the keys it read while it was up no longer read", after, ended)
			}
			if !mouseOffFirst(kb) {
				t.Error("the keys stopped being read, the terminal out of its raw mode, before the mouse stopped being reported")
			}
			got := screen(raw)
			golden(t, tt.golden, got+"\n")
			for _, l := range lines(got) {
				if w := ansi.StringWidth(l); w > look.Width {
					t.Errorf("%q is %d columns, want at most %d", l, w, look.Width)
				}
			}
		})
	}
}

// The record names each thing a step left to do, as its panel does, what
// to do about it under it when it says: a step by hand with nothing more to
// say shows its name alone.
func TestApplyingRecordNamesWhatsLeft(t *testing.T) {
	face, out, _ := applyAt(160, 64, time.Minute, appliedRun())
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"  ▲ By hand  2 to do",
		"  │ Allow remote users Full Disk Access: System Settings › General › Sharing",
		"  │ → then: kit manual done sharing",
		"  │ Install the editor from its site (no Homebrew cask)",
		"  ● 1password  fine",
	}, "\n")
	if record := screen(out.String()); !strings.Contains(record, want) {
		t.Errorf("the record is\n%s\nwant By hand's row, each thing to do by name under it\n%s", record, want)
	}
}

// However applying's screen is left, the terminal's own screen comes back,
// the cursor shown, the mouse no longer reported, and its keys stop being
// read, the terminal out of its raw mode, as the question needs it: for a question asked mid-run, and
// back after it; when the window's made too small for it, the run drawn in
// place, and taken again when it's big enough; when the face closes
// without the run finishing.
func TestApplyingGivesTheScreenBack(t *testing.T) {
	var out syncBuffer
	var mu sync.Mutex
	width := 160
	kb := &keyboard{out: &out}
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return width, 64
	}).Keys(kb.listen, kb.halt)
	resize := func(w int) {
		mu.Lock()
		width = w
		mu.Unlock()
		time.Sleep(300 * time.Millisecond)
	}
	for _, e := range sixMinutesIn() {
		face.Emit(e)
	}
	time.Sleep(300 * time.Millisecond)
	onScreen := func() bool { return strings.Contains(screen(out.String()), "BOOT     ● GitHub") }
	mouse := func() bool { return mouseReported(out.String()) }
	if !onScreen() || !mouse() || !kb.isRaw() {
		t.Error("with its screen up, applying isn't reading keys, the terminal in its raw mode, and the mouse")
	}
	cursorShown := func() bool {
		raw := out.String()
		return strings.LastIndex(raw, "\x1b[?25h") > strings.LastIndex(raw, "\x1b[?25l")
	}
	back := face.StepAside()
	if onScreen() || mouse() || !mouseOffFirst(kb) || !cursorShown() || kb.isRaw() {
		t.Errorf("stepping aside for a question, the face left\n%s\nraw: %v; want the terminal's own screen, the cursor shown, the mouse no longer reported, then not raw", screen(out.String()), kb.isRaw())
	}
	back()
	if !onScreen() || !mouse() || !kb.isRaw() {
		t.Error("back from the question, the face didn't take its screen, its keys and the mouse again")
	}
	resize(99)
	if got := screen(out.String()); onScreen() || mouse() || !mouseOffFirst(kb) || kb.isRaw() || !strings.Contains(got, "◐ Homebrew formulae  installing ffmpeg") {
		t.Errorf("made too small, the window shows\n%s\nraw: %v; want the run in place on the terminal's own screen, the mouse no longer reported, then not raw", got, kb.isRaw())
	}
	resize(160)
	if !onScreen() || !mouse() || !kb.isRaw() {
		t.Errorf("made big enough again, the window shows\n%s\nwant the screen, its keys and the mouse taken again", screen(out.String()))
	}
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	if onScreen() || mouse() || !mouseOffFirst(kb) || !cursorShown() || kb.isRaw() {
		t.Errorf("closed mid-run, the face left\n%s\nraw: %v; want the terminal's own screen, the cursor shown, the mouse no longer reported, then not raw", screen(out.String()), kb.isRaw())
	}
	if n := kb.listens(); n != 3 {
		t.Errorf("the face read keys %d times; want once each time its screen went up, 3", n)
	}
}
