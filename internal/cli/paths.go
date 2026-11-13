package cli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/steps"
)

// pathsKind names the paths section, and its command.
const pathsKind = "path"

// addPaths declares each directory last in the paths section of this Mac's
// declarations (every Mac's, with --shared), writes the shell's PATH, then
// commits and pushes. A directory in the home folder is declared with ~.
func (a *app) addPaths(ctx context.Context, r *run, dirs []string, opts addOptions) error {
	if opts.temp {
		return errors.New("--temp is for packages: a directory is on the PATH, or it isn't")
	}
	scope := r.scope(opts.shared)
	dirs = r.tildePaths(dirs)
	c := pathChanges(r, dirs)
	for _, dir := range dirs {
		c.step(ctx, dir, func(context.Context) check.Result {
			said, err := c.declare(config.PathsKind, scope, config.Entry{Name: dir, Note: opts.note})
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			return check.Result{State: check.OK, Summary: "declared in " + scope + said + ", last on the PATH"}
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
	c := pathChanges(r, dirs)
	for _, dir := range dirs {
		c.step(ctx, dir, func(context.Context) check.Result {
			return c.undeclare(config.PathsKind, dir, shared, "PATH")
		})
	}
	a.writeShellPath(ctx, r, c)
	c.sync(ctx, commitMessage("remove", pathsKind, dirs, r.machine, ""))
	return c.finish()
}

// undeclare takes name out of the section of the kind called kindName,
// this Mac's (and the shared one's, with shared), as a step of c: list
// names what the section is, as in PATH, to say where name isn't. One
// declared for every Mac, without shared, is refused, saying so.
func (c *changes) undeclare(kindName, name string, shared bool, list string) check.Result {
	from, err := c.undeclareHere(kindName, name, shared)
	if refused, ok := errors.AsType[*everyMacError](err); ok {
		return check.Result{State: check.Failed, Reason: "on every Mac's " + list + ", in " + refused.at.Pos() + ": --shared takes it out of every Mac's"}
	}
	if err != nil {
		return failed(err)
	}
	if len(from) == 0 {
		return check.Result{State: check.Failed, Reason: name + " isn't on this Mac's " + list}
	}
	return check.Result{State: check.OK, Summary: "out of " + strings.Join(from, " and ")}
}

// pathChanges starts a run of r's command over dirs, then the shell's PATH
// written (writeShellPath).
func pathChanges(r *run, dirs []string) *changes {
	return startChanges(r, append(slices.Clone(dirs), shellPathStep))
}

// shellPathStep names the step writing the shell's PATH.
const shellPathStep = "path"

// writeShellPath writes the shell's PATH as the paths sections now say, as
// a step of c.
func (a *app) writeShellPath(ctx context.Context, r *run, c *changes) {
	c.step(ctx, shellPathStep, func(ctx context.Context) check.Result {
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
