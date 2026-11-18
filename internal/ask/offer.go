package ask

import (
	"context"
	"fmt"

	"github.com/leeovery/kit/internal/look"
)

// Offer shows lines at t, under what's on screen, then keys: enter takes
// up what they offer, and any other key leaves it, as the window's focus
// coming and going doesn't. Nothing stays on screen.
func Offer(ctx context.Context, t Terminal, lines []string, keys []look.Key) (bool, error) {
	o := &offer{lines: lines, keys: keys}
	if err := show(ctx, t, o); err != nil {
		return false, fmt.Errorf("offer: %w", err)
	}
	return o.taken, nil
}

// offer is what Offer shows.
type offer struct {
	lines []string
	keys  []look.Key
	taken bool
}

func (o *offer) update(k key) bool {
	if k.is("focus in", "focus out") {
		return false
	}
	o.taken = k.is("enter")
	return true
}

func (o *offer) view(width, _ int) []string {
	out := make([]string, 0, len(o.lines)+2)
	for _, l := range o.lines {
		out = append(out, look.Cut(l, width))
	}
	return append(out, "", look.Cut(look.Keys(o.keys...), width))
}

func (o *offer) leaves(int) []string { return nil }
