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

// Changes reads status whole, whatever the user's config says: a staged
// rename as its two sides, named with an arrow; unicode unquoted; new
// files hidden by status.showUntrackedFiles=no; a file added then gone is
// no change; one staged as deleted and back untracked is edited; one
// staged as deleted and on disk with no untracked twin, as git ignores it,
// is untracked; a nested repository is one, by its folder, apart from a
// file of the same name that was tracked. Lines are counted against the
// last commit, in kit's own index of it, whatever's staged: a file new to
// it, by its own lines. What git prints is what real git does.
func TestChanges(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{"kept": "k\n", "n ew": "n\nw\n", "r s": "1\n2\n3\n4\n5\n6\n", "secret": "s\n", "x/in": "in\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := func(args ...string) []string { return append([]string{"-C", root}, args...) }
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: in("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")}).
		Prints(" M café\x00AD gone\x00D  kept\x00D  p -> q\x00A  r s\x00D  secret\x00D  x\x00?? kept\x00?? n ew\x00?? nested/\x00?? x/\x00")
	s.expect(call{args: in("rev-parse", "--verify", "--quiet", "HEAD")}).Prints(base + "\n")
	s.expect(call{args: in("read-tree", base), index: readIndex})
	s.expect(call{args: in("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD"), index: readIndex}).Prints("1\t0\tcafé\x000\t6\tp -> q\x001\t1\tx\x00")
	got, err := gitrepo.Repo{Dir: root, Run: fake}.Changes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []gitrepo.Change{
		{Path: "café", State: gitrepo.Edited, Added: 1},
		{Path: "kept", State: gitrepo.Edited},
		{Path: "p -> q", State: gitrepo.Deleted, Removed: 6},
		{Path: "r s", State: gitrepo.Added, Added: 6},
		{Path: "secret", State: gitrepo.Untracked},
		{Path: "x", State: gitrepo.Deleted, Added: 1, Removed: 1},
		{Path: "n ew", State: gitrepo.Added, Added: 2},
		{Path: "nested/", State: gitrepo.Repository},
		{Path: "x/", State: gitrepo.Repository},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Changes() = %+v\nwant %+v", got, want)
	}
	checkCalls(t, "changes", fake, "", s.want)
}

// Diff is a file's change on disk against the last commit, in kit's own
// index of it, whatever's staged: real git shows a file staged then edited
// whole. Before the first commit there's none.
func TestDiff(t *testing.T) {
	const diff = "diff --git a/f b/f\nindex 422c2b7..d68dd40 100644\n--- a/f\n+++ b/f\n@@ -1,2 +1,4 @@\n a\n b\n+c\n+d\n"
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: headOf}).Prints(base + "\n")
	s.expect(call{args: git("read-tree", base), index: readIndex})
	s.expect(call{args: git("--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "HEAD", "--", "f"), index: readIndex}).Prints(diff)
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).Diff(context.Background(), "f"); got != diff || err != nil {
		t.Errorf("Diff() = %q, %v; want %q", got, err, diff)
	}
	checkCalls(t, "diff", fake, "", s.want)

	fake = runnertest.New(t)
	fake.On("git", headOf...).Exits(1)
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).Diff(context.Background(), "f"); got != "" || err != nil || len(fake.Calls()) != 1 {
		t.Errorf("Diff() before the first commit = %q, %v, running %q", got, err, fake.Calls())
	}
}

// Track puts a file the user untracked back in git's index as last
// committed, the file on disk left as it is: real git then shows it
// edited.
func TestTrack(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("--literal-pathspecs", "restore", "--source=HEAD", "--staged", "--", "secret*")...)
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Track(context.Background(), "secret*"); err != nil || len(fake.Calls()) != 1 {
		t.Errorf("Track() = %v, running %q", err, fake.Calls())
	}
}
