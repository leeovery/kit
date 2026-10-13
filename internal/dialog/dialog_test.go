package dialog_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/dialog"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

func TestAsking(t *testing.T) {
	for _, tc := range []struct {
		owners, want string
	}{
		{"Dock\nFinder\nWindow Server\n", ""},
		{"Dock\nCoreServicesUIAgent\nFinder\n", "asking whether to open a program from the internet"},
		{"SecurityAgent\nGhostty\n", "asking for an administrator's password"},
		{"Ghostty\nUserNotificationCenter\n", "asking for a permission"},
	} {
		fake := runnertest.New(t)
		fake.On("osascript", "-l", "JavaScript", "-e", dialog.Owners).Prints(tc.owners)
		if got := dialog.Asking(context.Background(), fake); got != tc.want {
			t.Errorf("with %q on screen, Asking() = %q, want %q", tc.owners, got, tc.want)
		}
	}
}

func TestAskingWhenTheWindowsCantBeListed(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("osascript", "-l", "JavaScript", "-e", dialog.Owners).Fails(errors.New("no window server"))
	if got := dialog.Asking(context.Background(), fake); got != "" {
		t.Errorf("Asking() = %q, want nothing", got)
	}
}

// watching runs a watch on what fake lists, every 5ms, a window told of once
// it's stayed 10ms, two looks after the first that saw it; until is told
// what macOS is asking each time it's told, and stops the watch when it
// returns true. It returns what the watch was told, in order.
func watching(t *testing.T, w *dialog.Watch, fake *runnertest.Fake, until func(said []string) bool) []string {
	t.Helper()
	defer dialog.SetEvery(5 * time.Millisecond)()
	defer dialog.SetStays(10 * time.Millisecond)()
	var mu sync.Mutex
	var said []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx, fake, func(what string) {
			mu.Lock()
			defer mu.Unlock()
			said = append(said, what)
			if what != "" && !w.Up() {
				t.Error("told it's asking, yet not Up")
			}
		})
	}()
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		stop := until(said)
		mu.Unlock()
		if stop {
			break
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("said %q in two seconds", said)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	return slices.Clone(said)
}

// A window that stays is told of once it's been seen on every look for
// long enough, and told gone once it's answered.
func TestWatch(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("osascript", "-l", "JavaScript", "-e", dialog.Owners).Prints("Finder\n").
		Then().Prints("UserNotificationCenter\n").
		Then().Prints("UserNotificationCenter\n").
		Then().Prints("UserNotificationCenter\n").
		Then().Prints("Finder\n")
	var w dialog.Watch
	said := watching(t, &w, fake, func(said []string) bool { return len(said) >= 2 })
	if want := []string{"asking for a permission", ""}; !slices.Equal(said[:2], want) {
		t.Errorf("said %q, want %q: once as it asked, once as it was answered", said, want)
	}
	if w.Up() {
		t.Error("Up once answered")
	}
}

// A window macOS closes itself, as CoreServicesUIAgent's "Verifying…" as a
// new program first opens, is never told of, gone before it's stayed; but
// the watch is Up while it's seen, so time limits wait from the first look.
// One that stays after it is told of.
func TestWatchIgnoresAWindowThatGoesByItself(t *testing.T) {
	fake := runnertest.New(t)
	var w dialog.Watch
	var upWhileSeen bool
	fake.On("osascript", "-l", "JavaScript", "-e", dialog.Owners).Prints("Finder\n").
		Then().Prints("CoreServicesUIAgent\nFinder\n").
		Then().Prints("CoreServicesUIAgent\nFinder\n").Does(func() { upWhileSeen = w.Up() }).
		Then().Prints("Finder\n").
		Then().Prints("SecurityAgent\n").
		Then().Prints("SecurityAgent\n").
		Then().Prints("SecurityAgent\n")
	said := watching(t, &w, fake, func(said []string) bool { return len(said) >= 1 })
	if want := []string{"asking for an administrator's password"}; !slices.Equal(said, want) {
		t.Errorf("said %q, want %q: nothing of the window that went by itself, then the one that stayed", said, want)
	}
	if !upWhileSeen {
		t.Error("not Up while the window that went by itself was on screen")
	}
}
