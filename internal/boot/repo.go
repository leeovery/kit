package boot

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
)

// RepoAsker asks which repository is kit-config, at a terminal, in a
// step's row: a choice, or a name typed, suggested to start with.
type RepoAsker interface {
	Choose(ctx context.Context, step, question string, answers []Answer) (int, error)
	Text(ctx context.Context, step, question, suggested string) (string, error)
}

// Suggested is the name kit suggests for a new config repository.
const Suggested = "kit-config"

// The names a repository can have on GitHub: one of the person's, and one
// as owner/name.
var (
	repoName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	fullName = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
)

// NewRepo is name, a new repository's on login's account, as owner/name,
// and that it's to be created.
func NewRepo(login, name string) (string, bool, error) {
	if !repoName.MatchString(name) || name == "." || name == ".." {
		return "", false, fmt.Errorf("%q can't name a repository on GitHub: letters, digits, dots, dashes and underscores, as in %s", name, Suggested)
	}
	return login + "/" + name, true, nil
}

// TheirRepo is repo, one of the person's, as owner/name, and that it's not
// to be created.
func TheirRepo(repo string) (string, bool, error) {
	if !fullName.MatchString(repo) {
		return "", false, fmt.Errorf("%q isn't a repository on GitHub, as owner/name", repo)
	}
	return repo, false, nil
}

// ChooseRepo asks, in step's row, which repository is kit-config for
// login, signed in with token: the repository, as owner/name, and whether
// it's to be created. has is whether login has one named Suggested,
// offered first then; else a new one is. said, when it's not "", is asked
// in the question's place: why the last one named was refused.
func (a Account) ChooseRepo(ctx context.Context, ask RepoAsker, step, token, login string, has bool, said string) (string, bool, error) {
	mine := login + "/" + Suggested
	ownNew := Answer{Label: "Create " + Suggested, Does: "a new private repository on " + login + "'s account"}
	pick := Answer{Label: "Join one of yours", Does: "pick from your private repositories"}
	other := Answer{Label: "Another name", Does: "create it under a name of your own"}
	question, answers := login+" has no repository named "+Suggested, []Answer{ownNew, pick, other}
	if has {
		question = login + " has " + Suggested
		answers = []Answer{{Label: "Join " + mine, Does: "your " + Suggested}, {Label: "Join another of yours", Does: pick.Does}, {Label: "Create a new one", Does: other.Does}}
	}
	if said != "" {
		question = said
	}
	i, err := ask.Choose(ctx, step, question, answers)
	switch {
	case err != nil:
		return "", false, err
	case i == 0:
		return mine, !has, nil
	case i == 1:
		return a.pickRepo(ctx, ask, step, token)
	}
	name, err := ask.Text(ctx, step, "its name, a new private repository on "+login+"'s account", Suggested)
	if err != nil {
		return "", false, err
	}
	return NewRepo(login, name)
}

// pickRepo is one of the person's private repositories, picked from them,
// or another named, to join.
func (a Account) pickRepo(ctx context.Context, ask RepoAsker, step, token string) (string, bool, error) {
	repos, err := a.GitHub.Repos(ctx, token)
	if err != nil {
		return "", false, fmt.Errorf("list your private repositories: %w", err)
	}
	answers := make([]Answer, 0, len(repos)+1)
	for _, r := range repos {
		answers = append(answers, Answer{Label: r})
	}
	answers = append(answers, Answer{Label: "Another", Does: "named as owner/name"})
	i, err := ask.Choose(ctx, step, "which of your private repositories?", answers)
	switch {
	case err != nil:
		return "", false, err
	case i < len(repos):
		return repos[i], false, nil
	}
	name, err := ask.Text(ctx, step, "its name, as owner/name", "")
	if err != nil {
		return "", false, err
	}
	return TheirRepo(name)
}

// CreateRepo creates repo, as owner/name, private, through GitHub's CLI,
// in step's row.
func (a Account) CreateRepo(ctx context.Context, step, repo string) error {
	a.doing(step, "creating "+repo)
	if _, err := a.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "create", repo, "--private"}, Timeout: time.Minute}); err != nil {
		return fmt.Errorf("create %s: %w", repo, err)
	}
	return nil
}

// Setting is one of git's settings in a clone: how it's read, and set,
// and what it's set to.
type Setting struct {
	Get, Set []string
	Want     string
}

// GitSetting is the setting git config keeps called key, set to want.
func GitSetting(key, want string) Setting {
	return Setting{Get: []string{"config", "--get", key}, Set: []string{"config", key, want}, Want: want}
}

// OnMain are the settings an empty repository's clone takes for kit-config
// to start in it: on main, tracking main alone, as kit-config's clone does,
// though the remote has no branch till the first push.
var OnMain = []Setting{
	{Get: []string{"symbolic-ref", "HEAD"}, Set: []string{"symbolic-ref", "HEAD", "refs/heads/main"}, Want: "refs/heads/main"},
	GitSetting("remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main"),
	GitSetting("branch.main.remote", "origin"),
	GitSetting("branch.main.merge", "refs/heads/main"),
}

// ProvenPrivate is why repo, kit-config's clone, isn't pushed to a
// repository proven private, each URL git pushes it to, after any pushurl
// or pushInsteadOf: nil when every one is. again is what to do once it's
// made private.
func ProvenPrivate(ctx context.Context, repo gitrepo.Repo, again string) error {
	err := repo.ProvePrivate(ctx)
	notPrivate, isNot := errors.AsType[gitrepo.NotPrivate](err)
	switch {
	case isNot && notPrivate.Place.Repo != "":
		return fmt.Errorf("%s, and kit-config is pushed only to a private repository: make it private, then %s", notPrivate, again)
	case isNot:
		return fmt.Errorf("%s: kit-config is pushed only to a repository kit can prove private", notPrivate)
	case err != nil:
		return fmt.Errorf("find where kit-config is pushed: %w", err)
	}
	return nil
}
