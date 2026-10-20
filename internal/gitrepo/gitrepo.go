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
	return r.Run.Run(ctx, r.command(args...))
}

func (r Repo) command(args ...string) runner.Command {
	return runner.Command{Name: "git", Args: slices.Concat([]string{"-C", r.Dir}, args)}
}

// gitOn runs git's args on paths in the repository, each taken as it's
// written: a name holding *, ? or [ is that file, never a pattern.
func (r Repo) gitOn(ctx context.Context, args []string, paths ...string) (runner.Result, error) {
	return r.git(ctx, slices.Concat([]string{"--literal-pathspecs"}, args, []string{"--"}, paths)...)
}

// Files are the files git keeps, or would keep, under paths in the
// repository: tracked, and new ones it doesn't ignore. Each is a path in the
// repository, once; a tracked file deleted but not yet committed is still
// among them.
func (r Repo) Files(ctx context.Context, paths ...string) ([]string, error) {
	res, err := r.gitOn(ctx, []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"}, paths...)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, name := range names(res.Stdout) {
		if !slices.Contains(files, name) {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	return files, nil
}

// names are the paths git prints separated by NULs, as -z has it.
func names(out []byte) []string {
	var paths []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			paths = append(paths, name)
		}
	}
	return paths
}

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
	// Path is the file, as a path in the repository.
	Path string
	// State is edited, added (new to the repository), deleted, or a
	// repository nested in this one.
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
		path := strings.TrimSuffix(e.path, "/")
		if i := slices.IndexFunc(changes, func(c Change) bool { return c.Path == path }); i >= 0 {
			// Staged as deleted, and on disk untracked: committed, and there.
			changes[i].State = Edited
			continue
		}
		changes = append(changes, Change{Path: path, State: state})
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

// Diff is the change to path not committed, as git shows it, uncoloured:
// none for a file new to the repository.
func (r Repo) Diff(ctx context.Context, path string) (string, error) {
	res, err := r.gitOn(ctx, []string{"diff", "--no-color", "--no-ext-diff", "HEAD"}, path)
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}

// Restore puts path back as it was last committed, staged and not.
func (r Repo) Restore(ctx context.Context, path string) error {
	_, err := r.gitOn(ctx, []string{"restore", "--source=HEAD", "--staged", "--worktree"}, path)
	return err
}

