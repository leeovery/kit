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
	// Getenv reads the environment kit runs in, for the ssh and proxy
	// commands git takes from it: os.Getenv when nil.
	Getenv func(string) string
	// onUnlock, when set, is called as a commit lets go of git's index.
	onUnlock func()
	// flock, when set, takes the kernel's lock on the file open at fd in
	// syscall.Flock's place.
	flock func(fd int) error
}

// getenv is the environment variable name, as Getenv reads it.
func (r Repo) getenv(name string) string {
	if r.Getenv == nil {
		return os.Getenv(name)
	}
	return r.Getenv(name)
}

func (r Repo) git(ctx context.Context, args ...string) (runner.Result, error) {
	return r.Run.Run(ctx, r.command(args...))
}

// command is git's args in the repository, in the C locale, as kit reads
// what git says, and git translates its words, fatal: among them.
func (r Repo) command(args ...string) runner.Command {
	return runner.Command{Name: "git", Args: slices.Concat([]string{"-C", r.Dir}, args), Env: []string{"LC_ALL=C"}}
}

// gitOn runs git's args on paths in the repository, each taken as it's
// written: a name holding *, ? or [ is that file, never a pattern.
func (r Repo) gitOn(ctx context.Context, args []string, paths ...string) (runner.Result, error) {
	return r.git(ctx, slices.Concat([]string{"--literal-pathspecs"}, args, []string{"--"}, paths)...)
}

// gitIn runs git's args with env, reading paths, each ended by a NUL.
func (r Repo) gitIn(ctx context.Context, env, paths []string, args ...string) (runner.Result, error) {
	cmd := r.command(args...)
	cmd.Env = append(cmd.Env, env...)
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

// setting is git's setting, as git config --get reads it with args, the
// key last: "" when it isn't set.
func (r Repo) setting(ctx context.Context, args ...string) (string, error) {
	res, err := r.git(ctx, slices.Concat([]string{"config", "--get"}, args)...)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), err
}

// flag is git's boolean setting key: false when it isn't set.
func (r Repo) flag(ctx context.Context, key string) (bool, error) {
	value, err := r.setting(ctx, "--type=bool", key)
	return value == "true", err
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
