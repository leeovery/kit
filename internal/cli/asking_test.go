package cli_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/leeovery/kit/internal/cli"
)

// notices notes the notifications posted and taken away: a clear in a
// context that's done, so that it can't run, says so.
type notices struct {
	mu   sync.Mutex
	seen []string
}

func (n *notices) Notify(_ context.Context, id, title, body string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = append(n.seen, "notify "+id+" | "+title+" | "+body)
}

func (n *notices) Clear(ctx context.Context, id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ctx.Err() != nil {
		id += " (in a context that's done)"
	}
	n.seen = append(n.seen, "clear "+id)
}

// While macOS is asking, a notification says so, under an id of its own,
// replaced as what it's asking changes; it's taken away once it's answered,
// or as the watch ends with it unanswered, and only when one's up.
func TestAskingNotice(t *testing.T) {
	n := &notices{}
	changed, end := cli.AskingNotice(n)
	ctx, stop := context.WithCancel(t.Context())
	changed(ctx, "asking for a password")
	changed(ctx, "asking to allow access")
	changed(ctx, "")
	changed(ctx, "")
	changed(ctx, "asking for a password")
	stop()
	end(ctx)
	end(ctx)
	want := []string{
		"notify dialog | macOS is asking for a password | Answer it in the window macOS shows",
		"notify dialog | macOS is asking to allow access | Answer it in the window macOS shows",
		"clear dialog",
		"notify dialog | macOS is asking for a password | Answer it in the window macOS shows",
		"clear dialog",
	}
	if !slices.Equal(n.seen, want) {
		t.Errorf("notifications %q, want %q", n.seen, want)
	}
}
