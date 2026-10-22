package notice_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/leeovery/kit/internal/notice"
)

// held is a Notifier whose notifications each wait to be let through,
// as one waits for kit's app to be built: going says which is going out,
// and through lets it. What it's passed, it notes, a notification or take-
// away whose context was done saying so.
type held struct {
	going   chan string
	through chan struct{}
	mu      sync.Mutex
	seen    []string
}

func newHeld() *held {
	return &held{going: make(chan string), through: make(chan struct{})}
}

func (h *held) Notify(ctx context.Context, id, title, body string) {
	h.going <- id
	<-h.through
	h.note(ctx, "notify "+id+" | "+title+" | "+body)
}

func (h *held) Clear(ctx context.Context, id string) { h.note(ctx, "clear "+id) }

func (h *held) note(ctx context.Context, what string) {
	if ctx.Err() != nil {
		what += " (done)"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen = append(h.seen, what)
}

// let lets the notification under id go out, once it's going.
func (h *held) let(t *testing.T, id string) {
	t.Helper()
	if got := <-h.going; got != id {
		t.Fatalf("%s went out, want %s", got, id)
	}
	h.through <- struct{}{}
}

// Notifications go out aside, in order, nothing given waiting for the one
// going out: one taken away as it's going out has its context done, and
// is taken away after; one taken away before it's gone out goes out with
// its context done, so it posts nothing, then the taking away; the rest
// go out as they were.
func TestNotificationsGoOutInOrder(t *testing.T) {
	h := newHeld()
	q := notice.New(h)
	q.Notify(t.Context(), "a", "A", "first")
	if got := <-h.going; got != "a" {
		t.Fatalf("%s went out, want a", got)
	}
	q.Clear(t.Context(), "a")
	q.Notify(t.Context(), "b", "B", "second")
	q.Notify(t.Context(), "c", "C", "third")
	q.Clear(t.Context(), "c")
	h.through <- struct{}{}
	h.let(t, "b")
	h.let(t, "c")
	q.Wait()

	want := []string{"notify a | A | first (done)", "clear a", "notify b | B | second", "notify c | C | third (done)", "clear c"}
	if !slices.Equal(h.seen, want) {
		t.Errorf("passed on %q, want %q", h.seen, want)
	}
}

// What's given goes out though the context it was given in is done by
// then, as a card's is once it's down.
func TestNotificationsOutliveTheirContext(t *testing.T) {
	h := newHeld()
	q := notice.New(h)
	ctx, stop := context.WithCancel(t.Context())
	stop()
	q.Notify(ctx, "a", "A", "first")
	h.let(t, "a")
	q.Wait()
	q.Clear(ctx, "a")
	q.Wait()

	if want := []string{"notify a | A | first", "clear a"}; !slices.Equal(h.seen, want) {
		t.Errorf("passed on %q, want %q", h.seen, want)
	}
}

// With nothing given, there's nothing to wait for.
func TestWaitingOnNothing(t *testing.T) {
	notice.New(newHeld()).Wait()
}
