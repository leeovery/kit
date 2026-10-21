package gitrepo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The commands of a push.
var (
	wholeStatus = git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")
	incomingOf  = git("diff", "--name-only", "-z", "--no-renames", "HEAD...@{u}")
	stashRef    = git("rev-parse", "--verify", "--quiet", "refs/stash")
	stashPush   = git("stash", "push", "--quiet", "--message", "kit: set aside while rebasing onto the remote")
	stashPop    = git("stash", "pop", "--index", "--quiet", "stash@{0}")
	rebase      = git("rebase", "--quiet")
	abort       = git("rebase", "--abort")
	pushIt      = git("push", "--quiet")
)

// The stashes the tests' git names: the user's own, and the one kit makes.
const (
	theirStash = "1111111111111111111111111111111111111111"
	kitsStash  = "bfcfca69ab8231f56369d4652e7ff0a5ffc81478"
)

// pushing scripts a push as far as the rebase, as real git answers: the
// remote; the git folder, and what's changed and not committed (status,
// -z); the fetch, and the upstream's commits HEAD hasn't (behind).
func pushing(fake *runnertest.Fake, gitDir, status, behind string) *sequence {
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	s.expect(call{args: wholeStatus}).Prints(status)
	s.expect(call{args: git("fetch", "--quiet")})
	s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Prints(behind + "\n")
	return s
}

// stashes scripts git stash push, git naming before as its latest stash
// before it, after after it, "" for none.
func (s *sequence) stashes(before, after string) {
	names(s.expect(call{args: stashRef}), before)
	s.expect(call{args: stashPush})
	names(s.expect(call{args: stashRef}), after)
}

// pops scripts the look at git's latest stash, latest, then, if it's kit's,
// its pop, and is that.
func (s *sequence) pops(latest string) *runnertest.Script {
	names(s.expect(call{args: stashRef}), latest)
	if latest != kitsStash {
		return nil
	}
	return s.expect(call{args: stashPop})
}

// names has rev-parse --verify --quiet name stash, or none when "", as it
// says by exiting 1.
func names(script *runnertest.Script, stash string) {
	if stash == "" {
		script.Exits(1)
		return
	}
	script.Prints(stash + "\n")
}

// rebasing has script start a rebase that stops part way, as git leaves
// one, its folder in gitDir; aborting, script undoes it.
func rebasing(script *runnertest.Script, gitDir string, stops bool) {
	script.Does(func() {
		path := filepath.Join(gitDir, "rebase-merge")
		if stops {
			_ = os.Mkdir(path, 0o700)
			return
		}
		_ = os.Remove(path)
	})
}

func push(t *testing.T, fake *runnertest.Fake) error {
	t.Helper()
	return gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
}

// With nothing new on the remote, Push pushes, touching nothing else: real
// git's staged work stays staged.
func TestPush(t *testing.T) {
	fake := runnertest.New(t)
	s := pushing(fake, t.TempDir(), "M  base\x00AM unst\x00", "0")
	s.expect(call{args: pushIt})
	if err := push(t, fake); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	checkCalls(t, "nothing new", fake, "", s.want)
}

// While git is part way through a merge, cherry-pick, revert, rebase or
// am, Push refuses before it fetches, as setting what's not committed
// aside would end it: real git's stash push takes away MERGE_HEAD and
// CHERRY_PICK_HEAD. A conflict waiting to be resolved is refused too.
func TestPushRefusesWhileGitIsPartWay(t *testing.T) {
	for _, file := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		gitDir := t.TempDir()
		if err := os.Mkdir(filepath.Join(gitDir, file), 0o700); err != nil {
			t.Fatal(err)
		}
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints("origin\n")
		s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
		if err := push(t, fake); err == nil || !strings.Contains(err.Error(), "under way in the repository ("+file+"): kit's change waits till it's finished") {
			t.Errorf("Push() with %s = %v, want it refused", file, err)
		}
		checkCalls(t, file, fake, "", s.want)
	}

	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
	s.expect(call{args: wholeStatus}).Prints("UU f\x00")
	if err := push(t, fake); err == nil || err.Error() != "a conflict in f is waiting to be resolved: kit's change waits till it's finished" {
		t.Errorf("Push() with a conflict waiting = %v, want it refused", err)
	}
	checkCalls(t, "a conflict waiting", fake, "", s.want)
}

