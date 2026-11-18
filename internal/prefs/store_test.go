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

// cloned is a Mac with no clone of the store, at clone, kit-config's
// remote having its prefs branch, and the clone's own git settings and its
// last save scripted on fake, cloning scripted by the test; and the clone's
// apps' settings as kit takes them, through fake.
func cloned(t *testing.T) (clone string, fake *runnertest.Fake, settings func() *prefs.Prefs) {
	t.Helper()
	clone, config := filepath.Join(t.TempDir(), "prefs"), t.TempDir()
	fake = runnertest.New(t)
	onTheBranch(fake, config, clone)
	fake.On("git", "ls-remote", "--quiet", "--exit-code", remote, "refs/heads/prefs").Prints(head + "\trefs/heads/prefs\n")
	fake.On("git", "-C", clone, "log", "-1", "--format=%cI", "--", "laptop").Prints(lastSave.Format(time.RFC3339) + "\n")
	now := func() time.Time { return time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC) }
	return clone, fake, func() *prefs.Prefs {
		return prefs.New(fake, t.TempDir(), t.TempDir(), clone, config, "laptop", prefs.Lists{}, now)
	}
}

// cloneOf is git cloning kit-config's prefs branch alone into dir.
func cloneOf(dir string) []string {
	return []string{"clone", "--quiet", "--single-branch", "--branch", "prefs", remote, dir}
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
	fake.On("git", cloneOf(aside)...).Does(func() {
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
	fake.On("git", cloneOf(aside)...).Does(func() {
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
	fake.On("git", cloneOf(aside)...).Does(func() {
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

// The store is kit-config's prefs branch, cloned alone from where
// kit-config's clone fetches: its one remote, whatever its name, by the
// URL git takes for it.
func TestTheStoreIsKitConfigsPrefsBranch(t *testing.T) {
	for _, c := range []struct{ name, remote, url string }{
		{"origin, over ssh", "origin", remote},
		{"another name, over https", "upstream", "https://github.com/someone/kit-config.git"},
	} {
		t.Run(c.name, func(t *testing.T) {
			clone, fake, settings := cloned(t)
			p := settings()
			fake.On("git", "-C", p.Config, "remote").Prints(c.remote + "\n")
			fake.On("git", "-C", p.Config, "remote", "get-url", c.remote).Prints(c.url + "\n")
			fake.On("git", "-C", clone, "remote", "get-url", "origin").Prints(c.url + "\n")
			fake.On("git", "ls-remote", "--quiet", "--exit-code", c.url, "refs/heads/prefs").Prints(head + "\trefs/heads/prefs\n")
			fake.On("git", "clone", "--quiet", "--single-branch", "--branch", "prefs", c.url, clone+".cloning").Does(func() { madeIn(t, clone+".cloning") })
			if saves, err := p.Saves(t.Context()); err != nil || !saved(saves) {
				t.Errorf("saves %v, %v; want the branch cloned and its last save", saves, err)
			}
			if n := clones(fake); n != 1 {
				t.Errorf("cloned %d times, ran %q; want the branch cloned once", n, fake.Calls())
			}
		})
	}
}

// Without one remote for kit-config, the store has nowhere to be: nothing's
// cloned, and kit says why.
func TestNoStoreWithoutKitConfigsRemote(t *testing.T) {
	for remotes, want := range map[string]string{
		"":               "kit-config has no remote, so apps' settings have nowhere to be saved: they're saved on its prefs branch",
		"origin\nfork\n": "find kit-config's remote, where apps' settings are saved: the repository has 2 remotes, so kit pushes nothing: kit pushes to one alone",
	} {
		_, fake, settings := cloned(t)
		p := settings()
		fake.On("git", "-C", p.Config, "remote").Prints(remotes)
		if _, err := p.Saves(t.Context()); err == nil || err.Error() != want || clones(fake) != 0 {
			t.Errorf("remotes %q: Saves() = %v, cloned %d times; want %q, nothing cloned", remotes, err, clones(fake), want)
		}
	}
}

// A remote with no prefs branch yet, as before any Mac's first capture,
// gives a clone of nothing, on the branch and tracking it: that capture
// commits there and its push makes the branch, beside kit-config's own.
func TestTheFirstCaptureMakesTheBranch(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	if err := os.RemoveAll(w.clone); err != nil {
		t.Fatal(err)
	}
	aside := w.clone + ".cloning"
	w.fake.On("git", "ls-remote", "--quiet", "--exit-code", remote, "refs/heads/prefs").Exits(2)
	made := []string{
		"git init --quiet --initial-branch=prefs " + aside,
		"git -C " + aside + " remote add -t prefs origin " + remote,
		"git -C " + aside + " config branch.prefs.remote origin",
		"git -C " + aside + " config branch.prefs.merge refs/heads/prefs",
	}
	w.fake.On("git", "init", "--quiet", "--initial-branch=prefs", aside).Does(func() { madeIn(t, aside) })
	w.fake.On("git", "-C", aside, "remote", "add", "-t", "prefs", "origin", remote)
	w.fake.On("git", "-C", aside, "config", "branch.prefs.remote", "origin")
	w.fake.On("git", "-C", aside, "config", "branch.prefs.merge", "refs/heads/prefs")
	writeRecord(t, w.state, `{"on": "started fresh"}`)
	w.fake.On("defaults", "domains").Prints("com.example.app\n")
	w.exports(t, "com.example.app", map[string]any{"Theme": "dark"})
	w.pushes(t, "Capture 2026-10-06 03:00: 1 new\n\nnew: com.example.app\nclaimed: the folder, for this Mac's hardware",
		"?? laptop/.gitignore\x00?? laptop/domains/com.example.app.plist\x00?? laptop/owner\x00", newFiles, newFiles)
	w.fake.On("git", "-C", w.clone, "rev-parse", "--verify", "--quiet", "HEAD").Exits(1)
	w.fake.On("git", "-C", w.clone, "fetch", "--prune", "--quiet", "origin").Exits(128).PrintsToStderr("fatal: couldn't find remote ref refs/heads/prefs\n")
	w.fake.On("git", "-C", w.clone, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/prefs").Exits(1)

	report, err := w.p.Capture(t.Context())
	if err != nil || !report.Committed || report.PushError != "" {
		t.Errorf("Capture() = %+v, %v; want it committed and pushed", report, err)
	}
	calls := w.fake.Calls()
	if i := slices.Index(calls, made[0]); i < 0 || len(calls) < i+len(made) || !slices.Equal(calls[i:i+len(made)], made) {
		t.Errorf("ran %q; want the clone made as %q", calls, made)
	}
	if !slices.Contains(calls, "git -C "+w.clone+" push --quiet origin HEAD:refs/heads/prefs") {
		t.Errorf("ran %q; want the push to make the branch", calls)
	}
}

// A clone that isn't of kit-config's prefs branch, as one of the repository
// the store was in before, is never captured into or restored from: kit
// names the clone, what it points at, and what it should.
func TestACloneElsewhereIsRefused(t *testing.T) {
	const moveAside = "move the clone aside for kit to clone the branch there"
	for _, c := range []struct{ name, url, on, says string }{
		{"the old repository", "git@github.com:someone/prefs.git", "refs/heads/main\n",
			" points at github.com:someone/prefs.git, not kit-config's remote (github.com:someone/kit-config.git): apps' settings are saved on its prefs branch now, so " + moveAside},
		{"no remote", "", "refs/heads/prefs\n",
			" points at no remote, not kit-config's remote (github.com:someone/kit-config.git): apps' settings are saved on its prefs branch now, so " + moveAside},
		{"kit-config's own branch", remote, "refs/heads/main\n", " is on main, not kit-config's prefs branch: " + moveAside},
		{"no branch", remote, "", " is on no branch, not kit-config's prefs branch: " + moveAside},
	} {
		for _, do := range []struct {
			name string
			run  func(context.Context, *prefs.Prefs) error
		}{
			{"capture", func(ctx context.Context, p *prefs.Prefs) error { _, err := p.Capture(ctx); return err }},
			{"restore", func(ctx context.Context, p *prefs.Prefs) error {
				_, err := p.Restore(ctx, prefs.RestoreOptions{})
				return err
			}},
		} {
			w := newWorld(t, prefs.Lists{})
			writeRecord(t, w.state, `{"on": "restored"}`)
			w.write(t, filepath.Join(w.clone, "laptop", "domains", "com.example.app.plist"), stored(t, map[string]any{"A": true}))
			url := w.fake.On("git", "-C", w.clone, "remote", "get-url", "origin").Prints(c.url + "\n")
			if c.url == "" {
				url.Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
			}
			branch := w.fake.On("git", "-C", w.clone, "symbolic-ref", "-q", "HEAD").Prints(c.on)
			if c.on == "" {
				branch.Exits(1)
			}
			if err := do.run(t.Context(), w.p); err == nil || err.Error() != w.clone+c.says {
				t.Errorf("%s, %s: %v; want %q", c.name, do.name, err, w.clone+c.says)
			}
			for _, call := range w.fake.Calls() {
				if !strings.Contains(call, " remote") && !strings.Contains(call, " symbolic-ref ") {
					t.Errorf("%s, %s: ran %s; want nothing pulled, saved or restored", c.name, do.name, call)
				}
			}
		}
	}
}
