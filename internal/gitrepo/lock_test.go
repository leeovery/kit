package gitrepo_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// commitLock is the file in the git folder kit's own lock on committing is
// on, and marked the mark it leaves before HEAD moves.
const (
	commitLock = "kit-commit.lock"
	marked     = "kit-index.made"
)

// stopped is the commit a run of kit's that stopped had moved HEAD onto.
const stopped = "5d1e0c4b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d"

// mark is kit's mark of a lock filled with index before HEAD moves onto
// made: the commit, and the lock's hash.
func mark(made, index string) string {
	sum := sha256.Sum256([]byte(index))
	return made + " " + hex.EncodeToString(sum[:]) + "\n"
}

// lockCommits takes the lock on committing in gitDir as another kit does,
// failing the test if it can't, and is the file to close to let go.
func lockCommits(t *testing.T, gitDir string) *os.File {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(gitDir, commitLock), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("another kit couldn't lock committing: %v", err)
	}
	return f
}

// commitsLocked reports whether a kit holds the lock on committing in
// gitDir, as another would find it.
func commitsLocked(t *testing.T, gitDir string) bool {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(gitDir, commitLock), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	return errors.Is(syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), syscall.EWOULDBLOCK)
}

// A commit holds kit's own lock on committing in the repository all the
// while, a kernel lock on a file of kit's in the git folder, so no other
// kit commits there meanwhile: while another holds it, kit waits a while,
// then refuses, touching nothing; once the other lets go, kit commits. The
// file stays, let go of, never swept, as a kit waiting for it would go on
// to lock a file gone.
func TestCommitHoldsKitsOwnLock(t *testing.T) {
	gitDir := gitFolder(t, false)
	other := lockCommits(t, gitDir)
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	var tries int
	repo := gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) { tries++ }}
	out, err := repo.Commit(context.Background(), "kit test", "brew")
	if want := "another kit is committing in the repository, so kit's change waits till it's done"; out.Committed || err == nil || err.Error() != want || tries < 2 {
		t.Errorf("Commit(), another kit committing = %+v, %v, after %d tries; want %q, after a few", out, err, tries, want)
	}
	checkCalls(t, "another kit committing", fake, gitDir, s.want)
	checkGitFolder(t, "another kit committing", gitDir, usersEntries)

	fake = runnertest.New(t)
	want := anEdit.script(t, fake, gitDir)
	letGo := func(time.Duration) { _ = other.Close() }
	committed, err := didCommit(gitrepo.Repo{Dir: dir, Run: fake, Sleep: letGo}.Commit(context.Background(), "kit test", anEdit.paths...))
	anEdit.check(t, committed, err)
	checkCalls(t, "once the other kit lets go", fake, gitDir, want)
	anEdit.checkFolder(t, gitDir)
	if _, err := os.Stat(filepath.Join(gitDir, commitLock)); err != nil || commitsLocked(t, gitDir) {
		t.Errorf("kit's commit lock, once it's committed: %v, held %v; want its file there, let go of", err, commitsLocked(t, gitDir))
	}
}

// A lock holding the index a run of kit's left, stopped once HEAD had taken
// its commit and before the lock took the index's place, is put in its
// place, as that run would have, and said, the commit then going ahead from
// that index: kit holding its own lock on committing, no other kit is at
// work, and the run's mark names HEAD's commit and the lock's very
// contents. Any other lock is left as it is, and named, with what to do
// should no git be running: one with no mark, one HEAD has moved on from,
// one holding other than the mark's. A lock kit put in place that another
// git then takes before kit does is said too, with why kit's change waits.
func TestCommitFinishesTheIndexAStoppedRunLeft(t *testing.T) {
	const left = "the index a run of kit's left"
	for _, c := range []struct {
		name, mark, head string
		// taken is whether another git takes the lock before kit, once
		// it's put in place.
		taken, finished bool
	}{
		{name: "a run of kit's that stopped", mark: mark(stopped, left), head: stopped, finished: true},
		{name: "no mark"},
		{name: "HEAD moved on", mark: mark(stopped, left), head: base},
		{name: "a lock holding other than the mark's", mark: mark(stopped, "another git's index")},
		{name: "taken by another git once put in place", mark: mark(stopped, left), head: stopped, taken: true, finished: true},
	} {
		gitDir := gitFolder(t, false)
		lock := filepath.Join(gitDir, "index.lock")
		if err := os.WriteFile(lock, []byte(left), 0o644); err != nil {
			t.Fatal(err)
		}
		if c.mark != "" {
			if err := os.WriteFile(filepath.Join(gitDir, marked), []byte(c.mark), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if c.taken {
			// The index a link to the lock, its rename leaves the lock
			// there, as when another git takes it first.
			if err := errors.Join(os.Remove(filepath.Join(gitDir, "index")), os.Link(lock, filepath.Join(gitDir, "index"))); err != nil {
				t.Fatal(err)
			}
		}
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
		s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
		if c.head != "" {
			s.expect(call{args: headOf}).Prints(c.head + "\n")
		}
		if c.finished && !c.taken {
			s.expect(call{args: headOf}).Prints(stopped + "\n").Does(func() {
				if data, _ := os.ReadFile(filepath.Join(gitDir, "index")); string(data) != left {
					t.Errorf("%s: kit committed from an index holding %q, want the one the run left", c.name, data)
				}
			})
			s.attempt(gitDir, stopped, "brew\x00")
		}
		out, err := gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) {}}.Commit(context.Background(), "kit test", "brew")
		checkCalls(t, c.name, fake, gitDir, s.want)
		switch {
		case out.Finished != c.finished:
			t.Errorf("%s: Commit() = %+v, %v; want finished %v", c.name, out, err, c.finished)
		case c.finished && !c.taken:
			if !out.Committed || err != nil {
				t.Errorf("%s: Commit() = %+v, %v; want committed", c.name, out, err)
			}
			checkGitFolder(t, c.name, gitDir, resetEntries)
		case out.Committed || err == nil || !strings.HasSuffix(err.Error(), "remove "+lock):
			t.Errorf("%s: Commit() = %+v, %v; want the lock named, and what to do", c.name, out, err)
		case !c.taken:
			if data, _ := os.ReadFile(lock); string(data) != left {
				t.Errorf("%s: the lock holds %q, want it left as it was", c.name, data)
			}
		}
	}
}

