package gitrepo_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const dir = "/Users/someone/.config/kit"

func git(args ...string) []string { return append([]string{"-C", dir}, args...) }

func TestCommit(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--", "brew.laptop")...).Prints(" M brew.laptop\n")
	fake.On("git", git("add", "--", "brew.laptop")...)
	fake.On("git", git("commit", "--quiet", "-m", "kit add brew jq (laptop)", "--", "brew.laptop")...)

	committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit add brew jq (laptop)", "brew.laptop")
	if err != nil || !committed {
		t.Fatalf("Commit() = %v, %v; want it committed", committed, err)
	}
	want := []string{
		"git -C " + dir + " status --porcelain -- brew.laptop",
		"git -C " + dir + " add -- brew.laptop",
		"git -C " + dir + " commit --quiet -m 'kit add brew jq (laptop)' -- brew.laptop",
	}
	if got := fake.Calls(); !slices.Equal(got, want) {
		t.Errorf("ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCommitWithNothingToCommit(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--", "brew")...)
	committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit add brew jq", "brew")
	if err != nil || committed || len(fake.Calls()) != 1 {
		t.Errorf("Commit() = %v, %v, running %q; want nothing committed, and no error", committed, err, fake.Calls())
	}
}

func TestCommitFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--", "brew")...).Prints(" M brew\n")
	fake.On("git", git("add", "--", "brew")...)
	fake.On("git", git("commit", "--quiet", "-m", "kit add brew jq", "--", "brew")...).Exits(1).PrintsToStderr("error: gpg failed to sign the data")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit add brew jq", "brew"); err == nil || !strings.Contains(err.Error(), "gpg failed to sign") {
		t.Errorf("Commit() error = %v, want git's", err)
	}
}

func TestPush(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...)
	fake.On("git", git("push", "--quiet")...)
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if got := len(fake.Calls()); got != 3 {
		t.Errorf("ran %q, want remote, pull, push", fake.Calls())
	}
}

func TestPushAbortsAConflictingRebase(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...).Exits(1).PrintsToStderr("CONFLICT (content): Merge conflict in brew")
	fake.On("git", git("rebase", "--abort")...)

	err := gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), "conflict with this Mac's, so nothing was pushed; the rebase was undone") {
		t.Errorf("Push() error = %v, want the conflict reported", err)
	}
	if got := fake.Calls(); got[len(got)-1] != "git -C "+dir+" rebase --abort" || slices.Contains(got, "git -C "+dir+" push --quiet") {
		t.Errorf("ran %q, want the rebase aborted and nothing pushed", got)
	}
}

func TestPushFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...).Exits(1).PrintsToStderr("fatal: unable to access")
	fake.On("git", git("rebase", "--abort")...).Exits(128).PrintsToStderr("fatal: No rebase in progress?")
	err := gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), "couldn't bring in the remote's changes, so nothing was pushed") {
		t.Errorf("Push() error = %v, want the pull's failure", err)
	}

	fake = runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...)
	fake.On("git", git("push", "--quiet")...).Exits(1).PrintsToStderr("! [rejected]")
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); err == nil || !strings.Contains(err.Error(), "couldn't push") {
		t.Errorf("Push() error = %v, want the push's failure", err)
	}
}

func TestPushWithoutARemote(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...)
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("Push() error = %v, want ErrNoRemote", err)
	}
}
