package gitrepo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The commands of a push.
var (
	stashRef  = git("rev-parse", "--verify", "--quiet", "refs/stash")
	stashPush = git("stash", "push", "--quiet", "--message", "kit: set aside while rebasing onto the remote")
	stashPop  = git("stash", "pop", "--index", "--quiet")
	rebase    = git("rebase", "--quiet")
	pushIt    = git("push", "--quiet")
)

// The stashes the tests' git names: the user's own, and the one kit makes.
const (
	theirStash = "1111111111111111111111111111111111111111"
	kitsStash  = "bfcfca69ab8231f56369d4652e7ff0a5ffc81478"
)

// fetches scripts a push as far as the rebase, as real git answers: the
// remote, the fetch, and the upstream's commits HEAD hasn't, behind; then,
// behind, the stash git names before git stash push and after, "" for
// none.
func fetches(fake *runnertest.Fake, behind, before, after string) *sequence {
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: git("fetch", "--quiet")})
	s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Prints(behind + "\n")
	if behind == "0" {
		return s
	}
	ref := s.expect(call{args: stashRef})
	names(ref, before)
	s.expect(call{args: stashPush})
	s.want = append(s.want, call{args: stashRef})
	names(ref.Then(), after)
	return s
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

// With nothing new on the remote, Push pushes, touching nothing else: real
// git's staged work stays staged.
func TestPush(t *testing.T) {
	fake := runnertest.New(t)
	s := fetches(fake, "0", "", "")
	s.expect(call{args: pushIt})
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	checkCalls(t, "nothing new", fake, "", s.want)
}

// With the remote ahead, what's committed here is rebased onto it, any
// work not committed set aside in a stash of its own first and put back
// after, staged or not, as real git keeps it; a stash of the user's own,
// there before, is never touched.
func TestPushRebasesOntoTheRemote(t *testing.T) {
	for _, c := range []struct {
		name          string
		before, after string
		pops          bool
	}{
		{"work set aside and put back", "", kitsStash, true},
		{"nothing to set aside", "", "", false},
		{"the user's own stash, nothing to set aside", theirStash, theirStash, false},
		{"the user's own stash, work set aside and put back", theirStash, kitsStash, true},
	} {
		fake := runnertest.New(t)
		s := fetches(fake, "1", c.before, c.after)
		s.expect(call{args: rebase})
		if c.pops {
			s.expect(call{args: stashPop})
		}
		s.expect(call{args: pushIt})
		if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); err != nil {
			t.Errorf("%s: Push() error = %v", c.name, err)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// A stash that won't go back, as real git's when the remote changed a file
// staged here, is kept, never dropped, and named; nothing's pushed.
func TestPushKeepsAStashThatWontGoBack(t *testing.T) {
	fake := runnertest.New(t)
	s := fetches(fake, "1", "", kitsStash)
	s.expect(call{args: rebase})
	s.expect(call{args: stashPop}).Exits(128).Prints("The stash entry is kept in case you need it again.\n").PrintsToStderr("error: patch failed: base:1\nerror: base: patch does not apply\nerror: conflicts in index. Try without --index.\n")
	err := gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
	if want := `what wasn't committed is in git's stash, ` + kitsStash + ` ("kit: set aside while rebasing onto the remote"), as it couldn't be put back, so nothing was pushed`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Push() error = %v, want %q", err, want)
	}
	checkCalls(t, "the stash kept", fake, "", s.want)
}

// A rebase that stops on a conflict is aborted, the work set aside put
// back, and nothing pushed; one that can't be aborted keeps the stash,
// named, never putting it back onto a rebase part way.
func TestPushAbortsAConflictingRebase(t *testing.T) {
	fake := runnertest.New(t)
	s := fetches(fake, "1", "", kitsStash)
	s.expect(call{args: rebase}).Exits(1).PrintsToStderr("error: could not apply 7ff9573... kit")
	s.expect(call{args: git("rebase", "--abort")})
	s.expect(call{args: stashPop})
	err := gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), "conflict with this Mac's, so nothing was pushed; the rebase was undone") {
		t.Errorf("Push() error = %v, want the conflict reported", err)
	}
	checkCalls(t, "a conflict", fake, "", s.want)

	fake = runnertest.New(t)
	s = fetches(fake, "1", "", kitsStash)
	s.expect(call{args: rebase}).Exits(1).PrintsToStderr("error: could not apply 7ff9573... kit")
	s.expect(call{args: git("rebase", "--abort")}).Exits(128).PrintsToStderr("fatal: could not move back to HEAD")
	err = gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), "couldn't bring in the remote's changes") || !strings.Contains(err.Error(), kitsStash) {
		t.Errorf("Push() error = %v, want the rebase's failure and the stash named", err)
	}
	checkCalls(t, "a rebase not undone", fake, "", s.want)
}

func TestPushFails(t *testing.T) {
	for _, c := range []struct {
		name, want string
		script     func(fake *runnertest.Fake) *sequence
	}{
		{"the fetch failing", "couldn't bring in the remote's changes, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: git("fetch", "--quiet")}).Exits(128).PrintsToStderr("fatal: unable to access")
			return s
		}},
		{"no upstream", "couldn't bring in the remote's changes, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: git("fetch", "--quiet")})
			s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Exits(128).PrintsToStderr("fatal: no upstream configured for branch 'main'")
			return s
		}},
		{"nothing set aside", "couldn't set aside what's not committed, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: git("fetch", "--quiet")})
			s.expect(call{args: git("rev-list", "--count", "HEAD..@{u}")}).Prints("1\n")
			s.expect(call{args: stashRef}).Exits(1)
			s.expect(call{args: stashPush}).Exits(1).PrintsToStderr("fatal: Unable to create '.git/index.lock': File exists.")
			return s
		}},
		{"the push refused", "couldn't push", func(fake *runnertest.Fake) *sequence {
			s := fetches(fake, "0", "", "")
			s.expect(call{args: pushIt}).Exits(1).PrintsToStderr("! [rejected]")
			return s
		}},
	} {
		fake := runnertest.New(t)
		s := c.script(fake)
		if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

func TestPushWithoutARemote(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...)
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); !errors.Is(err, gitrepo.ErrNoRemote) {
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
