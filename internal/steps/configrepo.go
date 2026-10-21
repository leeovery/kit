// Package steps is kit's built-in steps, besides kinds'.
package steps

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
)

// ConfigPrivateName is the name of the step that checks the config
// repository is private.
const ConfigPrivateName = "config-private"

// ConfigPrivate checks the config repository, repo, is proven private
// wherever it goes (gitrepo.Place): every URL kit pushes it to (PushURLs),
// pushing, and origin's, fetching, each place that isn't named. The
// config is personal, and must never be public. With no remote it's pushed
// to or fetched from, it isn't public; a place git, ssh or gh can't be
// asked about fails.
func ConfigPrivate(repo gitrepo.Repo) engine.Step {
	return engine.Step{
		Name:  ConfigPrivateName,
		Title: "Privacy",
		Area:  AreaConfig,
		Check: func(ctx context.Context) check.Result {
			all, err := whereTo(ctx, repo)
			switch {
			case err != nil:
				return failed(err)
			case len(all) == 0:
				return check.Result{State: check.OK, Summary: "no remote it's pushed to or fetched from, so not public"}
			}
			return privacy(ctx, repo, all)
		},
	}
}

// where is a URL the config repository goes to or comes from, its remote,
// and which way git goes there.
type where struct {
	remote, url string
	way         gitrepo.Way
}

// whereTo is every URL kit pushes repo to, none where Push can't tell where
// to push, as it pushes nowhere, and origin's, fetched from: the same URL
// both ways twice, as ssh's config can send each way elsewhere.
func whereTo(ctx context.Context, repo gitrepo.Repo) ([]where, error) {
	remote, urls, err := repo.PushURLs(ctx)
	if err != nil && !errors.Is(err, gitrepo.ErrNoRemote) && !errors.Is(err, gitrepo.ErrNowhere) {
		return nil, err
	}
	var all []where
	for _, url := range urls {
		all = append(all, where{remote, url, gitrepo.Pushing})
	}
	origin, err := repo.URL(ctx, "origin")
	if err == nil && origin != "" {
		all = append(all, where{"origin", origin, gitrepo.Fetching})
	}
	return all, err
}

// privacy is how the config repository's places, all, stand: each that
// isn't proven private named, a repository on GitHub that isn't private,
// with how to make it so.
func privacy(ctx context.Context, repo gitrepo.Repo, all []where) check.Result {
	var private, github, unproven []string
	var items []check.Item
	seen := map[string]bool{}
	for _, w := range all {
		place, err := repo.Place(ctx, w.remote, w.url, w.way)
		if err != nil {
			return failed(err)
		}
		// A repository on GitHub is named by its owner and name, wherever
		// its URLs lead.
		key := cmp.Or(place.Repo, gitrepo.Shown(w.url))
		if place.OnThisMac || seen[key] {
			continue
		}
		seen[key] = true
		switch {
		case place.Private():
			private = append(private, place.Repo)
		case place.Repo != "":
			github = append(github, fmt.Sprintf("%s on GitHub (%s)", place.Visibility, place.Repo))
			items = append(items, check.Item{
				ID: ConfigPrivateName + ":" + place.Repo, Name: place.Repo, State: "not-private",
				Detail: "make it private: gh repo edit " + place.Repo + " --visibility private --accept-visibility-change-consequences",
			})
		default:
			unproven = append(unproven, fmt.Sprintf("can't prove %s private: %s", key, place.Unproven))
			items = append(items, check.Item{ID: ConfigPrivateName + ":" + key, Name: key, State: "unproven", Detail: place.Unproven})
		}
	}
	var says []string
	switch len(github) {
	case 0:
	case 1:
		says = append(says, github[0]+": it must be private")
	default:
		says = append(says, strings.Join(github, ", ")+": they must be private")
	}
	says = append(says, unproven...)
	switch {
	case len(says) > 0:
		return check.Result{State: check.Attention, Summary: strings.Join(says, "; "), Items: items}
	case len(private) > 0:
		return check.Result{State: check.OK, Summary: "private on GitHub (" + strings.Join(private, ", ") + ")", Glance: "private"}
	}
	return check.Result{State: check.OK, Summary: "on this Mac, so private"}
}
