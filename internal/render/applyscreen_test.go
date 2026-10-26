package render_test

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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
	"github.com/leeovery/kit/internal/hand"
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

// cardsWaiting is the run once 1Password's sign-in has come up with
// another card waiting after it.
func cardsWaiting() []event.Event {
	card := signIn
	card.More = 1
	return cardUp(card)
}

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

// waitingRun is a run all well whose rows have things waiting that don't
// need the person: Claude's servers for projects whose folders aren't
// there yet, and login items for apps not installed.
func waitingRun() []event.Event {
	all := append(slices.Clone(newMac), event.Step{Name: "claude-mcp", Title: "Claude MCP", Area: "Drift", Row: "Developer tools"})
	waiting := func(step, detail string, names ...string) []check.Item {
		var items []check.Item
		for _, n := range names {
			items = append(items, check.Item{ID: step + ":" + n, Name: n, State: "missing", Quiet: "new", Detail: detail})
		}
		return items
	}
	events := []event.Event{event.RunStarted{Time: at, Command: "apply", Machine: "laptop", Steps: all}}
	for i, s := range all {
		res := check.Result{State: check.OK, Summary: "fine"}
		switch s.Name {
		case "claude-mcp":
			res.Summary, res.Items = "19 declared, 8 installed", waiting("claude-mcp", "no project folder yet", numbered("~/Code/site", 11)...)
		case "login-item":
			res.Summary, res.Items = "5 declared, 2 installed", waiting("login-item", "not installed: install the app, or open the app it comes with", "Tool", "Monitor", "Clipper")
		}
		events = append(events, finished(10+i, s.Name, res))
	}
	return append(events, event.RunFinished{Time: after(60), Duration: time.Minute, Counts: map[check.State]int{check.OK: len(all)}})
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
// following the log, a card up, a card up with another waiting, a row
// chosen, scrolled back, a step
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
		{"cards", cardsWaiting, nil},
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
		{event.Step{Name: "path", Area: "Drift"}, "Files and shell", true},
		{event.Step{Name: "oh-my-zsh", Area: "Steps"}, "Your steps", true},
		{event.Step{Name: "secret", Area: "Drift", Row: "Secrets"}, "Secrets", true},
		{event.Step{Name: "a-new-kind", Area: "Drift", Row: "Developer tools"}, "Developer tools", true},
		{event.Step{Name: "oh-my-zsh", Area: "Steps", Row: "Developer tools"}, "Your steps", true},
		{event.Step{Name: "config-private", Area: "Config"}, "Git", true},
		{event.Step{Name: "checks", Area: "Checks"}, "Your steps", true},
		{event.Step{Name: "prefs", Area: "Backups"}, "Backups", true},
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
// finished, how many things are to do at the end, done's mark, as there's
// nothing to do till then; all well, what
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
		{"to do at the end", "Backups", "  ● Backups  2 to do at the end", []event.Event{
			finished(2, "time-machine", toDo("Add a Time Machine disk")), finished(3, "arq", toDo("Choose Arq's backup plan"))}, look.Pink("●"), ""},
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

// spaceman is a step by hand whose why and steps hold a link, a bare host
// and path, with a version and a path on the Mac that aren't links.
var spaceman = event.NeedsYou{Time: after(360), Step: "manual", Item: "spaceman", Title: "Install Spaceman",
	Why:   "Spaceman isn't in Homebrew: its releases are at github.com/example/spaceman/releases, 1.3.1 the latest.",
	Steps: []string{"Download the latest Spaceman .dmg from github.com/example/spaceman/releases", "Drag Spaceman.app to /Applications"}}

// linkTargets are where the links in what a face wrote go, each time it
// wrote one.
var linkTargets = regexp.MustCompile("\x1b]8;;([^\x07\x1b]+)")

// A link in a card's text is one the terminal opens when it's clicked, its
// text as written, on applying's screen and in a small window alike; it
// takes no room, every line as wide as without it; nothing else is linked.
func TestApplyingCardsLinkTheirLinks(t *testing.T) {
	linked := ansi.SetHyperlink("https://github.com/example/spaceman/releases") + "github.com/example/spaceman/releases" + ansi.ResetHyperlink()
	for _, size := range [][2]int{{160, 64}, {100, 30}, {80, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			face, out, _ := applyAt(size[0], size[1], 6*time.Minute, cardUp(spaceman))
			defer func() { _ = face.Close() }()
			raw := out.String()
			if !strings.Contains(raw, linked) {
				t.Errorf("the card's link isn't one:\n%s", screen(raw))
			}
			targets := linkTargets.FindAllStringSubmatch(raw, -1)
			if len(targets) < 2 {
				t.Errorf("%d links; want the why's and the step's", len(targets))
			}
			for _, m := range targets {
				if m[1] != "https://github.com/example/spaceman/releases" {
					t.Errorf("a link to %q; want only the releases linked", m[1])
				}
			}
			for i, l := range lines(screen(raw)) {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("line %d is %d wide, wider than the window, %d", i, w, size[0])
				}
			}
		})
	}
	var plain bytes.Buffer
	show(t, render.NewPlain(&plain), cardUp(spaceman))
	if !strings.Contains(plain.String(), "github.com/example/spaceman/releases") || strings.Contains(plain.String(), "\x1b]8") {
		t.Errorf("plain, the card is\n%q\nwant its words, no link", plain.String())
	}
}

