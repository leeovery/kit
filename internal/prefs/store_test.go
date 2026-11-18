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
// kit-config's clone fetches: its one remote, whatever its name. The clone
// is given the URL kit-config's remote has set, so git's insteadOf and
// pushInsteadOf rules rewrite both alike, and it's checked as git takes
// both: fetching from kit-config's URL, pushing where kit-config pushes.
// With git's pushInsteadOf rewriting kit-config's https URL to ssh for
// pushing, the clone's taken as kit-config's; with insteadOf too, rewriting
// the URL kit-config's remote has set for fetching, the clone pushes where
// kit-config does only given that URL as set, as git applies pushInsteadOf
// to it, never to insteadOf's.
func TestTheStoreIsKitConfigsPrefsBranch(t *testing.T) {
	const https = "https://github.com/someone/kit-config.git"
	for _, c := range []struct{ name, remote, set, url, pushes string }{
		{"origin, over ssh", "origin", remote, remote, remote},
		{"another name, over https", "upstream", https, https, https},
		{"pushing over ssh, by pushInsteadOf", "origin", https, https, remote},
		{"fetching by insteadOf, pushing by pushInsteadOf", "origin", https, remote, "ssh://git@github.com/someone/kit-config.git"},
	} {
		t.Run(c.name, func(t *testing.T) {
			clone, fake, settings := cloned(t)
			p := settings()
			fake.On("git", "-C", p.Config, "remote").Prints(c.remote + "\n")
			fake.On("git", "-C", p.Config, "remote", "get-url", c.remote).Prints(c.url + "\n")
			fake.On("git", "-C", p.Config, "config", "--get", "remote."+c.remote+".url").Prints(c.set + "\n")
			fake.On("git", "-C", p.Config, "remote", "get-url", "--push", "--all", c.remote).Prints(c.pushes + "\n")
			fake.On("git", "-C", clone, "remote", "get-url", "origin").Prints(c.url + "\n")
			fake.On("git", "-C", clone, "remote", "get-url", "--push", "--all", "origin").Prints(c.pushes + "\n")
			fake.On("git", "ls-remote", "--quiet", "--exit-code", c.set, "refs/heads/prefs").Prints(head + "\trefs/heads/prefs\n")
			fake.On("git", "clone", "--quiet", "--single-branch", "--branch", "prefs", c.set, clone+".cloning").Does(func() { madeIn(t, clone+".cloning") })
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

// bootSignIn is the boot's sign-in, as kit-config's clone has it: GitHub's
// CLI, for github.com alone; pushURL, a push URL kit-config's remote has
// set, its key as the store's clone takes it.
var (
	bootSignIn = [2]string{"credential.https://github.com.helper", "!gh auth git-credential"}
	pushURL    = [2]string{"remote.origin.pushurl", "git@github.com:someone/kit-config.git"}
)

// The store's git reaches kit-config's remote as kit-config's does: the
// boot's GitHub sign-in in kit-config's own settings goes with each git
// that reaches the remote as the store's cloned, and that and the push URL
// kit-config's remote has set are kept in the clone's own settings, whether
// the branch is there to clone or not yet; the clone then pushes where
// kit-config does. Git prints each setting as its key, then its value,
// ended by a NUL.
func TestTheStoreReachesTheRemoteAsKitConfigDoes(t *testing.T) {
	const url = "https://github.com/someone/kit-config.git"
	options := []string{"-c", bootSignIn[0] + "=" + bootSignIn[1], "-c", pushURL[0] + "=" + pushURL[1]}
	for _, has := range []bool{true, false} {
		clone, fake, settings := cloned(t)
		p := settings()
		aside := clone + ".cloning"
		fake.On("git", "-C", p.Config, "remote", "get-url", "origin").Prints(url + "\n")
		fake.On("git", "-C", p.Config, "config", "--get", "remote.origin.url").Prints(url + "\n")
		fake.On("git", "-C", p.Config, "remote", "get-url", "--push", "--all", "origin").Prints(pushURL[1] + "\n")
		fake.On("git", "-C", clone, "remote", "get-url", "origin").Prints(url + "\n")
		fake.On("git", "-C", clone, "remote", "get-url", "--push", "--all", "origin").Prints(pushURL[1] + "\n")
		fake.On("git", carries(p.Config)...).Prints(bootSignIn[0] + "\n" + bootSignIn[1] + "\x00" + pushURL[0] + "\n" + pushURL[1] + "\x00")
		looks := fake.On("git", slices.Concat(options, []string{"ls-remote", "--quiet", "--exit-code", url, "refs/heads/prefs"})...).Prints(head + "\trefs/heads/prefs\n")
		want := []string{"git clone -c '" + options[1] + "' -c " + options[3] + " --quiet --single-branch --branch prefs " + url + " " + aside}
		fake.On("git", slices.Concat([]string{"clone"}, options, []string{"--quiet", "--single-branch", "--branch", "prefs", url, aside})...).Does(func() { madeIn(t, aside) })
		if !has {
			looks.Exits(2)
			want = []string{
				"git -C " + aside + " config --add " + bootSignIn[0] + " '" + bootSignIn[1] + "'",
				"git -C " + aside + " config --add " + pushURL[0] + " " + pushURL[1],
			}
			fake.On("git", "init", "--quiet", "--initial-branch=prefs", aside).Does(func() { madeIn(t, aside) })
			fake.On("git", "-C", aside, "remote", "add", "-t", "prefs", "origin", url)
			fake.On("git", "-C", aside, "config", "branch.prefs.remote", "origin")
			fake.On("git", "-C", aside, "config", "branch.prefs.merge", "refs/heads/prefs")
			fake.On("git", "-C", aside, "config", "--add", bootSignIn[0], bootSignIn[1])
			fake.On("git", "-C", aside, "config", "--add", pushURL[0], pushURL[1])
		}
		if _, err := p.Saves(t.Context()); err != nil {
			t.Errorf("branch there %v: Saves() = %v", has, err)
		}
		for _, w := range want {
			if calls := fake.Calls(); !slices.Contains(calls, w) {
				t.Errorf("branch there %v: ran %q; want %q", has, calls, w)
			}
		}
	}
}

// A remote with no prefs branch yet, as before any Mac's first capture,
// gives a clone with nothing in it, on the branch and tracking it: that
// capture commits there and its push makes the branch, beside kit-config's
// own. Till the first commit the branch isn't born, and git gives it no
// upstream; after it, its upstream isn't on the remote, git fails the
// fetch, and the remote-tracking ref is forgotten, so it's pushed there.
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
	w.fake.On("git", "-C", w.clone, "for-each-ref", "--format=%(upstream) %(upstream:remoteref)", "refs/heads/prefs").Prints("").
		Then().Prints("refs/remotes/origin/prefs refs/heads/prefs\n")
	w.fake.On("git", "-C", w.clone, "fetch", "--prune", "--quiet", "origin").Exits(128).PrintsToStderr("fatal: couldn't find remote ref refs/heads/prefs\n")
	w.fake.On("git", "-C", w.clone, "update-ref", "-d", "refs/remotes/origin/prefs")
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

// A clone with no commit, made before the branch was, as on a Mac whose
// boot came before any Mac's first capture, is cloned again by a restore,
// or a capture, holding the lock on the store, once another Mac's capture
// has made the branch, so this Mac restores what that one saved. The branch
// is cloned aside, the empty clone in place till it's whole: a clone that
// fails leaves the empty one. With the branch still to be made, or the
// remote out of reach, it's kept, and the restore goes on in it. Git gives
// an unborn branch no upstream, so no pull of the empty clone would ever
// bring the branch in; git lists no branch in a clone with no commit.
func TestAnEmptyCloneTakesTheBranchOnceMade(t *testing.T) {
	const offline = "ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n"
	for _, c := range []struct {
		name       string
		exit       int
		cloneFails bool
	}{{"the branch made", 0, false}, {"the branch made, its clone failing", 0, true}, {"the branch still to be made", 2, false}, {"offline", 128, false}} {
		w := newWorld(t, prefs.Lists{})
		aside := w.clone + ".cloning"
		w.fake.On("git", "-C", w.clone, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads").Prints("").
			Then().Prints("refs/heads/prefs\n")
		looks := w.fake.On("git", "ls-remote", "--quiet", "--exit-code", remote, "refs/heads/prefs").Prints(head + "\trefs/heads/prefs\n")
		if c.exit != 0 {
			looks.Exits(c.exit)
		}
		if c.exit == 128 {
			looks.PrintsToStderr(offline)
		}
		cloning := w.fake.On("git", cloneOf(aside)...).Does(func() {
			if _, err := os.Stat(filepath.Join(w.clone, ".git")); err != nil {
				t.Errorf("%s: cloning with the empty clone gone (%v)", c.name, err)
			}
			madeIn(t, aside)
			w.write(t, filepath.Join(aside, "laptop", "domains", "com.example.app.plist"), stored(t, map[string]any{"A": true}))
		})
		if c.cloneFails {
			cloning.Exits(128).PrintsToStderr(offline)
		}
		w.fake.On("defaults", "export", "com.example.app", "-").Exits(1)
		w.fake.On("git", "-C", w.clone, "rev-parse", "HEAD").Prints("abc123\n")

		report, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.app"}})
		_, statErr := os.Stat(filepath.Join(w.clone, ".git"))
		switch {
		case c.cloneFails:
			if err == nil || clones(w.fake) != 1 || statErr != nil {
				t.Errorf("%s: Restore() = %v, the clone %v; want it failed, the empty clone kept", c.name, err, statErr)
			}
		case c.exit == 0:
			if err != nil || report.Failed["com.example.app"] == "" || clones(w.fake) != 1 {
				t.Errorf("%s: Restore() = %+v, %v, cloned %d times; want the branch cloned once, and restored from", c.name, report, err, clones(w.fake))
			}
		default:
			if err == nil || err.Error() != "no settings stored for laptop" || clones(w.fake) != 0 || statErr != nil {
				t.Errorf("%s: Restore() = %v, cloned %d times, the clone %v; want the empty clone kept, and restored from, finding nothing", c.name, err, clones(w.fake), statErr)
			}
		}
	}
}

// A clone with no commit has no history: git's log there fails, so it's
// never asked.
func TestAnEmptyCloneHasNoHistory(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.fake.On("git", "-C", w.clone, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads")
	if _, err := w.p.History(t.Context(), "", "", 5); !errors.Is(err, prefs.ErrNoHistory) {
		t.Errorf("History() = %v; want ErrNoHistory", err)
	}
	if slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.Contains(c, " log ") }) {
		t.Errorf("ran %q; want git's log unasked", w.fake.Calls())
	}
}

// A clone with no commit that isn't of kit-config's branch is refused,
// never deleted to clone the branch in its place.
func TestAnEmptyCloneElsewhereIsRefused(t *testing.T) {
	w := newWorld(t, prefs.Lists{})
	w.fake.On("git", "-C", w.clone, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads")
	w.fake.On("git", "-C", w.clone, "remote", "get-url", "origin").Prints("git@github.com:someone/prefs.git\n")
	_, err := w.p.Restore(t.Context(), prefs.RestoreOptions{Domains: []string{"com.example.app"}})
	if err == nil || !strings.Contains(err.Error(), " points at github.com:someone/prefs.git, not kit-config's remote") {
		t.Errorf("Restore() = %v; want the clone refused", err)
	}
	if _, statErr := os.Stat(filepath.Join(w.clone, ".git")); statErr != nil || slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.Contains(c, "ls-remote") }) {
		t.Errorf("the clone %v, ran %q; want it kept, the remote unasked", statErr, w.fake.Calls())
	}
}

// A look at the store, as the settings to-do's, takes the branch into a
// clone with no commit once another Mac's capture has made it, holding the
// lock on the store, so a paused Mac never starts afresh, or offers no
// restore, while that Mac's save is there. With the branch still to be
// made, the clone's kept, with no saves.
func TestALookTakesTheBranchOnceMade(t *testing.T) {
	for _, made := range []bool{true, false} {
		clone, fake, settings := cloned(t)
		madeIn(t, clone)
		lock := clone + ".capture.lock"
		locked := false
		branches := fake.On("git", "-C", clone, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads")
		if made {
			branches.Then().Prints("refs/heads/prefs\n")
		}
		fake.On("git", cloneOf(clone+".cloning")...).Does(func() {
			locked = !lockFree(lock)
			madeIn(t, clone+".cloning")
		})
		if !made {
			fake.On("git", "ls-remote", "--quiet", "--exit-code", remote, "refs/heads/prefs").Exits(2)
		}
		saves, err := settings().Saves(t.Context())
		switch {
		case made && (err != nil || !saved(saves) || clones(fake) != 1 || !locked):
			t.Errorf("the branch made: Saves() = %v, %v, cloned %d times, locked %v; want its last save, the branch cloned once, holding the lock", saves, err, clones(fake), locked)
		case !made && (err != nil || len(saves) != 0 || clones(fake) != 0):
			t.Errorf("the branch still to be made: Saves() = %v, %v, cloned %d times; want none, nothing cloned", saves, err, clones(fake))
		}
	}
}

// A clone that isn't of kit-config's prefs branch, as one of the repository
// the store was in before, is never captured into, restored from, read, or
// taken over: kit names the clone, what it points at, and what it should.
func TestACloneElsewhereIsRefused(t *testing.T) {
	const moveAside = "move the clone aside for kit to clone the branch there"
	for _, c := range []struct{ name, url, pushURL, on, says string }{
		{"the old repository", "git@github.com:someone/prefs.git", "git@github.com:someone/prefs.git", "refs/heads/main\n",
			" points at github.com:someone/prefs.git, not kit-config's remote (github.com:someone/kit-config.git): apps' settings are saved on its prefs branch now, so " + moveAside},
		{"no remote", "", "", "refs/heads/prefs\n",
			" points at no remote, not kit-config's remote (github.com:someone/kit-config.git): apps' settings are saved on its prefs branch now, so " + moveAside},
		{"pushing elsewhere", remote, "git@github.com:someone/public-prefs.git", "refs/heads/prefs\n",
			" pushes to github.com:someone/public-prefs.git, where kit-config pushes to github.com:someone/kit-config.git: " + moveAside},
		{"kit-config's own branch", remote, remote, "refs/heads/main\n", " is on main, not kit-config's prefs branch: " + moveAside},
		{"no branch", remote, remote, "", " is on no branch, not kit-config's prefs branch: " + moveAside},
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
			{"history", func(ctx context.Context, p *prefs.Prefs) error { _, err := p.History(ctx, "", "", 5); return err }},
			{"saved for an app", func(ctx context.Context, p *prefs.Prefs) error {
				_, err := p.SavedFor(ctx, "com.example.app")
				return err
			}},
			{"take over", func(ctx context.Context, p *prefs.Prefs) error { return p.TakeOver(ctx) }},
		} {
			w := newWorld(t, prefs.Lists{})
			writeRecord(t, w.state, `{"on": "restored"}`)
			w.write(t, filepath.Join(w.clone, "laptop", "domains", "com.example.app.plist"), stored(t, map[string]any{"A": true}))
			w.write(t, filepath.Join(w.clone, "laptop", "owner"), "hardware HW-OLD\nmodel Mac1,1\n")
			url := w.fake.On("git", "-C", w.clone, "remote", "get-url", "origin").Prints(c.url + "\n")
			if c.url == "" {
				url.Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
			}
			w.fake.On("git", "-C", w.clone, "remote", "get-url", "--push", "--all", "origin").Prints(c.pushURL + "\n")
			branch := w.fake.On("git", "-C", w.clone, "symbolic-ref", "-q", "HEAD").Prints(c.on)
			if c.on == "" {
				branch.Exits(1)
			}
			if err := do.run(t.Context(), w.p); err == nil || err.Error() != w.clone+c.says {
				t.Errorf("%s, %s: %v; want %q", c.name, do.name, err, w.clone+c.says)
			}
			for _, call := range w.fake.Calls() {
				if slices.ContainsFunc([]string{" fetch ", " commit ", " push ", " log ", "defaults "}, func(s string) bool { return strings.Contains(call, s) }) {
					t.Errorf("%s, %s: ran %s; want nothing pulled, saved, restored or read", c.name, do.name, call)
				}
			}
			if got := w.read(t, filepath.Join(w.clone, "laptop", "owner")); got != "hardware HW-OLD\nmodel Mac1,1\n" {
				t.Errorf("%s, %s: the owner file says %q; want it as it was", c.name, do.name, got)
			}
		}
	}
}

// A clone part way through a rebase, as one cut short, is on no branch till
// it's finished, and holds commits not yet pushed: it's refused as the
// rebase under way, never as a clone to move aside. Git's symbolic-ref
// prints nothing then, exiting 1.
func TestARebaseCutShortIsRefusedAsSuch(t *testing.T) {
	const says = ": a rebase is under way in the repository (rebase-merge): kit's change waits till it's finished"
	for name, run := range map[string]func(context.Context, *prefs.Prefs) error{
		"capture": func(ctx context.Context, p *prefs.Prefs) error { _, err := p.Capture(ctx); return err },
		"history": func(ctx context.Context, p *prefs.Prefs) error { _, err := p.History(ctx, "", "", 5); return err },
	} {
		w := newWorld(t, prefs.Lists{})
		writeRecord(t, w.state, `{"on": "restored"}`)
		if err := os.MkdirAll(filepath.Join(w.clone, ".git", "rebase-merge"), 0o700); err != nil {
			t.Fatal(err)
		}
		w.fake.On("git", "-C", w.clone, "symbolic-ref", "-q", "HEAD").Exits(1)
		if err := run(t.Context(), w.p); err == nil || err.Error() != "the store's clone, "+w.clone+says {
			t.Errorf("%s: %v; want the rebase named", name, err)
		}
	}
}
