package capture

import (
	"cmp"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
)

// A new Mac's boot is two scenarios, as it's two windows: in Terminal, on
// its own background, then in the terminal it hands over to. Each is made
// for a window 120 by 34, four lines taller than Terminal's on a new Mac,
// so the boot at its tallest, Homebrew's last lines and the bar under the
// self-test, fits without its head scrolling away; and each plays at a
// moment of a day, as the boot's head and apply's header say.
var scenarios = []Scenario{
	{
		Name: "boot", Width: 120, Height: 34, start: time.Date(2026, 11, 20, 9, 41, 0, 0, time.UTC), play: boot,
		About: "the boot in Terminal: the splash, the self-test, signing in with the phone, the boot order to the hand-off",
	},
	{
		Name: "arrival", Width: 120, Height: 34, start: time.Date(2026, 11, 20, 9, 52, 0, 0, time.UTC), play: arrival,
		About: "the boot arriving in Ghostty: hello again, the decode in colour, then kit apply's screen, a card coming up",
	},
}

// newMac is the Mac the scenarios set up, as kit-config knows it.
const newMac = "laptop"

// selfTest is what a new Mac's self-test finds.
func selfTest(at time.Time) event.SelfTest {
	return event.SelfTest{Time: at, Mac: "MacBook-Pro", Tests: []event.Test{
		{Name: "Apple M4 Pro", State: check.OK, Says: []string{"14 cores"}},
		{Name: "Memory", State: check.OK, Says: []string{"49152 MB"}},
		{Name: "Disk", State: check.OK, Says: []string{"Macintosh HD", "937 GB free of 994 GB"}},
		{Name: "macOS", State: check.OK, Says: []string{"26.1"}},
		{Name: "Network", State: check.OK, Says: []string{"github.com", "41 ms"}},
	}}
}

// boot is the boot in Terminal, as kit bootstrap plays it: the splash,
// enter pressed once it's resolved; the self-test; signing in to GitHub
// with the phone, its QR code the whole window; then the boot order, a
// step at a time, what it asks answered in its row, to the hand-off.
func boot(s *stage) error {
	s.pressAfter(3300*time.Millisecond, "enter")
	if err := render.NewSplash(true).Play(s.ctx, s.term); err != nil {
		return err
	}
	b := &booting{s: s, face: render.NewBootFace(s.term, true, s.now, nil)}
	if err := b.face.PlaySelfTest(s.ctx, selfTest(s.now())); err != nil {
		return err
	}
	order := rendertest.BootOrder()
	b.face.Start(order[:1])
	b.signIn()
	b.face.Show(order)
	b.order()
	b.face.Finish("this window can be closed")
	s.pause(400 * time.Millisecond)
	if err := b.face.Close(); err != nil {
		return err
	}
	s.hold()
	return b.err
}

// booting is the boot's face, at a stage, and the first error a question
// it asked was answered with.
type booting struct {
	s    *stage
	face *render.BootFace
	err  error
}

// emit shows e, stamped by the stage's clock.
func (b *booting) emit(events ...func(time.Time) event.Event) {
	for _, e := range events {
		b.face.Emit(e(b.s.now()))
	}
}

func started(step string) func(time.Time) event.Event {
	return func(t time.Time) event.Event { return event.StepStarted{Time: t, Step: step} }
}

func doing(step, says, todo string) func(time.Time) event.Event {
	return func(t time.Time) event.Event { return event.Doing{Time: t, Step: step, Says: says, Todo: todo} }
}

func printed(step, line string) func(time.Time) event.Event {
	return func(t time.Time) event.Event { return event.Output{Time: t, Step: step, Line: line} }
}

func done(step, summary string) func(time.Time) event.Event {
	return func(t time.Time) event.Event {
		return event.StepFinished{Time: t, Step: step, Result: check.Result{State: check.OK, Summary: summary}}
	}
}

// ask asks the face question, as the boot does, while keys presses what
// answers it; then waits for the answer, as the boot goes on only once it
// has it, so a key pressed next can't answer it in its place.
func (b *booting) ask(question func() error, keys func()) {
	answered := make(chan error, 1)
	go func() { answered <- question() }()
	keys()
	b.err = cmp.Or(b.err, <-answered)
}