// A row at work whose words won't fit has what it's doing cut, never how
// far it's got, on the left and at the log's foot alike, chosen or not.
func TestApplyingKeepsTheCountWhole(t *testing.T) {
	tools := []event.Step{{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"}}
	events := rowsRun(tools, append(applying(10, "brew"),
		event.Doing{Time: after(20), Step: "brew", Says: "installing a-formula-whose-name-goes-on-and-on-past-the-pane", Done: 25, Of: 128})...)
	for _, tt := range []struct {
		name string
		keys func(string, int) []ask.Key
	}{{"following", nil}, {"chosen", clickRow("Homebrew formulae")}} {
		t.Run(tt.name, func(t *testing.T) {
			face, out, kb := applyAt(100, 30, time.Minute, events)
			defer func() { _ = face.Close() }()
			if tt.keys != nil {
				kb.press(t, tt.keys(screen(out.String()), 100)...)
			}
			got := screen(out.String())
			if line := rowLine(t, got, 100, "Homebrew formulae"); !strings.HasSuffix(line, "… · 25 of 128") {
				t.Errorf("the row is %q; want what it's doing cut, then 25 of 128", line)
			}
			if log := logOf(got, 100); len(log) == 0 || !strings.HasSuffix(log[len(log)-1], "… · 25 of 128") {
				t.Errorf("the log ends %q; want what's under way cut, then 25 of 128", log)
			}
		})
	}
}

// Without weights, as an older run's log has none, the bar counts each
// step one: a step finished one, a step at work that counts as far as it's
// got; the header says the same percentage, and so does the terminal's tab.
func TestApplyingBarWithoutWeights(t *testing.T) {
	steps := []event.Step{{Name: "a", Title: "a", Area: "Steps"}, {Name: "b", Title: "b", Area: "Steps"}, {Name: "c", Title: "c", Area: "Steps"}, {Name: "d", Title: "d", Area: "Steps"}}
	out := barRun(rowsRun(steps, fine(1, "a", "fine"), event.StepStarted{Step: "b", Doing: "applying"}, event.Doing{Step: "b", Says: "installing", Done: 1, Of: 4})...)
	head := lines(screen(out))[2]
	if want := "  ▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮▮  31% · 0.0s so far"; !strings.HasSuffix(head, want) {
		t.Errorf("one of four done, one a quarter of the way, the header's bar is %q; want it to end %q", head, want)
	}
	if !strings.Contains(out, "\x1b]9;4;1;31\x07") {
		t.Error("the tab's progress isn't 31%")
	}
}

// weighed are a run's steps weighted as kit apply weights them on a new
// Mac: a step of the user's own and Homebrew, one each; the casks, 44
// declared; the formulae, 123; a health check, one: 170 in all.
var weighed = []event.Step{
	{Name: "fonts", Title: "fonts", Area: "Steps"},
	{Name: "homebrew", Title: "Homebrew", Area: "Drift", Row: "Homebrew formulae"},
	{Name: "cask", Title: "Homebrew casks", Area: "Drift", Row: "Homebrew casks", Weight: 44},
	{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Weight: 123},
	{Name: "disk", Title: "Disk space", Area: "Mac"},
}

// casksAt are the casks at work, done of 44 installed.
func casksAt(done int) event.Event {
	return event.Doing{Step: "cask", Says: "installing", Done: done, Of: 44}
}

// The bar counts each thing the run has to do, as known as it started, one:
// each step its weight, one for a step with none; a step at work that counts,
// its weight as far as it's got; a finished step its whole weight, however
// little it found to do; the header and the tab say the same.
func TestApplyingBarIsWeighted(t *testing.T) {
	quick := []event.Event{fine(1, "fonts", "16 installed"), fine(2, "homebrew", "/opt/homebrew"), event.StepStarted{Step: "cask", Doing: "applying"}}
	for _, tt := range []struct {
		name   string
		events []event.Event
		want   int
	}{
		{"casks at 11 of 44", append(slices.Clone(quick), casksAt(11)), 7},
		{"casks at 22 of 44", append(slices.Clone(quick), casksAt(22)), 14},
		{"casks all downloaded, half the way", append(slices.Clone(quick), event.Doing{Step: "cask", Says: "downloading", Done: 44, Of: 44, Far: 0.5}), 14},
		{"casks done, having found less to do", append(slices.Clone(quick), event.Doing{Step: "cask", Says: "installing", Done: 3, Of: 10},
			finished(9, "cask", check.Result{State: check.OK, Summary: "44 declared, all installed"}), event.StepStarted{Step: "brew", Doing: "applying"}), 27},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := barRun(rowsRun(weighed, tt.events...)...)
			if got := headerPercent(t, out); got != tt.want {
				t.Errorf("the header's bar says %d%%; want %d%%", got, tt.want)
			}
			if tabs := tabPercents(out); len(tabs) == 0 || tabs[len(tabs)-1] != tt.want {
				t.Errorf("the tab's progress went %v; want it to end at %d", tabs, tt.want)
			}
		})
	}
}

