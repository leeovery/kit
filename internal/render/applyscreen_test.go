package render_test

import (
	"fmt"
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

// newMac are a new Mac's steps, as kit apply runs them once the boot's in
// the terminal: the boot's, checked again, kit's own, by the names kit
// gives them, the user's own, and two steps by hand: 1Password's sign-in,
// which the secrets wait for, and the rest, at the end.
var newMac = []event.Step{
	{Name: "boot-github", Title: "GitHub", Area: boot.AreaBooted},
	{Name: "boot-mac", Title: "This Mac", Area: boot.AreaBooted},
	{Name: "boot-homebrew", Title: "Homebrew", Area: boot.AreaBooted},
	{Name: "boot-kit-config", Title: "kit-config", Area: boot.AreaBooted},
	{Name: "homebrew", Title: "Homebrew", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"},
	{Name: "file", Title: "Linked files", Area: "Drift", Part: render.PartFiles},
	{Name: "path", Title: "Shell PATH", Area: "Drift"},
	{Name: "touch-id-sudo", Title: "Touch ID for sudo", Area: "Drift"},
	{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings"},
	{Name: "power", Title: "Power settings", Area: "Drift", Row: "Mac settings"},
	{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Waits: []string{"homebrew"}},
	{Name: "cask", Title: "Homebrew casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Waits: []string{"homebrew"}},
	{Name: "mas", Title: "App Store apps", Area: "Drift", Row: "App Store apps"},
	{Name: "npm", Title: "npm packages", Area: "Drift", Row: "Developer tools", Waits: []string{"brew"}},
	{Name: "composer", Title: "Composer packages", Area: "Drift", Row: "Developer tools", Waits: []string{"brew"}},
	{Name: "login-item", Title: "macOS login items", Area: "Drift", Row: "Login items", Waits: []string{"cask", "mas"}},
	{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"1password"}},
	{Name: "git-config", Title: "git config", Area: "Drift", Row: "Git", Waits: []string{"secret"}},
	{Name: "config", Title: "kit-config edits", Area: "Drift"},
	{Name: "config-sync", Title: "Sync", Area: "Config"},
	{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
	{Name: "arq", Title: "Arq", Area: "Backups", Waits: []string{"cask"}},
	{Name: "prefs", Title: "App settings", Area: "Backups", Waits: []string{"cask"}},
	{Name: "disk", Title: "Disk space", Area: "Mac"},
	{Name: "load", Title: "Load", Area: "Mac"},
	{Name: "claude-code", Title: "claude-code", Area: "Steps"},
	{Name: "fonts", Title: "fonts", Area: "Steps"},
	{Name: "mise", Title: "mise", Area: "Steps", Waits: []string{"brew"}},
	{Name: "1password", Title: "1password", Area: "Manual", ByHand: []event.Hand{{Name: "1password", Title: "Sign in to 1Password"}}},
	{Name: "manual", Title: "By hand", Area: "Manual", Waiting: "at the end, once the rest is done", ByHand: []event.Hand{
		{Name: "sharing", Title: "Allow remote users Full Disk Access"},
		{Name: "editor", Title: "Install the editor from its site"},
		{Name: "karabiner", Title: "Allow Karabiner-Elements"},
	}},
}

// after is sec seconds into the run.
func after(sec int) time.Time { return at.Add(time.Duration(sec) * time.Second) }

// finished is the step called step finished sec seconds in, as r.
func finished(sec int, step string, r check.Result) event.Event {
	return event.StepFinished{Time: after(sec), Step: step, Result: r}
}

// fine is the step called step finished well sec seconds in, saying
// summary.
func fine(sec int, step, summary string) event.Event {
	return finished(sec, step, check.Result{State: check.OK, Summary: summary})
}

// applying is the step called step starting sec seconds in, checking, then
// applying.
func applying(sec int, step string) []event.Event {
	return []event.Event{event.StepStarted{Time: after(sec), Step: step, Doing: "checking"}, event.StepStarted{Time: after(sec), Step: step, Doing: "applying"}}
}

// installed are a kind's lines for the run's log: what it installed, names
// one at a time from sec seconds in, every every seconds.
func installed(step string, sec, every int, names ...string) []event.Event {
	var events []event.Event
	for i, n := range names {
		events = append(events, event.Logged{Time: after(sec + i*every), Step: step, Line: "Installed " + n})
	}
	return events
}

// numbered are n names, what each and its number.
func numbered(what string, n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%d", what, i+1)
	}
	return names
}

// sixMinutesIn is the run six minutes in: the boot's steps done, what's
// quick done too, Time Machine left for later; Homebrew's formulae and
// casks installing, each telling the log what it's installed; Claude Code
// setting itself up; the rest waiting.
func sixMinutesIn() []event.Event { return sixMinutesInWith(12, 8) }

// sixMinutesInWith is sixMinutesIn, Homebrew having installed formulae
// formulae and casks casks.
func sixMinutesInWith(formulae, casks int) []event.Event {
	events := []event.Event{event.RunStarted{Time: at, Command: "bootstrap", Machine: "laptop", Steps: newMac}}
	for _, s := range []string{"boot-github", "boot-mac", "boot-homebrew", "boot-kit-config"} {
		events = append(events, fine(1, s, "fine"))
	}
	events = append(events,
		fine(2, "homebrew", "/opt/homebrew"),
		fine(3, "file", "54 linked"),
		fine(3, "path", "15 directories"),
		finished(5, "time-machine", check.Result{State: check.Attention, Summary: "no backup disk", Items: []check.Item{
			{ID: "time-machine:disk", Name: "Add a Time Machine disk", State: check.Problem, Detail: "System Settings › General › Time Machine › Add Backup Disk"}}}),
		fine(6, "disk", "71% free"),
		fine(6, "load", "load 3.1 on 8 cores"),
		fine(8, "touch-id-sudo", "turned on"),
		fine(21, "fonts", "16 installed"),
		fine(40, "defaults", "99 set"),
		fine(41, "power", "6 set"),
		fine(44, "config", "all committed"),
		fine(46, "config-sync", "pushed"),
	)
	events = append(events, applying(30, "cask")...)
	events = append(events, applying(52, "brew")...)
	events = append(events, applying(60, "claude-code")...)
	events = append(events, installed("cask", 31, 37, numbered("cask", casks)...)...)
	events = append(events, installed("brew", 53, 25, numbered("formula", formulae)...)...)
	events = append(events,
		event.Doing{Time: after(350), Step: "brew", Says: "installing ffmpeg", Done: 38, Of: 129},
		event.Doing{Time: after(350), Step: "cask", Says: "installing slack", Done: 4, Of: 42},
	)
	// The run tells them as they happen.
	slices.SortStableFunc(events, func(a, b event.Event) int { return a.At().Compare(b.At()) })
	return events
}

// signIn is 1Password's sign-in come up, a step by hand the secrets wait
// for: why, as its notes say; its steps, a subheading among them; and kit
// tells by itself once it's done.
var signIn = event.NeedsYou{Time: after(345), Step: "1password", Item: "1password", Title: "Sign in to 1Password",
	Why: "Every secret kit puts in place comes from 1Password, so it needs the app signed in and its command line turned on.",
	Steps: []string{
		"Open 1Password",
		"Scan the QR code it shows with the 1Password app on your phone",
		"If it doesn't ask, in 1Password:",
		"Settings › Developer › turn on Integrate with 1Password CLI",
	}, Tells: true}

// cardUp is sixMinutesIn with card come up for its step, applying.
func cardUp(card event.NeedsYou) []event.Event {
	return append(sixMinutesIn(), event.StepStarted{Time: card.Time, Step: card.Step, Doing: "applying"}, card)
}

// waitingOnYou is the run once 1Password's sign-in has come up.
func waitingOnYou() []event.Event { return cardUp(signIn) }

// casksFailed is the run once Homebrew's casks have failed, one wouldn't
// install, and macOS login items, which need them, weren't checked.
func casksFailed() []event.Event {
	events := sixMinutesIn()
	for _, l := range []string{
		"==> Installing Cask tailscale-app",
		"installer: Package name is Tailscale",
		"installer: The install failed. (The Installer encountered an error that caused the installation to fail.)",
	} {
		events = append(events, event.Output{Time: after(355), Step: "cask", Command: "brew install --cask tailscale-app", Line: l})
	}
	return append(events,
		finished(356, "cask", check.Result{State: check.Failed, Reason: "couldn't install tailscale-app: installer: The install failed. (The Installer encountered an error that caused the installation to fail.)"}),
		finished(357, "login-item", check.Result{State: check.Deferred, Reason: "needs Homebrew casks"}),
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
	face, out, kb, _ := applyClocked(profile, w, h, took, events)
	return face, out, kb
}

// applyClocked is applyIn, with the clock the face tells the time by, at
// each event's time as it comes.
func applyClocked(profile colorprofile.Profile, w, h int, took time.Duration, events []event.Event) (*render.Pretty, *syncBuffer, *keyboard, *clock) {
	out := &syncBuffer{}
	c := &clock{t: at}
	kb := &keyboard{out: out}
	face := render.NewPretty(&colorprofile.Writer{Forward: out, Profile: profile}, 80, true).
		Sized(func() (int, int) { return w, h }).Clocked(c.now).Keys(kb.listen, kb.halt)
	for _, e := range events {
		if t := e.At(); !t.IsZero() {
			c.set(t)
		}
		face.Emit(e)
	}
	c.set(at.Add(took))
	time.Sleep(300 * time.Millisecond)
	return face, out, kb, c
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
	for _, s := range newMac {
		if !done[s.Name] {
			events = append(events, event.StepFinished{Time: after(1400), Step: s.Name, Result: end(s)})
		}
	}
	return events
}

// editor is the step by hand at the end come up with steps of its own, a
// subheading among them, and left to do.
var editor = event.NeedsYou{Time: after(1380), Step: "manual", Item: "editor", Title: "Install the editor from its site", Steps: []string{
	"Download it from editor.example.com",
	"Once it's downloaded, in Finder:",
	"Drag it to Applications",
}, More: 1}

// appliedRun is casksFailed to its end: the rest done, but Time Machine's
// disk left to do, and two steps by hand, one saying what to run once it's
// done, one come up with steps of its own and left to do.
func appliedRun() []event.Event {
	events := theRest(append(casksFailed(), editor), func(s event.Step) check.Result {
		if s.Name == "manual" {
			return check.Result{State: check.Attention, Summary: "1 of 3 done", Items: []check.Item{
				{ID: "manual:sharing", Name: "Allow remote users Full Disk Access", State: check.ByHand, Detail: "then: kit manual done sharing"},
				{ID: "manual:editor", Name: "Install the editor from its site", State: check.ByHand}}}
		}
		return check.Result{State: check.OK, Summary: "fine"}
	})
	return append(events, event.RunFinished{Time: after(1471), Duration: 24*time.Minute + 31*time.Second, Counts: map[check.State]int{check.OK: 27, check.Attention: 2, check.Failed: 1}})
}

// stoppedRun is waitingOnYou stopped by ctrl+c: what was running cut off,
// what hadn't started never run, both failing as the engine has them.
func stoppedRun() []event.Event {
	events := theRest(waitingOnYou(), func(event.Step) check.Result {
		return check.Result{State: check.Failed, Reason: event.ErrStopped.Error()}
	})
	return append(events, event.RunFinished{Time: after(782), Duration: 13*time.Minute + 2*time.Second, Counts: map[check.State]int{check.OK: 17, check.Attention: 1, check.Failed: 12}})
}

// lines are a screen's lines.
func lines(screen string) []string { return strings.Split(screen, "\n") }

// headLines are how many lines the screen's header and the rule under it
// take: a blank line, the wordmark's three, a blank line, the rule.
const headLines = 6

// split is the column applying's panes part at in a window w wide.
func split(w int) int { return max(52, min(w*60/160, w-41)) }

// panes are a screen w wide's left and right panes, a line each of the
// screen's under its header, trailing spaces trimmed: the left without the
// rule between them, the right without it.
func panes(screen string, w int) (left, right []string) {
	at := split(w)
	for _, l := range lines(screen)[headLines:] {
		r := []rune(l)
		left = append(left, strings.TrimRight(string(r[:min(at, len(r))]), " "))
		right = append(right, strings.TrimRight(string(r[min(at+1, len(r)):]), " "))
	}
	return left, right
}

// rowAt is the screen's line the row called name is on, in a screen w
// wide: -1 for none.
func rowAt(screen string, w int, name string) int {
	left, _ := panes(screen, w)
	for i, l := range left {
		if rest, ok := strings.CutPrefix(l, "  "); ok && len([]rune(rest)) > 2 {
			if r := string([]rune(rest)[1:]); strings.TrimSpace(r) == name || strings.HasPrefix(strings.TrimSpace(r), name+"  ") {
				return headLines + i
			}
		}
	}
	return -1
}

// rowLine is the row called name's line in a screen w wide.
func rowLine(t *testing.T, screen string, w int, name string) string {
	t.Helper()
	at := rowAt(screen, w, name)
	if at < 0 {
		t.Fatalf("no row %s in\n%s", name, screen)
	}
	left, _ := panes(screen, w)
	return left[at-headLines]
}

// logOf is what the log shows in a screen w wide: the right pane's lines
// under its heading, to the last that isn't blank.
func logOf(screen string, w int) []string {
	_, right := panes(screen, w)
	at := slices.IndexFunc(right, func(l string) bool {
		return l == "  LOG" || strings.HasPrefix(l, "  LOG · ")
	})
	if at < 0 {
		return nil
	}
	shown := right[at+2:]
	for len(shown) > 0 && shown[len(shown)-1] == "" {
		shown = shown[:len(shown)-1]
	}
	return shown
}

// clickRow is a click on the row called name, as the screen w wide has it.
func clickRow(name string) func(screen string, w int) []ask.Key {
	return func(screen string, w int) []ask.Key { return []ask.Key{click(8, rowAt(screen, w, name))} }
}

// pressing are keys, pressed whatever the screen.
func pressing(keys ...ask.Key) func(string, int) []ask.Key {
	return func(string, int) []ask.Key { return keys }
}

// While applying, at a terminal big enough, kit has a screen of its own,
// the window's whole size: the header, the bar and how the run stands
// beside the wordmark; a rule across with a junction where the panes part;
// the rows at the left, the log at the right. Each is drawn as round 10
// has it, at the size round 10 drew and at the least the screen takes:
// following the log, a card up, a row chosen, scrolled back, a step
// failed, a row chosen that's printed something, and its raw view.
func TestApplyingHasAScreenOfItsOwn(t *testing.T) {
	took := 6*time.Minute + 12*time.Second
	for _, tt := range []struct {
		name   string
		events func() []event.Event
		keys   func(screen string, w int) []ask.Key
	}{
		{"following", sixMinutesIn, nil},
		{"card", waitingOnYou, nil},
		{"row", sixMinutesIn, clickRow("Homebrew casks")},
		{"scrolled", func() []event.Event { return sixMinutesInWith(40, 12) }, pressing(keyUp, keyUp, keyUp, keyUp, keyUp)},
		{"failed", casksFailed, nil},
		{"row-printed", printing, clickRow("Homebrew formulae")},
		{"raw", printing, func(screen string, w int) []ask.Key { return append(clickRow("Homebrew formulae")(screen, w), keyR) }},
	} {
		for _, size := range [][2]int{{160, 64}, {100, 30}} {
			name := fmt.Sprintf("apply-screen-%s-%dx%d.golden", tt.name, size[0], size[1])
			t.Run(name, func(t *testing.T) {
				face, out, kb := applyAt(size[0], size[1], took, tt.events())
				defer func() { _ = face.Close() }()
				if tt.keys != nil {
					kb.press(t, tt.keys(screen(out.String()), size[0])...)
				}
				got := screen(out.String())
				golden(t, name, got+"\n")
				ls := lines(got)
				if len(ls) != size[1] {
					t.Errorf("the screen is %d lines, want %d", len(ls), size[1])
				}
				for i, l := range ls {
					if w := ansi.StringWidth(l); w > size[0] {
						t.Errorf("line %d is %d wide, wider than the window, %d", i, w, size[0])
					}
				}
				if rule := []rune(ls[headLines-1]); len(rule) != size[0] || rule[split(size[0])] != '┬' || strings.Trim(string(rule), "─┬") != "" {
					t.Errorf("under the header is %q; want a rule across, ┬ at column %d", ls[headLines-1], split(size[0]))
				}
				if !strings.Contains(out.String(), "\x1b[?1049h\x1b[?25l") {
					t.Error("applying didn't take the alternate screen, its cursor hidden")
				}
			})
		}
	}
}

// The screen's header is the one the arrival lands on: a blank line, then
// the wordmark two cells in, what runs, the Mac and when beside it, so the
// hand-over moves nothing; the bar and how the run stands fill in under
// that line.
func TestApplyingScreenTakesOverFromTheArrival(t *testing.T) {
	face, out, _ := applyAt(160, 64, time.Second, sixMinutesIn()[:3])
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
}

// Each of kit's steps is in the row it says, as a kind's does, or one of its
// own or its area's, never an app's; a step of the user's own is in Your
// steps whatever it's called; a step by hand in none; a step kit doesn't
// know falls back on its area.
func TestApplyingRowsByKitsOwnStructure(t *testing.T) {
	for _, tt := range []struct {
		step  event.Step
		row   string
		known bool
	}{
		{event.Step{Name: "boot-github", Area: boot.AreaBooted}, "This Mac", true},
		{event.Step{Name: "full-disk-access", Area: "Backups"}, "This Mac", true},
		{event.Step{Name: "oh-my-zsh", Area: "Drift"}, "Files and shell", true},
		{event.Step{Name: "oh-my-zsh", Area: "Steps"}, "Your steps", true},
		{event.Step{Name: "secret", Area: "Drift", Row: "Secrets"}, "Secrets", true},
		{event.Step{Name: "a-new-kind", Area: "Drift", Row: "Developer tools"}, "Developer tools", true},
		{event.Step{Name: "oh-my-zsh", Area: "Steps", Row: "Developer tools"}, "Your steps", true},
		{event.Step{Name: "config-private", Area: "Config"}, "Git", true},
		{event.Step{Name: "checks", Area: "Checks"}, "Your steps", true},
		{event.Step{Name: "scratch", Area: "Mac"}, "Backups", true},
		{event.Step{Name: "nightly", Area: "Jobs"}, "Backups", true},
		{event.Step{Name: "manual", Area: "Manual"}, "", true},
		{event.Step{Name: "1password", Area: "Manual"}, "", true},
		{event.Step{Name: "something-new", Area: boot.AreaBooted}, "This Mac", false},
		{event.Step{Name: "something-new", Area: "Mac"}, "This Mac", false},
		{event.Step{Name: "something-new", Area: "Backups"}, "Backups", false},
		{event.Step{Name: "hourly:asimov", Area: "Jobs"}, "Backups", false},
		{event.Step{Name: "something-new", Area: "Config"}, "Git", false},
		{event.Step{Name: "something-new", Area: "Drift"}, "This Mac", false},
	} {
		if row, known := render.RowOf(tt.step); row != tt.row || known != tt.known {
			t.Errorf("RowOf(%s in %s) = %q, known %v; want %q, known %v", tt.step.Name, tt.step.Area, row, known, tt.row, tt.known)
		}
	}
}

// rowsRun is a run of steps, the steps by name in rows of their own, and
// events after it started, at a screen 160 by 64.
func rowsRun(steps []event.Step, events ...event.Event) []event.Event {
	return append([]event.Event{event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: steps}}, events...)
}

// What a row says is its steps taken together, the first rule that holds:
// one failed, why, or how many did; a card up for a step by hand holding it
// up, that it waits for you; one at work, what it's doing and how far it's
// got, a bar under it when it counts; some waiting, what for, how many are
// done first, the one waiting named unless it's named as its row is; all
// finished, how many things are to do at the end; all well, what
// its step did, or that all are done; none started, that it's waiting.
// Each in its state's mark and colour.
func TestApplyingRowSays(t *testing.T) {
	tools := []event.Step{
		{Name: "homebrew", Title: "Homebrew", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"},
		{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Waits: []string{"homebrew"}},
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"1password"}},
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "arq", Title: "Arq", Area: "Backups"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
		{Name: "mise", Title: "mise", Area: "Steps", Waits: []string{"brew"}},
		{Name: "valet", Title: "valet", Area: "Steps"},
		{Name: "1password", Title: "1password", Area: "Manual"},
	}
	toDo := func(name string) check.Result {
		return check.Result{State: check.Attention, Summary: "to do", Items: []check.Item{{ID: "x:" + name, Name: name, State: check.Problem}}}
	}
	for _, tt := range []struct {
		name, row, want string
		events          []event.Event
		mark            string
		under           string
	}{
		{"one failed", "Homebrew formulae", "  ✗ Homebrew formulae  couldn't install jq", []event.Event{
			finished(3, "brew", check.Result{State: check.Failed, Reason: "couldn't install jq: it broke"})}, look.Red("✗"), ""},
		{"two failed", "Homebrew formulae", "  ✗ Homebrew formulae  2 failed", []event.Event{
			finished(2, "homebrew", check.Result{State: check.Failed, Reason: "couldn't install Homebrew"}),
			finished(3, "brew", check.Result{State: check.Failed, Reason: "couldn't install jq"})}, look.Red("✗"), ""},
		{"needs you", "Secrets", "  ▲ Secrets  waits for you: Sign in to 1Password", []event.Event{
			event.StepStarted{Time: after(2), Step: "1password", Doing: "applying"},
			event.NeedsYou{Time: after(2), Step: "1password", Item: "1password", Title: "Sign in to 1Password"}}, look.Orange("▲"), ""},
		{"at work", "Homebrew formulae", "  ◐ Homebrew formulae  installing jq · 67 of 129", append(applying(10, "brew"),
			event.Doing{Time: after(20), Step: "brew", Says: "installing jq", Done: 67, Of: 129}), look.Cyan("◐"), "    ▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮  50.0s"},
		{"at work, a step of several", "Your steps", "  ◐ Your steps  applying valet", applying(10, "valet"), look.Cyan("◐"), ""},
		{"waiting, some done", "Your steps", "  ○ Your steps  1 of 3 · mise waits for Homebrew formulae", []event.Event{fine(2, "fonts", "16 installed")}, look.Cyan("○"), ""},
		{"waiting for a thing, at work no more", "Homebrew formulae", "  ○ Homebrew formulae  waits for TOKEN: 2 to install", append(applying(10, "brew"),
			event.Doing{Time: after(20), Step: "brew", Says: "waiting for TOKEN: 2 to install"}), look.Cyan("○"), ""},
		{"waiting, one step", "Secrets", "  ○ Secrets  waits for 1password", nil, look.Cyan("○"), ""},
		{"waiting, some done, one named as its row", "Homebrew formulae", "  ○ Homebrew formulae  1 of 2 · waits for Homebrew casks", append([]event.Event{fine(2, "homebrew", "/opt/homebrew")},
			append(applying(10, "brew"), event.Doing{Time: after(20), Step: "brew", Says: "waiting for Homebrew casks"})...), look.Cyan("○"), ""},
		{"to do at the end", "Backups", "  △ Backups  2 to do at the end", []event.Event{
			finished(2, "time-machine", toDo("Add a Time Machine disk")), finished(3, "arq", toDo("Choose Arq's backup plan"))}, look.White("△"), ""},
		{"done, one step", "Secrets", "  ● Secrets  24 synced", []event.Event{fine(2, "secret", "24 synced")}, look.Pink("●"), ""},
		{"done, several", "Your steps", "  ● Your steps  all 3 done", []event.Event{
			fine(2, "fonts", "16 installed"), fine(3, "mise", "fine"), fine(4, "valet", "fine")}, look.Pink("●"), ""},
		{"not started", "Your steps", "  ○ Your steps  waiting", nil, look.Cyan("○"), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			events := tt.events
			if tt.name == "not started" {
				events = nil
				tools = slices.DeleteFunc(slices.Clone(tools), func(s event.Step) bool { return s.Name == "mise" })
			}
			face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, rowsRun(tools, events...))
			defer func() { _ = face.Close() }()
			got := screen(uncoloured(out.String()))
			if line := rowLine(t, got, 160, tt.row); !strings.HasPrefix(line, tt.want) || len(line) > len(tt.want) && !strings.HasPrefix(line[len(tt.want):], " ") && line != tt.want {
				t.Errorf("the row is %q; want %q", line, tt.want)
			}
			frame := strings.Join(lastFrame(out.String()), "\n")
			if !strings.Contains(frame, "  "+tt.mark+" ") {
				t.Errorf("the row's mark isn't %q", ansi.Strip(tt.mark))
			}
			if tt.under != "" {
				left, _ := panes(got, 160)
				if at := rowAt(got, 160, tt.row) - headLines; left[at+1] != tt.under {
					t.Errorf("under the row is %q; want its bar and how long it's been at work, %q", left[at+1], tt.under)
				}
			}
		})
	}
}

