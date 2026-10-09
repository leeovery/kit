package render

// DecodeAt is the arrival's decode at frame, for a test to look at a frame
// without waiting for it.
func (a *Arrival) DecodeAt(width, height, frame int) []string { return a.decode(width, height, frame) }

// ShrinkAt is the arrival's shrink at step.
func (a *Arrival) ShrinkAt(width, height, step int) []string { return a.shrink(width, height, step) }
