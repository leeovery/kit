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
// on, marked the mark it leaves before HEAD moves, and markEnd what ends a
// mark's records.
const (
	commitLock = "kit-commits"
	marked     = "kit-mark"
	markEnd    = "end\x00"
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
// and inode, then what the index held (records), then their end.
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
	return fmt.Sprintf("%s %s %s %d:%d\n", cmp.Or(base, "-"), made, hex.EncodeToString(sum[:]), st.Dev, st.Ino) + records(held) + markEnd
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
// very contents and its file, in its first line, which is all it takes, a
// mark whose records kit can't use as well. A lock kit put in place that
// another git then takes before kit does is said too, with why kit's
// change waits.
func TestCommitDealsWithTheLockAStoppedRunLeft(t *testing.T) {
	for _, c := range []struct {
		name string
		// base and at are the commit the stopped run moved HEAD from and
		// the one HEAD's on now: "" before the first; cut whether the
		// mark's records are cut short.
		base, at string
		unborn   bool
		cut      bool
		// index is the user's index the commit goes on from: none when "",
		// another git taking the lock once kit put it in place.
		index, note string
	}{
		{name: "HEAD moved", base: base, at: stopped, index: left, note: "finished the last run's index"},
		{name: "HEAD not moved", base: base, at: base, index: usersEntries, note: "removed the lock on git's index a stopped run left; HEAD is where that run started"},
		{name: "before the first commit", unborn: true, note: "removed the lock on git's index a stopped run left; HEAD is where that run started"},
		{name: "taken by another git once put in place", base: base, at: stopped, note: "finished the last run's index"},
		{name: "HEAD moved, the mark's records cut short", base: base, at: stopped, cut: true, index: left, note: "finished the last run's index"},
		{name: "HEAD not moved, the mark's records cut short", base: base, at: base, cut: true, index: usersEntries, note: "removed the lock on git's index a stopped run left; HEAD is where that run started"},
	} {
		gitDir := gitFolder(t, c.unborn)
		leave(t, gitDir, c.base)
		if c.cut {
			mark := readFile(gitDir, marked)
			writeFile(t, gitDir, marked, mark[:len(mark)-1])
		}
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

// entriesIn is git listing the entries of commit, as kit reads them; and
// setsEntries git's update-index setting entries in an index, as kit gives
// them.
var setsEntries = git("update-index", "-z", "--index-info")

func entriesIn(commit string) []string {
	return git("ls-tree", "-r", "-z", "--full-tree", "--end-of-options", commit)
}

// listed is a file's entry in a commit, as ls-tree -r prints it; removed
// the entry that removes path's from an index.
func listed(object, path string) string { return "100644 blob " + object + "\t" + path + "\x00" }

func removed(path string) string {
	return "0 0000000000000000000000000000000000000000\t" + path + "\x00"
}

// enters scripts kit repairing the user's index: git listing the stopped
// run's commit's entries, tree, as ls-tree prints them, then taking
// entries, each the commit's or a removal, into the index's copy; done is
// what git's update-index does.
func (s *sequence) enters(tree, entries string, done func()) *runnertest.Script {
	s.expect(call{args: entriesIn(stopped)}).Prints(tree)
	return s.expect(call{args: setsEntries, input: entries, index: nextIndex}).Does(done)
}

// entry is a file's entry in git's index, as ls-files --stage prints it,
// at stage 0.
func entry(object, path string) string { return "100644 " + object + " 0\t" + path + "\x00" }

// markText is kit's mark of a stopped run's lock, before HEAD moved from
// base, "" before the first, onto stopped, the user's index holding held
// for the files it committed.
func markText(base string, held map[string]string) string {
	return cmp.Or(base, "-") + " " + stopped + " a-hash 1:2\n" + records(held) + markEnd
}

// A run of kit's that stopped once HEAD had moved onto its commit, its lock
// then removed as git advises, leaves the user's index showing the files
// it committed as it read them, so a git commit would undo them: kit,
// holding git's lock on the index from before it reads it, resets those
// whose entries in the user's index are still as that run read them, as
// its mark says, to the commit's, as that run would have, each its entry
// alone, the commit's, as ls-tree lists it, or one removing it, so a
// path never takes the files under it, and says so, then goes on: a file
// staged before that run, as it read it, too, as the run would have taken
// it, and a file the run made a folder, or a folder it made a file. A file
// the user has staged since, or in conflict, it leaves be; with nothing to
// repair, or HEAD moved on from the mark's commit, it clears the mark,
// saying nothing.
func TestCommitRepairsTheIndexAStoppedRunLeft(t *testing.T) {
	const repaired = "the user's entries, the stopped run's files as committed"
	const said = "repaired the index a stopped run left"
	// read is what the stopped run read of the user's index for the files
	// it committed: brew edited, added new, tmp added then gone; tree the
	// files its commit holds, as ls-tree lists them.
	read := map[string]string{"brew": "100644 " + brewBefore + " 0", "added": "", "tmp": "100644 " + tmpBefore + " 0"}
	tree := listed(addedMade, "added") + listed(brewMade, "brew") + listed(declTheirs, "decl")
	for _, c := range []struct {
		name, at string
		// mark is the mark, and index the user's index's entries, as
		// ls-files lists them, none read when ""; tree the commit's files,
		// as ls-tree lists them; entered the entries kit gives git's
		// update-index, none when "". A conflict's last entry is the one
		// the run read but for its stage.
		mark, index, tree, entered string
		says                       []string
	}{
		{name: "the index as it was", at: stopped, mark: markText(base, read), index: entry(brewBefore, "brew") + entry(declTheirs, "decl") + entry(tmpBefore, "tmp"), tree: tree, entered: listed(addedMade, "added") + listed(brewMade, "brew") + removed("tmp"), says: []string{said}},
		{name: "the index as a finished run leaves it", at: stopped, mark: markText(base, read), index: entry(addedMade, "added") + entry(brewMade, "brew") + entry(declTheirs, "decl")},
		{name: "staged since", at: stopped, mark: markText(base, read), index: entry(addedMade, "added") + entry(brewTheirs, "brew") + entry(declTheirs, "decl") + entry(brewTheirs, "tmp")},
		{name: "staged before the run, as it read it", at: stopped, mark: markText(base, map[string]string{"brew": "100644 " + brewTheirs + " 0"}), index: entry(brewTheirs, "brew") + entry(declTheirs, "decl"), tree: tree, entered: listed(brewMade, "brew"), says: []string{said}},
		{name: "in conflict", at: stopped, mark: markText(base, map[string]string{"brew": "100644 " + brewBefore + " 0"}), index: "100644 " + brewMade + " 1\tbrew\x00100644 " + brewTheirs + " 2\tbrew\x00100644 " + brewBefore + " 3\tbrew\x00"},
		{name: "before the first commit", at: stopped, mark: markText("", map[string]string{"brew": "", "added": ""}), index: entry(declTheirs, "decl"), tree: listed(addedMade, "added") + listed(brewMade, "brew"), entered: listed(addedMade, "added") + listed(brewMade, "brew"), says: []string{said}},
		{name: "HEAD moved on", at: base, mark: markText(base, read)},
		{name: "files in folders", at: stopped, mark: markText(base, map[string]string{"etc/brew": "100644 " + brewBefore + " 0"}), index: entry(brewBefore, "etc/brew") + entry(declTheirs, "etc/decl"), tree: listed(brewMade, "etc/brew") + listed(declTheirs, "etc/decl"), entered: listed(brewMade, "etc/brew"), says: []string{said}},
		{name: "a file made a folder", at: stopped, mark: markText(base, map[string]string{"etc": "100644 " + brewBefore + " 0", "etc/brew": ""}), index: entry(brewBefore, "etc"), tree: listed(brewMade, "etc/brew"), entered: removed("etc") + listed(brewMade, "etc/brew"), says: []string{said}},
		{name: "a folder made a file", at: stopped, mark: markText(base, map[string]string{"etc": "", "etc/brew": "100644 " + brewBefore + " 0"}), index: entry(brewBefore, "etc/brew"), tree: listed(brewMade, "etc"), entered: listed(brewMade, "etc") + removed("etc/brew"), says: []string{said}},
		{name: "a folder's path, its entry alone", at: stopped, mark: markText(base, map[string]string{"etc": ""}), index: entry(declTheirs, "etc/decl"), tree: listed(declTheirs, "etc/decl"), entered: removed("etc"), says: []string{said}},
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
		if c.entered != "" {
			index = repaired
			s.enters(c.tree, c.entered, func() {
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

// A mark kit can't use, with no lock of its own beside it, it clears
// before anything else, leaving the index, and any other lock, as they
// are, and says so, then goes on: one cut short at any byte, at a record's
// end too, as only a whole one ends with its end; one with more after its
// end; a first line that isn't the mark's, its commits each a hash, the
// base or "-"; a record whose path isn't one in the repository as git
// names a file: empty, ., .., absolute, with an empty, . or .. part, or
// ending in a slash.
func TestCommitClearsAMarkItCantUse(t *testing.T) {
	const unusable = "left the index a stopped run left as it is, as kit can't use its mark: git status shows whether that run's files are staged as they were before its commit"
	whole := markText(base, map[string]string{"brew": "100644 " + brewBefore + " 0", "added": ""})
	clears := func(name, mark string) {
		t.Helper()
		gitDir := gitFolder(t, false)
		writeFile(t, gitDir, marked, mark)
		fake := runnertest.New(t)
		s := started(fake, gitDir)
		s.goesOn(t, name, gitDir, stopped, usersEntries)
		out, err := commitBrew(fake)
		checkCalls(t, name, fake, gitDir, s.want)
		if !out.Committed || err != nil || !slices.Equal(out.Notes, []string{unusable}) {
			t.Errorf("%s: Commit() = %+v, %v; want committed, and %q said", name, out, err, unusable)
		}
		checkGitFolder(t, name, gitDir, resetEntries)
	}
	for at := range len(whole) {
		clears(fmt.Sprintf("cut at byte %d of %d", at, len(whole)), whole[:at])
	}
	first := base + " " + stopped + " a-hash 1:2\n"
	for name, mark := range map[string]string{
		"more after its end":        whole + "-\tbrew\x00",
		"another mark after it":     whole + whole,
		"a first line of three":     base + " " + stopped + " a-hash\n" + markEnd,
		"a first line spaced twice": base + "  " + stopped + " a-hash 1:2\n" + markEnd,
		"a base like a flag":        "--full-name " + stopped + " a-hash 1:2\n" + markEnd,
		"a commit not a hash":       base + " HEAD a-hash 1:2\n" + markEnd,
		"a commit cut short":        base + " " + stopped[:39] + " a-hash 1:2\n" + markEnd,
		"a record without a tab":    first + "brew\x00" + markEnd,
		"the end run into a record": first + "-\tbrew" + markEnd,
		"a record with no entry":    first + "\tbrew\x00" + markEnd,
		"an empty path":             first + "-\t\x00" + markEnd,
		"the repository's folder":   first + "-\t.\x00" + markEnd,
		"the folder above":          first + "-\t..\x00" + markEnd,
		"a path above":              first + "-\t../x\x00" + markEnd,
		"an absolute path":          first + "100644 " + brewBefore + " 0\t/etc/passwd\x00" + markEnd,
		"an empty part":             first + "-\tetc//brew\x00" + markEnd,
		"a . part":                  first + "-\tetc/./brew\x00" + markEnd,
		"a .. part":                 first + "-\tetc/../brew\x00" + markEnd,
		"a slash at its end":        first + "-\tetc/\x00" + markEnd,
	} {
		clears(name, mark)
	}

	// A lock beside a mark whose first line kit can't use stays, for the
	// refusal to come to name, the mark cleared; beside one whose records
	// it can't use, a lock that isn't the one it names stays, and the mark
	// with it, nothing said.
	for _, c := range []struct {
		name, mark string
		says       []string
		kept       []string
	}{
		{name: "a first line kit can't use", mark: "--full-name " + stopped + " a-hash 1:2\n" + markEnd, says: []string{unusable}, kept: []string{"index.lock"}},
		{name: "records kit can't use", mark: whole[:len(whole)-1], kept: []string{"index.lock", marked}},
	} {
		gitDir := gitFolder(t, false)
		writeFile(t, gitDir, "index.lock", left)
		writeFile(t, gitDir, marked, c.mark)
		fake := runnertest.New(t)
		s := started(fake, gitDir)
		out, err := commitBrew(fake)
		checkCalls(t, c.name, fake, gitDir, s.want)
		if out.Committed || err == nil || !strings.Contains(err.Error(), "index.lock") || !slices.Equal(out.Notes, c.says) {
			t.Errorf("%s, a lock beside: Commit() = %+v, %v; want %q said, then refused, naming the lock", c.name, out, err, c.says)
		}
		checkGitFolder(t, c.name, gitDir, usersEntries, c.kept...)
	}
}

// A repair that fails leaves the mark, so the next run tries it again, and
// says so, naming the mark, and what to do should it go on failing.
func TestCommitRepairsAgainOnceARepairFailed(t *testing.T) {
	gitDir := gitFolder(t, false)
	mark := markText(base, map[string]string{"brew": "100644 " + brewBefore + " 0"})
	writeFile(t, gitDir, marked, mark)
	repairs := func(s *sequence) *runnertest.Script {
		s.head(stopped)
		s.expect(call{args: indexed}).Prints(entry(brewBefore, "brew"))
		return s.enters(listed(brewMade, "brew"), listed(brewMade, "brew"), nil)
	}
	fake := runnertest.New(t)
	s := started(fake, gitDir)
	repairs(s).Exits(128).PrintsToStderr("fatal: index file corrupt\n")
	path := filepath.Join(gitDir, marked)
	opens := "couldn't repair the index a stopped run of kit's left, by its mark, " + path + ": "
	ends := "fatal: index file corrupt; kit tries again next run, or, once git status shows the index as it should be, remove " + path
	if out, err := commitBrew(fake); out.Committed || err == nil || !strings.HasPrefix(err.Error(), opens) || !strings.HasSuffix(err.Error(), ends) {
		t.Errorf("Commit(), the repair failing = %+v, %v; want %q, git's error, then %q", out, err, opens, ends)
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