// The bar is weighted by work: each step one, a step finished one, a step
// at work that counts as far as it's got; the header says the same
// percentage, and so does the terminal's tab.
func TestApplyingBarIsWeighted(t *testing.T) {
	steps := []event.Step{{Name: "a", Title: "a", Area: "Steps"}, {Name: "b", Title: "b", Area: "Steps"}, {Name: "c", Title: "c", Area: "Steps"}, {Name: "d", Title: "d", Area: "Steps"}}
	var out syncBuffer
	c := &clock{t: at}
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).
		Sized(func() (int, int) { return 160, 64 }).Clocked(c.now).ShowTabProgress()
	defer func() { _ = face.Close() }()
	for _, e := range rowsRun(steps, fine(1, "a", "fine"), event.StepStarted{Step: "b", Doing: "applying"}, event.Doing{Step: "b", Says: "installing", Done: 1, Of: 4}) {
		face.Emit(e)
	}
	time.Sleep(300 * time.Millisecond)
	head := lines(screen(out.String()))[2]
	if want := "  ▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮  31% · 0.0s so far"; !strings.HasSuffix(head, want) {
		t.Errorf("one of four done, one a quarter of the way, the header's bar is %q; want it to end %q", head, want)
	}
	if !strings.Contains(out.String(), "\x1b]9;4;1;31\x07") {
		t.Error("the tab's progress isn't 31%")
	}
}