// The bar never goes back: a big step starting late, its count far bigger
// than the step at work's, takes nothing off it; nor does a step counting
// again from nothing, as Homebrew's formulae install the lines other steps
// wait for first, then the rest, then those held back for a secret, saying
// what they wait for between; nor a step finishing having found less to do
// than its weight. What a step's counts have done adds up: the formulae's
// 4, 50 and 2 are 56 of their 123.
func TestApplyingBarNeverGoesBack(t *testing.T) {
	events := []event.Event{fine(1, "fonts", "16 installed"), fine(2, "homebrew", "/opt/homebrew"), event.StepStarted{Step: "cask", Doing: "applying"}}
	for done := 0; done <= 30; done += 5 {
		events = append(events, casksAt(done))
	}
	formulae := func(says string, done, of int) event.Event {
		return event.Doing{Step: "brew", Says: says, Done: done, Of: of}
	}
	events = append(events,
		event.StepStarted{Step: "brew", Doing: "applying"},
		formulae("installing gnupg", 0, 4),
		casksAt(35),
		formulae("installing nginx", 4, 4),
		formulae("updating Homebrew", 0, 0),
		formulae("installing jq", 0, 115),
		finished(9, "cask", check.Result{State: check.OK, Summary: "44 declared, all installed"}),
		formulae("installing ffmpeg", 50, 115),
		formulae("waiting for TOKEN: 4 to install", 0, 0),
		formulae("installing private-tool", 0, 4),
		formulae("installing other-tool", 2, 4),
	)
	out := barRun(rowsRun(weighed, events...)...)
	tabs := tabPercents(out)
	if len(tabs) < len(events)-3 {
		t.Fatalf("the tab's progress was set %d times: %v", len(tabs), tabs)
	}
	for i := 1; i < len(tabs); i++ {
		if tabs[i] < tabs[i-1] {
			t.Errorf("the bar went back from %d%% to %d%%: %v", tabs[i-1], tabs[i], tabs)
		}
	}
	// fonts, Homebrew and the casks, 46, and 56 of the formulae, of 170.
	if got := headerPercent(t, out); got != 60 {
		t.Errorf("the header's bar ends at %d%%; want 60%%", got)
	}
}