// Commit commits paths in the repository with message, each whole as it is
// on disk: a file edited, new or deleted, staged or not, a rename's two
// sides, a folder's every file, a file become a folder or a folder a file.
// Anything else staged is left as it is, the commit made from an index of
// the last commit's and these alone; hooks and signing run as for any. A
// file staged as deleted, on disk and ignored, is committed as deleted, as
// the user staged it. What git can't keep is left out, never failing the
// rest: an ignored file, an empty folder, a path gone git never kept, a
// repository nested in this one. Then the index holds those files as
// committed, so a file added then gone before it was committed (AD) is out
// of it. It reports whether it committed: with nothing to commit it
// doesn't, which is no error; an error once it has is the index's.
func (r Repo) Commit(ctx context.Context, message string, paths ...string) (bool, error) {
	if len(paths) == 0 {
		return false, nil
	}
	changed, err := r.status(ctx, paths...)
	if err != nil || len(changed) == 0 {
		return false, err
	}
	stage, deleted, files := plan(changed)
	if len(files) == 0 {
		return false, nil
	}
	committed := false
	err = r.withHeadIndex(ctx, func(env []string) error {
		if len(deleted) > 0 {
			if _, err := r.gitIn(ctx, env, deleted, "update-index", "--force-remove", "-z", "--stdin"); err != nil {
				return err
			}
		}
		if len(stage) > 0 {
			if _, err := r.gitIn(ctx, env, stage, "update-index", "--add", "--remove", "--replace", "-z", "--stdin"); err != nil {
				return err
			}
		}
		res, err := r.gitIn(ctx, env, nil, "diff", "--cached", "--name-only", "-z", "--no-renames")
		if err != nil || len(names(res.Stdout)) == 0 {
			return err
		}
		if _, err := r.gitIn(ctx, env, nil, "commit", "--quiet", "-m", message); err != nil {
			return err
		}
		committed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if _, err := r.gitIn(ctx, nil, files, "--literal-pathspecs", "reset", "--quiet", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
		return committed, fmt.Errorf("git's index still shows them as they were: %w", err)
	}
	return committed, nil
}

// plan is what a commit does with each file status names: those to stage as
// they are on disk; those to commit as deleted, staged so and on disk, with
// no untracked twin, as an ignored file has none; and every file it
// touches, whose index entries then take the last commit's. A nested
// repository it leaves alone.
func plan(changed []entry) (stage, deleted, files []string) {
	untracked := func(path string) bool {
		return slices.Contains(changed, entry{code: "??", path: path})
	}
	for _, e := range changed {
		switch {
		case e.nested() || slices.Contains(files, e.path):
			continue
		case e.code == "D " && !untracked(e.path):
			deleted = append(deleted, e.path)
		default:
			stage = append(stage, e.path)
		}
		files = append(files, e.path)
	}
	return stage, deleted, files
}

// withHeadIndex runs do with an index of the last commit's alone (empty
// before the first), a file of its own in the repository's git folder, so
// the user's is never touched: git run with env uses it. The file goes
// once do returns.
func (r Repo) withHeadIndex(ctx context.Context, do func(env []string) error) error {
	res, err := r.git(ctx, "rev-parse", "--absolute-git-dir", "--verify", "--quiet", "HEAD")
	unborn := false
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		// HEAD names no commit yet: the git folder's printed all the same.
		unborn, err = true, nil
	}
	if err != nil {
		return err
	}
	gitDir, _, _ := strings.Cut(string(res.Stdout), "\n")
	f, err := os.CreateTemp(gitDir, "kit-index-*")
	if err != nil {
		return fmt.Errorf("couldn't make an index of its own: %w", err)
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
	if !unborn {
		if _, err := r.gitIn(ctx, env, nil, "read-tree", "HEAD"); err != nil {
			return err
		}
	}
	return do(env)
}

// gitIn runs git's args with env, reading paths, each ended by a NUL.
func (r Repo) gitIn(ctx context.Context, env, paths []string, args ...string) (runner.Result, error) {
	cmd := r.command(args...)
	cmd.Env = env
	for _, p := range paths {
		cmd.Input += p + "\x00"
	}
	return r.Run.Run(ctx, cmd)
}

// Rule is one of git's rules ignoring a file: its pattern, and the file and
// line it's in.
type Rule struct {
	Pattern, Source string
	Line            int
}

// Ignored are those of paths in the repository git ignores, each with the
// rule that does: a file at one, git would never keep.
func (r Repo) Ignored(ctx context.Context, paths ...string) (map[string]Rule, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	// check-ignore refuses --literal-pathspecs, and needs none: it matches
	// each path against the rules alone, never against the files there.
	res, err := r.gitIn(ctx, nil, paths, "check-ignore", "--verbose", "--stdin", "-z")
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		// No rule matched any of them.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ignored := map[string]Rule{}
	f := strings.Split(string(res.Stdout), "\x00")
	for i := 0; i+3 < len(f); i += 4 {
		source, line, pattern, path := f[i], f[i+1], f[i+2], f[i+3]
		if strings.HasPrefix(pattern, "!") {
			// A rule that keeps it.
			continue
		}
		n, _ := strconv.Atoi(line)
		ignored[path] = Rule{Pattern: pattern, Source: source, Line: n}
	}
	return ignored, nil
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

// Unpushed is how many commits are waiting to be pushed to the branch's
// upstream: none without an upstream.
func (r Repo) Unpushed(ctx context.Context) int {
	res, err := r.git(ctx, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	return n
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
