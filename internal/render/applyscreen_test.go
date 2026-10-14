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
		finished("Drift", "Homebrew casks", check.Result{State: check.Failed, Reason: "couldn't install tailscale-app: installer: The install failed. (The Installer encountered an error that caused the installation to fail.)"}),
		finished("Backups", "Arq", check.Result{State: check.Attention, Summary: "no backup plan", Items: []check.Item{
			{ID: "arq:plan", Name: "no backup plan is chosen", State: "problem", Detail: "open Arq and choose its backup plan"}}}),
		fine("Drift", "macOS login items", "14 added"),
	)
	return append(events, applyingStep("Backups", "App settings", "kit prefs restore", "com.example.editor restored", "com.example.terminal restored")...)
}

// signIn is 1Password's sign-in come up, a step by hand: kit opened the
// app for it, and tells by itself once it's done.
var signIn = event.NeedsYou{Time: at, Step: stepName("Manual", "1password"), Item: "1password", Title: "Sign in to 1Password", Steps: []string{
	"Scan the QR code it shows with the 1Password app on your phone",
	"In 1Password, Settings › Developer › turn on Integrate with 1Password CLI",
}, Opened: "1Password", Tells: true}

// waitingOnYou is the run once 1Password's sign-in has come up, while
// macOS asks for an administrator's password.
func waitingOnYou() []event.Event {
	return append(casksFailed(),
		event.StepStarted{Step: signIn.Step, Doing: "applying"},
		signIn,
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

// editor is a step by hand come up with steps of its own, then put off.
var editor = event.NeedsYou{Time: at, Step: stepName("Manual", "By hand"), Item: "editor", Title: "Install the editor from its site (no Homebrew cask)", Steps: []string{
	"Download it from editor.example.com",
	"Drag it to Applications",
}, More: 1}

// appliedRun is casksFailed to its end: the rest done, but Time Machine's
// disk and Arq's plan left to do, and two steps by hand, one saying what to
// run once it's done, one come up with steps of its own and put off.
func appliedRun() []event.Event {
	events := theRest(append(casksFailed(), editor), func(s event.Step) check.Result {
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
var panelRow = regexp.MustCompile(`(?m)^  ── [●◐✗▲–○△] (\S+)`)

// The keys at the screen's foot while it reads them, before anything's
// pinned, with no card up; and with 1Password's sign-in up.
const (
	runningFoot = "  tab panel · ↑↓ scroll · click a step to see it · q stop"
	signInFoot  = "  o open 1Password again · l later · tab panel · ↑↓ scroll · click a step to see it · q stop"
)

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
// step in a grid, a rule, the card up for you and the log's panels, a
// rule, macOS asking, the keys; a blank line over the first rule and under
// the second, none between the panels. What's left to do is counted on
// its own, never as needing you.
func TestApplyingHasAScreenOfItsOwn(t *testing.T) {
	for _, tt := range []struct {
		golden, counts, keys string
		took                 time.Duration
		events               func() []event.Event
	}{
		{"apply-screen.golden", "31 of 51 · 3 running · 1 to do · 6m12s", runningFoot, 6*time.Minute + 12*time.Second, sixMinutesIn},
		{"apply-screen-failed.golden", "35 of 51 · 1 failed · 2 running · 2 to do · 10m48s", runningFoot, 10*time.Minute + 48*time.Second, casksFailed},
		{"apply-screen-you.golden", "35 of 51 · 1 failed · 2 running · 1 needs you · 2 to do · 12m40s", signInFoot, 12*time.Minute + 40*time.Second, waitingOnYou},
	} {
		t.Run(tt.golden, func(t *testing.T) {
			face, out, _ := applyAt(160, 64, tt.took, tt.events())
			got := screen(out.String())
			if err := face.Close(); err != nil {
				t.Fatal(err)
			}
			golden(t, tt.golden, got+"\n")
			ls := lines(got)
			if len(ls) != 64 || !strings.HasSuffix(ls[3], "│  "+tt.counts) || ls[63] != tt.keys {
				t.Errorf("the screen is %d lines, its counts and keys\n%s\n%s\nwant 64 lines, the counts %q, the keys %q", len(ls), ls[3], ls[len(ls)-1], tt.counts, tt.keys)
			}
			if !strings.Contains(out.String(), "\x1b[?1049h\x1b[?25l") {
				t.Error("applying didn't take the alternate screen, its cursor hidden")
			}
			if strings.Contains(out.String(), "shift+drag") {
				t.Error("the keys say shift+drag selects; want no word of it")
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

// The log's panels, with nothing pinned, are the steps at work applying,
// in the order they started: a step finishing leaves its panel at once,
// failed or not; one only checking has none; one waiting partway through
// applying, for a thing another step puts in place, gives its panel up,
// and has it again once it's back at work.
func TestApplyingPanelsAreForStepsAtWork(t *testing.T) {
	var steps []event.Step
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		steps = append(steps, event.Step{Name: n, Title: n, Area: "Steps"})
	}
	face, out, _ := applyAt(160, 64, time.Second, []event.Event{event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps}})
	defer func() { _ = face.Close() }()
	start := func(names ...string) {
		for _, n := range names {
			face.Emit(event.StepStarted{Step: n, Doing: "checking"})
			face.Emit(event.StepStarted{Step: n, Doing: "applying"})
			face.Emit(event.Output{Step: n, Command: "run apply", Line: n + " at work"})
		}
	}
	panels := func(when string, want ...string) {
		t.Helper()
		time.Sleep(300 * time.Millisecond)
		if got := panelTitles(screen(out.String())); !slices.Equal(got, want) {
			t.Errorf("%s, the log's panels are %q; want %q\n%s", when, got, want, screen(out.String()))
		}
	}
	start("a", "b", "c")
	face.Emit(event.StepStarted{Step: "d", Doing: "checking"})
	panels("with a, b and c applying, d checking", "a", "b", "c")
	face.Emit(event.StepFinished{Step: "b", Result: check.Result{State: check.Failed, Reason: "couldn't apply: it broke"}})
	face.Emit(event.Doing{Step: "a", Says: "waiting for TOKEN: 2 to install"})
	start("e")
	panels("b failed, a waiting, e started", "c", "e")
	face.Emit(event.Doing{Step: "a", Says: "installing owner/private/fetcher", Of: 2})
	face.Emit(event.Output{Step: "a", Command: "brew install", Line: "a at work again"})
	face.Emit(event.StepFinished{Step: "c", Result: check.Result{State: check.OK, Summary: "done"}})
	panels("a back at work, c done", "a", "e")
	face.Emit(event.StepFinished{Step: "a", Result: check.Result{State: check.OK, Summary: "done"}})
	face.Emit(event.StepFinished{Step: "e", Result: check.Result{State: check.OK, Summary: "done"}})
	time.Sleep(300 * time.Millisecond)
	got := screen(out.String())
	rules := edges(lines(got))
	if len(rules) != 2 || panelTitles(got) != nil || strings.TrimSpace(strings.Join(lines(got)[rules[0]+1:rules[1]], "")) != "" {
		t.Errorf("with nothing at work, nothing pinned and no card, the log is\n%s\nwant it empty", got)
	}
}

// scratchLeft is a run in which the Scratch volume's step made the volume,
// printing as it did, but found two things, each of state, still left once
// it checked again, one saying what to do, while Claude Code installs.
func scratchLeft(state string) []event.Event {
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
			{ID: "scratch:tmpdir", Name: "Claude Code's settings don't send TMPDIR to it", State: state, Detail: "set it in Claude Code's settings"},
			{ID: "scratch:backups", Name: "Time Machine still backs it up", State: state},
		},
		Done: []check.Item{{ID: "scratch:mounted", Name: "/Volumes/Scratch", Action: "create"}},
	}))
	events = append(events, fine("Mac", "Disk space", "71% free"))
	return append(events, applyingStep("Steps", "claude-code", "run apply", "Setting up Claude Code...", "Verifying checksum")...)
}

// A finished step has a panel only once it's pinned: under the last lines
// it printed, which give way to it when the panel hasn't the room for
// both, what's left of it, as the record has it. What it's left you to do
// is each thing by name, white, how under it; what's wrong with a step
// unwell, in orange. The record has the step unwell's row with what's left
// under it; the to-do's row says it's below, under TO DO, cutting what
// else it says to keep that.
func TestApplyingPinnedPanelSaysWhatsLeft(t *testing.T) {
	left := []string{"Claude Code's settings don't send TMPDIR to it", "→ set it in Claude Code's settings", "Time Machine still backs it up"}
	for _, tt := range []struct {
		state, golden string
		named         func(string) string
		record        []string
	}{
		{check.Problem, "apply-screen-left.golden", look.White, []string{
			"\n  △ Scratch volume  created /Volumes/Scratch · not set up as it … · to do, below\n  ● Disk space",
			"\n  TO DO  2, in any order: kit apply checks them\n  △ 1  Claude Code's settings don't send TMPDIR to it\n       → set it in Claude Code's settings\n  △ 2  Time Machine still backs it up\n",
		}},
		{string(check.Attention), "", look.Orange, []string{"\n  ▲ Scratch volume  created /Volumes/Scratch · not set up as it should be\n  │ " + strings.Join(left, "\n  │ ") + "\n"}},
	} {
		t.Run(tt.state, func(t *testing.T) {
			face, out, kb := applyIn(colorprofile.TrueColor, 100, 30, time.Minute, scratchLeft(tt.state))
			if got := panelTitles(screen(uncoloured(out.String()))); !slices.Equal(got, []string{"claude-code"}) {
				t.Errorf("before it's pinned, the panels are %q; want claude-code's alone, at work", got)
			}
			pin(t, out, kb, "Scratch volume")
			got := screen(uncoloured(out.String()))
			if tt.golden != "" {
				golden(t, tt.golden, got+"\n")
			}
			if _, under := panelOf(got, "Scratch volume"); len(under) < 4 || !slices.Equal(under[len(under)-4:], append([]string{"    Indexing disabled."}, left...)) {
				t.Errorf("the Scratch volume's panel shows\n%s\nwant its last line printed, then what's left: %q", strings.Join(under, "\n"), left)
			}
			if frame := strings.Join(lastFrame(out.String()), "\n"); !strings.Contains(frame, tt.named(left[0])) || !strings.Contains(frame, tt.named(left[2])) {
				t.Errorf("the things left aren't named as they should be in\n%s", frame)
			}
			kb.press(t, slices.Repeat([]ask.Key{keyUp}, 20)...)
			if _, under := panelOf(screen(uncoloured(out.String())), "Scratch volume"); len(under) < 4 || under[0] != "==> Adding volume 1" || !slices.Equal(under[len(under)-3:], left) {
				t.Errorf("scrolled back as far as it goes, the Scratch volume's panel shows\n%s\nwant its first lines printed, then what's left", strings.Join(under, "\n"))
			}
			face.Emit(event.RunFinished{Time: at, Duration: time.Minute, Counts: map[check.State]int{check.OK: 2, check.Attention: 1}})
			if err := face.Close(); err != nil {
				t.Fatal(err)
			}
			record := screen(uncoloured(out.String()))
			for _, want := range tt.record {
				if !strings.Contains(record, want) {
					t.Errorf("the record is\n%s\nwant it to hold\n%s", record, want)
				}
			}
			if tt.state == string(check.Attention) && strings.Contains(record, "TO DO") {
				t.Errorf("the record is\n%s\nwant no TO DO: nothing's left only for you to do", record)
			}
		})
	}
}

// handCard is a step by hand come up with nothing to tell kit it's done:
// kit opened System Settings for it, and two more wait after it.
var handCard = event.NeedsYou{Time: at, Step: stepName("Manual", "By hand"), Item: "karabiner", Title: "Allow Karabiner-Elements", Steps: []string{
	"Allow Karabiner-Elements' driver (the Allow button, near the bottom)",
	"Then Input Monitoring › turn on karabiner_grabber and karabiner_observer",
}, Opened: "System Settings", More: 2}

// paperCard is a step by hand come up that kit neither opened anything for
// nor can tell is done.
var paperCard = event.NeedsYou{Time: at, Step: stepName("Manual", "By hand"), Item: "paper", Title: "Install Paper", Steps: []string{"Download it from paper.example.com"}}

// cardUp is casksFailed with card come up for its step, running.
func cardUp(card event.NeedsYou) []event.Event {
	return append(casksFailed(), event.StepStarted{Step: card.Step, Doing: "applying"}, card)
}

// A step by hand come up is a card, straight under the first rule, over
// the panels: an orange rule carrying the needs-you mark, its title, that
// it needs you, and how many more wait after it; its steps numbered, the
// app kit opened for it the first, a blank line after each; and, when kit
// tells by itself it's done, that kit carries on. Its cell in the grid
// needs you, in orange; the counts say so; the keys answering it lead the
// foot: o when kit opened an app, enter when kit can't tell by itself, l
// always. The step finishing takes it down.
func TestApplyingCard(t *testing.T) {
	for _, tt := range []struct {
		name, golden string
		card         event.NeedsYou
		lines        []string
		keys         string
	}{
		{"kit opened it, and tells", "", signIn, []string{
			"  ── ▲ Sign in to 1Password  needs you ─",
			"",
			"       1  1Password is open: kit opened it for you",
			"",
			"       2  Scan the QR code it shows with the 1Password app on your phone",
			"",
			"       3  In 1Password, Settings › Developer › turn on Integrate with 1Password CLI",
			"",
			"       kit carries on by itself once it's done",
			"",
		}, signInFoot},
		{"kit opened it, more after it", "apply-screen-hand.golden", handCard, []string{
			"  ── ▲ Allow Karabiner-Elements  needs you · 2 more after this ─",
			"",
			"       1  System Settings is open: kit opened it for you",
			"",
			"       2  Allow Karabiner-Elements' driver (the Allow button, near the bottom)",
			"",
			"       3  Then Input Monitoring › turn on karabiner_grabber and karabiner_observer",
			"",
		}, "  o open System Settings again · enter done · l later · tab panel · ↑↓ scroll · click a step to see it · q stop"},
		{"neither", "", paperCard, []string{
			"  ── ▲ Install Paper  needs you ─",
			"",
			"       1  Download it from paper.example.com",
			"",
		}, "  enter done · l later · tab panel · ↑↓ scroll · click a step to see it · q stop"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, cardUp(tt.card))
			defer func() { _ = face.Close() }()
			got := screen(uncoloured(out.String()))
			if tt.golden != "" {
				golden(t, tt.golden, got+"\n")
			}
			ls := lines(got)
			rules := edges(ls)
			card := ls[rules[0]+1 : rules[0]+1+len(tt.lines)]
			for i, want := range tt.lines {
				if !strings.HasPrefix(card[i], want) || (want == "" && card[i] != "") {
					t.Errorf("the card is\n%s\nwant\n%s", strings.Join(card, "\n"), strings.Join(tt.lines, "\n"))
					break
				}
			}
			if after := ls[rules[0]+1+len(tt.lines)]; !strings.HasPrefix(after, "  ── ◐ Homebrew formulae") {
				t.Errorf("under the card is %q; want the panels", after)
			}
			if !strings.Contains(ls[3], " · 1 needs you · ") || foot(got, 1)[0] != tt.keys {
				t.Errorf("the counts are %q, the keys %q; want 1 needs you, the keys %q", ls[3], foot(got, 1)[0], tt.keys)
			}
			frame := strings.Join(lastFrame(out.String()), "\n")
			if cell := look.Orange("▲") + " " + look.Orange("By hand"); tt.card.Step == stepName("Manual", "By hand") && !strings.Contains(frame, cell) {
				t.Errorf("the grid has no cell %q", cell)
			}
			if rule := "  " + look.Orange("── ") + look.Orange("▲") + " " + look.Strong(tt.card.Title) + "  " + look.Orange("needs you"); !strings.Contains(frame, rule) {
				t.Errorf("the card's rule isn't orange, its title strong, needs you orange:\n%s", frame)
			}
			face.Emit(event.StepFinished{Time: at, Step: tt.card.Step, Result: check.Result{State: check.OK, Summary: "done"}})
			time.Sleep(300 * time.Millisecond)
			got = screen(uncoloured(out.String()))
			if strings.Contains(got, tt.card.Title) || strings.Contains(got, "needs you") || foot(got, 1)[0] != runningFoot {
				t.Errorf("its step done, the screen is\n%s\nwant the card gone, nothing needing you, the keys as they were", got)
			}
		})
	}
}

// There's one card at a time: the last come up, whatever its step. A step
// finishing takes its own card down, never another's; a step by hand
// moving on to its next line replaces its card. A step queued for its
// card's turn has the needs-you mark in the grid, but only the card up is
// counted as needing you.
func TestApplyingOneCardAtATime(t *testing.T) {
	face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, append(cardUp(signIn),
		event.StepStarted{Step: paperCard.Step, Doing: "applying"},
		event.Doing{Step: paperCard.Step, Says: "needs you"},
	))
	defer func() { _ = face.Close() }()
	shows := func(when, title string) {
		t.Helper()
		time.Sleep(300 * time.Millisecond)
		got := screen(uncoloured(out.String()))
		ls := lines(got)
		rules := edges(ls)
		if rule := ls[rules[0]+1]; !strings.HasPrefix(rule, "  ── ▲ "+title+"  needs you") || strings.Count(got, "  needs you") != 1 {
			t.Errorf("%s, the screen is\n%s\nwant one card, %s's", when, got, title)
		}
		if !strings.Contains(ls[3], " · 1 needs you · ") {
			t.Errorf("%s, the counts are %q; want 1 needs you, the card up alone", when, ls[3])
		}
		frame := strings.Join(lastFrame(out.String()), "\n")
		if cell := look.Orange("▲") + " " + look.Orange("By hand"); !strings.Contains(frame, cell) {
			t.Errorf("%s, By hand isn't marked as needing you in the grid", when)
		}
	}
	shows("1Password's card up, By hand queued for its turn", signIn.Title)
	face.Emit(paperCard)
	shows("By hand's card come up", paperCard.Title)
	face.Emit(event.StepFinished{Time: at, Step: signIn.Step, Result: check.Result{State: check.OK, Summary: "signed in"}})
	shows("1Password's step finished after", paperCard.Title)
	face.Emit(handCard)
	shows("By hand on to its next line", handCard.Title)
}

// A step waiting its turn says its own words, when it has some, rather
// than what it waits for.
func TestApplyingWaitingInItsOwnWords(t *testing.T) {
	steps := []event.Step{
		{Name: "cask", Title: "Homebrew casks", Area: "Drift"},
		{Name: "arq", Title: "Arq", Area: "Backups", Waits: []string{"cask"}},
		{Name: "manual", Title: "By hand", Area: "Manual", Waits: []string{"cask", "arq"}, Waiting: "at the end, once the rest is done"},
	}
	face, out, kb := applyAt(160, 64, time.Minute, []event.Event{
		event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps},
		event.StepStarted{Step: "cask", Doing: "applying"},
	})
	defer func() { _ = face.Close() }()
	pin(t, out, kb, "Arq", "By hand")
	got := screen(out.String())
	for _, want := range []string{"  ── ○ Arq  waiting for Homebrew casks ─", "  ── ○ By hand  at the end, once the rest is done ─"} {
		if !strings.Contains(got, want) {
			t.Errorf("the screen is\n%s\nwant a panel %q", got, want)
		}
	}
}

