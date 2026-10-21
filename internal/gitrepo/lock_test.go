package gitrepo_test

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
	commitLock = "kit-commits"
	marked     = "kit-mark"
)

// stopped is the commit a run of kit's that stopped was moving HEAD onto,
// and left the index in a lock for.
const (
	stopped = "5d1e0c4b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d"
	left    = "the index a run of kit's left"
)

// readFile is what the file name in gitDir holds: "" when there's none.
func readFile(gitDir, name string) string {
	data, _ := os.ReadFile(filepath.Join(gitDir, name))
	return string(data)
}

// markOf is kit's mark of the lock at path, as it is, before HEAD moves from
// base, "" before the first, onto made, the user's index holding held for
// the files committed: the commits, the lock's hash and its file, by device
// and inode, then what the index held (records).
func markOf(t *testing.T, base, made, path string, held map[string]string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Error(err)
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Error(err)
		return ""
	}
	st := info.Sys().(*syscall.Stat_t)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%s %s %s %d:%d\n", cmp.Or(base, "-"), made, hex.EncodeToString(sum[:]), st.Dev, st.Ino) + records(held)
}

// records is what a mark says the user's index held for each file, held
// by path: in order of path, its entry, "-" for none, a tab and the path,
// each ended by a NUL.
func records(held map[string]string) string {
	var all string
	for _, path := range slices.Sorted(maps.Keys(held)) {
		all += cmp.Or(held[path], "-") + "\t" + path + "\x00"
	}
	return all
}

// writeFile writes data to the file name in gitDir, failing the test if it
// can't.
func writeFile(t *testing.T, gitDir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(gitDir, name), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// leave leaves in gitDir what a run of kit's that stopped once it had
// marked its lock, before HEAD moved from base onto stopped, leaves: the
// lock, holding the index it left, and its mark.
func leave(t *testing.T, gitDir, base string) {
	t.Helper()
	writeFile(t, gitDir, "index.lock", left)
	writeFile(t, gitDir, marked, markOf(t, base, stopped, filepath.Join(gitDir, "index.lock"), map[string]string{"brew": "100644 " + brewBefore + " 0"}))
}

// started scripts a commit of brew, edited, as far as its git folder,
// gitDir.
func started(fake *runnertest.Fake, gitDir string) *sequence {
	s := &sequence{fake: fake}
	s.expect(call{args: statusOf("brew")}).Prints(" M brew\x00")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	return s
}

// head scripts git's answer to which commit HEAD's on: at, or none before
// the first, for "".
func (s *sequence) head(at string) *runnertest.Script {
	if at == "" {
		return s.expect(call{args: headOf}).Exits(1)
	}
	return s.expect(call{args: headOf}).Prints(at + "\n")
}

// goesOn scripts the rest of the commit of brew, edited, onto HEAD at at,
// "" before the first, in gitDir, checking it's made with the user's index
// holding index, and is the script of HEAD's move.
func (s *sequence) goesOn(t *testing.T, name, gitDir, at, index string) *runnertest.Script {
	s.head(at).Does(func() {
		if got := readFile(gitDir, "index"); got != index {
			t.Errorf("%s: kit committed with the user's index holding %q, want %q", name, got, index)
		}
	})
	return s.attempt(gitDir, at, "brew\x00")
}

// commitBrew has Commit commit brew, edited, with fake as git.
func commitBrew(fake *runnertest.Fake) (gitrepo.Outcome, error) {
	return gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) {}}.Commit(context.Background(), "kit test", "brew")
}

// lockCommits takes the lock on committing in gitDir as another kit does,
// failing the test if it can't, and is the file to close to let go.
func lockCommits(t *testing.T, gitDir string) *os.File {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(gitDir, commitLock), os.O_RDONLY|os.O_CREATE, 0o644)
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
	f, err := os.OpenFile(filepath.Join(gitDir, commitLock), os.O_RDONLY|os.O_CREATE, 0o644)
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
	s := started(fake, gitDir)
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

