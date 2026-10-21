// Package steps is kit's built-in steps, besides kinds'.
package steps

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
)

// ConfigPrivateName is the name of the step that checks the config
// repository is private.
const ConfigPrivateName = "config-private"

// ConfigPrivate checks the config repository in dir is private on GitHub,
// asking GitHub through gh, wherever it goes: every URL kit pushes it to
// (PushURLs), and origin's, which it's fetched from, each that isn't
// private named. The config is personal, and must never be public. A
// repository with no remote, or one elsewhere, or on this Mac, isn't public
// on GitHub, so stands ok; one whose place can't be told fails, never
// taken for not on GitHub.
func ConfigPrivate(run runner.Runner, dir string) engine.Step {
	return engine.Step{
		Name:  ConfigPrivateName,
		Title: "Privacy",
		Area:  AreaConfig,
		Check: func(ctx context.Context) check.Result {
			// Where Push can't tell where to push, it pushes nowhere, so
			// there's nothing of its to check.
			urls, pushes := gitrepo.Repo{Dir: dir, Run: run}.PushURLs(ctx)
			origin, err := originURL(ctx, run, dir)
			switch {
			case err != nil:
				return failed(err)
			case origin != "" && !slices.Contains(urls, origin):
				urls = append(urls, origin)
			}
			switch {
			case len(urls) > 0:
				return privacy(ctx, run, urls)
			case errors.Is(pushes, gitrepo.ErrNoRemote):
				return check.Result{State: check.OK, Summary: "no remote, so not public"}
			}
			return check.Result{State: check.OK, Summary: "no remote it's pushed to, so not public"}
		},
	}
}

// privacy is how the config repository's places, urls, stand on GitHub:
// each repository there that isn't private named.
func privacy(ctx context.Context, run runner.Runner, urls []string) check.Result {
	var private, notPrivate []string
	var items []check.Item
	seen := map[string]bool{}
	for _, url := range urls {
		repo, visibility, err := gitrepo.GitHubVisibility(ctx, run, url)
		switch {
		case err != nil:
			return failed(err)
		case repo == "" || seen[repo]:
			continue
		case visibility == "private":
			private = append(private, repo)
		default:
			notPrivate = append(notPrivate, fmt.Sprintf("%s on GitHub (%s)", visibility, repo))
			items = append(items, check.Item{
				ID: ConfigPrivateName + ":" + repo, Name: repo, State: "not-private",
				Detail: "make it private: gh repo edit " + repo + " --visibility private --accept-visibility-change-consequences",
			})
		}
		seen[repo] = true
	}
	switch {
	case len(items) == 1:
		return check.Result{State: check.Attention, Summary: notPrivate[0] + ": it must be private", Items: items}
	case len(items) > 1:
		return check.Result{State: check.Attention, Summary: strings.Join(notPrivate, ", ") + ": they must be private", Items: items}
	case len(private) > 0:
		return check.Result{State: check.OK, Summary: "private on GitHub (" + strings.Join(private, ", ") + ")", Glance: "private"}
	}
	return check.Result{State: check.OK, Summary: "not on GitHub, so not public there"}
}

// originURL is origin's URL in the repository in dir, as git takes it: ""
// without one, as git says by exiting 2.
func originURL(ctx context.Context, run runner.Runner, dir string) (string, error) {
	res, err := run.Run(ctx, runner.Command{Name: "git", Args: []string{"-C", dir, "remote", "get-url", "origin"}})
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 2 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), err
}