// There's no later during the run (Lee, 16 Oct): what's left to do waits
// for its end, counted neither in the state line nor listed on the left.
func TestApplyingHasNoLater(t *testing.T) {
	face, out, _ := applyAt(160, 64, time.Minute, sixMinutesIn())
	defer func() { _ = face.Close() }()
	got := screen(out.String())
	if strings.Contains(got, "FOR LATER") || strings.Contains(got, "for later") {
		t.Errorf("the screen says for later\n%s", got)
	}
	if state := lines(got)[3]; !strings.HasSuffix(state, "│  ◐ working · nothing needs you") {
		t.Errorf("the state line is %q; want working, nothing needs you, and nothing more", state)
	}
}

// The log: every line of the run, oldest first, its time and a mark: what
// a kind installed, dim +; a step finished well, pink, and what it did; one
// left to do, white △; one failed, red, why and what to run on the spine
// under it; one not checked, grey; a card up, orange ▲, and done with, how
// long it was up, or left to do. Steps starting and their checks have no
// line. What's under way is at its foot, the time now on its first line.
func TestApplyingLog(t *testing.T) {
	events := append(casksFailed(),
		event.StepStarted{Time: after(360), Step: "1password", Doing: "applying"},
		event.NeedsYou{Time: after(360), Step: "1password", Item: "1password", Title: "Sign in to 1Password"},
		finished(400, "1password", check.Result{State: check.OK, Summary: "done"}),
	)
	face, out, _ := applyAt(160, 64, 7*time.Minute, events)
	defer func() { _ = face.Close() }()
	log := logOf(screen(out.String()), 160)
	for _, want := range []string{
		"  03:04:06  ● GitHub: fine",
		"  03:04:10  △ Time Machine: no backup disk",
		"  03:04:26  ● fonts: 16 installed",
		"  03:04:36  + Installed cask-1",
		"  03:04:58  + Installed formula-1",
		"  03:10:01  ✗ Homebrew casks failed: couldn't install tailscale-app\n" +
			"            │ installer: The install failed. (The Installer encountered an error that caused the i…\n" +
			"            │ → kit apply cask",
		"  03:10:02  – macOS login items: not checked · needs Homebrew casks",
		"  03:10:05  ▲ Sign in to 1Password: waiting for you",
		"  03:10:45  ● Sign in to 1Password: done, in 40.0s",
		"  03:11:05  ◐ Installing ffmpeg · 38 of 129\n            ◐ Applying claude-code",
	} {
		if !strings.Contains(strings.Join(log, "\n"), want) {
			t.Errorf("the log is\n%s\nwant it to hold\n%s", strings.Join(log, "\n"), want)
		}
	}
	for _, l := range log {
		if strings.Contains(l, "checking") {
			t.Errorf("the log has a line for a step starting or checking: %q", l)
		}
	}
	if first, last := slices.Index(log, "  03:04:06  ● GitHub: fine"), slices.IndexFunc(log, func(l string) bool { return strings.Contains(l, "Sign in to 1Password: done") }); first < 0 || last < first {
		t.Error("the log isn't oldest first")
	}
	if slices.ContainsFunc(log, func(l string) bool { return strings.Contains(l, "1password") }) {
		t.Error("the log says a step by hand's end twice: its card's line, and its step's by its line's name")
	}
}

