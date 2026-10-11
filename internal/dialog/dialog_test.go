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

func TestWatch(t *testing.T) {
	defer dialog.SetEvery(5 * time.Millisecond)()
	fake := runnertest.New(t)
	fake.On("osascript", "-l", "JavaScript", "-e", dialog.Owners).Prints("Finder\n").
		Then().Prints("UserNotificationCenter\n").
		Then().Prints("UserNotificationCenter\n").
		Then().Prints("Finder\n")
	var w dialog.Watch
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
		n := len(said)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("said %q in two seconds", said)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if want := []string{"asking for a permission", ""}; !slices.Equal(said[:2], want) {
		t.Errorf("said %q, want %q: once as it asked, once as it was answered", said, want)
	}
	if w.Up() {
		t.Error("Up once answered")
	}
}
