package setup

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/runner"
)

// account is the person's GitHub account, as setup signs in: as the boot
// does, signing in, without a terminal, the first way.
func (s *Setup) account() boot.Account {
	var ask boot.Picker = &boot.FirstKey{}
	if s.Ask != nil {
		ask = s.Ask
	}
	return boot.Account{Run: s.Run, Sink: s.Sink, Ask: ask, GitHub: s.GitHub, Now: s.Now}
}

func (s *Setup) signedIn() (token, login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token, s.login
}

func (s *Setup) hold(token, login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token, s.login = token, login
}

// gitHubStep signs in to GitHub, as GitHub's CLI is signed in, else as the
// boot does, then hands the sign-in to GitHub's CLI, installed if need be:
// done when GitHub takes the sign-in, and GitHub's CLI has it.
func (s *Setup) gitHubStep() engine.Step {
	return engine.Step{
		Name: GitHubStep, Title: "GitHub",
		Check: func(ctx context.Context) check.Result {
			token, _ := s.signedIn()
			if token == "" {
				token = s.account().Known(ctx)
			}
			if token == "" {
				return check.Result{State: check.Attention, Summary: "not signed in"}
			}
			login, err := s.GitHub.Login(ctx, token)
			switch {
			case errors.Is(err, boot.ErrSignedOut):
				return check.Result{State: check.Attention, Summary: "signed out"}
			case err != nil:
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			s.hold(token, login)
			if !s.account().CLISignedIn(ctx) {
				return check.Result{State: check.Attention, Summary: login + " · GitHub's CLI not signed in yet"}
			}
			return check.Result{State: check.OK, Summary: says(login, "GitHub's CLI's sign-in")}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			token, _ := s.signedIn()
			if token == "" {
				var err error
				if token, err = s.account().SignIn(ctx, GitHubStep); err != nil {
					return err
				}
				login, err := s.GitHub.Login(ctx, token)
				if err != nil {
					return fmt.Errorf("find who signed in: %w", err)
				}
				s.hold(token, login)
				s.account().Keep(ctx, token)
			}
			return s.account().HandOver(ctx, GitHubStep, token)
		},
	}
}

// configStep creates the config repository, private, on the person's
// account, or joins one of theirs, private, then clones it, as the boot
// does: done when it's cloned.
func (s *Setup) configStep() engine.Step {
	return engine.Step{
		Name: ConfigStep, Title: "kit-config",
		Check: func(context.Context) check.Result {
			rec := s.record()
			switch {
			case rec.Repo == "":
				return check.Result{State: check.Attention, Summary: "not chosen yet"}
			case !s.cloned():
				return check.Result{State: check.Attention, Summary: rec.Repo + " · not cloned yet"}
			case rec.Created:
				return check.Result{State: check.OK, Summary: says(rec.Repo, "created, private")}
			}
			parts := []string{rec.Repo, "joined"}
			if cfg, err := s.config(); err == nil {
				parts = append(parts, "has "+look.InWords(cfg.MacNames()))
			}
			return check.Result{State: check.OK, Summary: says(parts...)}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			token, login := s.signedIn()
			if token == "" {
				return errors.New("not signed in to GitHub")
			}
			if s.record().Repo == "" {
				repo, create, err := s.chooseRepo(ctx, token, login)
				if err != nil {
					return err
				}
				if err := s.ready(ctx, token, repo, create); err != nil {
					return err
				}
				if err := s.update(func(r *record) { r.Repo, r.Created = repo, create }); err != nil {
					return err
				}
			}
			if s.cloned() {
				return nil
			}
			rec := s.record()
			if err := s.account().Clone(ctx, ConfigStep, rec.Repo, s.Dirs.Config); err != nil {
				return err
			}
			if rec.Created {
				return s.trackMain(ctx)
			}
			return nil
		},
	}
}

// ready creates repo, private, through GitHub's CLI, or checks it's one to
// join: there, and private.
func (s *Setup) ready(ctx context.Context, token, repo string, create bool) error {
	if create {
		s.doing(ConfigStep, "creating "+repo)
		_, name, _ := strings.Cut(repo, "/")
		if _, err := s.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "create", name, "--private"}, Timeout: time.Minute}); err != nil {
			return fmt.Errorf("create %s: %w", repo, err)
		}
		return nil
	}
	s.doing(ConfigStep, "reading "+repo)
	private, err := s.GitHub.Private(ctx, token, repo)
	switch {
	case errors.Is(err, boot.ErrNotFound):
		return fmt.Errorf("%s isn't on GitHub, or this sign-in can't see it", repo)
	case err != nil:
		return fmt.Errorf("find %s: %w", repo, err)
	case !private:
		return fmt.Errorf("%s is public on GitHub, and a config repository is always private: make it private, then run kit setup again", repo)
	}
	return nil
}