// A step by hand's card found not done after all goes up again, as signing
// in to the App Store does once the App Store's first install finds no one
// signed in: the row it holds up waits for you again, the panel's back, its
// time from now, and the log says it waits for you once more, then done, how
// long it was up the second time; its step finishing again has no line of
// its own.
func TestApplyingCardUpAgain(t *testing.T) {
	steps := []event.Step{
		{Name: "appstore", Title: "appstore", Area: "Manual", ByHand: []event.Hand{{Name: "appstore", Title: "Sign in to the App Store"}}},
		{Name: "mas", Title: "App Store apps", Area: "Drift", Row: "App Store apps", Waits: []string{"appstore"}},
	}
	card := event.NeedsYou{Step: "appstore", Item: "appstore", Title: "Sign in to the App Store", Steps: []string{"Open the App Store", "Store › Sign In, with your Apple Account"}}
	first, second := card, card
	first.Time, second.Time = after(10), after(60)
	events := append(rowsRun(steps, event.StepStarted{Time: after(10), Step: "appstore", Doing: "applying"}, first, fine(30, "appstore", "done")), applying(31, "mas")...)
	events = append(events, event.Doing{Time: after(60), Step: "mas", Says: "waiting for Sign in to the App Store"}, second)
	face, out, _ := applyAt(160, 64, 70*time.Second, events)
	got := screen(out.String())
	if line := rowLine(t, got, 160, "App Store apps"); !strings.HasPrefix(line, "  ▲ App Store apps  waits for you: Sign in to the App Store") {
		t.Errorf("with the card up again, the row is %q; want it waiting for you", line)
	}
	if _, right := panes(got, 160); !strings.HasPrefix(right[1], "  ▲ Sign in to the App Store") || !strings.HasSuffix(right[1], "waiting 10.0s") {
		t.Errorf("with the card up again, the right pane is\n%s\nwant its panel, waiting from when it went up again", strings.Join(right, "\n"))
	}
	_ = face.Close()

	events = append(events,
		event.StepStarted{Time: after(105), Step: "appstore", Doing: "checking"},
		fine(105, "appstore", "done"),
		event.Doing{Time: after(105), Step: "mas", Says: "applying"},
		fine(120, "mas", "3 installed"),
	)
	face, out, _ = applyAt(160, 64, 2*time.Minute, events)
	defer func() { _ = face.Close() }()
	want := []string{
		"  03:04:15  ▲ Sign in to the App Store: waiting for you",
		"  03:04:35  ● Sign in to the App Store: done, in 20.0s",
		"  03:05:05  ▲ Sign in to the App Store: waiting for you",
		"  03:05:50  ● Sign in to the App Store: done, in 45.0s",
		"  03:06:05  ● App Store apps: 3 installed",
	}
	if log := logOf(screen(out.String()), 160); !slices.Equal(log, want) {
		t.Errorf("the log is\n%s\nwant\n%s", strings.Join(log, "\n"), strings.Join(want, "\n"))
	}
}

