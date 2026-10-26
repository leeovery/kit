// Package hand carries what the person at the Mac answers a step by hand
// with, on applying's screen, back to the step waiting on it.
package hand

import "sync"

// Answer is what the person answers a step by hand with: that it's done,
// that they're stopping here, that they're moving on to the next card
// waiting, that they're leaving the to-do up for later, or, a choice, its
// key.
type Answer string

const (
	// Done is the person saying they've done it.
	Done Answer = "done"
	// Stop is the person stopping where they are, at the run's to-dos: what's
	// left stays to do.
	Stop Answer = "stop"
	// Next is the person moving on to the next card waiting, the one up
	// staying to do, to come back.
	Next Answer = "next"
	// Skip is the person leaving the to-do up, at the run's end, for later:
	// it comes down, staying to do, and the next comes up.
	Skip Answer = "skip"
)

// Desk passes answers from the screen to the step whose card is up.
type Desk struct {
	mu      sync.Mutex
	waiting map[string]chan Answer
}

// New returns a desk with no card up.
func New() *Desk { return &Desk{waiting: map[string]chan Answer{}} }

// Listen is the channel step's answers come on while its card is up, and
// what takes its card down, after which answers for it go nowhere.
func (d *Desk) Listen(step string) (<-chan Answer, func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Room for a Stop behind a Done waiting.
	ch := make(chan Answer, 2)
	d.waiting[step] = ch
	return ch, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.waiting[step] == ch {
			delete(d.waiting, step)
		}
	}
}

// Answer passes a to step's card, when one is up and isn't still taking
// its last answer: false when nothing takes it. A Stop, or a Skip, is never
// refused while a card's up: it queues behind a Done still waiting, the
// only sign, for a step kit can't test, that the person did it, so the
// Done's taken first; any other answer waiting it takes the place of, as
// it's what the person said last, but a Skip a Stop's, as stopping goes
// further.
func (d *Desk) Answer(step string, a Answer) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch, ok := d.waiting[step]
	switch {
	case !ok:
		return false
	case a == Stop, a == Skip:
		last(ch, a)
		return true
	case len(ch) > 0:
		return false
	}
	ch <- a
	return true
}

// last puts a, a Stop or a Skip, on ch, behind a Done waiting there, in
// place of any other answer waiting, but a Skip a Stop's: only the card's
// step takes from ch meanwhile.
func last(ch chan Answer, a Answer) {
	done, stop := false, a == Stop
	for len(ch) > 0 {
		select {
		case waiting := <-ch:
			done, stop = done || waiting == Done, stop || waiting == Stop
		default:
		}
	}
	if done {
		ch <- Done
	}
	if stop {
		a = Stop
	}
	ch <- a
}