// barRun is events shown at a screen 160 by 64 that shows the run's progress
// on its tab: what the face wrote.
func barRun(events ...event.Event) string {
	var out syncBuffer
	c := &clock{t: at}
	face := render.NewPretty(&colorprofile.Writer{Forward: &out, Profile: colorprofile.Ascii}, 80, true).
		Sized(func() (int, int) { return 160, 64 }).Clocked(c.now).ShowTabProgress()
	defer func() { _ = face.Close() }()
	for _, e := range events {
		face.Emit(e)
	}
	time.Sleep(300 * time.Millisecond)
	return out.String()
}

// percent is a percentage as the header says it.
var percent = regexp.MustCompile(`(\d+)% · `)

// headerPercent is the percentage the header's bar says, in what a face
// wrote.
func headerPercent(t *testing.T, out string) int {
	t.Helper()
	m := percent.FindStringSubmatch(lines(screen(out))[2])
	if m == nil {
		t.Fatalf("the header says no percentage: %q", lines(screen(out))[2])
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// tabProgress is the terminal's tab told how far the run's got.
var tabProgress = regexp.MustCompile(`\x1b\]9;4;1;(\d+)\x07`)

// tabPercents are the percentages a face told the tab, in order.
func tabPercents(out string) []int {
	var got []int
	for _, m := range tabProgress.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[1])
		got = append(got, n)
	}
	return got
}

