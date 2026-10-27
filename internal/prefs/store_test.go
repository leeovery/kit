package prefs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// lastSave is when the store's clone says this Mac last saved, as cloned
// says it.
var lastSave = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

// cloned is a Mac with no clone of the store, at clone, the clone's own
// git settings and its last save scripted on fake, cloning scripted by the
// test; and the clone's apps' settings as kit takes them, through fake.
func cloned(t *testing.T) (clone string, fake *runnertest.Fake, settings func() *prefs.Prefs) {
	t.Helper()
	clone = filepath.Join(t.TempDir(), "prefs")
	fake = runnertest.New(t)
	for _, kv := range [][2]string{{"user.name", "kit"}, {"user.email", "kit@localhost"}, {"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"}} {
		fake.On("git", "-C", clone, "config", kv[0], kv[1])
	}
	fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", "laptop").Prints(lastSave.Format(time.RFC3339) + "\n")
	now := func() time.Time { return time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC) }
	return clone, fake, func() *prefs.Prefs {
		return prefs.New(fake, t.TempDir(), t.TempDir(), clone, remote, "laptop", prefs.Lists{}, now)
	}
}

// madeIn has a clone made in dir, as git clone makes one, with this Mac's
// folder.
func madeIn(t *testing.T, dir string) {
	for _, d := range []string{".git", "laptop"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Error(err)
		}
	}
}

// saved reports whether saves are the clone's: this Mac's last save alone.
func saved(saves []prefs.Save) bool {
	return slices.Equal(saves, []prefs.Save{{Mac: "laptop", At: lastSave}})
}

// clones counts the clones of the store fake ran.
func clones(fake *runnertest.Fake) int {
	n := 0
	for _, c := range fake.Calls() {
		if strings.HasPrefix(c, "git clone ") {
			n++
		}
	}
	return n
}

// The store's cloned aside and put in place whole: a clone cut short, as by
// the run stopping, leaves no clone, nor what it got done, and the next
// look clones again.
func TestACloneCutShort(t *testing.T) {
	clone, fake, settings := cloned(t)
	aside := clone + ".cloning"
	cloning := make(chan struct{})
	fake.On("git", "clone", "--quiet", remote, aside).Does(func() {
		madeIn(t, aside)
		close(cloning)
	}).Hangs().Then().Does(func() { madeIn(t, aside) })
	p := settings()

	ctx, stop := context.WithCancel(t.Context())
	go func() {
		<-cloning
		stop()
	}()
	if _, err := p.Saves(ctx); err == nil {
		t.Fatal("a clone cut short gave a last save")
	}
	for _, left := range []string{clone, aside} {
		if _, err := os.Stat(left); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is left (%v), want nothing of the clone cut short", left, err)
		}
	}
	if saves, err := p.Saves(t.Context()); err != nil || !saved(saves) {
		t.Errorf("next look: %v, %v; want the store cloned again and its last save", saves, err)
	}
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		t.Errorf("the clone isn't in place: %v", err)
	}
}

// What a kit killed as it cloned left aside is cleared before the store's
// cloned there again.
func TestACloneLeftByAKitKilled(t *testing.T) {
	clone, fake, settings := cloned(t)
	aside := clone + ".cloning"
	madeIn(t, aside)
	if err := os.WriteFile(filepath.Join(aside, "half"), []byte("half a pack"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake.On("git", "clone", "--quiet", remote, aside).Does(func() {
		if _, err := os.Stat(aside); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("cloning into %s, which is there (%v), left by the kit killed", aside, err)
		}
		madeIn(t, aside)
	})
	if saves, err := settings().Saves(t.Context()); err != nil || !saved(saves) {
		t.Errorf("saves %v, %v; want the store cloned and its last save", saves, err)
	}
	if _, err := os.Stat(filepath.Join(clone, "half")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("what the kit killed left is in the clone (%v)", err)
	}
}

// Two kits with no clone look at once: one clones, the other waiting for it
// rather than cloning too, then taking the clone it made; one whole clone,
// cloned once.
func TestTwoKitsCloneAtOnce(t *testing.T) {
	clone, fake, settings := cloned(t)
	aside := clone + ".cloning"
	cloning, through := make(chan struct{}), make(chan struct{})
	began := sync.OnceFunc(func() { close(cloning) })
	fake.On("git", "clone", "--quiet", remote, aside).Does(func() {
		began()
		<-through
		madeIn(t, aside)
	})
	first, second := settings(), settings()
	var waiting sync.Once
	waited := make(chan struct{})
	second.Sleep = func(d time.Duration) {
		waiting.Do(func() { close(waited) })
		time.Sleep(d)
	}
	var wg sync.WaitGroup
	looks := make([]error, 2)
	wg.Go(func() { _, looks[0] = first.Saves(t.Context()) })
	<-cloning
	wg.Go(func() { _, looks[1] = second.Saves(t.Context()) })
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Error("the second kit never waited for the first's clone")
	}
	close(through)
	wg.Wait()
	if looks[0] != nil || looks[1] != nil || clones(fake) != 1 {
		t.Errorf("looks %v, cloned %d times; want both to find the last save, the store cloned once", looks, clones(fake))
	}
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		t.Errorf("the clone isn't in place: %v", err)
	}
}

// A folder in the clone's place that isn't a clone, and isn't empty, is
// refused at once, nothing downloaded, saying what to do.
func TestAFolderInTheClonesPlace(t *testing.T) {
	clone, fake, settings := cloned(t)
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "notes"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := settings().Saves(t.Context())
	if err == nil || !strings.Contains(err.Error(), "move it aside") || clones(fake) != 0 {
		t.Errorf("last save = %v, cloned %d times; want it refused, saying what to do, nothing cloned", err, clones(fake))
	}
}
