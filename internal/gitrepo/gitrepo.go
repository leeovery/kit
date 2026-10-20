// Package gitrepo commits the config repository's changes, and keeps it in
// step with its remote, through git, run as every program is: through the
// runner.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// Repo is a git repository, driven through run.
type Repo struct {
	Dir string
	Run runner.Runner
	// Sleep waits between tries for git's index while another git holds it:
	// time.Sleep when nil.
	Sleep func(time.Duration)
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

// gitIn runs git's args with env, reading paths, each ended by a NUL.
func (r Repo) gitIn(ctx context.Context, env, paths []string, args ...string) (runner.Result, error) {
	cmd := r.command(args...)
	cmd.Env = env
	for _, p := range paths {
		cmd.Input += p + "\x00"
	}
	return r.Run.Run(ctx, cmd)
}

// gitDir is the repository's git folder, as an absolute path.
func (r Repo) gitDir(ctx context.Context) (string, error) {
	res, err := r.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(res.Stdout))
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("git named no git folder for %s", r.Dir)
	}
	return dir, nil
}

// commitAt is the commit rev names: "" for none, as HEAD before the first
// commit, or a first commit's parent.
func (r Repo) commitAt(ctx context.Context, rev string) (string, error) {
	res, err := r.git(ctx, "rev-parse", "--verify", "--quiet", rev)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), err
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
