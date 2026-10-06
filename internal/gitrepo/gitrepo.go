// Package gitrepo commits the config repository's changes, and keeps it in
// step with its remote, through git, run as every program is: through the
// runner.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// ErrNoRemote is returned by Push for a repository with nothing to push to.
var ErrNoRemote = errors.New("no remote to push to")

// Repo is a git repository, driven through run.
type Repo struct {
	Dir string
	Run runner.Runner
}

func (r Repo) git(ctx context.Context, args ...string) (runner.Result, error) {
	return r.Run.Run(ctx, runner.Command{Name: "git", Args: slices.Concat([]string{"-C", r.Dir}, args)})
}

// Files are the files git keeps, or would keep, under paths in the
// repository: tracked, and new ones it doesn't ignore. Each is a path in the
// repository, once; a tracked file deleted but not yet committed is still
// among them.
func (r Repo) Files(ctx context.Context, paths ...string) ([]string, error) {
	res, err := r.git(ctx, slices.Concat([]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, paths)...)
	if err != nil {
		return nil, err
	}
	var files []string
	for name := range strings.SplitSeq(string(res.Stdout), "\x00") {
		if name != "" && !slices.Contains(files, name) {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	return files, nil
}

// The states of a change not committed.
const (
	Edited  = "edited"
	Added   = "added"
	Deleted = "deleted"
)

// Change is a file in the repository changed and not committed.
type Change struct {
	// Path is the file, as a path in the repository.
	Path string
	// State is edited, added (new to the repository) or deleted.
	State string
	// Added and Removed count the lines the change adds and removes.
	Added, Removed int
}

// Changes are the files changed and not committed, staged or not, new ones
// each by name, with the lines each adds and removes.
func (r Repo) Changes(ctx context.Context) ([]Change, error) {
	res, err := r.git(ctx, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var changes []Change
	for line := range strings.Lines(string(res.Stdout)) {
		line = strings.TrimRight(line, "\n")
		if len(line) < 4 {
			continue
		}
		code, name := line[:2], unquote(line[3:])
		if _, to, ok := strings.Cut(name, " -> "); ok {
			name = unquote(to)
		}
		c := Change{Path: name, State: Edited}
		switch {
		case code == "??" || strings.Contains(code, "A"):
			c.State = Added
		case strings.Contains(code, "D"):
			c.State = Deleted
		}
		changes = append(changes, c)
	}
	if len(changes) == 0 {
		return nil, nil
	}
	counts := make(map[string][2]int)
	if res, err := r.git(ctx, "diff", "--numstat", "HEAD"); err == nil {
		for line := range strings.Lines(string(res.Stdout)) {
			f := strings.SplitN(strings.TrimRight(line, "\n"), "\t", 3)
			if len(f) == 3 {
				added, _ := strconv.Atoi(f[0])
				removed, _ := strconv.Atoi(f[1])
				counts[unquote(f[2])] = [2]int{added, removed}
			}
		}
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

// unquote is a path as git prints it, without the quotes it puts round one
// with unusual characters.
func unquote(name string) string {
	if s, err := strconv.Unquote(name); err == nil {
		return s
	}
	return name
}

// Diff is the change to path not committed, as git shows it: none for a
// file new to the repository.
func (r Repo) Diff(ctx context.Context, path string) (string, error) {
	res, err := r.git(ctx, "diff", "HEAD", "--", path)
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}

// Restore puts path back as it was last committed, staged and not.
func (r Repo) Restore(ctx context.Context, path string) error {
	_, err := r.git(ctx, "restore", "--source=HEAD", "--staged", "--worktree", "--", path)
	return err
}

// Commit commits the changes to files, paths in the repository, alone, with
// message: anything else uncommitted is left as it is. Files with no changes
// commit nothing, which is no error; it reports whether it committed.
func (r Repo) Commit(ctx context.Context, message string, files ...string) (bool, error) {
	paths := slices.Concat([]string{"--"}, files)
	res, err := r.git(ctx, slices.Concat([]string{"status", "--porcelain"}, paths)...)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(res.Stdout)) == "" {
		return false, nil
	}
	if _, err := r.git(ctx, slices.Concat([]string{"add"}, paths)...); err != nil {
		return false, err
	}
	if _, err := r.git(ctx, slices.Concat([]string{"commit", "--quiet", "-m", message}, paths)...); err != nil {
		return false, err
	}
	return true, nil
}

// Push brings in the remote's changes, rebasing what's committed here onto
// them, then pushes. Anything uncommitted is set aside while it rebases, and
// put back. A rebase that stops on a conflict is aborted, leaving the
// repository as it was, and nothing is pushed.
func (r Repo) Push(ctx context.Context) error {
	res, err := r.git(ctx, "remote")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(res.Stdout)) == "" {
		return ErrNoRemote
	}
	if _, err := r.git(ctx, "pull", "--rebase", "--autostash", "--quiet"); err != nil {
		if _, abortErr := r.git(ctx, "rebase", "--abort"); abortErr == nil {
			return fmt.Errorf("the remote's changes conflict with this Mac's, so nothing was pushed; the rebase was undone: %w", err)
		}
		return fmt.Errorf("couldn't bring in the remote's changes, so nothing was pushed: %w", err)
	}
	if _, err := r.git(ctx, "push", "--quiet"); err != nil {
		return fmt.Errorf("couldn't push: %w", err)
	}
	return nil
}

// githubRemote reads a GitHub remote's owner and repository, in either of
// git's forms.
var githubRemote = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// GitHubVisibility is the visibility on GitHub (private, internal or
// public) of the repository at remote, a URL in either of git's forms,
// asked through gh, and its owner/name; repo is "" for a remote that isn't
// on GitHub, so isn't public there.
func GitHubVisibility(ctx context.Context, run runner.Runner, remote string) (repo, visibility string, err error) {
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return "", "", nil
	}
	repo = m[1] + "/" + m[2]
	res, err := run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "view", repo, "--json", "visibility", "--jq", ".visibility"}})
	if err != nil {
		return repo, "", fmt.Errorf("couldn't ask GitHub: %w", err)
	}
	return repo, strings.ToLower(strings.TrimSpace(string(res.Stdout))), nil
}
