// Package gitrepo commits the config repository's changes, and keeps it in
// step with its remote, through git, run as every program is: through the
// runner.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