// A lock new as kit asks for the index is said to be made just now.
func TestCommitSaysALockIsNew(t *testing.T) {
	gitDir := gitFolder(t, false, "index.lock")
	fake := runnertest.New(t)
	fake.On("git", statusOf("brew")...).Prints(" M brew\x00")
	fake.On("git", gitDirOf...).Prints(gitDir + "\n")
	_, err := gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) {}}.Commit(context.Background(), "kit test", "brew")
	if err == nil || !strings.Contains(err.Error(), "index.lock, made just now, so") {
		t.Errorf("Commit(), the index locked just now = %v", err)
	}
}

// Kit never puts in place or removes a lock on the index that's no longer
// its own, as when another git removed it and took the index meanwhile, nor
// sweeps its own files away then: committed, it says how to bring the index
// up to date; failing, it leaves that git's lock as it is.
func TestCommitLeavesALockNoLongerItsBe(t *testing.T) {
	const theirs = "another git's lock"
	gitDir := gitFolder(t, false)
	lock := filepath.Join(gitDir, "index.lock")
	// takeIt has another git take the index, git having written kit's own
	// index.
	takeIt := func() {
		if err := errors.Join(os.Remove(lock), os.WriteFile(lock, []byte(theirs), 0o600), os.WriteFile(filepath.Join(filepath.Dir(lock), "kit-index"), nil, 0o600)); err != nil {
			t.Error(err)
		}
	}
	fake := runnertest.New(t)
	s := editing(fake, gitDir, base)
	s.attempt(gitDir, base, "brew\x00").Does(takeIt)
	committed, err := didCommit(gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", "brew"))
	if want := "git's index still shows them as they were, so a git commit would undo them: git -C " + dir + " --literal-pathspecs reset -q -- brew brings it up to date: kit's lock on git's index was removed or replaced by another git meanwhile, so kit left it be"; !committed || err == nil || err.Error() != want {
		t.Errorf("Commit(), the lock taken once HEAD moved = %v, %v; want committed, and %q", committed, err, want)
	}
	checkCalls(t, "the lock taken once HEAD moved", fake, gitDir, s.want)
	if data, _ := os.ReadFile(lock); string(data) != theirs {
		t.Errorf("once HEAD moved, the other git's lock holds %q", data)
	}

	gitDir = gitFolder(t, false)
	lock = filepath.Join(gitDir, "index.lock")
	fake = runnertest.New(t)
	s = editing(fake, gitDir, base)
	s.expect(call{args: git("read-tree", base), index: ownIndex})
	s.expect(call{args: stageIn, input: "brew\x00", index: ownIndex}).Does(takeIt)
	s.expect(call{args: stagedIn, index: ownIndex}).Prints("brew\x00")
	s.expect(call{args: writeIn, index: ownIndex}).Exits(128).PrintsToStderr("fatal: index file smaller than expected\n")
	if committed, err := didCommit(gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", "brew")); committed || err == nil {
		t.Errorf("Commit(), the lock taken, then failing = %v, %v", committed, err)
	}
	checkCalls(t, "the lock taken, then failing", fake, gitDir, s.want)
	checkGitFolder(t, "the lock taken, then failing", gitDir, usersEntries, "index.lock", "kit-index")
	if data, _ := os.ReadFile(lock); string(data) != theirs {
		t.Errorf("the commit failing, the other git's lock holds %q", data)
	}
}