// There's no later during the run (the maintainer, 16 Oct): what's left to do waits
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
// left to do at the end, pink too, as there's nothing to do till then, the
// to-do mark nowhere mid-run; one failed, red, why and what to run on the
// spine under it; one not checked, grey; a card up, orange ▲, and done
// with, how long it was up, or left to do. Steps starting and their checks
// have no line. What's under way is at its foot, the time now on its first
// line.
func TestApplyingLog(t *testing.T) {
	events := append(casksFailed(),
		event.StepStarted{Time: after(360), Step: "1password", Doing: "applying"},
		event.NeedsYou{Time: after(360), Step: "1password", Item: "1password", Title: "Sign in to 1Password"},
		finished(400, "1password", check.Result{State: check.OK, Summary: "done"}),
	)
	face, out, _ := applyAt(160, 64, 7*time.Minute, events)
	defer func() { _ = face.Close() }()
	if strings.Contains(screen(out.String()), "△") {
		t.Errorf("mid-run, the screen has the to-do mark, which is the end's:\n%s", screen(out.String()))
	}
	log := logOf(screen(out.String()), 160)
	for _, want := range []string{
		"  03:04:06  ● GitHub: fine",
		"  03:04:10  ● Time Machine: no backup disk",
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

// A card's why is said once: as its line's notes say, when it has some;
// else, in kit's words, why the run waits for it. On applying's screen and
// in a window too small for it alike.
func TestApplyingCardsWhySaidOnce(t *testing.T) {
	noted := []event.Step{
		{Name: "appstore", Title: "appstore", Area: "Manual", ByHand: []event.Hand{{Name: "appstore", Title: "Sign in to the App Store"}}},
		{Name: "mas", Title: "App Store apps", Area: "Drift", Row: "App Store apps", Waits: []string{"appstore"}},
		{Name: "login-item", Title: "macOS login items", Area: "Drift", Row: "Login items", Waits: []string{"appstore"}},
	}
	card := event.NeedsYou{Time: after(10), Step: "appstore", Item: "appstore", Title: "Sign in to the App Store", Steps: []string{"Open the App Store"}}
	withNote := card
	withNote.Why = "macOS won't let kit check it, so say when; the first app tells."
	kits := "The run waits for this, for App Store apps and macOS login items."
	for _, tt := range []struct {
		name     string
		card     event.NeedsYou
		why, not string
	}{
		{"its notes", withNote, withNote.Why, "The run waits for this"},
		{"kit's words", card, kits, "kit won't"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			events := rowsRun(noted, event.StepStarted{Time: after(10), Step: "appstore", Doing: "applying"}, tt.card)
			face, out, _ := applyAt(160, 64, 20*time.Second, events)
			_, right := panes(screen(out.String()), 160)
			_ = face.Close()
			panel := strings.Join(right[:6], "\n")
			if !strings.Contains(panel, "      "+tt.why) || strings.Contains(panel, tt.not) {
				t.Errorf("the panel starts\n%s\nwant it to say %q, and that alone", panel, tt.why)
			}

			var narrow syncBuffer
			small := render.NewPretty(&colorprofile.Writer{Forward: &narrow, Profile: colorprofile.Ascii}, 80, true).Sized(func() (int, int) { return 80, 40 })
			for _, e := range events {
				small.Emit(e)
			}
			time.Sleep(300 * time.Millisecond)
			live := turned(screen(narrow.String()))
			if err := small.Close(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(live, tt.why) || strings.Contains(live, tt.not) {
				t.Errorf("in a small window:\n%s\nwant the card to say %q, and that alone", live, tt.why)
			}
		})
	}
}

// Cards take turns: one more of the run waits on goes up in place of the
// card up, whose step says it waits its turn again; the card set aside is
// still to do, so the log doesn't call it done, nor says again that it's
// waiting for you when it comes back, and its panel counts from when it
// first came up, as its done does. While another card waits, the panel
// says how many and that tab moves on, the foot's keys losing tab, and tab
// passes that on to the card up's step; with none waiting, tab chooses the
// next row as ever.
func TestApplyingCardsTakeTurns(t *testing.T) {
	steps := []event.Step{
		{Name: "appstore", Title: "appstore", Area: "Manual", ByHand: []event.Hand{{Name: "appstore", Title: "Sign in to the App Store"}}},
		{Name: "vault", Title: "vault", Area: "Manual", ByHand: []event.Hand{{Name: "vault", Title: "Sign in to the vault"}}},
		{Name: "mas", Title: "App Store apps", Area: "Drift", Row: "App Store apps", Waits: []string{"appstore"}},
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"vault"}},
	}
	store := event.NeedsYou{Time: after(10), Step: "appstore", Item: "appstore", Title: "Sign in to the App Store"}
	vault := event.NeedsYou{Time: after(40), Step: "vault", Item: "vault", Title: "Sign in to the vault", More: 1}
	events := rowsRun(steps,
		event.StepStarted{Time: after(10), Step: "appstore", Doing: "applying"},
		event.Doing{Time: after(10), Step: "appstore", Says: event.Queued},
		store,
		event.StepStarted{Time: after(40), Step: "vault", Doing: "applying"},
		event.Doing{Time: after(40), Step: "vault", Says: event.Queued},
		event.Doing{Time: after(40), Step: "appstore", Says: event.Queued},
		vault,
	)
	face, out, kb := applyAt(160, 64, 45*time.Second, events)
	var mu sync.Mutex
	var got []answered
	face.Hands(func(step string, a hand.Answer) bool {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, answered{step, a})
		return true
	})
	scr := screen(out.String())
	_, right := panes(scr, 160)
	panel := strings.Join(right[:8], "\n")
	if !strings.Contains(right[1], "Sign in to the vault") || !strings.Contains(right[1], "1 more after this") || !strings.Contains(panel, "      tab next card, this one comes back") {
		t.Errorf("the panel starts\n%s\nwant the vault's card, 1 more after it, tab moving on", panel)
	}
	if f := leftFoot(scr, 160, 1); !slices.Equal(f, []string{"  ↑↓ scroll · q stop"}) {
		t.Errorf("the foot is %q; want its keys but tab", f)
	}
	kb.press(t, keyTab)
	mu.Lock()
	if !slices.Equal(got, []answered{{"vault", hand.Next}}) {
		t.Errorf("tab answered %v; want the vault's card told to move on", got)
	}
	mu.Unlock()
	_ = face.Close()

	back := store
	back.Time, back.More = after(50), 1
	events = append(events, event.Doing{Time: after(50), Step: "vault", Says: event.Queued}, back)
	face, out, _ = applyAt(160, 64, 55*time.Second, events)
	_, right = panes(screen(out.String()), 160)
	if !strings.Contains(right[1], "Sign in to the App Store") || !strings.HasSuffix(right[1], "waiting 45.0s") {
		t.Errorf("the App Store's card back, the panel's title line is %q; want it waiting since it first came up", right[1])
	}
	_ = face.Close()

	events = append(events, fine(70, "appstore", "done"), event.StepStarted{Time: after(70), Step: "vault", Doing: "applying"}, event.NeedsYou{Time: after(70), Step: "vault", Item: "vault", Title: "Sign in to the vault"})
	face, out, kb = applyAt(160, 64, 75*time.Second, events)
	defer func() { _ = face.Close() }()
	want := []string{
		"  03:04:15  ▲ Sign in to the App Store: waiting for you",
		"  03:04:45  ▲ Sign in to the vault: waiting for you",
		"  03:05:15  ● Sign in to the App Store: done, in 1m00s",
	}
	if log := logOf(screen(out.String()), 160); !slices.Equal(log, want) {
		t.Errorf("the log is\n%s\nwant\n%s", strings.Join(log, "\n"), strings.Join(want, "\n"))
	}
	face.Hands(func(string, hand.Answer) bool {
		t.Error("with no card waiting, tab answered the card up")
		return true
	})
	kb.press(t, keyTab)
	if _, right := panes(screen(out.String()), 160); !slices.ContainsFunc(right, func(l string) bool { return strings.HasPrefix(l, "  LOG · ") }) {
		t.Errorf("tab with no card waiting, the right pane is\n%s\nwant a row chosen", strings.Join(right, "\n"))
	}
}

