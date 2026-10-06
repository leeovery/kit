// Package steps is kit's built-in steps, besides kinds'.
package steps

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// ConfigPrivateName is the name of the step that checks the config
// repository is private.
const ConfigPrivateName = "config-private"

// githubRemote reads a GitHub remote's owner and repository, in either of
// git's forms.
var githubRemote = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

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
			remote := strings.TrimSpace(string(res.Stdout))
			m := githubRemote.FindStringSubmatch(remote)
			if m == nil {
				return check.Result{State: check.OK, Summary: "not on GitHub, so not public there"}
			}
			repo := m[1] + "/" + m[2]
			res, err = run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "view", repo, "--json", "visibility", "--jq", ".visibility"}})
			if err != nil {
				return check.Result{State: check.Failed, Reason: "couldn't ask GitHub: " + err.Error()}
			}
			visibility := strings.ToLower(strings.TrimSpace(string(res.Stdout)))
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