// Kit locks the file at its commit lock's name as it finds it once locked:
// one a cleaner removed, and another kit made anew, while kit waited to
// lock it, kit locks in its turn, never holding a lock no other kit sees.
func TestCommitLocksTheCommitLockThere(t *testing.T) {
	gitDir := gitFolder(t, false)
	var tries int
	replaced := func(fd int) error {
		if tries++; tries == 1 {
			path := filepath.Join(gitDir, commitLock)
			if err := errors.Join(os.WriteFile(path+".new", nil, 0o644), os.Rename(path+".new", path)); err != nil {
				t.Error(err)
			}
		}
		return syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	}
	fake := runnertest.New(t)
	want := anEdit.script(t, fake, gitDir)
	repo := gitrepo.WithFlock(gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) {}}, replaced)
	committed, err := didCommit(repo.Commit(context.Background(), "kit test", anEdit.paths...))
	anEdit.check(t, committed, err)
	checkCalls(t, "the commit lock replaced", fake, gitDir, want)
	if tries != 2 {
		t.Errorf("kit locked its commit lock %d times, want twice: the file replaced, then the one there", tries)
	}
}

// Where the git folder's filesystem takes no kernel lock, kit commits
// without its own lock, and says so, leaving what a run that stopped left
// as it is, its mark too: without that lock, it can't tell that run from
// one at work. It leaves no mark of its own, as no run would read it.
func TestCommitWithoutKitsOwnLock(t *testing.T) {
	const left = "- " + stopped + " a-hash 1:2\n"
	for _, errno := range []syscall.Errno{syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.ENOLCK} {
		gitDir := gitFolder(t, false)
		writeFile(t, gitDir, marked, left)
		fake := runnertest.New(t)
		s := started(fake, gitDir)
		s.goesOn(t, errno.Error(), gitDir, base, usersEntries).Does(func() {
			if got := readFile(gitDir, marked); got != left {
				t.Errorf("%v: HEAD moved with the mark holding %q, want the stopped run's, untouched", errno, got)
			}
		})
		repo := gitrepo.WithFlock(gitrepo.Repo{Dir: dir, Run: fake, Sleep: func(time.Duration) {}}, func(int) error { return errno })
		out, err := repo.Commit(context.Background(), "kit test", "brew")
		if !out.Committed || err != nil || !slices.Equal(out.Notes, []string{"committed without kit's own lock, which the git folder's filesystem can't take"}) {
			t.Errorf("Commit(), the filesystem saying %v = %+v, %v; want committed, and said", errno, out, err)
		}
		checkCalls(t, errno.Error(), fake, gitDir, s.want)
		checkGitFolder(t, errno.Error(), gitDir, resetEntries, marked)
		if got := readFile(gitDir, marked); got != left {
			t.Errorf("%v: the mark holds %q once committed, want the stopped run's, untouched", errno, got)
		}
	}
}

// Kit's lock on committing removed or replaced while it commits, as by a
// cleaner, another kit could take the new one's: kit commits nothing, HEAD
// left where it was, before its mark's left for that kit to read.
func TestCommitStopsOnceItsCommitLockIsGone(t *testing.T) {
	gitDir := gitFolder(t, false)
	fake := runnertest.New(t)
	s := editing(fake, gitDir, base)
	s.expect(call{args: git("read-tree", base), index: ownIndex})
	s.expect(call{args: stageIn, input: "brew\x00", index: ownIndex})
	s.expect(call{args: stagedIn, index: ownIndex}).Prints("brew\x00")
	s.commits(base, false, nil)
	s.reads(false, "brew")
	s.expect(call{args: resetTo(made), input: "brew\x00", index: nextIndex}).Does(func() {
		writeFile(t, gitDir, nextIndex, resetEntries)
		path := filepath.Join(gitDir, commitLock)
		if err := errors.Join(os.WriteFile(path+".new", nil, 0o644), os.Rename(path+".new", path)); err != nil {
			t.Error(err)
		}
	})
	out, err := commitBrew(fake)
	if want := "kit's lock on committing in the repository was removed or replaced meanwhile, so nothing was committed, as another kit may be committing"; out.Committed || err == nil || err.Error() != want {
		t.Errorf("Commit(), the commit lock replaced = %+v, %v; want %q", out, err, want)
	}
	checkCalls(t, "the commit lock replaced", fake, gitDir, s.want)
	checkGitFolder(t, "the commit lock replaced", gitDir, usersEntries)
}

