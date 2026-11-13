package render

import "time"

// DecodeAt is the arrival's decode at frame, for a test to look at a frame
// without waiting for it.
func (a *Arrival) DecodeAt(width, height, frame int) []string { return a.decode(width, height, frame) }

// HeaderAt is the header the arrival lands on, its line typed to typed
// characters.
func (a *Arrival) HeaderAt(typed int) []string { return a.header(typed) }

// ShrinkAt is the arrival's shrink at step.
func (a *Arrival) ShrinkAt(width, height, step int) []string { return a.shrink(width, height, step) }

// Clocked has the face tell the time by now, for how long a run has taken.
func (p *Pretty) Clocked(now func() time.Time) *Pretty {
	p.now = now
	return p
}

// Turned has the face's spinner turn only when Turn turns it, never as time
// goes by.
func (p *Pretty) Turned() *Pretty {
	p.turning = func() (<-chan time.Time, func()) { return nil, func() {} }
	return p
}

// Turn turns the spinner once, while it's turning, as a tick does once the
// last drawing is drawEvery old: what's come since drawn, at once.
func (p *Pretty) Turn() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop == nil {
		return
	}
	p.drawn = time.Time{}
	p.turn()
}