// signIn signs in with the phone: the ways, enter chosen, the QR code and
// the code, approved.
func (b *booting) signIn() {
	s, ctx := b.s, b.s.ctx
	b.emit(started("github"))
	b.ask(func() error {
		_, err := b.face.Pick(ctx, "github", []look.Key{{Key: "enter", Does: "with your phone"}, {Key: "s", Does: "here, in your browser"}})
		return err
	}, func() {
		s.pause(1100 * time.Millisecond)
		s.press("enter")
	})
	s.pause(400 * time.Millisecond)
	b.face.Emit(event.DeviceCode{Time: s.now(), Step: "github", URI: "https://github.com/login/device", Code: "WDJB-MJHT", Expires: s.now().Add(15 * time.Minute)})
	s.pause(2300 * time.Millisecond)
	b.emit(done("github", "someone · kit-config has laptop and studio"))
}

// order plays the boot order: which Mac this is, chosen; the password,
// typed; Homebrew at work, its last lines under it; the apps, kit-config
// and kit; Full Disk Access, done by hand; then Ghostty opening.
func (b *booting) order() {
	s, ctx := b.s, b.s.ctx
	b.emit(started("mac"))
	b.ask(func() error {
		_, err := b.face.Choose(ctx, "mac", "which of your Macs is this?", []look.Choice{
			{Label: "laptop", Does: "MacBook, the primary"}, {Label: "studio", Does: "Mac Studio"},
			{Label: "new", Does: "a new machine ID, started fresh or from one of your Macs"},
		})
		return err
	}, func() {
		s.pause(1100 * time.Millisecond)
		s.press("enter")
	})
	b.emit(done("mac", newMac), started("password"))
	b.ask(func() error {
		_, err := b.face.Secret(ctx, "password", "this Mac's password, once")
		return err
	}, func() {
		s.pause(400 * time.Millisecond)
		s.typeIn("opensesame")
		s.press("enter")
	})
	b.emit(done("password", "Touch ID on for sudo"), started("homebrew"), doing("homebrew", "installing", ""))
	for _, l := range []string{
		"==> Searching online for the Command Line Tools",
		"==> Installing Command Line Tools for Xcode-26.1",
		"==> Downloading and installing Homebrew...",
		"==> Pouring portable-ruby-3.4.7.arm64_big_sur.bottle.tar.gz",
		"==> Installation successful!",
	} {
		s.pause(600 * time.Millisecond)
		b.emit(printed("homebrew", l))
	}
	s.pause(400 * time.Millisecond)
	b.emit(done("homebrew", "5.0.3 · Xcode tools 26.1"))
	b.step("apps", "installing Ghostty", "Ghostty 1.3.1", 900*time.Millisecond)
	b.step("kit-config", "cloning", "cloned · 61 files linked", 600*time.Millisecond)
	b.step("kit", "installing through Homebrew", "0.1.2 · through Homebrew", 500*time.Millisecond)
	b.emit(started("full-disk-access"))
	b.ask(func() error {
		_, err := b.face.Wait(ctx, "full-disk-access", "for Ghostty, before it first opens",
			"System Settings is open at Full Disk Access, and Finder at Applications: drag Ghostty into the list, then press enter",
			[]look.Key{{Key: "enter", Does: "done"}})
		return err
	}, func() {
		s.pause(1700 * time.Millisecond)
		s.press("enter")
	})
	b.emit(done("full-disk-access", "granted to Ghostty"), started("terminal"),
		doing("terminal", "opening", "macOS asks before Ghostty first opens: choose Open"))
	s.pause(1400 * time.Millisecond)
	b.emit(done("terminal", "opened · kit carries on there"))
}

// step is step at work for d, saying what it's doing, then done, saying
// summary.
func (b *booting) step(step, says, summary string, d time.Duration) {
	b.emit(started(step), doing(step, says, ""))
	b.s.pause(d)
	b.emit(done(step, summary))
}

