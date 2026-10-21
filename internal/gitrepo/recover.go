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
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// What recover says it did.
const (
	finishedIndex = "finished the last run's index"
	clearedLock   = "removed the lock on git's index a stopped run left; HEAD is where that run started"
	repairedIndex = "repaired the index a stopped run left"
	// unrepaired is said of a mark that doesn't say what the index held,
	// as an older kit's.
	unrepaired = "left the index a stopped run left as it is, as its mark doesn't say what it held: git status shows whether that run's files are staged as they were before its commit"
	// unlocked is said where kit commits without its own lock.
	unlocked = "committed without kit's own lock, which the git folder's filesystem can't take"
)

// mark is what a run of kit's leaves beside git's lock on the index before
// HEAD moves (marked): the commit HEAD's on (base, "" before the first) and
// the one it's moving onto (made); the lock it filled, by its hash and its
// file (fileID), so a lock written later that's the same byte for byte
// isn't taken for it; and what the user's index held for each path it
// commits as it read them (read, as indexed has it): nil when the mark
// doesn't say, as an older kit's.
type mark struct {
	base, made, sum, file string
	read                  map[string]string
}

// String is the mark as it's written: a line of its commits and lock, each
// a word, "-" for no base; then, for each path in read, in order, what the
// index held, "-" for nothing, a tab and the path, ended by a NUL, as a
// path can hold any byte but that.
func (m mark) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s %s\n", cmp.Or(m.base, "-"), m.made, m.sum, m.file)
	for _, path := range slices.Sorted(maps.Keys(m.read)) {
		fmt.Fprintf(&b, "%s\t%s\x00", cmp.Or(m.read[path], "-"), path)
	}
	return b.String()
}

// readMark is the mark in the git folder gitDir, if there's one to read:
// its read nil when what follows its first line isn't whole.
func readMark(gitDir string) (mark, bool) {
	data, err := os.ReadFile(filepath.Join(gitDir, marked))
	if err != nil {
		return mark{}, false
	}
	first, rest, _ := strings.Cut(string(data), "\n")
	f := strings.Fields(first)
	if len(f) != 4 {
		return mark{}, false
	}
	m := mark{base: f[0], made: f[1], sum: f[2], file: f[3], read: readHeld(rest)}
	if m.base == "-" {
		m.base = ""
	}
	return m, true
}

// readHeld is what a mark says the index held for each path, records
// written as String writes them: nil for none, or any record cut short.
func readHeld(records string) map[string]string {
	if !strings.HasSuffix(records, "\x00") {
		return nil
	}
	read := map[string]string{}
	for record := range strings.SplitSeq(strings.TrimSuffix(records, "\x00"), "\x00") {
		held, path, ok := strings.Cut(record, "\t")
		if !ok || held == "" || path == "" {
			return nil
		}
		if held == "-" {
			held = ""
		}
		read[path] = held
	}
	return read
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
// tells, kit holding its lock on committing, so that run's over: the lock
// on the index it filled, still there, it puts in the index's place once
// HEAD's on its commit, or removes while HEAD's on the one that run started
// from; the lock gone, as when it was removed as git advises, HEAD on its
// commit, it brings the index up to date with it (repair). Any other lock
// it leaves be, the refusal to come naming it. What it did, it says.
func (r Repo) recover(ctx context.Context, gitDir string) ([]string, error) {
	m, ok := readMark(gitDir)
	if !ok {
		return nil, nil
	}
	path := filepath.Join(gitDir, "index.lock")
	info, sum, err := hashed(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r.repair(ctx, gitDir, m)
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
// commit has it; any the user has staged since, it leaves be. A mark HEAD's
// moved on from, with nothing stale, or not saying what the index held, it
// clears, saying so of the last; one whose repair fails stays, for the next
// run to try again.
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
	case m.read == nil:
		return cleared(gitDir, unrepaired)
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
	if err := r.prepare(ctx, lock, m.made, stale); err != nil {
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