// A lock a run of kit's left, stopped once it had marked it, is put in the
// index's place where HEAD's on the commit it was moving onto, as that run
// would have, and removed where HEAD's on the one that run started from,
// as when it stopped before HEAD moved; the commit then goes on, from that
// index, and
// says what it did: kit holds its own lock on committing, so no other kit
// is at work, and the run's mark names the commits, and the lock by its
// very contents and its file. A lock kit put in place that another git
// then takes before kit does is said too, with why kit's change waits.
func TestCommitDealsWithTheLockAStoppedRunLeft(t *testing.T) {
	for _, c := range []struct {
		name string
		// base and at are the commit the stopped run moved HEAD from and
		// the one HEAD's on now: "" before the first.
		base, at string
		unborn   bool
		// index is the user's index the commit goes on from: none when "",
		// another git taking the lock once kit put it in place.
		index, note string
	}{
		{name: "HEAD moved", base: base, at: stopped, index: left, note: "finished the last run's index"},
		{name: "HEAD not moved", base: base, at: base, index: usersEntries, note: "removed the lock on git's index a stopped run left; HEAD is where that run started"},
		{name: "before the first commit", unborn: true, note: "removed the lock on git's index a stopped run left; HEAD is where that run started"},
		{name: "taken by another git once put in place", base: base, at: stopped, note: "finished the last run's index"},
	} {
		gitDir := gitFolder(t, c.unborn)
		leave(t, gitDir, c.base)
		lock := filepath.Join(gitDir, "index.lock")
		taken := !c.unborn && c.index == ""
		if taken {
			// The index a link to the lock, its rename leaves the lock
			// there, as when another git takes it first.
			if err := errors.Join(os.Remove(filepath.Join(gitDir, "index")), os.Link(lock, filepath.Join(gitDir, "index"))); err != nil {
				t.Fatal(err)
			}
		}
		fake := runnertest.New(t)
		s := started(fake, gitDir)
		s.head(c.at)
		if !taken {
			s.goesOn(t, c.name, gitDir, c.at, c.index)
		}
		out, err := commitBrew(fake)
		checkCalls(t, c.name, fake, gitDir, s.want)
		switch {
		case !slices.Equal(out.Notes, []string{c.note}):
			t.Errorf("%s: Commit() = %+v, %v; want %q said", c.name, out, err, c.note)
		case taken:
			if out.Committed || err == nil || !strings.HasSuffix(err.Error(), "remove "+lock) {
				t.Errorf("%s: Commit() = %+v, %v; want the lock named, and what to do", c.name, out, err)
			}
			checkGitFolder(t, c.name, gitDir, left, "index.lock")
		case !out.Committed || err != nil:
			t.Errorf("%s: Commit() = %+v, %v; want committed", c.name, out, err)
		default:
			checkGitFolder(t, c.name, gitDir, resetEntries)
		}
	}
}