// trackMain has a new repository's clone, empty, on main, tracking main
// alone, as kit-config's clone does, though the remote has no branch till
// the first push.
func (s *Setup) trackMain(ctx context.Context) error {
	for _, args := range [][]string{
		{"symbolic-ref", "HEAD", "refs/heads/main"},
		{"config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main"},
		{"config", "branch.main.remote", "origin"},
		{"config", "branch.main.merge", "refs/heads/main"},
	} {
		if _, err := s.Run.Run(ctx, runner.Command{Name: "git", Args: append([]string{"-C", s.Dirs.Config}, args...)}); err != nil {
			return fmt.Errorf("set the clone on main: %w", err)
		}
	}
	return nil
}

// The names a repository can have on GitHub: one of the person's, and one
// as owner/name.
var (
	repoName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	fullName = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
)

// created is name, a new repository's on login's account, as owner/name.
func created(login, name string) (string, bool, error) {
	if !repoName.MatchString(name) || name == "." || name == ".." {
		return "", false, fmt.Errorf("%q can't name a repository on GitHub: letters, digits, dots, dashes and underscores, as in kit-config", name)
	}
	return login + "/" + name, true, nil
}

// joined is repo, one to join, as owner/name.
func joined(repo string) (string, bool, error) {
	if !fullName.MatchString(repo) {
		return "", false, fmt.Errorf("%q isn't a repository on GitHub, as owner/name", repo)
	}
	return repo, false, nil
}

// suggested is the name setup suggests for a new config repository.
const suggested = "kit-config"

// chooseRepo is the config repository, as owner/name, and whether it's to
// be created: as a flag says, else asked. The person's kit-config is
// offered when they have one; else a new one, one of their private
// repositories, or another name.
func (s *Setup) chooseRepo(ctx context.Context, token, login string) (string, bool, error) {
	g := s.Given
	switch {
	case g.Create != "" && g.Join != "":
		return "", false, errors.New("--create and --join both name kit-config: give one")
	case g.Create != "":
		return created(login, g.Create)
	case g.Join != "":
		return joined(g.Join)
	case s.Ask == nil:
		return "", false, s.unanswered("--create <name> makes a new config repository (" + suggested + ", unless you've one), or --join owner/name joins one of yours")
	}
	mine := login + "/" + suggested
	_, err := s.GitHub.Private(ctx, token, mine)
	has := err == nil
	if err != nil && !errors.Is(err, boot.ErrNotFound) {
		return "", false, fmt.Errorf("find %s: %w", mine, err)
	}
	ownNew := boot.Answer{Label: "Create " + suggested, Does: "a new private repository on " + login + "'s account"}
	pick := boot.Answer{Label: "Join one of yours", Does: "pick from your private repositories"}
	other := boot.Answer{Label: "Another name", Does: "create it under a name of your own"}
	question, answers := login+" has no repository named "+suggested, []boot.Answer{ownNew, pick, other}
	if has {
		question = login + " has " + suggested
		answers = []boot.Answer{{Label: "Join " + mine, Does: "your " + suggested}, {Label: "Join another of yours", Does: pick.Does}, {Label: "Create a new one", Does: other.Does}}
	}
	i, err := s.Ask.Choose(ctx, ConfigStep, question, answers)
	switch {
	case err != nil:
		return "", false, err
	case i == 0:
		return mine, !has, nil
	case i == 1:
		return s.pickRepo(ctx, token)
	}
	name, err := s.Ask.Text(ctx, ConfigStep, "its name, a new private repository on "+login+"'s account", suggested)
	if err != nil {
		return "", false, err
	}
	return created(login, name)
}

// pickRepo is one of the person's private repositories, picked from them,
// or another named, to join.
func (s *Setup) pickRepo(ctx context.Context, token string) (string, bool, error) {
	repos, err := s.GitHub.Repos(ctx, token)
	if err != nil {
		return "", false, fmt.Errorf("list your private repositories: %w", err)
	}
	answers := make([]boot.Answer, 0, len(repos)+1)
	for _, r := range repos {
		answers = append(answers, boot.Answer{Label: r})
	}
	answers = append(answers, boot.Answer{Label: "Another", Does: "named as owner/name"})
	i, err := s.Ask.Choose(ctx, ConfigStep, "which of your private repositories?", answers)
	switch {
	case err != nil:
		return "", false, err
	case i < len(repos):
		return repos[i], false, nil
	}
	name, err := s.Ask.Text(ctx, ConfigStep, "its name, as owner/name", "")
	if err != nil {
		return "", false, err
	}
	return joined(name)
}
