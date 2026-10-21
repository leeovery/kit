package gitrepo

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

// What recover says it did.
const (
	finishedIndex = "finished the last run's index"
	clearedLock   = "removed the lock on git's index a stopped run left; HEAD is where that run started"
	repairedIndex = "repaired the index a stopped run left"
	// unrepaired is said of a mark kit can't use: its first line not the
	// mark's, or, to repair the index by, its records not whole or not
	// sound.
	unrepaired = "left the index a stopped run left as it is, as kit can't use its mark: git status shows whether that run's files are staged as they were before its commit"
	// unlocked is said where kit commits without its own lock.
	unlocked = "committed without kit's own lock, which the git folder's filesystem can't take"
)

// mark is what a run of kit's leaves beside git's lock on the index before
// HEAD moves (marked): the commit HEAD's on (base, "" before the first) and
// the one it's moving onto (made); the lock it filled, by its hash and its
// file (fileID), so a lock written later that's the same byte for byte
// isn't taken for it; and what the user's index held for each path it
// commits as it read them (read, as indexed has it). made is "" for a mark
// whose first line kit can't use, read nil for one whose records it can't.
type mark struct {
	base, made, sum, file string
	read                  map[string]string
}

// markEnd ends a mark's records, so a mark cut short anywhere, between
// records too, is never taken for whole. No record is it, as each holds a
// tab.
const markEnd = "end\x00"

// String is the mark as it's written: a line of its commits and lock, each
// a word, "-" for no base; then, for each path in read, in order, what the
// index held, "-" for nothing, a tab and the path, ended by a NUL, as a
// path can hold any byte but that; then markEnd.
func (m mark) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s %s\n", cmp.Or(m.base, "-"), m.made, m.sum, m.file)
	for _, path := range slices.Sorted(maps.Keys(m.read)) {
		fmt.Fprintf(&b, "%s\t%s\x00", cmp.Or(m.read[path], "-"), path)
	}
	b.WriteString(markEnd)
	return b.String()
}

// objectID is an object's name as git writes one: a SHA-1's or a
// SHA-256's hash, in hex.
var objectID = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// readMark is the mark in the git folder gitDir, if there's one: none of
// it read when its first line isn't four words, as String writes them, its
// commits each an objectID, "-" for no base; its read nil when its records
// aren't whole, or a record's path isn't sound (clean).
func readMark(gitDir string) (mark, bool) {
	data, err := os.ReadFile(filepath.Join(gitDir, marked))
	if err != nil {
		return mark{}, false
	}
	first, rest, _ := strings.Cut(string(data), "\n")
	f := strings.Split(first, " ")
	if len(f) != 4 || !objectID.MatchString(f[1]) || f[0] != "-" && !objectID.MatchString(f[0]) {
		return mark{}, true
	}
	m := mark{base: f[0], made: f[1], sum: f[2], file: f[3], read: readHeld(rest)}
	if m.base == "-" {
		m.base = ""
	}
	return m, true
}

// readHeld is what a mark says the index held for each path, records
// written as String writes them, then markEnd: nil for any record cut
// short, or with a path that isn't clean, or no markEnd after them all.
func readHeld(records string) map[string]string {
	records, ended := strings.CutSuffix(records, markEnd)
	if !ended {
		return nil
	}
	read := map[string]string{}
	for records != "" {
		record, rest, whole := strings.Cut(records, "\x00")
		held, path, tabbed := strings.Cut(record, "\t")
		if !whole || !tabbed || held == "" || !clean(path) {
			return nil
		}
		if held == "-" {
			held = ""
		}
		read[path] = held
		records = rest
	}
	return read
}

// clean reports whether p is a path in the repository as git names a file
// in it: relative, and with no empty, . or .. part.
func clean(p string) bool {
	return p != "" && p != "." && path.Clean(p) == p && !strings.HasPrefix(p, "/") && !slices.Contains(strings.Split(p, "/"), "..")
}

// fileID is the device and inode of the file info describes.
func fileID(info fs.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}

// recovered recovers what a run of kit's that stopped left (recover),
// holding kit's lock on committing while it does: nothing without a mark,
// as a live kit's is there only while it holds that lock, so never waiting
// for one at work; nor where the git folder's filesystem takes no such
// lock.
func (r Repo) recovered(ctx context.Context, gitDir string) ([]string, error) {
	if _, ok := readMark(gitDir); !ok {
		return nil, nil
	}
	commits, err := r.lockCommits(ctx, gitDir)
	if err != nil || commits == nil {
		return nil, err
	}
	defer func() { _ = commits.Close() }()
	return r.recover(ctx, gitDir)
}

// recover deals with what a run of kit's that stopped left, as its mark
// tells, kit holding its lock on committing, so that run's over: a mark
// whose first line it can't use it clears, leaving the index and any lock,
// and says so; the lock on the index it filled, still there, it puts in
// the index's place once HEAD's on its commit, or removes while HEAD's on
// the one that run started from, by the mark's first line alone; the lock
// gone, as when it was removed as git advises, HEAD on its commit, it
// brings the index up to date with it (repaired). Any other lock it leaves
// be, the refusal to come naming it. What it did, it says.
func (r Repo) recover(ctx context.Context, gitDir string) ([]string, error) {
	m, ok := readMark(gitDir)
	switch {
	case !ok:
		return nil, nil
	case m.made == "":
		return cleared(gitDir, unrepaired)
	}
	path := filepath.Join(gitDir, "index.lock")
	info, sum, err := hashed(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r.repaired(ctx, gitDir, m)
	case err != nil || sum != m.sum || fileID(info) != m.file:
		return nil, nil
	}
	head, err := r.commitAt(ctx, "HEAD")
	switch {
	case err != nil:
		return nil, err
	case head == m.made:
		return said(finishedIndex, replace(path, info, filepath.Join(gitDir, "index")), gitDir)
	case head == m.base:
		return said(clearedLock, replace(path, info, ""), gitDir)
	}
	return nil, nil
}