// A card set aside and done meanwhile, its step finishing, is done: the log
// says so, how long since it first came up, the card up staying up; raised
// again later, it's a card come up afresh, the log saying it waits for you
// once more, its time from then.
func TestApplyingCardDoneWhileSetAside(t *testing.T) {
	steps := []event.Step{
		{Name: "appstore", Title: "appstore", Area: "Manual", ByHand: []event.Hand{{Name: "appstore", Title: "Sign in to the App Store"}}},
		{Name: "vault", Title: "vault", Area: "Manual", ByHand: []event.Hand{{Name: "vault", Title: "Sign in to the vault"}}},
		{Name: "mas", Title: "App Store apps", Area: "Drift", Row: "App Store apps", Waits: []string{"appstore"}},
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"vault"}},
	}
	store := event.NeedsYou{Time: after(10), Step: "appstore", Item: "appstore", Title: "Sign in to the App Store"}
	events := rowsRun(steps,
		event.StepStarted{Time: after(10), Step: "appstore", Doing: "applying"},
		store,
		event.StepStarted{Time: after(40), Step: "vault", Doing: "applying"},
		event.Doing{Time: after(40), Step: "appstore", Says: event.Queued},
		event.NeedsYou{Time: after(40), Step: "vault", Item: "vault", Title: "Sign in to the vault", More: 1},
		fine(50, "appstore", "done"),
		event.NeedsYou{Time: after(50), Step: "vault", Item: "vault", Title: "Sign in to the vault"},
	)
	face, out, _ := applyAt(160, 64, 55*time.Second, events)
	got := screen(out.String())
	want := []string{
		"  03:04:15  ▲ Sign in to the App Store: waiting for you",
		"  03:04:45  ▲ Sign in to the vault: waiting for you",
		"  03:04:55  ● Sign in to the App Store: done, in 40.0s",
	}
	if log := logOf(got, 160); !slices.Equal(log, want) {
		t.Errorf("the log is\n%s\nwant\n%s", strings.Join(log, "\n"), strings.Join(want, "\n"))
	}
	if _, right := panes(got, 160); !strings.Contains(right[1], "Sign in to the vault") {
		t.Errorf("the panel's title line is %q; want the vault's card still up", right[1])
	}
	_ = face.Close()

	again := store
	again.Time = after(60)
	events = append(events, event.StepStarted{Time: after(60), Step: "appstore", Doing: "applying"}, event.Doing{Time: after(60), Step: "vault", Says: event.Queued}, again)
	face, out, _ = applyAt(160, 64, 65*time.Second, events)
	defer func() { _ = face.Close() }()
	got = screen(out.String())
	if log := logOf(got, 160); log[len(log)-1] != "  03:05:05  ▲ Sign in to the App Store: waiting for you" {
		t.Errorf("raised again, the log is\n%s\nwant it waiting for you once more", strings.Join(log, "\n"))
	}
	if _, right := panes(got, 160); !strings.HasSuffix(right[1], "waiting 5.0s") {
		t.Errorf("raised again, the panel's title line is %q; want its time from then", right[1])
	}
}

