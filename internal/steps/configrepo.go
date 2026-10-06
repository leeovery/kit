// Package steps is kit's built-in steps, besides kinds'.
package steps

import (
	"context"
	"errors"
	"fmt"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
)

// ConfigPrivateName is the name of the step that checks the config
// repository is private.
const ConfigPrivateName = "config-private"

// ConfigPrivate checks the config repository in dir is private on GitHub,
// asking GitHub through gh: the config is personal, and must never be
// public. A repository with no remote, or one elsewhere, isn't public on
// GitHub, so stands ok.
func ConfigPrivate(run runner.Runner, dir string) engine.Step {
	return engine.Step{
		Name:  ConfigPrivateName,
		Title: "Privacy",
		Area:  AreaConfig,
		Check: func(ctx context.Context) check.Result {
			res, err := run.Run(ctx, runner.Command{Name: "git", Args: []string{"-C", dir, "remote", "get-url", "origin"}})
			if _, exited := errors.AsType[*runner.ExitError](err); exited {
				return check.Result{State: check.OK, Summary: "no remote, so not public"}
			}
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			repo, visibility, err := gitrepo.GitHubVisibility(ctx, run, string(res.Stdout))
			switch {
			case err != nil:
				return check.Result{State: check.Failed, Reason: err.Error()}
			case repo == "":
				return check.Result{State: check.OK, Summary: "not on GitHub, so not public there"}
			}
			if visibility == "private" {
				return check.Result{State: check.OK, Summary: "private on GitHub (" + repo + ")", Glance: "private"}
			}
			return check.Result{
				State:   check.Attention,
				Summary: fmt.Sprintf("%s on GitHub (%s): it must be private", visibility, repo),
				Items: []check.Item{{
					ID: ConfigPrivateName + ":" + repo, Name: repo, State: "not-private",
					Detail: "make it private: gh repo edit " + repo + " --visibility private --accept-visibility-change-consequences",
				}},
			}
		},
	}
}
