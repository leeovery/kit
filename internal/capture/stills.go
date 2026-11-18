package capture

import (
	"context"
	"slices"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
)

// applyCard is kit apply's screen on the new Mac, minutes in, as the
// clip has it once 1Password's sign-in has come up as a card.
func applyCard(s *stage) error {
	face := s.applyFace(false)
	defer face.Unwind()
	s.applyStarts(face, 2*time.Second)
	s.pause(4 * time.Minute)
	s.play(face, toTheCard())
	s.holdShown()
	return face.Close()
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
	s.holdShown()
	return face.Close()
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
	s.holdShown()
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
	return s.holdAsking(func() error {
		_, err := ask.Walk(s.ctx, s.term, "Reconcile", drifted())
		return err
	}, func(ctx context.Context) { s.answerIn(ctx, 0, "1 of 3", 0, func() { s.press("enter") }) })
}

// drifted are the questions kit reconcile asks on a Mac where ffmpeg and
// Firefox were installed by hand two days ago, and node@20, which nothing
// needs now: each with the answers kit reconcile offers its state, in
// their order, as its drifters give them.
func drifted() []ask.Question {
	declare := []render.Answer{
		{Words: kind.Words{Label: "Declare", Does: "declare it, for this Mac"}},
		{Words: kind.Words{Label: "Declare", Does: "declare it, for every Mac"}, Shared: true},
	}
	uninstall := []render.Answer{{Words: kind.UninstallWords}}
	snooze := []render.Answer{{Words: kind.Words{Label: "Snooze", Does: "snooze it for 7 days"}}}
	extra, unused := slices.Concat(declare, uninstall, snooze), slices.Concat(uninstall, declare, snooze)
	question := func(name, of, what string, answers []render.Answer) ask.Question {
		return render.ReconcileQuestion(render.Drifted{Name: name, Kind: of, What: what, Since: reconcileAt.Add(-2 * 24 * time.Hour)}, answers, reconcileAt)
	}
	return []ask.Question{
		question("ffmpeg", "brew", "installed, not declared", extra),
		question("node@20", "brew", "installed for something since removed, needed by nothing", unused),
		question("firefox", "cask", "installed, not declared", extra),
	}
}

// holdAsking asks question, as a face asking something does till it's
// stopped, and holds the still once it shows what it shows, having done
// first what's done meanwhile, if anything. Ended early, it holds nothing,
// nor waits on.
func (s *stage) holdAsking(question func() error, meanwhile func(ctx context.Context)) error {
	asking, stop := context.WithCancel(s.ctx)
	defer stop()
	asked := make(chan error, 1)
	go func() {
		defer stop()
		asked <- question()
	}()
	if meanwhile != nil {
		meanwhile(asking)
	}
	s.shown.since(asking, 0, s.shows)
	if asking.Err() == nil {
		s.hold()
	}
	return <-asked
}

// logAt is when kit log is run, on a Mac in daily use.
var logAt = time.Date(2026, 11, 20, 16, 30, 0, 0, time.UTC)

// runLog is kit log at a terminal, its runs to pick one from: a Mac in daily
// use, run every hour by itself, and by hand now and then.
func runLog(s *stage) error {
	return s.holdAsking(func() error {
		_, err := ask.Pick(s.ctx, s.term, []string{""}, render.RunList(runsLogged()), render.RunListKeys)
		return err
	}, nil)
}

// runsLogged are the runs kit log lists, newest first: today, reconcile
// and status by hand, the hourly runs either side of an apply; yesterday,
// the hourly runs, Time Machine late at one.
func runsLogged() []logs.Run {
	type ran struct {
		at      time.Time
		command string
		fine    int
		needs   int
		took    time.Duration
	}
	day := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }
	var all []ran
	hourly := func(d, from, to int) {
		for h := to; h >= from; h-- {
			took := 12400 * time.Millisecond
			if h == 0 {
				took = 64 * time.Second
			}
			all = append(all, ran{day(d, h, 0), "nightly", 31, 0, took})
		}
	}
	all = append(all, ran{day(20, 16, 20), "reconcile", 1, 0, 6100 * time.Millisecond}, ran{day(20, 16, 12), "status", 13, 2, 1600 * time.Millisecond})
	hourly(20, 10, 16)
	all = append(all, ran{day(20, 9, 14), "apply", 31, 0, 48200 * time.Millisecond})
	hourly(20, 0, 9)
	hourly(19, 3, 23)
	all = append(all, ran{day(19, 2, 0), "nightly", 30, 1, 12900 * time.Millisecond})
	hourly(19, 0, 1)
	runs := make([]logs.Run, len(all))
	for i, r := range all {
		runs[i] = logs.Run{
			Started:  logs.Record{Time: r.at, Event: "run_started", Command: r.command},
			Finished: logs.Record{Time: r.at.Add(r.took), Event: "run_finished", DurationMS: r.took.Milliseconds(), Counts: map[check.State]int{check.OK: r.fine, check.Attention: r.needs}},
		}
	}
	return runs
}