// arrival is the boot carrying on in Ghostty, as kit bootstrap does there:
// the arrival, a key pressed at its greeting; then kit apply's screen,
// under the header it lands on.
func arrival(s *stage) error {
	s.pressAfter(1300*time.Millisecond, "enter")
	a := render.NewArrival(true, "bootstrap", newMac, s.now()).KeepHeader()
	if err := a.Play(s.ctx, s.term); err != nil {
		return err
	}
	width, _ := s.term.Size()
	face := render.NewPretty(s.term.Out, width, true).Sized(s.term.Size).Clocked(s.now).
		Keys(func() (<-chan ask.Key, func()) { return ask.Listen(s.term) }, func() {})
	if a.Landed() {
		face.UnderHome()
	}
	defer face.Unwind()
	steps := rendertest.NewMac()
	face.Emit(event.Preparing{Time: s.now(), Command: "bootstrap", Machine: newMac, Doing: "checking what will need an administrator's password", Steps: steps})
	s.pause(700 * time.Millisecond)
	face.Emit(event.RunStarted{Time: s.now(), Command: "bootstrap", Machine: newMac, Steps: steps})
	for _, b := range applying() {
		s.pause(b.pause)
		for _, e := range b.events {
			face.Emit(e)
		}
	}
	s.hold()
	return face.Close()
}

// beat is what happens in a run a pause after the beat before.
type beat struct {
	pause  time.Duration
	events []event.Event
}

// applying is a new Mac's run as kit apply has it, a few seconds of it: the boot's steps done, what's quick done, Homebrew
// installing formulae and casks, 1Password's sign-in coming up once its
// casks are in, and Claude Code finishing setting itself up.
func applying() []beat {
	ok := func(step, summary string) event.Event {
		return event.StepFinished{Step: step, Result: check.Result{State: check.OK, Summary: summary}}
	}
	work := func(step string) []event.Event {
		return []event.Event{event.StepStarted{Step: step, Doing: "checking"}, event.StepStarted{Step: step, Doing: "applying"}}
	}
	beats := []beat{
		{300 * time.Millisecond, []event.Event{
			ok("boot-github", "someone · kit-config has laptop and studio"), ok("boot-mac", newMac),
			ok("boot-homebrew", "/opt/homebrew"), ok("boot-kit-config", "cloned"),
		}},
		{400 * time.Millisecond, []event.Event{
			ok("homebrew", "/opt/homebrew"), ok("file", "54 linked"), ok("path", "15 directories"),
			ok("disk", "93% free"), ok("load", "load 2.4 on 14 cores"),
		}},
		{300 * time.Millisecond, append(append(work("cask"), work("brew")...), work("claude-code")...)},
	}
	casks := []string{"1password", "1password-cli", "raycast", "slack", "arc", "obsidian"}
	formulae := []string{"git", "gh", "jq", "ripgrep", "fzf", "ffmpeg"}
	for i := range casks {
		beats = append(beats,
			beat{350 * time.Millisecond, installing("cask", casks, i, 42)},
			beat{350 * time.Millisecond, installing("brew", formulae, i, 129)},
		)
		switch i {
		case 0:
			beats = append(beats, beat{0, []event.Event{ok("touch-id-sudo", "turned on"), ok("fonts", "16 installed")}})
		case 1:
			beats = append(beats, beat{300 * time.Millisecond, []event.Event{
				ok("defaults", "99 set"), ok("power", "6 set"),
				event.StepFinished{Step: "time-machine", Result: check.Result{State: check.Attention, Summary: "no backup disk", Items: []check.Item{
					{ID: "time-machine:disk", Name: "Add a Time Machine disk", State: check.Problem, Detail: "System Settings › General › Time Machine › Add Backup Disk"}}}},
			}})
		case 2:
			beats = append(beats, beat{200 * time.Millisecond, []event.Event{
				event.StepStarted{Step: "1password", Doing: "applying"}, rendertest.SignIn(time.Time{}),
			}})
		case 4:
			beats = append(beats, beat{300 * time.Millisecond, []event.Event{ok("claude-code", "set up · 3 plugins")}})
		}
	}
	return append(beats, beat{800 * time.Millisecond, nil})
}

// installing is a Homebrew step having installed names[i], and at work on
// the next, of of all it installs.
func installing(step string, names []string, i, of int) []event.Event {
	events := []event.Event{event.Logged{Step: step, Line: "Installed " + names[i]}}
	if i+1 < len(names) {
		events = append(events, event.Doing{Step: step, Says: "installing " + names[i+1], Done: i + 1, Of: of})
	}
	return events
}