// A step that's left only something for you to do (a problem only you can
// put right, or a step by hand), or that was deferred behind such steps
// alone, is to do: in the grid a white △, its name white, never orange;
// counted on its own, white; its area's light the white △ once nothing in
// it failed or needs you, at once, before what's running. A step deferred
// behind a failure isn't, nor one with anything else wrong. In a window
// too small for the screen, it isn't listed as unwell.
func TestApplyingToDo(t *testing.T) {
	steps := []event.Step{
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "prefs", Title: "App settings", Area: "Backups"},
		{Name: "scratch", Title: "Scratch volume", Area: "Mac", Waits: []string{"prefs"}},
		{Name: "disk", Title: "Disk space", Area: "Mac"},
		{Name: "cask", Title: "Homebrew casks", Area: "Drift"},
		{Name: "login", Title: "macOS login items", Area: "Drift", Waits: []string{"cask"}},
		{Name: "brew", Title: "Homebrew formulae", Area: "Drift"},
		{Name: "by-hand", Title: "By hand", Area: "Manual"},
		{Name: "1password", Title: "1password", Area: "Manual"},
	}
	problem := func(name, detail string) check.Result {
		return check.Result{State: check.Attention, Summary: name, Items: []check.Item{{ID: "x:" + name, Name: name, State: check.Problem, Detail: detail}}}
	}
	events := []event.Event{
		event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps},
		event.StepFinished{Step: "time-machine", Result: problem("no backup disk yet", "System Settings › General › Time Machine › Add Backup Disk")},
		event.StepFinished{Step: "prefs", Result: problem("not restored yet", "kit prefs restore")},
		event.StepFinished{Step: "scratch", Result: check.Result{State: check.Deferred, Reason: "waits for App settings"}},
		event.StepFinished{Step: "disk", Result: check.Result{State: check.Attention, Summary: "8% free", Items: []check.Item{{ID: "disk:low", Name: "only 8% free", State: string(check.Attention)}}}},
		event.StepFinished{Step: "cask", Result: check.Result{State: check.Failed, Reason: "couldn't install tailscale-app"}},
		event.StepFinished{Step: "login", Result: check.Result{State: check.Deferred, Reason: "needs Homebrew casks"}},
		event.StepStarted{Step: "brew", Doing: "applying"},
		event.StepFinished{Step: "by-hand", Result: check.Result{State: check.Attention, Summary: "1 to do", Items: []check.Item{{ID: "manual:paper", Name: "Install Paper", State: check.ByHand}}}},
		event.StepStarted{Step: "1password", Doing: "applying"},
	}
	face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, events)
	got := screen(uncoloured(out.String()))
	if counts := lines(got)[3]; !strings.HasSuffix(counts, "│  7 of 9 · 1 failed · 2 running · 4 to do · 1m00s") {
		t.Errorf("the counts are %q; want 4 to do, on their own", counts)
	}
	frame := strings.Join(lastFrame(out.String()), "\n")
	for _, title := range []string{"Time Machine", "App settings", "Scratch volume", "By hand"} {
		if !strings.Contains(frame, look.White("△")+" "+look.White(title)) || strings.Contains(frame, look.Orange(title)) {
			t.Errorf("%s isn't a white △ and its name white in the grid", title)
		}
	}
	for _, cell := range []string{look.Orange("▲") + " " + look.Orange("Disk space"), look.Muted("– macOS login items")} {
		if !strings.Contains(frame, cell) {
			t.Errorf("the grid has no cell %q: want what's wrong, and what's deferred behind a failure, as they were", cell)
		}
	}
	if !strings.Contains(frame, look.White("4 to do")) {
		t.Error("what's to do isn't counted in white")
	}
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}

	var narrow syncBuffer
	small := render.NewPretty(&colorprofile.Writer{Forward: &narrow, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
	for _, e := range events {
		small.Emit(e)
	}
	time.Sleep(300 * time.Millisecond)
	live := turned(screen(narrow.String()))
	if want := "  △ BACKUPS   ▲ MAC    ✗ CONFIG   △ MANUAL\n"; !strings.Contains(live, want) {
		t.Errorf("in a small window, the screen is\n%s\nwant the lights\n%s", live, want)
	}
	for _, title := range []string{"Time Machine", "App settings", "Scratch volume", "By hand"} {
		if strings.Contains(live, title) {
			t.Errorf("in a small window, the screen is\n%s\nwant no row for %s: it's to do, not unwell", live, title)
		}
	}
	if !strings.Contains(live, "  ▲ Disk space  8% free\n") || !strings.Contains(live, "  ✗ Homebrew casks  couldn't install tailscale-app\n") {
		t.Errorf("in a small window, the screen is\n%s\nwant what's unwell listed", live)
	}
	if err := small.Close(); err != nil {
		t.Fatal(err)
	}
}

// In a window too small for applying's screen, keys aren't read: a card
// waiting for an answer says a bigger window shows them; one kit tells
// about by itself says it carries on.
func TestApplyingCardInASmallWindow(t *testing.T) {
	for _, tt := range []struct {
		card event.NeedsYou
		want string
	}{
		{paperCard, "a bigger window shows the keys to answer it"},
		{signIn, "kit carries on by itself once it's done"},
	} {
		var narrow syncBuffer
		small := render.NewPretty(&colorprofile.Writer{Forward: &narrow, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
		for _, e := range cardUp(tt.card) {
			small.Emit(e)
		}
		time.Sleep(300 * time.Millisecond)
		live := turned(screen(narrow.String()))
		if !strings.Contains(live, tt.card.Title) || !strings.Contains(live, tt.want) {
			t.Errorf("%s in a small window:\n%s\nwant its card, saying %q", tt.card.Title, live, tt.want)
		}
		if err := small.Close(); err != nil {
			t.Fatal(err)
		}
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
// ended, then every step, what failed with why and what to run under it,
// then what's left to do. A run stopped greys what it cut off and what
// never ran, says so, nothing red but what failed, and says where to carry
// on.
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

// The record ends with what the run's left you to do, numbered, each with
// how to do it under it: a thing's own words, or, for a step by hand that
// came up with steps of its own, those. Its steps' rows say it's below,
// in white, never as needing you; the bar counts it on its own.
func TestApplyingRecordListsWhatsToDo(t *testing.T) {
	face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, appliedRun())
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	record := screen(uncoloured(out.String()))
	for _, want := range []string{
		"\n  △ Time Machine  no backup disk · to do, below\n  △ Arq  no backup plan · to do, below\n",
		"\n  △ By hand  2 to do · to do, below\n",
		"  1 failed · 4 to do · 24m31s\n",
		"\n" + strings.Join([]string{
			"  TO DO  4, in any order: kit apply checks them",
			"  △ 1  no backup disk is set up",
			"       → System Settings › General › Time Machine › Add Backup Disk",
			"  △ 2  no backup plan is chosen",
			"       → open Arq and choose its backup plan",
			"  △ 3  Allow remote users Full Disk Access: System Settings › General › Sharing",
			"       → then: kit manual done sharing",
			"  △ 4  Install the editor from its site (no Homebrew cask)",
			"       → Download it from editor.example.com",
			"       → Drag it to Applications",
		}, "\n") + "\n",
	} {
		if !strings.Contains(record, want) {
			t.Errorf("the record is\n%s\nwant it to hold\n%s", record, want)
		}
	}
	raw := out.String()
	raw = raw[strings.LastIndex(raw, "\x1b[?1049l"):]
	for _, want := range []string{look.White("△") + " " + look.Strong("By hand") + "  " + look.White("2 to do"), look.White("4 to do"), look.Strong("Install the editor from its site (no Homebrew cask)")} {
		if !strings.Contains(raw, want) {
			t.Errorf("the record doesn't hold %q", want)
		}
	}
	if strings.Contains(raw, look.Orange("▲")) || strings.Contains(raw, "need you") {
		t.Error("the record has something needing you; want what's left to do white")
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

// A step that failed says what went wrong in a few words, never a command
// or a path: pinned, its panel has the command it ran after them, dim, on
// one line however many lines it has, then the last lines it printed. In
// the record, what went wrong is red, why is under it, or, when nothing
// says, the last line it printed, then what to run to try it again.
func TestApplyingFailedStepsSayWhatWentWrong(t *testing.T) {
	gpg := stepName("Steps", "gpg-key")
	run := "/Users/someone/.config/kit/laptop/steps/gpg-key/run"
	login := stepName("Drift", "macOS login items")
	script := "osascript -l JavaScript -e 'function run() {\n  var se = Application(1);\n  return 1;\n}'"
	events := []event.Event{
		event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: newMacSteps()},
		event.StepStarted{Time: at, Step: gpg, Doing: "applying"},
		event.Output{Step: gpg, Command: run + " apply", Line: run + ": line 53: gpg: command not found"},
		finished("Steps", "gpg-key", check.Result{State: check.Failed, Reason: "couldn't apply: gpg: command not found"}),
		event.StepStarted{Time: at, Step: login, Doing: "applying"},
		event.Output{Step: login, Command: script, Line: "execution error: Error: AppleEvent timed out. (-1712)"},
		finished("Drift", "macOS login items", check.Result{State: check.Failed, Reason: "couldn't add com.example.app: execution error: Error: AppleEvent timed out. (-1712)"}),
		finished("Drift", "Sync", check.Result{State: check.Failed, Reason: "not a git repository"}),
	}
	face, out, kb := applyAt(160, 64, time.Minute, events)
	if got := panelTitles(screen(out.String())); got != nil {
		t.Errorf("with nothing at work, the panels are %q; want none: what failed stays in the grid", got)
	}
	pin(t, out, kb, "gpg-key", "macOS login items")
	live := screen(out.String())
	for title, want := range map[string]string{
		"gpg-key":           "  ── ✗ gpg-key  couldn't apply · " + run + " apply ─",
		"macOS login items": "  ── ✗ macOS login items  couldn't add com.example.app · osascript -l JavaScript -e 'function run() { var se = Application(1); return 1; }' ─",
	} {
		if rule, _ := panelOf(live, title); !strings.HasPrefix(rule, want) {
			t.Errorf("%s's panel's rule is\n%q\nwant it to start\n%q", title, rule, want)
		}
	}
	if _, under := panelOf(live, "macOS login items"); len(under) != 1 {
		t.Errorf("the login items' panel shows %q, want the line it printed alone: its command on its rule, on one line", under)
	}
	face.Emit(event.RunFinished{Time: at, Duration: time.Minute, Counts: map[check.State]int{check.Failed: 3}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	record := screen(out.String())
	for _, want := range []string{
		"  ✗ gpg-key  couldn't apply\n  │ gpg: command not found\n  │ → kit apply Steps/gpg-key\n",
		"  ✗ macOS login items  couldn't add com.example.app\n  │ execution error: Error: AppleEvent timed out. (-1712)\n  │ → kit apply Drift/macOS login items\n  ○ Claude MCP",
		"  ✗ Sync  couldn't check\n  │ not a git repository\n  │ → kit apply Drift/Sync\n",
	} {
		if !strings.Contains(record, want) {
			t.Errorf("the record is\n%s\nwant\n%s", record, want)
		}
	}
}