// A row chosen, the log is its lines alone, a step by hand's in the row of
// the step it holds up, what's under way its own; the steps by hand at the
// end are in no row, shown with everything alone.
func TestApplyingLogOfARow(t *testing.T) {
	events := append(sixMinutesIn(),
		event.StepStarted{Time: after(360), Step: "1password", Doing: "applying"},
		event.NeedsYou{Time: after(360), Step: "1password", Item: "1password", Title: "Sign in to 1Password"},
		finished(400, "1password", check.Result{State: check.OK, Summary: "done"}),
		fine(401, "secret", "24 synced"),
		event.StepStarted{Time: after(402), Step: "manual", Doing: "applying"},
		event.NeedsYou{Time: after(402), Step: "manual", Item: "editor", Title: "Install the editor from its site"},
	)
	face, out, kb := applyAt(160, 64, 7*time.Minute, events)
	defer func() { _ = face.Close() }()
	kb.press(t, clickRow("Secrets")(screen(out.String()), 160)...)
	got := screen(out.String())
	_, right := panes(got, 160)
	if !slices.Contains(right, "  LOG · SECRETS") {
		t.Errorf("with Secrets chosen, the right pane is\n%s\nwant its heading", strings.Join(right, "\n"))
	}
	want := []string{
		"  03:10:05  ▲ Sign in to 1Password: waiting for you",
		"  03:10:45  ● Sign in to 1Password: done, in 40.0s",
		"  03:10:46  ● Secrets: 24 synced",
	}
	if log := logOf(got, 160); !slices.Equal(log, want) {
		t.Errorf("with Secrets chosen, the log is\n%s\nwant\n%s", strings.Join(log, "\n"), strings.Join(want, "\n"))
	}
	kb.press(t, clickRow("Homebrew formulae")(screen(out.String()), 160)...)
	log := logOf(screen(out.String()), 160)
	if len(log) == 0 || log[len(log)-1] != "  03:11:05  ◐ Installing ffmpeg · 38 of 129" || slices.ContainsFunc(log, func(l string) bool { return strings.Contains(l, "cask") }) {
		t.Errorf("with Homebrew formulae chosen, the log is\n%s\nwant its lines alone, what it's doing at its foot", strings.Join(log, "\n"))
	}
	kb.press(t, keyEsc)
	if log := strings.Join(logOf(screen(out.String()), 160), "\n"); !strings.Contains(log, "Install the editor from its site: waiting for you") {
		t.Errorf("with everything, the log is\n%s\nwant the steps by hand at the end with it", log)
	}
}