// repaired is the index brought up to date with m's commit (repair), a
// failure naming the mark, and what to do; a mark whose records kit can't
// use it clears, leaving the index, and says so.
func (r Repo) repaired(ctx context.Context, gitDir string, m mark) ([]string, error) {
	if m.read == nil {
		return cleared(gitDir, unrepaired)
	}
	notes, err := r.repair(ctx, gitDir, m)
	if err != nil {
		mark := filepath.Join(gitDir, marked)
		return nil, fmt.Errorf("couldn't repair the index a stopped run of kit's left, by its mark, %s: %w; kit tries again next run, or, once git status shows the index as it should be, remove %s", mark, err, mark)
	}
	return notes, nil
}

// replace puts the file at path, info as it was read, at to, or removes it
// for "", unless another's taken its place since.
func replace(path string, info fs.FileInfo, to string) error {
	now, err := os.Lstat(path)
	switch {
	case err != nil:
		return err
	case !os.SameFile(info, now):
		return errLockTaken
	case to == "":
		return os.Remove(path)
	}
	return os.Rename(path, to)
}

// said is what's said once recover has dealt with what a stopped run left,
// failing unless it could, its mark then gone from gitDir.
func said(what string, err error, gitDir string) ([]string, error) {
	if err != nil {
		return nil, fmt.Errorf("couldn't deal with the lock on git's index a stopped run of kit's left: %w", err)
	}
	_ = os.Remove(filepath.Join(gitDir, marked))
	return []string{what}, nil
}

// repair brings the user's index up to date with m's commit, HEAD on it
// and the lock gone, as the stopped run would have, holding git's lock on
// the index from before it reads it: each path the run committed whose
// entry the index still holds as that run read it (m's read) as the
// commit has it, its entry alone (entered); any the user has staged since,
// it leaves be. A mark HEAD's moved on from, or with nothing stale, it
// clears; one whose repair fails stays, for the next run to try again.
func (r Repo) repair(ctx context.Context, gitDir string, m mark) ([]string, error) {
	lock, err := r.lockIndex(ctx, gitDir)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	head, err := r.commitAt(ctx, "HEAD")
	switch {
	case err != nil:
		return nil, err
	case head != m.made:
		return cleared(gitDir)
	}
	paths := slices.Sorted(maps.Keys(m.read))
	now, err := r.indexed(ctx, paths)
	if err != nil {
		return nil, err
	}
	stale := slices.DeleteFunc(paths, func(p string) bool { return now[p] != m.read[p] })
	if len(stale) == 0 {
		return cleared(gitDir)
	}
	if err := r.entered(ctx, lock, m.made, stale); err != nil {
		return nil, err
	}
	if err := lock.commit(); err != nil {
		return nil, err
	}
	return cleared(gitDir, repairedIndex)
}

// cleared is what's said, once the mark is gone from gitDir.
func cleared(gitDir string, says ...string) ([]string, error) {
	_ = os.Remove(filepath.Join(gitDir, marked))
	return says, nil
}

// indexed is what the user's index holds for each of paths: its entry, as
// ls-files --stage has it, "mode object stage", "" for none; a conflict's
// last, never at stage 0, as no run commits a path in conflict, so never
// what one read.
func (r Repo) indexed(ctx context.Context, paths []string) (map[string]string, error) {
	res, err := r.git(ctx, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	held := make(map[string]string, len(paths))
	for _, p := range paths {
		held[p] = ""
	}
	for _, line := range names(res.Stdout) {
		entry, path, _ := strings.Cut(line, "\t")
		if _, ok := held[path]; ok {
			held[path] = entry
		}
	}
	return held, nil
}

// entered writes the user's index's next contents into the lock: a copy of
// the index, each of paths in it as commit has it, its entry, as ls-tree
// lists it, or none, by git's update-index, which sets each path's entry
// alone, where git's reset of a path takes every file under it, a folder's.
// An entry the index holds in the way of one, as a file where the commit
// has a folder, git replaces, as an index can't hold both.
func (r Repo) entered(ctx context.Context, lock *indexLock, commit string, paths []string) error {
	res, err := r.git(ctx, "ls-tree", "-r", "-z", "--full-tree", "--end-of-options", commit)
	if err != nil {
		return err
	}
	entries := map[string]string{}
	for _, line := range names(res.Stdout) {
		_, path, _ := strings.Cut(line, "\t")
		entries[path] = line
	}
	// An entry of mode 0 removes the path's.
	none := "0 " + strings.Repeat("0", len(commit)) + "\t"
	var info []string
	for _, p := range paths {
		info = append(info, cmp.Or(entries[p], none+p))
	}
	next := lock.file(nextIndex)
	mode, err := copyIndex(lock.file("index"), next)
	if err != nil {
		return err
	}
	if _, err := r.gitIn(ctx, []string{"GIT_INDEX_FILE=" + next}, info, "update-index", "-z", "--index-info"); err != nil {
		return err
	}
	return lock.fill(next, mode)
}

// hashed is the file at path, and the hash of what it holds.
func hashed(path string) (fs.FileInfo, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, "", err
	}
	return info, hex.EncodeToString(h.Sum(nil)), nil
}
