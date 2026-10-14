// Package hand carries what the person at the Mac answers a step by hand
// with, on applying's screen, back to the step waiting on it.
package hand

import "sync"

// Answer is what the person answers a step by hand with.
type Answer int

const (
	// Done is the person saying they've done it.
	Done Answer = iota + 1
	// Later is the person putting it off till the run's end: what's left to
	// do then lists it.
	Later
	// Open is the person asking for its app to be opened again.
	Open
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
	ch := make(chan Answer, 1)
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
// its last answer: false when nothing takes it.
func (d *Desk) Answer(step string, a Answer) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch, ok := d.waiting[step]
	if !ok {
		return false
	}
	select {
	case ch <- a:
		return true
	default:
		return false
	}
}