// While a card's up, it's a panel over the log, ruled off: its title,
// orange, how long it's waited at the right, how many more are after it;
// why it's there, muted and wrapped, then why the run waits for it; its
// steps, numbered, a subheading muted and unnumbered after a blank line,
// the numbering carrying on across it; that kit carries on by itself, when
// it tells, and no key to answer it. The header says the run's waiting for
// you; the row it holds up does too. The card done, the panel goes.
func TestApplyingPanel(t *testing.T) {
	face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, 6*time.Minute+12*time.Second, waitingOnYou())
	defer func() { _ = face.Close() }()
	got := screen(uncoloured(out.String()))
	_, right := panes(got, 160)
	want := []string{
		"",
		"  ▲ Sign in to 1Password" + strings.Repeat(" ", 60) + "waiting 27.0s",
		"",
		"      Every secret kit puts in place comes from 1Password, so it needs the app signed in and its",
		"      command line turned on.",
		"      The run waits for this, for Secrets.",
		"",
		"      1  Open 1Password",
		"      2  Scan the QR code it shows with the 1Password app on your phone",
		"",
		"      If it doesn't ask, in 1Password:",
		"      3  Settings › Developer › turn on Integrate with 1Password CLI",
		"",
		"      kit carries on by itself once it's done",
		"",
		"───────────────────────────────────────────────────────────────────────────────────────────────────",
		"",
		"  LOG",
	}
	if !slices.Equal(right[:len(want)], want) {
		t.Errorf("the right pane starts\n%s\nwant\n%s", strings.Join(right[:len(want)], "\n"), strings.Join(want, "\n"))
	}
	if rule := lines(got)[headLines+15]; !strings.Contains(rule, "├───") {
		t.Errorf("the panel's rule is %q; want it to join the rule between the panes", rule)
	}
	if state := lines(got)[3]; !strings.HasSuffix(state, "│  ▲ waiting for you · Sign in to 1Password") {
		t.Errorf("the state line is %q; want it waiting for you, the card's title", state)
	}
	frame := strings.Join(lastFrame(out.String()), "\n")
	for _, styled := range []string{look.Orange("▲") + " " + look.Urgent("Sign in to 1Password"), look.Urgent("1") + "  ", look.Muted("command line turned on."), look.Muted("If it doesn't ask, in 1Password:"), look.Orange("▲ waiting for you")} {
		if !strings.Contains(frame, styled) {
			t.Errorf("the screen has no %q, styled as it should be", ansi.Strip(styled))
		}
	}
	face.Emit(finished(400, "1password", check.Result{State: check.OK, Summary: "done"}))
	time.Sleep(300 * time.Millisecond)
	got = screen(uncoloured(out.String()))
	if _, right := panes(got, 160); right[1] != "  LOG" || strings.Contains(lines(got)[3], "waiting for you") {
		t.Errorf("the card done, the screen is\n%s\nwant the panel gone, the log with the whole side", got)
	}
}

// A card's panel says why the run waits for it: the steps it holds up; the
// steps by hand at the end, that the rest is done; one nothing waits for,
// that the run carries on. More after it, it says how many, by how long
// it's waited. One kit can't tell is done offers space.
func TestApplyingPanelSaysWhy(t *testing.T) {
	for _, tt := range []struct {
		card       event.NeedsYou
		why, title string
		keys       []string
	}{
		{event.NeedsYou{Time: after(360), Step: "manual", Item: "karabiner", Title: "Allow Karabiner-Elements", More: 2},
			"      The rest is done: this is what's left.", "2 more after this · waiting 12.0s", []string{"      space when it's done"}},
		{event.NeedsYou{Time: after(360), Step: "1password", Item: "1password", Title: "Sign in to 1Password", Tells: true},
			"      The run waits for this, for Secrets.", "waiting 12.0s", []string{"      kit carries on by itself once it's done"}},
	} {
		steps := slices.Clone(newMac)
		face, out, _ := applyAt(160, 64, 6*time.Minute+12*time.Second, append(rowsRun(steps, event.StepStarted{Time: after(360), Step: tt.card.Step, Doing: "applying"}), tt.card))
		_, right := panes(screen(out.String()), 160)
		if !strings.HasSuffix(right[1], tt.title) || right[3] != tt.why || !slices.Equal(right[5:5+len(tt.keys)], tt.keys) {
			t.Errorf("%s's panel is\n%s\nwant it to say %q, %q, then %q", tt.card.Title, strings.Join(right[:10], "\n"), tt.title, tt.why, tt.keys)
		}
		_ = face.Close()
	}
	steps := []event.Step{{Name: "cask", Title: "Homebrew casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew"}, {Name: "switchboard", Title: "switchboard", Area: "Manual"}}
	face, out, _ := applyAt(160, 64, time.Minute, rowsRun(steps, event.StepStarted{Step: "switchboard", Doing: "applying"},
		event.NeedsYou{Step: "switchboard", Item: "switchboard", Title: "Set up switchboard"}))
	defer func() { _ = face.Close() }()
	if _, right := panes(screen(out.String()), 160); right[3] != "      Nothing waits for this: the rest of the run carries on meanwhile." {
		t.Errorf("a card nothing waits for says %q", right[3])
	}
	steps = []event.Step{{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"1password"}},
		{Name: "1password", Title: "1password", Area: "Manual"}, {Name: "manual", Title: "By hand", Area: "Manual", Waits: []string{"secret", "1password"}}}
	held, out, _ := applyAt(160, 64, time.Minute, rowsRun(steps, event.StepStarted{Step: "1password", Doing: "applying"},
		event.NeedsYou{Step: "1password", Item: "1password", Title: "Sign in to 1Password"}))
	defer func() { _ = held.Close() }()
	if _, right := panes(screen(out.String()), 160); right[3] != "      The run waits for this, for Secrets." {
		t.Errorf("a card the steps by hand at the end wait for too says %q; want them left out", right[3])
	}
}