// A card is taken down the moment it's done, before its step finishes: the
// panel's gone and the log says it's done, how long it was up; its step
// finishing after says nothing more. One set aside and done meanwhile is
// taken down too, the card up staying up.
func TestApplyingCardTakenDown(t *testing.T) {
	steps := []event.Step{
		{Name: "appstore", Title: "appstore", Area: "Manual", ByHand: []event.Hand{{Name: "appstore", Title: "Sign in to the App Store"}}},
		{Name: "vault", Title: "vault", Area: "Manual", ByHand: []event.Hand{{Name: "vault", Title: "Sign in to the vault"}}},
		{Name: "mas", Title: "App Store apps", Area: "Drift", Row: "App Store apps", Waits: []string{"appstore"}},
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"vault"}},
	}
	events := rowsRun(steps,
		event.StepStarted{Time: after(10), Step: "appstore", Doing: "applying"},
		event.NeedsYou{Time: after(10), Step: "appstore", Item: "appstore", Title: "Sign in to the App Store"},
		event.StepStarted{Time: after(40), Step: "vault", Doing: "applying"},
		event.Doing{Time: after(40), Step: "appstore", Says: event.Queued},
		event.NeedsYou{Time: after(40), Step: "vault", Item: "vault", Title: "Sign in to the vault", More: 1},
		event.CardDown{Time: after(50), Step: "appstore", Item: "appstore"},
	)
	face, out, _ := applyAt(160, 64, 55*time.Second, events)
	got := screen(out.String())
	if _, right := panes(got, 160); !strings.Contains(right[1], "Sign in to the vault") {
		t.Errorf("the panel's title line is %q; want the vault's card still up", right[1])
	}
	_ = face.Close()

	events = append(events,
		event.CardDown{Time: after(60), Step: "vault", Item: "vault"},
		fine(70, "appstore", "done"),
		fine(80, "vault", "done"),
	)
	face, out, _ = applyAt(160, 64, 65*time.Second, events[:len(events)-2])
	if _, right := panes(screen(out.String()), 160); strings.HasPrefix(right[1], "  ▲ ") {
		t.Errorf("taken down, its step not finished, the right pane is\n%s\nwant no card", strings.Join(right, "\n"))
	}
	_ = face.Close()
	face, out, _ = applyAt(160, 64, 85*time.Second, events)
	defer func() { _ = face.Close() }()
	want := []string{
		"  03:04:15  ▲ Sign in to the App Store: waiting for you",
		"  03:04:45  ▲ Sign in to the vault: waiting for you",
		"  03:04:55  ● Sign in to the App Store: done, in 40.0s",
		"  03:05:05  ● Sign in to the vault: done, in 20.0s",
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
// why it's there, muted and wrapped, as its notes say; its steps, numbered, a subheading muted and unnumbered after a blank line,
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
	if rule := lines(got)[headLines+14]; !strings.Contains(rule, "├───") {
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
// beside it; then what's to do, what failed, and the rows done, a line
// each, nothing of theirs that waits under them. A run stopped says so,
// and where to carry on; nothing's red but what failed.
func TestApplyingLeavesItsRecord(t *testing.T) {
	for _, tt := range []struct {
		golden string
		events func() []event.Event
	}{{"apply-record.golden", appliedRun}, {"apply-record-stopped.golden", stoppedRun}, {"apply-record-waiting.golden", waitingRun}} {
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
// row is; a row done is its line alone, what of its steps is still
// waiting, a password say, in the log.
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
		"\n  ● App Store apps  2 declared, 1 installed\n",
	} {
		if !strings.Contains(record, want) {
			t.Errorf("the record is\n%s\nwant it to hold\n%s", record, want)
		}
	}
	if strings.Contains(record, "bear") {
		t.Errorf("the record is\n%s\nwant the App Store's row alone", record)
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
// step itself isn't named again; a step of the user's own that says what it
// did isn't said to be applied too. More than
// a few things alike under it are one line, counted.
func TestApplyingLogSaysWhatEachDid(t *testing.T) {
	steps := []event.Step{
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Part: "Secrets", Did: "synced"},
		{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings", Part: "Settings", Did: "set"},
		{Name: "file-sharing", Title: "File Sharing", Area: "Drift", Part: "Settings"},
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
		finished(6, "fonts", check.Result{State: check.OK, Summary: "16 fonts installed", Done: []check.Item{{ID: "fonts", Name: "Operator Mono", Action: "run"}}}),
		finished(7, "claude-mcp", check.Result{State: check.OK, Summary: "19 declared, 8 installed", Done: install("dex"), Items: waiting}),
	))
	defer func() { _ = face.Close() }()
	log := strings.Join(logOf(screen(out.String()), 160), "\n")
	for _, want := range []string{
		"  03:04:07  ● Secrets: synced 4\n",
		"  03:04:08  ● macOS settings: set one, two\n",
		"  03:04:09  ● File Sharing: on\n",
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
// it's met on the way, which neither holds it back nor leaves it to do, two
// waiting only for each other left not yet; and a step deferred behind
// steps that left only things to do is to do, however many ways lead to
// them, a2 behind e as it waits for b2, which waits for it; but not one
// behind a step deferred for its own reasons, as a3 is behind x.
func TestApplyingStepsWaitingForEachOther(t *testing.T) {
	deferred := check.Result{State: check.Deferred, Reason: "needs a secret"}
	toDo := check.Result{State: check.Attention, Summary: "to do", Items: []check.Item{{ID: "x:disk", Name: "Add a disk", State: check.Problem}}}
	steps := []event.Step{
		{Name: "e", Title: "e", Area: "Backups"},
		{Name: "d", Title: "d", Area: "Backups", Waits: []string{"e"}},
		{Name: "c", Title: "c", Area: "Backups", Waits: []string{"d"}},
		{Name: "b", Title: "b", Area: "Backups", Waits: []string{"d"}},
		{Name: "a", Title: "a", Area: "Backups", Waits: []string{"b", "c"}},
		{Name: "b2", Title: "b2", Area: "Backups", Waits: []string{"a2"}},
		{Name: "a2", Title: "a2", Area: "Backups", Waits: []string{"e", "b2"}},
		{Name: "x", Title: "x", Area: "Backups"},
		{Name: "a3", Title: "a3", Area: "Backups", Waits: []string{"e", "x"}},
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
		"Backups":        "  ● Backups  1 to do at the end",
	} {
		if line := rowLine(t, got, 160, row); !strings.HasPrefix(line, want) {
			t.Errorf("the row is %q; want %q", line, want)
		}
	}
	_, log := panes(got, 160)
	for _, step := range []string{"a", "b", "c", "d", "a2"} {
		if !slices.ContainsFunc(log, func(l string) bool { return strings.Contains(l, "● "+step+":") }) {
			t.Errorf("the log has no %s to do at the end:\n%s", step, strings.Join(log, "\n"))
		}
	}
	if !slices.ContainsFunc(log, func(l string) bool { return strings.Contains(l, "– a3: not checked") }) {
		t.Errorf("a3, behind x, deferred for its own reasons, is to do:\n%s", strings.Join(log, "\n"))
	}
}
