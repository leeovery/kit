package steps

import (
	"context"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
)

// work is what a card does aside, a thing at a time, as one can touch what
// another does (an answer bringing back what a look checks for): looking
// whether it's done, as asked and every few seconds; doing what the
// person's answers say, in the order they came; and readying what the card
// says. Each, once it's done, comes on out, for the card's loop, which
// waits on none of them. Halted, as the card comes down, it starts no look
// and readies nothing more, but does the answers it was given, and a look
// or answer under way finishes, never cut short, what they come to kept,
// should one find the card done, for outcome; readying, which can never
// find it done, is cut short, its context (readyIn) done, as a store's
// first clone can take minutes. It ends once it's found the card done,
// what's left undone.
type work struct {
	c       raised
	now     func() time.Time
	after   func(time.Duration) <-chan time.Time
	every   time.Duration
	out     chan result
	wake    chan struct{}
	halted  chan struct{}
	halt    func()
	over    chan struct{}
	readyIn context.Context

	// mu guards what's asked for: the number of the last look asked for, and
	// whether one's still to start; the answers to do; whether to ready the
	// card; and, once halted, what the work came to, should it have found
	// the card done.
	mu       sync.Mutex
	asked    int
	looking  bool
	keys     []hand.Answer
	readying bool
	late     result
}

// result is what a card's work came to: a look, begun once the look
// numbered asked had been asked for, or what an answer did (answered);
// whether it found the card done, what kit saw, and what went wrong, if
// anything. Or, readied, what the card says.
type result struct {
	asked    int
	answered bool
	readied  *event.NeedsYou
	done     bool
	saw      string
	err      error
}

// job is a thing a card's work does: an answer's (key), a look, numbered
// as the last asked for as it began, or readying the card.
type job struct {
	key   hand.Answer
	asked int
	ready bool
}

// working starts c's work, aside, till it's halted, finds c done, or ctx
// is: looking every manualPoll, as it is now.
func (r *Raise) working(ctx context.Context, c raised) *work {
	readyIn, cut := context.WithCancel(ctx)
	w := &work{c: c, now: r.Now, after: r.after, every: manualPoll, out: make(chan result), wake: make(chan struct{}, 1), halted: make(chan struct{}), over: make(chan struct{}), readyIn: readyIn}
	w.halt = sync.OnceFunc(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		close(w.halted)
		cut()
	})
	r.aside.Go(func() { w.run(ctx) })
	return w
}

// run does what's asked for, a thing at a time, and looks every few
// seconds after the last, till there's nothing more to do.
func (w *work) run(ctx context.Context) {
	defer close(w.over)
	for {
		j, ok := w.next(ctx, w.after(w.every))
		if !ok {
			return
		}
		res := w.do(ctx, j)
		w.report(ctx, res)
		if res.done || res.err != nil {
			return
		}
	}
}

// next is the next thing to do once one's asked for, or, a look, when poll
// says it's time: false once halted, or ctx is done.
func (w *work) next(ctx context.Context, poll <-chan time.Time) (job, bool) {
	for {
		if j, ok, found := w.take(ctx); !ok || found {
			return j, ok
		}
		select {
		case <-w.wake:
		case <-poll:
			poll = nil
			w.mu.Lock()
			w.looking = true
			w.mu.Unlock()
		case <-w.halted:
		case <-ctx.Done():
		}
	}
}

// take takes the next thing asked for, an answer before a look and a look
// before readying, reporting whether there's one (found); not ok once
// there's nothing more to do: ctx done, or halted with no answer left, so
// that no look starts once the card's down, whatever else came at once.
func (w *work) take(ctx context.Context) (j job, ok, found bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	halted := false
	select {
	case <-w.halted:
		halted = true
	default:
	}
	switch {
	case ctx.Err() != nil:
		return job{}, false, false
	case len(w.keys) > 0:
		j.key, w.keys = w.keys[0], w.keys[1:]
	case halted:
		return job{}, false, false
	case w.looking:
		j.asked, w.looking = w.asked, false
	case w.readying:
		j.ready, w.readying = true, false
	default:
		return job{}, true, false
	}
	return j, true, true
}

// do does j.
func (w *work) do(ctx context.Context, j job) result {
	switch {
	case j.key != "":
		done, saw, err := w.c.answer(ctx, j.key, w.now())
		return result{answered: true, done: done, saw: saw, err: err}
	case j.ready:
		card := w.c.(readier).ready(w.readyIn)
		return result{readied: &card}
	}
	done, saw := w.c.done(ctx)
	return result{asked: j.asked, done: done, saw: saw}
}

// report passes res on to the card's loop: halted first, it's kept
// instead, should it have found the card done.
func (w *work) report(ctx context.Context, res result) {
	select {
	case w.out <- res:
	case <-w.halted:
		if res.done {
			w.mu.Lock()
			w.late = res
			w.mu.Unlock()
		}
	case <-ctx.Done():
	}
}

// look asks for a look as soon as one can start, and returns its number: a
// look begun from now on has it, or a later one's.
func (w *work) look() int {
	w.mu.Lock()
	w.asked++
	asked := w.asked
	w.looking = true
	w.mu.Unlock()
	w.poke()
	return asked
}

// answer asks for what a, the person's answer, says to be done, after
// what's under way and the answers given before it.
func (w *work) answer(a hand.Answer) {
	w.mu.Lock()
	w.keys = append(w.keys, a)
	w.mu.Unlock()
	w.poke()
}

// readyCard asks for what the card says to be readied.
func (w *work) readyCard() {
	w.mu.Lock()
	w.readying = true
	w.mu.Unlock()
	w.poke()
}

// poke tells the work something's been asked for.
func (w *work) poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// outcome is what the work left as the card came down, the person having
// stopped there, came to, once it's over: whether it found the card done,
// and what kit saw. Nothing, should ctx be done first.
func (w *work) outcome(ctx context.Context) (saw string, done bool) {
	w.halt()
	select {
	case <-w.over:
	case <-ctx.Done():
		return "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.late.saw, w.late.done
}

// aside runs f aside, waited for by Wait, and returns where what it comes
// to comes.
func aside[T any](r *Raise, f func() T) <-chan T {
	out := make(chan T, 1)
	r.aside.Go(func() { out <- f() })
	return out
}
