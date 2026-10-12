package render

import "time"

// DecodeAt is the arrival's decode at frame, for a test to look at a frame
// without waiting for it.
func (a *Arrival) DecodeAt(width, height, frame int) []string { return a.decode(width, height, frame) }

// ShrinkAt is the arrival's shrink at step.
func (a *Arrival) ShrinkAt(width, height, step int) []string { return a.shrink(width, height, step) }

// Clocked has the face tell the time by now, for how long a run has taken.
func (p *Pretty) Clocked(now func() time.Time) *Pretty {
	p.now = now
	return p
}
