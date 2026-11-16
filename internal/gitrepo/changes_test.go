package gitrepo_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// numstat is git's count of the lines each file on disk adds and removes
// against the last commit, as git's index has the files.
var numstat = git("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD")

// Changes reads status whole, whatever the user's config says: a staged
// rename as its two sides, named with an arrow; unicode unquoted; new
// files hidden by status.showUntrackedFiles=no. What's changed is what
// differs on disk from the last commit, as git's own index has it, with the
// lines it adds and removes: a file new to it by its own lines; a file
// added then gone is none, and so is one whose difference is only in git's
// index, its file on disk as last committed. A file taken out of git's
// index, kept on disk and ignored, is deleted, as git reads it; back
// untracked, it's edited. A nested repository is one, by its folder, apart
// from a file of the same name that was tracked. What git prints is what
// real git does.
func TestChanges(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{"n ew": "n\nw\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fake := runnertest.New(t)
	s := &sequence{fake: fake, root: root}
	s.expect(statusOf()...).
		Prints(" M café\x00AD gone\x00D  hidden\x00 A ita\x00D  kept\x00D  p -> q\x00A  r s\x00MM same\x00D  secret\x00D  twin\x00D  x\x00?? kept\x00?? n ew\x00?? nested/\x00?? twin\x00?? x/\x00")
	s.expect(headOf...).Prints(base + "\n")
	s.expect(numstat...).Prints("1\t0\tcafé\x000\t1\thidden\x003\t0\tita\x000\t1\tkept\x000\t6\tp -> q\x006\t0\tr s\x000\t1\tsecret\x000\t1\ttwin\x000\t1\tx\x00")
	got, err := gitrepo.Repo{Dir: root, Run: fake}.Changes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []gitrepo.Change{
		{Path: "café", State: gitrepo.Edited, Added: 1},
		{Path: "hidden", State: gitrepo.Deleted, Removed: 1},
		{Path: "ita", State: gitrepo.Added, Added: 3},
		{Path: "kept", State: gitrepo.Edited, Removed: 1},
		{Path: "p -> q", State: gitrepo.Deleted, Removed: 6},
		{Path: "r s", State: gitrepo.Added, Added: 6},
		{Path: "secret", State: gitrepo.Deleted, Removed: 1},
		{Path: "twin", State: gitrepo.Edited, Removed: 1},
		{Path: "x", State: gitrepo.Deleted, Removed: 1},
		{Path: "n ew", State: gitrepo.Added, Added: 2},
		{Path: "nested/", State: gitrepo.Repository},
		{Path: "x/", State: gitrepo.Repository},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Changes() = %+v\nwant %+v", got, want)
	}
	s.check(t, "changes")

	// Counting fails: an error, never every file shown as committed.
	fake = runnertest.New(t)
	fake.On("git", statusOf()...).Prints(" M café\x00")
	fake.On("git", headOf...).Prints(base + "\n")
	fake.On("git", numstat...).Exits(128).PrintsToStderr("fatal: unable to read 0e0bf17a")
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).Changes(context.Background()); err == nil {
		t.Errorf("Changes(), counting failing = %+v; want an error", got)
	}

	// Before the first commit, every file is new, counted by its lines.
	fake = runnertest.New(t)
	s = &sequence{fake: fake, root: root}
	s.expect(statusOf()...).Prints("?? n ew\x00")
	s.expect(headOf...).Exits(1)
	got, err = gitrepo.Repo{Dir: root, Run: fake}.Changes(context.Background())
	if want := []gitrepo.Change{{Path: "n ew", State: gitrepo.Added, Added: 2}}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Changes() before the first commit = %+v, %v; want %+v", got, err, want)
	}
	s.check(t, "before the first commit")
}

// Diff is a file's change on disk against the last commit, whatever's
// staged, as git shows it: real git shows a file staged then edited whole.
// Before the first commit there's none.
func TestDiff(t *testing.T) {
	const diff = "diff --git a/f b/f\nindex 422c2b7..d68dd40 100644\n--- a/f\n+++ b/f\n@@ -1,2 +1,4 @@\n a\n b\n+c\n+d\n"
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(headOf...).Prints(base + "\n")
	s.expect(git("--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "HEAD", "--", "f")...).Prints(diff)
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).Diff(context.Background(), "f"); got != diff || err != nil {
		t.Errorf("Diff() = %q, %v; want %q", got, err, diff)
	}
	s.check(t, "diff")

	fake = runnertest.New(t)
	fake.On("git", headOf...).Exits(1)
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).Diff(context.Background(), "f"); got != "" || err != nil || len(fake.Calls()) != 1 {
		t.Errorf("Diff() before the first commit = %q, %v, running %q", got, err, fake.Calls())
	}
}

// Restore puts a file back as last committed, staged and on disk, taken
// as it's named.
func TestRestore(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(git("--literal-pathspecs", "restore", "--source=HEAD", "--staged", "--worktree", "--", "secret*")...)
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Restore(context.Background(), "secret*"); err != nil {
		t.Errorf("Restore() = %v", err)
	}
	s.check(t, "restore")
}
