package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// Place is where a remote's URL takes the repository, as far as kit can
// prove it private: a repository on GitHub (Repo, as owner/name) with its
// visibility there, as gh says it; else Unproven says why kit can't tell.
type Place struct {
	Repo, Visibility string
	Unproven         string
}

// Private reports whether the place is a repository GitHub says is
// private.
func (p Place) Private() bool {
	return p.Repo != "" && p.Visibility == "private"
}

// onGitHub matches the URL of a repository on github.com in the three ways
// kit takes one, exactly, then its owner and name: https, ssh as scp writes
// it, and ssh:// URLs.
var onGitHub = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([^/]+)/([^/]+?)(?:\.git)?$`)

// githubName is an owner's or a repository's name on GitHub, never one gh
// would take for a flag.
var githubName = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

// notGitHub is why kit can't prove private a URL that isn't onGitHub's.
const notGitHub = "kit proves private only a repository on github.com, at https://github.com/<owner>/<name>, git@github.com:<owner>/<name> or ssh://git@github.com/<owner>/<name>"

// ghSignedOut is what gh exits with when it isn't signed in to GitHub.
const ghSignedOut = 4

// Place is where url, a remote's URL as git reads it (its insteadOf
// applied), takes the repository: a repository on github.com, written
// exactly as onGitHub has it, and its visibility as gh says it, asked by
// its URL, so of github.com whatever host GH_HOST names; anything else is
// unproven. An error is a failure to ask gh.
func (r Repo) Place(ctx context.Context, url string) (Place, error) {
	m := onGitHub.FindStringSubmatch(url)
	if m == nil || !githubName.MatchString(m[1]) || !githubName.MatchString(m[2]) {
		return Place{Unproven: notGitHub}, nil
	}
	repo := m[1] + "/" + m[2]
	res, err := r.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "view", "https://github.com/" + repo, "--json", "visibility", "--jq", ".visibility"}})
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == ghSignedOut {
		return Place{}, errors.New("gh isn't signed in to GitHub: run gh auth login")
	}
	if err != nil {
		return Place{}, fmt.Errorf("couldn't ask GitHub: %w", err)
	}
	return Place{Repo: repo, Visibility: strings.ToLower(strings.TrimSpace(string(res.Stdout)))}, nil
}

// Shown is remote, a URL, as kit names it: without who it signs in as,
// which a URL's sign-in may carry a token in.
func Shown(remote string) string {
	scheme, rest, ok := strings.Cut(remote, "://")
	if !ok {
		head, _, _ := strings.Cut(remote, "/")
		return remote[strings.LastIndex(head, "@")+1:]
	}
	authority, _, _ := strings.Cut(rest, "/")
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return remote
	}
	opened := ""
	if strings.HasPrefix(authority, "[") {
		opened = "["
	}
	return scheme + "://" + opened + rest[at+1:]
}