// With the remote ahead, what's committed here is rebased onto it. Work not
// committed, none of it changed by the remote's commits, is set aside in a
// stash of its own first and put back after, by that stash, staged or not,
// as real git keeps it; untracked files alone, which git stash leaves, set
// nothing aside; a stash of the user's own, there before, is never
// touched.
func TestPushRebasesOntoTheRemote(t *testing.T) {
	for _, c := range []struct {
		name, status, incoming string
		before, after          string
		stashes                bool
	}{
		{"nothing changed here", "", "", "", "", false},
		{"work set aside and put back", "M  base\x00 M shared\x00", "r\x00", "", kitsStash, true},
		{"untracked files alone", "?? notes\x00", "r\x00", "", "", false},
		{"the user's own stash, work set aside and put back", "M  base\x00", "r\x00", theirStash, kitsStash, true},
		{"the user's own stash, nothing git stashes", "MM base\x00", "r\x00", theirStash, theirStash, true},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), c.status, "1")
		if c.status != "" {
			s.expect(call{args: incomingOf}).Prints(c.incoming)
		}
		if c.stashes {
			s.stashes(c.before, c.after)
		}
		s.expect(call{args: rebase})
		if c.after == kitsStash {
			s.pops(kitsStash)
		}
		s.expect(call{args: pushIt})
		if err := push(t, fake); err != nil {
			t.Errorf("%s: Push() error = %v", c.name, err)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// With the remote's commits changing a file changed here and not
// committed, or one in a repository nested here, putting it back after
// rebasing would conflict, leaving git's markers in it, as real git does:
// nothing's set aside or rebased, nothing pushed, and Push names the files.
func TestPushLeavesAClashAlone(t *testing.T) {
	for _, c := range []struct{ name, status, incoming, want string }{
		{"a file edited here", " M f\x00?? notes\x00", "f\x00g\x00", "changed on another Mac, and changed here, not committed: f; so nothing was touched or pushed: commit those changes or put them aside, and kit pushes then"},
		{"a file new here", "?? r\x00", "r\x00", "changed on another Mac, and changed here, not committed: r;"},
		{"a nested repository", "?? vendor/tool/\x00", "vendor/tool/x\x00", "changed on another Mac, and changed here, not committed: vendor/tool/x;"},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), c.status, "1")
		s.expect(call{args: incomingOf}).Prints(c.incoming)
		if err := push(t, fake); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// A stash that won't go back is kept, never dropped, and nothing's pushed:
// one real git leaves whole, the tree clean, as when it can't put back
// what was staged, said to be wholly in the stash; one it put back with
// conflicts, its markers in a file, said so, naming the file and what to
// run; one no longer git's latest left be, never popped.
func TestPushKeepsAStashThatWontGoBack(t *testing.T) {
	for _, c := range []struct {
		name, latest, after string
		exit                int
		want                string
	}{
		{"its staged part won't go back", kitsStash, "", 128, `what wasn't committed is wholly in git's stash, ` + kitsStash + ` ("kit: set aside while rebasing onto the remote"), as it couldn't be put back, so nothing was pushed: git stash apply ` + kitsStash + ` puts it back`},
		{"back with conflicts", kitsStash, "UU f\x00 M g\x00", 1, "what wasn't committed came back with conflicts in f: each holds the remote's version and this Mac's, between git's <<<<<<< Updated upstream and >>>>>>> Stashed changes lines, so nothing was pushed: make each what it should be, git add it, then git stash drop, as git's stash, " + kitsStash + ", still holds it all"},
		{"no longer git's latest", theirStash, "", 0, "what wasn't committed is wholly in git's stash, " + kitsStash},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), " M f\x00 M g\x00", "1")
		s.expect(call{args: incomingOf}).Prints("r\x00")
		s.stashes("", kitsStash)
		s.expect(call{args: rebase})
		if pop := s.pops(c.latest); pop != nil {
			pop.Exits(c.exit).PrintsToStderr("error: conflicts in index. Try without --index.")
			// What's left once it won't go back.
			s.expect(call{args: wholeStatus}).Prints(c.after)
		}
		if err := push(t, fake); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// A rebase that stops on a conflict is aborted, by git's own account of it
// under way, the work set aside put back, and nothing pushed. One git
// couldn't abort, still under way, keeps the stash, named, never putting it
// back onto it; one that never started, as when a pre-rebase hook refuses
// or an untracked file's in the way, leaves nothing to abort, and the work
// goes back. What git does is what real git does.
func TestPushWhenTheRebaseStops(t *testing.T) {
	for _, c := range []struct {
		name            string
		starts, aborted bool
		want            string
	}{
		{"a conflict", true, true, "the remote's changes conflict with this Mac's, so nothing was pushed; the rebase was undone"},
		{"a conflict git couldn't abort", true, false, "git rebase --abort undoes it"},
		{"never started", false, false, "couldn't rebase onto the remote's changes, so nothing was pushed"},
	} {
		gitDir := t.TempDir()
		fake := runnertest.New(t)
		s := pushing(fake, gitDir, " M f\x00", "1")
		s.expect(call{args: incomingOf}).Prints("r\x00")
		s.stashes("", kitsStash)
		rebasing(s.expect(call{args: rebase}).Exits(1).PrintsToStderr("error: could not apply 7ff9573... kit"), gitDir, c.starts)
		if c.starts {
			ab := s.expect(call{args: abort})
			if c.aborted {
				rebasing(ab, gitDir, false)
			} else {
				ab.Exits(128).PrintsToStderr("fatal: could not move back to 22f46b0")
			}
		}
		if c.aborted || !c.starts {
			s.pops(kitsStash)
		}
		err := push(t, fake)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		if kept := strings.Contains(err.Error(), kitsStash); kept == (c.aborted || !c.starts) {
			t.Errorf("%s: Push() error = %v; the stash named %v", c.name, err, kept)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

func TestPushFails(t *testing.T) {
	fetchFails := func(stderr string) func(*runnertest.Fake) *sequence {
		return func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
			s.expect(call{args: wholeStatus})
			s.expect(call{args: git("fetch", "--quiet")}).Exits(128).PrintsToStderr(stderr)
			return s
		}
	}
	for _, c := range []struct {
		name, want string
		script     func(fake *runnertest.Fake) *sequence
	}{
		{"offline, over https", "couldn't reach the remote, so nothing was pushed", fetchFails("fatal: unable to access 'https://github.com/someone/kit-config.git/': Could not resolve host: github.com")},
		{"offline, over ssh", "couldn't reach the remote, so nothing was pushed", fetchFails("ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.")},
		{"the remote gone", "couldn't bring in the remote's changes, so nothing was pushed", fetchFails("fatal: 'someone/kit-config.git' does not appear to be a git repository")},
		{"no upstream", "couldn't bring in the remote's changes, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
			s.expect(call{args: wholeStatus})
			s.expect(call{args: git("fetch", "--quiet")})
			s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Exits(128).PrintsToStderr("fatal: no upstream configured for branch 'main'")
			return s
		}},
		{"nothing set aside", "couldn't set aside what's not committed, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := pushing(fake, t.TempDir(), " M f\x00", "1")
			s.expect(call{args: incomingOf}).Prints("r\x00")
			s.expect(call{args: stashRef}).Exits(1)
			s.expect(call{args: stashPush}).Exits(1).PrintsToStderr("fatal: Unable to create '.git/index.lock': File exists.")
			return s
		}},
		{"the push refused", "couldn't push", func(fake *runnertest.Fake) *sequence {
			s := pushing(fake, t.TempDir(), "", "0")
			s.expect(call{args: pushIt}).Exits(1).PrintsToStderr("! [rejected]")
			return s
		}},
	} {
		fake := runnertest.New(t)
		s := c.script(fake)
		err := push(t, fake)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.HasPrefix(c.name, "offline") && strings.Contains(err.Error(), "fatal") {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

func TestPushWithoutARemote(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...)
	if err := push(t, fake); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("Push() error = %v, want ErrNoRemote", err)
	}
}

// Unpushed counts the commits waiting for the upstream: none without one,
// as git says when there isn't.
func TestUnpushed(t *testing.T) {
	for _, c := range []struct {
		prints string
		exit   int
		want   int
	}{{"1\n", 0, 1}, {"0\n", 0, 0}, {"", 128, 0}} {
		fake := runnertest.New(t)
		fake.On("git", git("rev-list", "--count", "@{u}..HEAD")...).Prints(c.prints).Exits(c.exit).PrintsToStderr(map[bool]string{true: "fatal: no upstream configured for branch 'main'"}[c.exit != 0])
		if got := (gitrepo.Repo{Dir: dir, Run: fake}).Unpushed(context.Background()); got != c.want {
			t.Errorf("Unpushed() with git printing %q, exit %d = %d, want %d", c.prints, c.exit, got, c.want)
		}
	}
}