// The state line says the run's working, else what it waits on you for,
// macOS asking among it, where to answer; then what failed, and what's for
// later.
func TestApplyingStateLine(t *testing.T) {
	face, out, _ := applyAt(160, 64, time.Minute, append(casksFailed(), event.Asking{Time: after(358), Says: "asking for an administrator's password"}))
	defer func() { _ = face.Close() }()
	if state := lines(screen(out.String()))[3]; !strings.HasSuffix(state, "│  ▲ waiting for you · macOS asking for an administrator's password · in its window · 1 failed") {
		t.Errorf("macOS asking, the state line is %q", state)
	}
	face.Emit(event.Asking{Time: after(359)})
	time.Sleep(300 * time.Millisecond)
	if state := lines(screen(out.String()))[3]; !strings.HasSuffix(state, "│  ◐ working · nothing needs you · 1 failed") {
		t.Errorf("macOS answered, the state line is %q", state)
	}
}

// A window too small for the screen keeps the run in place under its
// heading, as before, the rows' lights at its top; the screen takes a
// window 100 by 30 or more.
func TestApplyingScreenNeedsTheRoom(t *testing.T) {
	for _, tt := range []struct {
		w, h   int
		screen bool
	}{{99, 64, false}, {100, 64, true}, {160, 29, false}, {160, 30, true}, {100, 30, true}} {
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
	got := turned(screen(out.String()))
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	golden(t, "apply-small.golden", got+"\n")
	for _, want := range []string{"  ● THIS MAC 6  ● FILES AND SHELL 2  ● MAC SETTINGS 3  ○ SECRETS\n  ◐ HOMEBREW FORMULAE", "  ◐ YOUR STEPS  ○ BACKUPS\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("in a small window, the screen is\n%s\nwant the rows' lights, as the screen's rows stand\n%s", got, want)
		}
	}
}

// In a window too small for applying's screen, keys aren't read: a card
// waiting for an answer says a bigger window shows them; one kit tells
// about by itself says it carries on. Why it's there comes under its
// title, before its steps, a subheading among them unnumbered, the
// numbering carrying on across it.
func TestApplyingCardInASmallWindow(t *testing.T) {
	paper := event.NeedsYou{Time: after(360), Step: "manual", Item: "editor", Title: "Install the editor from its site", Steps: []string{"Download it from editor.example.com"}}
	for _, tt := range []struct {
		card  event.NeedsYou
		want  string
		order []string
	}{
		{paper, "a bigger window shows the keys to answer it", nil},
		{signIn, "kit carries on by itself once it's done", []string{
			"Sign in to 1Password", "Every secret kit puts in place", "1  Open 1Password", "2  Scan the QR code",
			"\n       If it doesn't ask, in 1Password:\n       3  Settings › Developer",
		}},
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
		if !inOrder(live, tt.order...) {
			t.Errorf("%s in a small window:\n%s\nwant in order %q", tt.card.Title, live, tt.order)
		}
		if err := small.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// When the run ends, the screen goes and its record is left in its place,
// kept to column 80: the wordmark, with what ran, the bar and the tally
// beside it; then what's to do, what failed, and the rows done. A run
// stopped says so, and where to carry on; nothing's red but what failed.
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
			if strings.Count(got, "█  ▄▀") != 1 {
				t.Errorf("the record is\n%s\nwant the wordmark once: the record's head in the heading's place", got)
			}
		})
	}
}

// The record's sections: what's to do, numbered, each with how, a step by
// hand's from its card; what failed, a row each, named by its row and its
// step when the row has others, why and what to run under it; the rows
// whose steps were all well, saying what they did. The tally counts to-do
// items, steps failed and rows fine, white, red and white; the bar's last
// cell red.
func TestApplyingRecordSections(t *testing.T) {
	face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, appliedRun())
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	record := screen(uncoloured(out.String()))
	for _, want := range []string{
		"│  bootstrap · laptop · Fri 2 Jan · 03:04\n",
		"│  3 to do · 1 failed · 9 fine · 24m31s\n",
		"\n\n  TO DO  3 · kit apply brings them back\n" +
			"  △ 1  Add a Time Machine disk\n" +
			"       → System Settings › General › Time Machine › Add Backup Disk\n" +
			"  △ 2  Allow remote users Full Disk Access\n" +
			"       → then: kit manual done sharing\n" +
			"  △ 3  Install the editor from its site\n" +
			"       → Download it from editor.example.com\n" +
			"       Once it's downloaded, in Finder:\n" +
			"       → Drag it to Applications\n\n",
		"\n  FAILED  1\n  ✗ Homebrew casks  couldn't install tailscale-app\n  │ installer: The install failed. (The Installer encountered an error that cau…\n  │ → kit apply cask\n\n",
		"\n  DONE  9\n  ● This Mac  all 6 done\n  ● Files and shell  all 2 done\n  ● Mac settings  all 3 done\n  ● Secrets  fine\n  ● Homebrew formulae  all 2 done\n  ● App Store apps  fine\n",
	} {
		if !strings.Contains(record, want) {
			t.Errorf("the record is\n%s\nwant it to hold\n%s", record, want)
		}
	}
	raw := out.String()
	raw = raw[strings.LastIndex(raw, "\x1b[?1049l"):]
	for _, styled := range []string{look.White("3 to do"), look.Red("1 failed"), look.White("9 fine"), look.Red("▮")} {
		if !strings.Contains(raw, styled) {
			t.Errorf("the record has no %q, styled as it should be", ansi.Strip(styled))
		}
	}
}

// The record names a step by its row alone when the step's called as its
// row is, and a row done keeps under it what of its steps is still waiting,
// a password say, in the log too.
func TestApplyingRecordKeepsWhatWaits(t *testing.T) {
	events := rowsRun(newMac[4:13],
		fine(10, "homebrew", "4.6.1"),
		finished(20, "brew", check.Result{State: check.Attention, Summary: "3 declared, all installed", Items: []check.Item{
			{ID: "brew:ffmpeg", Name: "ffmpeg", State: "extra", Detail: "installed, not declared"}}}),
		finished(30, "mas", check.Result{State: check.OK, Summary: "2 declared, 1 installed", Items: []check.Item{
			{ID: "mas:bear", Name: "bear", State: "missing", Quiet: "new", Detail: "needs an administrator's password"}}}),
	)
	face, out, _ := applyAt(160, 64, time.Minute, events)
	if log := strings.Join(logOf(screen(out.String()), 160), "\n"); !strings.Contains(log, "● App Store apps: 2 declared, 1 installed\n            │ bear · needs an administrator's password") {
		t.Errorf("the log is\n%s\nwant Bear waiting under the App Store's line", log)
	}
	face.Emit(event.RunFinished{Time: after(40), Duration: 40 * time.Second, Counts: map[check.State]int{check.OK: 2, check.Attention: 1}})
	if err := face.Close(); err != nil {
		t.Fatal(err)
	}
	record := screen(out.String())
	for _, want := range []string{
		"\n  NEEDS YOU  1\n  ▲ Homebrew formulae  3 declared, all installed\n  │ ffmpeg · ",
		"\n  ● App Store apps  2 declared, 1 installed\n  │ bear · needs an administrator's password\n",
	} {
		if !strings.Contains(record, want) {
			t.Errorf("the record is\n%s\nwant it to hold\n%s", record, want)
		}
	}
}