// Any other lock on the index kit leaves as it is, and names, with what to
// do should no git be running: one with no mark; one HEAD's on neither of
// the mark's commits for; one holding other than the mark's; one holding
// the same, a file written anew. A mark kit leaves be too, as the lock may
// yet be its run's.
func TestCommitLeavesAnyOtherLockBe(t *testing.T) {
	for _, c := range []struct {
		name string
		// leave leaves the lock, and any mark, in gitDir.
		leave func(gitDir string)
		// at is the commit HEAD's on, when kit asks.
		at string
	}{
		{name: "no mark", leave: func(gitDir string) { writeFile(t, gitDir, "index.lock", left) }},
		{name: "HEAD on another commit", leave: func(gitDir string) { leave(t, gitDir, base) }, at: "0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c"},
		{name: "holding other than the mark's", leave: func(gitDir string) {
			leave(t, gitDir, base)
			writeFile(t, gitDir, "index.lock", "another git's index")
		}},
		{name: "the same, a file written anew", leave: func(gitDir string) {
			leave(t, gitDir, base)
			lock := filepath.Join(gitDir, "index.lock")
			if err := errors.Join(os.WriteFile(lock+".new", []byte(left), 0o644), os.Rename(lock+".new", lock)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		gitDir := gitFolder(t, false)
		c.leave(gitDir)
		lock := filepath.Join(gitDir, "index.lock")
		was, mark := readFile(gitDir, "index.lock"), readFile(gitDir, marked)
		fake := runnertest.New(t)
		s := started(fake, gitDir)
		if c.at != "" {
			s.head(c.at)
		}
		out, err := commitBrew(fake)
		checkCalls(t, c.name, fake, gitDir, s.want)
		if out.Committed || out.Notes != nil || err == nil || !strings.HasSuffix(err.Error(), "remove "+lock) {
			t.Errorf("%s: Commit() = %+v, %v; want the lock named, and what to do", c.name, out, err)
		}
		if readFile(gitDir, "index.lock") != was || readFile(gitDir, marked) != mark {
			t.Errorf("%s: the lock holds %q, the mark %q; want both left as they were", c.name, readFile(gitDir, "index.lock"), readFile(gitDir, marked))
		}
	}
}

// A lock another git puts in place of the stopped run's as kit asks where
// HEAD is kit leaves be, and says why kit's change waits.
func TestCommitLeavesALockReplacedAsItLooks(t *testing.T) {
	gitDir := gitFolder(t, false)
	leave(t, gitDir, base)
	lock := filepath.Join(gitDir, "index.lock")
	fake := runnertest.New(t)
	s := started(fake, gitDir)
	s.head(stopped).Does(func() {
		if err := errors.Join(os.WriteFile(lock+".new", []byte(left), 0o644), os.Rename(lock+".new", lock)); err != nil {
			t.Error(err)
		}
	})
	out, err := commitBrew(fake)
	checkCalls(t, "replaced as kit looks", fake, gitDir, s.want)
	if want := "couldn't deal with the lock on git's index a stopped run of kit's left: kit's lock on git's index was removed or replaced by another git meanwhile, so kit left it be"; out.Committed || err == nil || err.Error() != want {
		t.Errorf("Commit(), the lock replaced as kit looks = %+v, %v; want %q", out, err, want)
	}
	checkGitFolder(t, "replaced as kit looks", gitDir, usersEntries, "index.lock", marked)
}

// The objects of the files in the tests' commits: brew's, decl's and
// tmp's before the stopped run's commit, and in it, and as the user staged
// them since.
const (
	brewBefore = "1111111111111111111111111111111111111111"
	brewMade   = "2222222222222222222222222222222222222222"
	brewTheirs = "3333333333333333333333333333333333333333"
	declTheirs = "5555555555555555555555555555555555555555"
	addedMade  = "6666666666666666666666666666666666666666"
	tmpBefore  = "8888888888888888888888888888888888888888"
)

// indexed is git listing the user's index, as kit reads what it holds.
var indexed = git("ls-files", "--stage", "-z")

// entry is a file's entry in git's index, as ls-files --stage prints it,
// at stage 0.
func entry(object, path string) string { return "100644 " + object + " 0\t" + path + "\x00" }

// markText is kit's mark of a stopped run's lock, before HEAD moved from
// base, "" before the first, onto stopped, the user's index holding held
// for the files it committed.
func markText(base string, held map[string]string) string {
	return cmp.Or(base, "-") + " " + stopped + " a-hash 1:2\n" + records(held)
}

// A run of kit's that stopped once HEAD had moved onto its commit, its lock
// then removed as git advises, leaves the user's index showing the files
// it committed as it read them, so a git commit would undo them: kit,
// holding git's lock on the index from before it reads it, resets those
// whose entries in the user's index are still as that run read them, as
// its mark says, to the commit's, as that run would have, and says so,
// then goes on: a file staged before that run, as it read it, too, as the
// run would have taken it. A file the user has staged since, or in
// conflict, it leaves be; with nothing to repair, or HEAD moved on from the
// mark's commit, it clears the mark, saying nothing; a mark that doesn't
// say what the index held, as an older kit's, or one cut short, it clears,
// leaving the index as it is, and says so.
func TestCommitRepairsTheIndexAStoppedRunLeft(t *testing.T) {
	const repaired = "the user's entries, the stopped run's files as committed"
	// read is what the stopped run read of the user's index for the files
	// it committed: brew edited, added new, tmp added then gone.
	read := map[string]string{"brew": "100644 " + brewBefore + " 0", "added": "", "tmp": "100644 " + tmpBefore + " 0"}
	first := base + " " + stopped + " a-hash 1:2\n"
	for _, c := range []struct {
		name, at string
		// mark is the mark, and index the user's index's entries, as
		// ls-files lists them, none read when ""; stale the files kit
		// resets in it, none when "". A conflict's last entry is the one
		// the run read but for its stage.
		mark, index, stale string
		says               []string
	}{
		{name: "the index as it was", at: stopped, mark: markText(base, read), index: entry(brewBefore, "brew") + entry(declTheirs, "decl") + entry(tmpBefore, "tmp"), stale: "added\x00brew\x00tmp\x00", says: []string{"repaired the index a stopped run left"}},
		{name: "the index as a finished run leaves it", at: stopped, mark: markText(base, read), index: entry(addedMade, "added") + entry(brewMade, "brew") + entry(declTheirs, "decl")},
		{name: "staged since", at: stopped, mark: markText(base, read), index: entry(addedMade, "added") + entry(brewTheirs, "brew") + entry(declTheirs, "decl") + entry(brewTheirs, "tmp")},
		{name: "staged before the run, as it read it", at: stopped, mark: markText(base, map[string]string{"brew": "100644 " + brewTheirs + " 0"}), index: entry(brewTheirs, "brew") + entry(declTheirs, "decl"), stale: "brew\x00", says: []string{"repaired the index a stopped run left"}},
		{name: "in conflict", at: stopped, mark: markText(base, map[string]string{"brew": "100644 " + brewBefore + " 0"}), index: "100644 " + brewMade + " 1\tbrew\x00100644 " + brewTheirs + " 2\tbrew\x00100644 " + brewBefore + " 3\tbrew\x00"},
		{name: "before the first commit", at: stopped, mark: markText("", map[string]string{"brew": "", "added": ""}), index: entry(declTheirs, "decl"), stale: "added\x00brew\x00", says: []string{"repaired the index a stopped run left"}},
		{name: "HEAD moved on", at: base, mark: markText(base, read)},
		{name: "an older kit's mark", at: stopped, mark: first, says: []string{"left the index a stopped run left as it is, as its mark doesn't say what it held: git status shows whether that run's files are staged as they were before its commit"}},
		{name: "a mark cut short", at: stopped, mark: first + "100644 " + brewBefore + " 0\tbrew", says: []string{"left the index a stopped run left as it is, as its mark doesn't say what it held: git status shows whether that run's files are staged as they were before its commit"}},
		{name: "a record without its tab", at: stopped, mark: first + "brew\x00", says: []string{"left the index a stopped run left as it is, as its mark doesn't say what it held: git status shows whether that run's files are staged as they were before its commit"}},
	} {
		gitDir := gitFolder(t, false)
		writeFile(t, gitDir, marked, c.mark)
		held := func() {
			if _, err := os.Lstat(filepath.Join(gitDir, "index.lock")); err != nil {
				t.Errorf("%s: kit read the index without holding it", c.name)
			}
		}
		fake := runnertest.New(t)
		s := started(fake, gitDir)
		s.head(c.at).Does(held)
		if c.index != "" {
			s.expect(call{args: indexed}).Prints(c.index).Does(held)
		}
		index := usersEntries
		if c.stale != "" {
			index = repaired
			s.expect(call{args: resetTo(stopped), input: c.stale, index: nextIndex}).Does(func() {
				held()
				writeFile(t, gitDir, nextIndex, repaired)
			})
		}
		s.goesOn(t, c.name, gitDir, c.at, index)
		out, err := commitBrew(fake)
		checkCalls(t, c.name, fake, gitDir, s.want)
		if !out.Committed || err != nil || !slices.Equal(out.Notes, c.says) {
			t.Errorf("%s: Commit() = %+v, %v; want committed, and %q said", c.name, out, err, c.says)
		}
		checkGitFolder(t, c.name, gitDir, resetEntries)
	}
}

// A repair that fails leaves the mark, so the next run tries it again.
func TestCommitRepairsAgainOnceARepairFailed(t *testing.T) {
	gitDir := gitFolder(t, false)
	mark := markText(base, map[string]string{"brew": "100644 " + brewBefore + " 0"})
	writeFile(t, gitDir, marked, mark)
	repairs := func(s *sequence) *runnertest.Script {
		s.head(stopped)
		s.expect(call{args: indexed}).Prints(entry(brewBefore, "brew"))
		return s.expect(call{args: resetTo(stopped), input: "brew\x00", index: nextIndex})
	}
	fake := runnertest.New(t)
	s := started(fake, gitDir)
	repairs(s).Exits(128).PrintsToStderr("fatal: index file corrupt\n")
	if out, err := commitBrew(fake); out.Committed || err == nil || !strings.Contains(err.Error(), "index file corrupt") {
		t.Errorf("Commit(), the repair failing = %+v, %v; want git's error", out, err)
	}
	checkCalls(t, "the repair failing", fake, gitDir, s.want)
	checkGitFolder(t, "the repair failing", gitDir, usersEntries, marked)
	if readFile(gitDir, marked) != mark {
		t.Errorf("the mark holds %q once the repair failed, want it as it was", readFile(gitDir, marked))
	}

	fake = runnertest.New(t)
	s = started(fake, gitDir)
	repairs(s).Does(func() { writeFile(t, gitDir, nextIndex, "repaired") })
	s.goesOn(t, "repaired again", gitDir, stopped, "repaired")
	if out, err := commitBrew(fake); !out.Committed || err != nil || !slices.Equal(out.Notes, []string{"repaired the index a stopped run left"}) {
		t.Errorf("Commit(), repairing again = %+v, %v; want repaired, then committed", out, err)
	}
	checkCalls(t, "repaired again", fake, gitDir, s.want)
	checkGitFolder(t, "repaired again", gitDir, resetEntries)
}

// While git's part way through one of its operations, as a rebase stopped
// on the stopped run's commit, kit refuses before it deals with anything a
// stopped run left, the mark kept for once it's done.
func TestCommitRecoversNothingWhileGitIsPartWay(t *testing.T) {
	gitDir := gitFolder(t, false)
	mark := base + " " + stopped + " a-hash 1:2\n"
	writeFile(t, gitDir, marked, mark)
	if err := os.Mkdir(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	s := started(fake, gitDir)
	if out, err := commitBrew(fake); out.Committed || out.Notes != nil || err == nil || !strings.Contains(err.Error(), "a rebase is under way") {
		t.Errorf("Commit(), mid-rebase = %+v, %v; want refused", out, err)
	}
	checkCalls(t, "mid-rebase", fake, gitDir, s.want)
	checkGitFolder(t, "mid-rebase", gitDir, usersEntries, marked, "rebase-merge")
	if readFile(gitDir, marked) != mark {
		t.Errorf("the mark holds %q mid-rebase, want it as it was", readFile(gitDir, marked))
	}
}

// A lock's age is said roughly, in the largest unit under two of the next:
// hours up to two days.
func TestSince(t *testing.T) {
	for _, c := range []struct {
		ago  time.Duration
		says string
	}{
		{0, "just now"},
		{999 * time.Millisecond, "just now"},
		{time.Second, "1 second ago"},
		{59 * time.Second, "59 seconds ago"},
		{time.Minute, "1 minute ago"},
		{3*time.Hour + time.Minute, "3 hours ago"},
		{47 * time.Hour, "47 hours ago"},
		{49 * time.Hour, "2 days ago"},
	} {
		if got := gitrepo.Since(c.ago); got != c.says {
			t.Errorf("since(%s) = %q, want %q", c.ago, got, c.says)
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
