package prefs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The store's cloned aside and put in place whole: a clone cut short, as by
// the run stopping, leaves no clone, nor what it got done, and the next
// look clones again.
func TestACloneCutShort(t *testing.T) {
	clone := filepath.Join(t.TempDir(), "prefs")
	aside := clone + ".cloning"
	fake := runnertest.New(t)
	cloning := make(chan struct{})
	fake.On("git", "clone", "--quiet", remote, aside).Does(func() {
		if err := os.MkdirAll(filepath.Join(aside, ".git"), 0o700); err != nil {
			t.Error(err)
		}
		close(cloning)
	}).Hangs().Then().Does(func() {
		if err := os.MkdirAll(filepath.Join(aside, ".git"), 0o700); err != nil {
			t.Error(err)
		}
	})
	for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
		fake.On("git", "-C", clone, "config", kv[0], kv[1])
	}
	fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", "laptop").Prints("2026-10-04T03:00:00Z\n")
	now := func() time.Time { return time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC) }
	p := prefs.New(fake, t.TempDir(), t.TempDir(), clone, remote, "laptop", prefs.Lists{}, now)

	ctx, stop := context.WithCancel(t.Context())
	go func() {
		<-cloning
		stop()
	}()
	if _, err := p.LastSave(ctx); err == nil {
		t.Fatal("a clone cut short gave a last save")
	}
	for _, left := range []string{clone, aside} {
		if _, err := os.Stat(left); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is left (%v), want nothing of the clone cut short", left, err)
		}
	}
	if last, err := p.LastSave(t.Context()); err != nil || !last.Equal(time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("next look: %v, %v; want the store cloned again and its last save", last, err)
	}
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		t.Errorf("the clone isn't in place: %v", err)
	}
}
