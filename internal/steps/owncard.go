package steps

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/runner"
)

// ownCard is a card of kit's own a step raises mid-run, as the data
// restore's are, which kit can't tell done by itself: a choice, answered by
// one of its keys, which it keeps (chosen); or, with no keys, done once the
// person says so. One with an app (app, by bundle id) opens it, once, as it
// first goes up.
type ownCard struct {
	says event.NeedsYou
	run  runner.Runner
	app  string

	opened sync.Once
	mu     sync.Mutex
	chose  hand.Answer
}

func (c *ownCard) card() event.NeedsYou { return c.says }

// done is never: the person's answer says it.
func (c *ownCard) done(context.Context) (bool, string) { return false, "" }

// answer is what the person's answer does: one of its keys, or, with none,
// done, is the card done, and kept; anything else does nothing.
func (c *ownCard) answer(_ context.Context, a hand.Answer, _ time.Time) (bool, string, error) {
	takes := slices.ContainsFunc(c.says.Keys, func(k check.Key) bool { return hand.Answer(k.Key) == a })
	if len(c.says.Keys) == 0 {
		takes = a == hand.Done
	}
	if !takes {
		return false, "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chose = a
	return true, "", nil
}

func (c *ownCard) open(ctx context.Context) {
	if c.app != "" {
		c.opened.Do(func() { openApp(ctx, c.run, c.app) })
	}
}

// chosen is the person's answer, once the card's done.
func (c *ownCard) chosen() hand.Answer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chose
}
