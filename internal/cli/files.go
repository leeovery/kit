package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/linked"
)

// addFiles moves each file at paths, or every file in a folder, into
// kit-config's home folder for this Mac (every Mac's, with --shared),
// links each back in its place, then commits and pushes.
func (a *app) addFiles(ctx context.Context, r *run, paths []string, opts addOptions) error {
	if opts.temp || opts.group != "" {
		return errors.New("--temp and --group are for packages: a file is linked, or it isn't")
	}
	scope := r.scope(opts.shared)
	full, names, err := r.filePaths(paths)
	if err != nil {
		return err
	}
	c := startChanges(r, names)
	for i, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			links, err := r.files.Add(ctx, full[i], scope)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			for _, l := range links {
				c.changed(l.Path)
			}
			if len(links) == 1 {
				return check.Result{State: check.OK, Summary: "in kit-config, linked: " + links[0].Path}
			}
			return check.Result{State: check.OK, Summary: fmt.Sprintf("%d files in kit-config, linked: %s", len(links), linked.HomeFolder(scope)+"/"+strings.TrimPrefix(name, "~/"))}
		})
	}
	c.sync(ctx, commitMessage("add", linked.StepName, names, r.machine, opts.note))
	return c.finish()
}

// removeFiles puts a copy of each linked file at paths back in place of its
// link, and takes it out of kit-config: one in every Mac's home folder
// needs --shared, as it goes from every Mac. Then it commits and pushes.
func (a *app) removeFiles(ctx context.Context, r *run, paths []string, shared bool) error {
	_, names, err := r.filePaths(paths)
	if err != nil {
		return err
	}
	declared, err := r.files.Declared(ctx)
	if err != nil {
		return err
	}
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			for _, l := range declared {
				if l.Name == name && l.Scope == config.Shared && !shared {
					return check.Result{State: check.Failed, Reason: "linked on every Mac, from " + l.Path + ": --shared takes it out of every Mac's"}
				}
			}
			l, err := r.files.Remove(ctx, name)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			c.changed(l.Path)
			return check.Result{State: check.OK, Summary: "a copy in place of the link; out of kit-config (" + l.Path + ")"}
		})
	}
	c.sync(ctx, commitMessage("remove", linked.StepName, names, r.machine, ""))
	return c.finish()
}

// filePaths are paths as typed, in full and as they're shown, ~ for the
// home folder.
func (r *run) filePaths(paths []string) (full, shown []string, err error) {
	for _, p := range paths {
		if rest, ok := strings.CutPrefix(p, "~/"); ok {
			p = filepath.Join(r.homeDir, rest)
		}
		if p, err = filepath.Abs(p); err != nil {
			return nil, nil, err
		}
		full = append(full, p)
		shown = append(shown, r.files.Tilde(p))
	}
	return full, shown, nil
}