// However applying's screen is left, the terminal's own screen comes back,
// the cursor shown, the mouse and focus no longer reported, and its keys
// stop being read, the terminal out of its raw mode, as the question needs
// it: for a question asked mid-run, and back after it; when the window's
// made too small for it, the run drawn in place, and taken again when it's
// big enough; when the face closes without the run finishing.
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
	onScreen := func() bool { return strings.Contains(screen(out.String()), "≡") }
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
	if got := turned(screen(out.String())); onScreen() || mouse() || !mouseOffFirst(kb) || kb.isRaw() || !strings.Contains(got, "◐ Homebrew formulae  installing ffmpeg") {
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

// A step finished says what it did once, in its own verb: a kind's things
// installed, set or synced, a few named, or counted, and its summary only
// when that says more than that all it declares is so; a thing named as the
// step itself isn't named again; problems fixed are counted; a step of the
// user's own that says what it did isn't said to be applied too. More than
// a few things alike under it are one line, counted.
func TestApplyingLogSaysWhatEachDid(t *testing.T) {
	steps := []event.Step{
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Part: "Secrets", Did: "synced"},
		{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings", Part: "Settings", Did: "set"},
		{Name: "file-sharing", Title: "File Sharing", Area: "Drift", Part: "Settings"},
		{Name: "scratch", Title: "Scratch volume", Area: "Mac"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
		{Name: "claude-mcp", Title: "Claude MCP", Area: "Drift", Row: "Developer tools", Part: "Packages", Did: "installed"},
	}
	install := func(names ...string) []check.Item {
		var items []check.Item
		for _, n := range names {
			items = append(items, check.Item{ID: "x:" + n, Name: n, Action: "install"})
		}
		return items
	}
	var waiting []check.Item
	for _, n := range []string{"a", "b", "c", "d"} {
		waiting = append(waiting, check.Item{ID: "claude-mcp:" + n, Name: n, State: "missing", Detail: "no project folder yet"})
	}
	face, out, _ := applyAt(160, 64, time.Minute, rowsRun(steps,
		finished(2, "secret", check.Result{State: check.OK, Summary: "24 declared, all synced", Done: install("A", "B", "C", "D")}),
		finished(3, "defaults", check.Result{State: check.OK, Summary: "99 declared, all set", Done: install("one", "two")}),
		finished(4, "file-sharing", check.Result{State: check.OK, Summary: "on", Done: install("File Sharing")}),
		finished(5, "scratch", check.Result{State: check.OK, Summary: "mounted", Done: []check.Item{{ID: "scratch:x", Name: "it isn't mounted", Action: "fix"}}}),
		finished(6, "fonts", check.Result{State: check.OK, Summary: "16 fonts installed", Done: []check.Item{{ID: "fonts", Name: "Operator Mono", Action: "run"}}}),
		finished(7, "claude-mcp", check.Result{State: check.OK, Summary: "19 declared, 8 installed", Done: install("dex"), Items: waiting}),
	))
	defer func() { _ = face.Close() }()
	log := strings.Join(logOf(screen(out.String()), 160), "\n")
	for _, want := range []string{
		"  03:04:07  ● Secrets: synced 4\n",
		"  03:04:08  ● macOS settings: set one, two\n",
		"  03:04:09  ● File Sharing: on\n",
		"  03:04:10  ● Scratch volume: fixed 1 problem · mounted\n",
		"  03:04:11  ● fonts: 16 fonts installed\n",
		"  03:04:12  ● Claude MCP: installed dex · 19 declared, 8 installed\n",
		"│ 4 declared, not installed · no project folder yet",
	} {
		if !strings.Contains(log+"\n", want) {
			t.Errorf("the log is\n%s\nwant %q", log, want)
		}
	}
}

// Steps can wait for each other partway through applying, which the run
// allows: the screen follows what a step waits for no further than a step
// it's met on the way, both left not yet with nothing to do behind them;
// and a step deferred behind steps that left only things to do is to do,
// however many ways lead to them.
func TestApplyingStepsWaitingForEachOther(t *testing.T) {
	deferred := check.Result{State: check.Deferred, Reason: "needs a secret"}
	toDo := check.Result{State: check.Attention, Summary: "to do", Items: []check.Item{{ID: "x:disk", Name: "Add a disk", State: check.Problem}}}
	steps := []event.Step{
		{Name: "e", Title: "e", Area: "Backups"},
		{Name: "d", Title: "d", Area: "Backups", Waits: []string{"e"}},
		{Name: "c", Title: "c", Area: "Backups", Waits: []string{"d"}},
		{Name: "b", Title: "b", Area: "Backups", Waits: []string{"d"}},
		{Name: "a", Title: "a", Area: "Backups", Waits: []string{"b", "c"}},
		{Name: "cask", Title: "Homebrew casks", Area: "Drift", Row: "Homebrew casks", Waits: []string{"mystep"}},
		{Name: "mystep", Title: "mystep", Area: "Steps", Waits: []string{"cask"}},
	}
	var events []event.Event
	for i, s := range steps {
		r := deferred
		if s.Name == "e" {
			r = toDo
		}
		events = append(events, finished(i+1, s.Name, r))
	}
	face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, rowsRun(steps, events...))
	defer func() { _ = face.Close() }()
	got := screen(uncoloured(out.String()))
	for row, want := range map[string]string{
		"Homebrew casks": "  – Homebrew casks  not checked · needs a secret",
		"Your steps":     "  – Your steps  not checked · needs a secret",
		"Backups":        "  △ Backups  1 to do at the end",
	} {
		if line := rowLine(t, got, 160, row); !strings.HasPrefix(line, want) {
			t.Errorf("the row is %q; want %q", line, want)
		}
	}
	_, log := panes(got, 160)
	for _, step := range []string{"a", "b", "c", "d"} {
		if !slices.ContainsFunc(log, func(l string) bool { return strings.Contains(l, "△ "+step+":") }) {
			t.Errorf("the log has no %s to do at the end:\n%s", step, strings.Join(log, "\n"))
		}
	}
}
