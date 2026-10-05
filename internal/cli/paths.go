package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/steps"
)

// pathsKind is how kit add and kit remove name the paths section.
const pathsKind = "path"

// addPaths declares each directory last in the paths section of this Mac's
// declarations (every Mac's, with --shared), writes the shell's PATH, then
// commits and pushes. A directory in the home folder is declared with ~.
func (a *app) addPaths(ctx context.Context, r *run, dirs []string, opts addOptions) error {
	if opts.temp || opts.group != "" {
		return errors.New("--temp and --group are for packages: a directory is on the PATH, or it isn't")
	}
	scope := r.scope(opts.shared)
	dirs = r.tildePaths(dirs)
	c := startChanges(r, dirs)
	for _, dir := range dirs {
		c.step(ctx, dir, func(context.Context) check.Result {
			if err := r.cfg.Declare(config.PathsKind, scope, config.Entry{Name: dir, Note: opts.note}, ""); err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			c.changed(config.DeclFile(scope))
			return check.Result{State: check.OK, Summary: "declared in " + scope + ", last on the PATH"}
		})
	}
	a.writeShellPath(ctx, r, c)
	c.sync(ctx, commitMessage("add", pathsKind, dirs, r.machine, opts.note))
	return c.finish()
}

// removePaths takes each directory out of the paths section, this Mac's
// (and the shared one, with --shared), writes the shell's PATH, then
// commits and pushes.
func (a *app) removePaths(ctx context.Context, r *run, dirs []string, shared bool) error {
	dirs = r.tildePaths(dirs)
	c := startChanges(r, dirs)
	for _, dir := range dirs {
		c.step(ctx, dir, func(context.Context) check.Result {
			where, err := r.cfg.Where(config.PathsKind, dir)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			var from []string
			for _, e := range where {
				switch {
				case e.Scope == config.Shared && !shared:
					return check.Result{State: check.Failed, Reason: "on every Mac's PATH, in " + e.Pos() + ": --shared takes it out of every Mac's"}
				case e.Scope != config.Shared && e.Scope != r.machine:
					continue
				}
				if err := r.cfg.Undeclare(config.PathsKind, e.Scope, dir); err != nil {
					return check.Result{State: check.Failed, Reason: err.Error()}
				}
				c.changed(config.DeclFile(e.Scope))
				from = append(from, e.Scope)
			}
			if len(from) == 0 {
				return check.Result{State: check.Failed, Reason: dir + " isn't on this Mac's PATH"}
			}
			return check.Result{State: check.OK, Summary: "out of " + strings.Join(from, " and ")}
		})
	}
	a.writeShellPath(ctx, r, c)
	c.sync(ctx, commitMessage("remove", pathsKind, dirs, r.machine, ""))
	return c.finish()
}

// writeShellPath writes the shell's PATH as the paths sections now say, as
// a step of c.
func (a *app) writeShellPath(ctx context.Context, r *run, c *changes) {
	c.step(ctx, "path", func(ctx context.Context) check.Result {
		dirs, err := r.cfg.ShellPath(r.homeDir, r.machine)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		step := steps.PathFile(dirs, filepath.Join(r.stateDir, steps.PathFileName))
		if err := step.Apply(ctx, check.Result{}); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't write the shell's PATH: " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "the shell's PATH written: new shells have it"}
	})
}

// tildePaths are directories as declared: one in the home folder with ~.
func (r *run) tildePaths(dirs []string) []string {
	out := make([]string, len(dirs))
	for i, dir := range dirs {
		if rest, ok := strings.CutPrefix(filepath.Clean(dir), r.homeDir+"/"); ok && filepath.IsAbs(dir) {
			dir = "~/" + rest
		}
		out[i] = dir
	}
	return out
}
