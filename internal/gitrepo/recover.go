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
	// unlocked is said where kit commits without its own lock.
	unlocked = "committed without kit's own lock, which the git folder's filesystem can't take"
)

// mark is what a run of kit's leaves beside git's lock on the index before
// HEAD moves (marked): the commit HEAD's on (base, "" before the first) and
// the one it's moving onto (made); and the lock it filled, by its hash and
// its file (fileID), so a lock written later that's the same byte for byte
// isn't taken for it.
type mark struct {
	base, made, sum, file string
}

// String is the mark as it's written: each part a word, "-" for no base.
func (m mark) String() string {
	return fmt.Sprintf("%s %s %s %s\n", cmp.Or(m.base, "-"), m.made, m.sum, m.file)
}

// readMark is the mark in the git folder gitDir, if there's one to read.
func readMark(gitDir string) (mark, bool) {
	data, err := os.ReadFile(filepath.Join(gitDir, marked))
	if err != nil {
		return mark{}, false
	}
	f := strings.Fields(string(data))
	if len(f) != 4 {
		return mark{}, false
	}
	m := mark{base: f[0], made: f[1], sum: f[2], file: f[3]}
	if m.base == "-" {
		m.base = ""
	}
	return m, true
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
// and the lock gone: the commit's files the index still holds as they were
// before it (stale), as the commit has them, under git's lock on the index,
// as the stopped run would have; any the user has staged since, it leaves
// be. A mark HEAD's moved on from, or with nothing stale, it clears; one
// whose repair fails stays, for the next run to try again.
func (r Repo) repair(ctx context.Context, gitDir string, m mark) ([]string, error) {
	head, err := r.commitAt(ctx, "HEAD")
	if err != nil {
		return nil, err
	}
	if head != m.made {
		_ = os.Remove(filepath.Join(gitDir, marked))
		return nil, nil
	}
	res, err := r.git(ctx, "diff-tree", "-r", "--root", "--no-commit-id", "--name-only", "-z", "--no-renames", m.made)
	if err != nil {
		return nil, err
	}
	stale, err := r.stale(ctx, m.base, names(res.Stdout))
	if err != nil {
		return nil, err
	}
	if len(stale) == 0 {
		_ = os.Remove(filepath.Join(gitDir, marked))
		return nil, nil
	}
	lock, err := r.lockIndex(ctx, gitDir)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	if err := r.prepare(ctx, lock, m.made, stale); err != nil {
		return nil, err
	}
	if err := lock.commit(); err != nil {
		return nil, err
	}
	_ = os.Remove(filepath.Join(gitDir, marked))
	return []string{repairedIndex}, nil
}

// stale are those of paths whose entry in the user's index is still base's,
// its mode and object, or, before the first commit, base "", none: the
// files git's index shows as they were before kit's commit.
func (r Repo) stale(ctx context.Context, base string, paths []string) ([]string, error) {
	res, err := r.git(ctx, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	index := map[string]string{}
	for _, line := range names(res.Stdout) {
		info, path, _ := strings.Cut(line, "\t")
		mode, rest, _ := strings.Cut(info, " ")
		object, stage, _ := strings.Cut(rest, " ")
		index[path] = mode + " " + object
		if stage != "0" {
			index[path] = "conflicted"
		}
	}
	before := map[string]string{}
	if base != "" {
		if res, err = r.git(ctx, "ls-tree", "-r", "-z", "--full-tree", base); err != nil {
			return nil, err
		}
		for _, line := range names(res.Stdout) {
			info, path, _ := strings.Cut(line, "\t")
			if f := strings.Fields(info); len(f) == 3 {
				before[path] = f[0] + " " + f[2]
			}
		}
	}
	return slices.DeleteFunc(paths, func(p string) bool { return index[p] != before[p] }), nil
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
