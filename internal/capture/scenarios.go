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

// The clip's window, in columns and lines: four lines taller than
// Terminal's on a new Mac, so the boot at its tallest, Homebrew's last
// lines and the bar under the self-test, fits without its head scrolling
// away. The README's screens are drawn in it too.
const windowColumns, windowLines = 120, 34

// A new Mac's boot is two scenarios, as it's two windows: in Terminal, on
// its own background, then in the terminal it hands over to. Each is made
// for the clip's window, and plays at a moment of a day, as the boot's head
// and apply's header say.
var scenarios = []Scenario{
	{
		Name: "boot", Width: windowColumns, Height: windowLines, start: bootAt, play: boot,
		About: "the boot in Terminal: the splash, the self-test, signing in with the phone, the boot order to the hand-off",
	},
	{
		Name: "arrival", Width: windowColumns, Height: windowLines, start: arrivedAt, play: arrival,
		About: "the boot arriving in Ghostty: hello again, the decode in colour, then kit apply flying through to its record",
	},
}

// newMac is the Mac the scenarios set up, as kit-config knows it.
const newMac = "laptop"

// When the boot starts on the new Mac, in Terminal, and when it arrives in
// Ghostty.
var (
	bootAt    = time.Date(2026, 11, 20, 9, 41, 0, 0, time.UTC)
	arrivedAt = time.Date(2026, 11, 20, 9, 52, 0, 0, time.UTC)
)

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
// enter pressed once it says to; the self-test; signing in to GitHub
// with the phone, its QR code the whole window; then the boot order, a
// step at a time, what it asks answered in its row, to the hand-off.
func boot(s *stage) error {
	s.meanwhile(func() { s.answer(0, "press enter", 900*time.Millisecond, func() { s.press("enter") }) })
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

// ask asks the face question, as the boot does, and once it shows what
// it asks, after read, answers it as does does; then waits for the answer,
// as the boot goes on only once it has it, so a key pressed next can't
// answer it in its place.
func (b *booting) ask(question func() error, shows string, read time.Duration, does func()) {
	b.err = cmp.Or(b.err, b.s.asked(question, b.s.shown.mark(), shows, read, does))
}

// signIn signs in with the phone: the ways, enter chosen, the QR code and
// the code, approved.
func (b *booting) signIn() {
	b.showCode()
	b.s.pause(1600 * time.Millisecond)
	b.emit(done("github", githubSaid))
}

// What the boot's steps say they did, in the clip and the stills alike.
const (
	githubSaid    = "someone · kit-config has laptop and studio"
	homebrewSaid  = "5.0.3 · Xcode tools 26.1"
	appsSaid      = "Ghostty 1.3.1"
	kitConfigSaid = "cloned · 61 files linked"
	kitSaid       = "0.1.2 · through Homebrew"
)

// deviceCode is the code the sign-in shows, as the phone enters it.
const deviceCode = "WDJB-MJHT"

// showCode starts signing in to GitHub: the ways, enter chosen; then the
// QR code and the code, waiting for the phone.
func (b *booting) showCode() {
	s, ctx := b.s, b.s.ctx
	b.emit(started("github"))
	b.ask(func() error {
		_, err := b.face.Pick(ctx, "github", []look.Key{{Key: "enter", Does: "with your phone"}, {Key: "s", Does: "here, in your browser"}})
		return err
	}, "enter with your phone", 800*time.Millisecond, func() { s.press("enter") })
	s.pause(400 * time.Millisecond)
	b.face.Emit(event.DeviceCode{Time: s.now(), Step: "github", URI: "https://github.com/login/device", Code: deviceCode, Expires: s.now().Add(15 * time.Minute)})
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
	}, "which of your Macs is this?", 800*time.Millisecond, func() { s.press("enter") })
	b.emit(done("mac", newMac), started("password"))
	b.ask(func() error {
		_, err := b.face.Secret(ctx, "password", "this Mac's password, once")
		return err
	}, "this Mac's password, once", 400*time.Millisecond, func() {
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
		s.pause(350 * time.Millisecond)
		b.emit(printed("homebrew", l))
	}
	s.pause(400 * time.Millisecond)
	b.emit(done("homebrew", homebrewSaid))
	b.step("apps", "installing Ghostty", appsSaid, 700*time.Millisecond)
	b.step("kit-config", "cloning", kitConfigSaid, 600*time.Millisecond)
	b.step("kit", "installing through Homebrew", kitSaid, 500*time.Millisecond)
	b.emit(started("full-disk-access"))
	b.ask(func() error {
		_, err := b.face.Wait(ctx, "full-disk-access", "for Ghostty, before it first opens",
			"System Settings is open at Full Disk Access, and Finder at Applications: drag Ghostty into the list, then press enter",
			[]look.Key{{Key: "enter", Does: "done"}})
		return err
	}, "drag Ghostty into the list", 1100*time.Millisecond, func() { s.press("enter") })
	b.emit(done("full-disk-access", "granted to Ghostty"), started("terminal"),
		doing("terminal", "opening", "macOS asks before Ghostty first opens: choose Open"))
	s.pause(1000 * time.Millisecond)
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
// under the header it lands on, flying through the run to its record.
func arrival(s *stage) error {
	s.meanwhile(func() { s.answer(0, "press any key", 800*time.Millisecond, func() { s.press("enter") }) })
	a := render.NewArrival(true, "bootstrap", newMac, s.now()).KeepHeader()
	if err := a.Play(s.ctx, s.term); err != nil {
		return err
	}
	face := s.applyFace(a.Landed())
	defer face.Unwind()
	// kit apply takes half an hour on a new Mac: its clock runs on through
	// it, its card minutes in, its end half an hour on, as the stills'.
	began := s.applyStarts(face, 500*time.Millisecond)
	s.lapsing(70)
	s.play(face, toTheCard())
	s.lapsing(200)
	s.play(face, toTheEnd())
	s.lapsing(0)
	face.Emit(applied(began, s.now()))
	s.hold()
	return face.Close()
}

// applyFace is kit apply's face at the stage, live, at the window's size,
// telling the time by the stage's clock, reading keys from it; under the
// header the arrival landed, when it did.
func (s *stage) applyFace(underHome bool) *render.Pretty {
	width, _ := s.term.Size()
	face := render.NewPretty(s.term.Out, width, true).Sized(s.term.Size).Clocked(s.now).
		Keys(func() (<-chan ask.Key, func()) { return ask.Listen(s.term) }, func() {})
	if underHome {
		face.UnderHome()
	}
	return face
}

// applyStarts starts the new Mac's run on face, as kit bootstrap does once
// in the terminal: preparing for preparing, then started. It returns when.
func (s *stage) applyStarts(face *render.Pretty, preparing time.Duration) time.Time {
	steps := rendertest.NewMac()
	face.Emit(event.Preparing{Time: s.now(), Command: "bootstrap", Machine: newMac, Doing: "checking what will need an administrator's password", Steps: steps})
	s.pause(preparing)
	began := s.now()
	face.Emit(event.RunStarted{Time: began, Command: "bootstrap", Machine: newMac, Steps: steps})
	return began
}
