package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The states of a change not committed: untracked is a file the user took
// out of git's index, kept on disk, ignored; a repository is one of its
// own, nested in this one, which git can't keep.
const (
	Edited     = "edited"
	Added      = "added"
	Deleted    = "deleted"
	Untracked  = "untracked"
	Repository = "repository"
)

// Change is a file in the repository changed and not committed.
type Change struct {
	// Path is the file, as a path in the repository; a nested repository's
	// ends in a slash.
	Path string
	// State is edited, added (new to the repository), deleted, untracked,
	// or a repository nested in this one.
	State string
	// Added and Removed count the lines the change adds and removes.
	Added, Removed int
}

// Changes are the files changed and not committed, staged or not, new ones
// each by name, a rename as its two sides, with the lines each adds and
// removes against the last commit: a file added then gone before it was
// committed is none.
func (r Repo) Changes(ctx context.Context) ([]Change, error) {
	entries, err := r.status(ctx)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, e := range entries {
		state, ok := e.state()
		if !ok {
			continue
		}
		if i := slices.IndexFunc(changes, func(c Change) bool { return c.Path == e.path }); i >= 0 {
			// Staged as deleted, and on disk untracked: committed, and there.
			changes[i].State = Edited
			continue
		}
		if e.code == "D " && r.isFile(e.path) {
			// Staged as deleted, on disk with no untracked twin: ignored.
			state = Untracked
		}
		changes = append(changes, Change{Path: e.path, State: state})
	}
	if len(changes) == 0 {
		return nil, nil
	}
	counts := r.counts(ctx)
	for i, c := range changes {
		n, ok := counts[c.Path]
		if !ok && c.State == Added {
			if data, err := os.ReadFile(filepath.Join(r.Dir, c.Path)); err == nil {
				n[0] = strings.Count(string(data), "\n")
			}
		}
		changes[i].Added, changes[i].Removed = n[0], n[1]
	}
	return changes, nil
}

// isFile reports whether path, in the repository, is on disk, and no
// folder.
func (r Repo) isFile(path string) bool {
	info, err := os.Lstat(filepath.Join(r.Dir, path))
	return err == nil && !info.IsDir()
}

// counts are the lines each file on disk adds and removes against the last
// commit, by path, whatever's staged: none it can't count.
func (r Repo) counts(ctx context.Context) map[string][2]int {
	counts := make(map[string][2]int)
	_ = r.withHeadIndex(ctx, func(env []string) error {
		res, err := r.gitIn(ctx, env, nil, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD")
		if err != nil {
			return err
		}
		for _, rec := range names(res.Stdout) {
			if f := strings.SplitN(rec, "\t", 3); len(f) == 3 {
				added, _ := strconv.Atoi(f[0])
				removed, _ := strconv.Atoi(f[1])
				counts[f[2]] = [2]int{added, removed}
			}
		}
		return nil
	})
	return counts
}

// withHeadIndex runs do with an index of the last commit's alone, a file of
// its own, so the user's is never read or written: git run with env uses
// it. Before the first commit, there's nothing to run it on.
func (r Repo) withHeadIndex(ctx context.Context, do func(env []string) error) error {
	base, err := r.commitAt(ctx, "HEAD")
	if err != nil || base == "" {
		return err
	}
	f, err := os.CreateTemp("", "kit-read-index-*")
	if err != nil {
		return err
	}
	index := f.Name()
	// git writes it: an empty file doesn't read as an index.
	_ = f.Close()
	_ = os.Remove(index)
	defer func() {
		_ = os.Remove(index)
		_ = os.Remove(index + ".lock")
	}()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := r.gitIn(ctx, env, nil, "read-tree", base); err != nil {
		return err
	}
	return do(env)
}

// entry is a file git status names: its two letters, staged and not, and
// its path in the repository, a nested repository's ending in a slash.
type entry struct {
	code, path string
}

// addedThenGone is status's code for a file added to the index, then
// deleted before it was committed: in neither the last commit nor on disk.
const addedThenGone = "AD"

// state is what e's change is: none for a file added then gone.
func (e entry) state() (string, bool) {
	switch {
	case e.code == addedThenGone:
		return "", false
	case e.nested():
		return Repository, true
	case e.code == "??" || strings.Contains(e.code, "A"):
		return Added, true
	case strings.Contains(e.code, "D"):
		return Deleted, true
	}
	return Edited, true
}

// nested reports whether e is a repository nested in this one: status
// names one as a folder, every other file by name.
func (e entry) nested() bool {
	return strings.HasSuffix(e.path, "/")
}

// status is git status of the files changed under paths, or the whole
// repository given none: each by its own path, new ones file by file and a
// rename as its two sides, whatever the user's config says of untracked
// files, renames or quoting.
func (r Repo) status(ctx context.Context, paths ...string) ([]entry, error) {
	res, err := r.gitOn(ctx, []string{"status", "--porcelain", "-z", "--untracked-files=all", "--no-renames"}, paths...)
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, rec := range names(res.Stdout) {
		if len(rec) > 3 {
			entries = append(entries, entry{code: rec[:2], path: rec[3:]})
		}
	}
	return entries, nil
}

// Diff is the change to path on disk against the last commit, whatever's
// staged, as git shows it, uncoloured: none for a file new to the
// repository.
func (r Repo) Diff(ctx context.Context, path string) (string, error) {
	var diff string
	err := r.withHeadIndex(ctx, func(env []string) error {
		res, err := r.gitIn(ctx, env, nil, "--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "HEAD", "--", path)
		diff = string(res.Stdout)
		return err
	})
	return diff, err
}

// Restore puts path back as it was last committed, staged and not.
func (r Repo) Restore(ctx context.Context, path string) error {
	_, err := r.gitOn(ctx, []string{"restore", "--source=HEAD", "--staged", "--worktree"}, path)
	return err
}

// Track puts path back in git's index as it was last committed, the file on
// disk left as it is.
func (r Repo) Track(ctx context.Context, path string) error {
	_, err := r.gitOn(ctx, []string{"restore", "--source=HEAD", "--staged"}, path)
	return err
}
