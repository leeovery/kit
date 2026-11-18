package capture

import (
	"slices"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
)

// holdOnce holds the still once text is drawn, its last screen.
func (s *stage) holdOnce(text string) {
	s.shown.since(s.ctx, 0, text)
	s.hold()
}

// applyCard is kit apply's screen on the new Mac, minutes in, as the
// clip has it once 1Password's sign-in has come up as a card.
func applyCard(s *stage) error {
	face := s.applyFace(false)
	defer face.Unwind()
	s.applyStarts(face, 2*time.Second)
	s.pause(4 * time.Minute)
	s.play(face, toTheCard())
	s.holdOnce(rendertest.SignIn(time.Time{}).Why[:30])
	return nil
}

// applyToDos is kit apply's screen on the new Mac at its run's end, the
// to-dos up: the first done, the second's card up.
func applyToDos(s *stage) error {
	face := s.applyFace(false)
	defer face.Unwind()
	s.applyStarts(face, 2*time.Second)
	s.pause(30 * time.Minute)
	s.play(face, slices.Concat(toTheCard(), toTheToDos()))
	list := slices.Clone(toDos)
	list[0].Done = true
	s.play(face, []beat{{time.Minute, toDoUp(list, 1)}})
	s.holdOnce(toDoCards[1].Why[:20])
	return nil
}

// bootSignIn is the boot in Terminal signing in to GitHub once its
// self-test has played, the phone chosen: its QR code the whole window,
// and the code.
func bootSignIn(s *stage) error {
	b := &booting{s: s, face: render.NewBootFace(s.term, true, s.now, nil)}
	if err := b.face.PlaySelfTest(s.ctx, selfTest(s.now())); err != nil {
		return err
	}
	b.face.Start(rendertest.BootOrder()[:1])
	b.showCode()
	s.holdOnce(deviceCode)
	if err := b.face.Close(); err != nil {
		return err
	}
	return b.err
}

// reconcileAt is when kit reconcile asks, two days after what it asks about
// was installed.
var reconcileAt = time.Date(2026, 11, 20, 16, 20, 0, 0, time.UTC)

// reconcile is kit reconcile at a terminal, asking about each thing that's
// drifted, one after another: ffmpeg, installed by hand, declared; the
// next, a dependency nothing needs now, asked.
func reconcile(s *stage) error {
	width, _ := s.term.Size()
	face := render.NewPretty(s.term.Out, width, true).Sized(s.term.Size).Clocked(s.now)
	face.Emit(event.Preparing{Time: s.now(), Command: "reconcile", Machine: "laptop", Doing: "looking for what differs from the config"})
	face.Emit(event.Preparing{Time: s.now(), Command: "reconcile", Machine: "laptop"})
	walked := make(chan error, 1)
	go func() {
		_, err := ask.Walk(s.ctx, s.term, "Reconcile", drifted())
		walked <- err
	}()
	s.answer(0, "1 of 3", 0, func() { s.press("enter") })
	s.holdOnce("2 of 3")
	return <-walked
}

// drifted are the questions kit reconcile asks on a Mac where ffmpeg and
// Firefox were installed by hand two days ago, and node@20, which nothing
// needs now: each a row saying its kind, what's wrong and for how long,
// and the answers its kind offers, as kit reconcile has them.
func drifted() []ask.Question {
	extra := []look.Choice{
		{Label: "Declare", Does: "declare it, for this Mac"}, {Label: "Declare everywhere", Does: "declare it, for every Mac"},
		{Label: "Uninstall", Does: "uninstall it"}, {Label: "Snooze", Does: "snooze it for 7 days"}, {Label: "Skip", Does: "not now"},
	}
	unused := slices.Concat(extra[2:3], extra[:2], extra[3:])
	question := func(name, kind, what string, answers []look.Choice) ask.Question {
		says := look.Says(look.Muted(kind), look.Orange(what), look.Muted("2 days"))
		return ask.Question{About: look.Row{State: look.NeedsYou, Name: name, Says: says}, Answers: answers}
	}
	return []ask.Question{
		question("ffmpeg", "brew", "installed, not declared", extra),
		question("node@20", "brew", "installed for something since removed, needed by nothing", unused),
		question("firefox", "cask", "installed, not declared", extra),
	}
}
