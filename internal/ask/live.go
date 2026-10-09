package ask

import "context"

// Screen is something shown in place while work goes on, as the boot is:
// its lines at the terminal's whole size, never more than its lines, and
// no wider than the screen means them to be; what a key does
// to it, and whether that's the end; what it leaves on screen once it ends;
// and a signal each time it's changed from outside, closed when that ends
// it.
type Screen interface {
	View(width, height int) []string
	Key(k Key) (done bool)
	Leaves(width int) []string
	Changes() <-chan struct{}
}

// FullScreen is a screen that takes the whole of the terminal for a while,
// as the boot's QR code does: its lines then, or nil while it's drawn in
// place. What was on screen comes back as it was.
type FullScreen interface {
	Full(width, height int) []string
}

// Key is a key pressed, as a screen is given it: its name, as up, down,
// enter, esc, ctrl+c or backspace; or text typed, or pasted.
type Key struct {
	Name, Text string
	Paste      bool
}

// Is is whether k is any of names, by name or, for a letter, as typed.
func (k Key) Is(names ...string) bool {
	return key{name: k.Name, text: k.Text, paste: k.Paste}.is(names...)
}

// Live shows s at t, under what's on screen, as a question is shown: drawn,
// and drawn over after each key, each change and when the terminal's
// resized, till a key or its changes end it; then its lines are cleared and
// what it leaves is written in their place.
func Live(ctx context.Context, t Terminal, s Screen) error {
	return show(ctx, t, live{s})
}

// live is a screen, as show takes a model.
type live struct{ s Screen }

func (l live) view(width, height int) []string { return l.s.View(width, height) }

func (l live) update(k key) bool {
	return l.s.Key(Key{Name: k.name, Text: k.text, Paste: k.paste})
}

func (l live) leaves(width int) []string { return l.s.Leaves(width) }

func (l live) changes() <-chan struct{} { return l.s.Changes() }

func (l live) full(width, height int) []string {
	if f, ok := l.s.(FullScreen); ok {
		return f.Full(width, height)
	}
	return nil
}
