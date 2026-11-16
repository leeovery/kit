package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The states of a change not committed: a repository is one of its own,
// nested in this one, which git can't keep.
const (
	Edited     = "edited"
	Added      = "added"
	Deleted    = "deleted"
	Repository = "repository"
)

// Change is a file in the repository changed and not committed.
type Change struct {
	// Path is the file, as a path in the repository; a nested repository's
	// ends in a slash.
	Path string
	// State is edited, added (new to the repository), deleted, or a
	// repository nested in this one.
	State string
	// Added and Removed count the lines the change adds and removes.
	Added, Removed int
}

// Changes are the files on disk that differ from the last commit, staged
// or not, new ones each by name, a rename as its two sides, with the lines
// each adds and removes against it: a file added then gone before it was
// committed is none, and so is one whose difference is only in git's
// index, as the user staged it, its file on disk as last committed, which
// is the user's own business in git. A file taken out of git's index and
// kept on disk, ignored, is deleted, as git reads it.
func (r Repo) Changes(ctx context.Context) ([]Change, error) {
	entries, err := r.status(ctx)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	counts, err := r.counts(ctx)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, e := range entries {
		state, ok := e.state()
		_, differs := counts[e.path]
		switch {
		case !ok:
			continue
		case inHead(e, entries) && !differs:
			continue
		}
		if i := slices.IndexFunc(changes, func(c Change) bool { return c.Path == e.path }); i >= 0 {
			// Staged as deleted, and on disk untracked: committed, and there.
			changes[i].State = Edited
			continue
		}
		changes = append(changes, Change{Path: e.path, State: state})
	}
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

// inHead reports whether e's path is in the last commit, as status says:
// one staged as new, or to be (git add -N), a nested repository, and one
// untracked with nothing staged as deleted at it aren't.
func inHead(e entry, entries []entry) bool {
	switch {
	case e.nested():
		return false
	case e.code == "??":
		return slices.Contains(entries, entry{code: "D ", path: e.path})
	}
	return !strings.Contains(e.code, "A")
}

// counts are the files on disk that differ from the last commit, by path,
// each with the lines it adds and removes, whatever's staged, as git's index
// has the files, so one taken out of it is deleted: none before the first
// commit; a binary file's, none counted.
func (r Repo) counts(ctx context.Context) (map[string][2]int, error) {
	counts := make(map[string][2]int)
	if head, err := r.commitAt(ctx, "HEAD"); err != nil || head == "" {
		return counts, err
	}
	res, err := r.git(ctx, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD")
	if err != nil {
		return nil, err
	}
	for _, rec := range names(res.Stdout) {
		if f := strings.SplitN(rec, "\t", 3); len(f) == 3 {
			added, _ := strconv.Atoi(f[0])
			removed, _ := strconv.Atoi(f[1])
			counts[f[2]] = [2]int{added, removed}
		}
	}
	return counts, nil
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
// repository, or before the first commit.
func (r Repo) Diff(ctx context.Context, path string) (string, error) {
	if head, err := r.commitAt(ctx, "HEAD"); err != nil || head == "" {
		return "", err
	}
	res, err := r.gitOn(ctx, []string{"diff", "--no-color", "--no-ext-diff", "HEAD"}, path)
	return string(res.Stdout), err
}

// Restore puts path back as it was last committed, staged and not.
func (r Repo) Restore(ctx context.Context, path string) error {
	_, err := r.gitOn(ctx, []string{"restore", "--source=HEAD", "--staged", "--worktree"}, path)
	return err
}
