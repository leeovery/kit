package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/runner"
)

// account is the person's GitHub account, as setup signs in: GitHub's
// CLI's sign-in first, else as the boot does, signing in, without a
// terminal, the first way.
func (s *Setup) account() boot.Account {
	var ask boot.Picker = &boot.FirstKey{}
	if s.Ask != nil {
		ask = s.Ask
	}
	return boot.Account{Run: s.Run, Sink: s.Sink, Ask: ask, GitHub: s.GitHub, Now: s.Now, CLIFirst: true}
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
			held, _ := s.signedIn()
			token, login, stands := s.account().Login(ctx, held)
			if stands.State != check.OK {
				return stands
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
				signedIn, login, err := s.account().SignInAndKeep(ctx, GitHubStep)
				if err != nil {
					return err
				}
				token = signedIn
				s.hold(token, login)
			}
			return s.account().HandOver(ctx, GitHubStep, token)
		},
	}
}

// configStep creates the config repository, private, on the person's
// account, or joins one of theirs, private, then clones it, as the boot
// does, and sets git in the clone (settings): done when it's cloned, looked
// at (lookAtClone), and git's set so. A --create or --join naming another
// repository than a run part-way chose starts afresh (renamed).
func (s *Setup) configStep() engine.Step {
	return engine.Step{
		Name: ConfigStep, Title: "kit-config",
		Check: func(ctx context.Context) check.Result {
			rec := s.record()
			switch {
			case s.renamed(rec):
				return check.Result{State: check.Attention, Summary: says(rec.Repo, "named anew")}
			case rec.Repo == "":
				return check.Result{State: check.Attention, Summary: "not chosen yet"}
			case !s.cloned():
				return check.Result{State: check.Attention, Summary: says(rec.Repo, "not cloned yet")}
			case !rec.Cloned:
				return check.Result{State: check.Attention, Summary: says(rec.Repo, "not looked at yet")}
			case len(s.unset(ctx, rec)) > 0:
				return check.Result{State: check.Attention, Summary: says(rec.Repo, "git not set to fetch and push it yet")}
			case rec.Created:
				return check.Result{State: check.OK, Summary: says(rec.Repo, "new, private")}
			case rec.Empty:
				return check.Result{State: check.OK, Summary: says(rec.Repo, "private, empty")}
			}
			parts := []string{rec.Repo, "joined"}
			if cfg, err := s.config(); err == nil {
				parts = append(parts, "has "+look.InWords(cfg.MacNames()))
			}
			return check.Result{State: check.OK, Summary: says(parts...)}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if rec := s.record(); s.renamed(rec) {
				if err := s.startAfresh(rec); err != nil {
					return err
				}
			}
			if s.record().Repo == "" {
				if err := s.chooseAndReady(ctx); err != nil {
					return err
				}
			}
			if err := s.clone(ctx, false); err != nil {
				return err
			}
			return s.set(ctx, s.unset(ctx, s.record()))
		},
	}
}

// renamed reports whether --create or --join names another repository than
// rec records: --create's on the account signed in to.
func (s *Setup) renamed(rec record) bool {
	_, login := s.signedIn()
	g := s.Given
	return rec.Repo != "" && (g.Join != "" && !strings.EqualFold(g.Join, rec.Repo) || g.Create != "" && !strings.EqualFold(login+"/"+g.Create, rec.Repo))
}

// startAfresh forgets rec, a run part-way, for another repository: its
// clone, setup's own, deleted, and this Mac's name, as setup gave it. Once
// the commit's edits are begun, files moved into the clone from the home
// folder and linked back, it's refused.
func (s *Setup) startAfresh(rec record) error {
	if rec.Declared {
		return fmt.Errorf("kit setup, part-way, has %s as kit-config, its edits made there, files moved into it and linked back: run kit setup again without --create or --join, or with --join %s", rec.Repo, rec.Repo)
	}
	if err := os.RemoveAll(s.Dirs.Config); err != nil {
		return err
	}
	if name, err := config.ReadMachine(s.Dirs.State); err != nil || name != "" && name == rec.Mac {
		if err != nil {
			return err
		}
		if err := config.WriteMachine(s.Dirs.State, ""); err != nil {
			return err
		}
	}
	return s.update(func(r *record) { *r = record{} })
}

// clone clones the recorded repository, unless it's cloned, has git sign
// in through GitHub's CLI there, and looks at it (lookAtClone), unless it
// was. A clone cut short, GitHub holding branches it hasn't, is cloned
// again, once: again says it's been.
func (s *Setup) clone(ctx context.Context, again bool) error {
	rec := s.record()
	if !s.cloned() {
		if err := s.account().Clone(ctx, ConfigStep, rec.Repo, s.Dirs.Config); err != nil {
			return err
		}
	}
	if rec.Cloned {
		return nil
	}
	if err := s.set(ctx, s.unset(ctx, record{})); err != nil {
		return err
	}
	cut, err := s.lookAtClone(ctx, rec)
	switch {
	case err != nil:
		return err
	case !cut:
		return nil
	case again:
		return fmt.Errorf("%s's clone, cloned again, hasn't the branches GitHub has: run kit setup again", rec.Repo)
	}
	if err := os.RemoveAll(s.Dirs.Config); err != nil {
		return err
	}
	return s.clone(ctx, true)
}

// chooseAndReady chooses the config repository (chooseRepo), readies it
// (ready), and records it, and whether it was to be created.
func (s *Setup) chooseAndReady(ctx context.Context) error {
	token, login := s.signedIn()
	if token == "" {
		return errors.New("not signed in to GitHub")
	}
	repo, create, err := s.chooseRepo(ctx, token, login)
	if err != nil {
		return err
	}
	if err := s.ready(ctx, token, repo, create); err != nil {
		return err
	}
	return s.update(func(r *record) { r.Repo, r.Created = repo, create })
}

// ready checks repo is one to join, there and private, or creates it,
// private, through GitHub's CLI, on the account setup signed in to, unless
// it's there, private, as when a run that created it stopped before it
// recorded it: its clone's then looked at for being empty (lookAtClone).
func (s *Setup) ready(ctx context.Context, token, repo string, create bool) error {
	s.doing(ConfigStep, "reading "+repo)
	private, err := s.GitHub.Private(ctx, token, repo)
	if errors.Is(err, boot.ErrNotFound) && create {
		s.doing(ConfigStep, "creating "+repo)
		if _, err := s.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "create", repo, "--private"}, Timeout: time.Minute}); err != nil {
			return fmt.Errorf("create %s: %w", repo, err)
		}
		return nil
	}
	return refusal(repo, private, err)
}

// refusal is why repo, private or not as GitHub says, unless asking it
// failed with err, isn't one for kit-config: nil when it's private.
func refusal(repo string, private bool, err error) error {
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

// lookAtClone looks at what rec's repository's clone holds, before
// anything's written in it, as git has it: no branch, and GitHub none, it's
// empty, and kit-config starts on this Mac, as one created does; no branch,
// and GitHub some, the clone was cut short, which it reports; a config,
// it's joined. Else, or holding anything when it was to be created, the
// clone's deleted, and the choice with it.
func (s *Setup) lookAtClone(ctx context.Context, rec record) (cut bool, err error) {
	branches, err := s.git(ctx, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads")
	if err != nil {
		return false, fmt.Errorf("read %s's clone: %w", rec.Repo, err)
	}
	if strings.TrimSpace(string(branches.Stdout)) == "" {
		heads, err := s.git(ctx, "ls-remote", "--heads", "origin")
		if err != nil {
			return false, fmt.Errorf("ask GitHub what %s holds: %w", rec.Repo, err)
		}
		if strings.TrimSpace(string(heads.Stdout)) != "" {
			return true, nil
		}
		return false, s.update(func(r *record) { r.Empty, r.Cloned = true, true })
	}
	if _, err := os.Stat(filepath.Join(s.Dirs.Config, config.File)); err == nil && !rec.Created {
		return false, s.update(func(r *record) { r.Cloned = true })
	}
	refused := fmt.Errorf("%s holds no %s, so it's no config repository: join yours, or create a new one", rec.Repo, config.File)
	if rec.Created {
		refused = fmt.Errorf("%s is on GitHub already, holding files: join it with --join %s, or create one named otherwise", rec.Repo, rec.Repo)
	}
	if err := os.RemoveAll(s.Dirs.Config); err != nil {
		return false, errors.Join(refused, err)
	}
	return false, errors.Join(refused, s.update(func(r *record) { *r = record{} }))
}

// setting is one of git's settings in the config repository's clone: how
// it's read, and set, and what it's set to.
type setting struct {
	get, set []string
	want     string
}

// gitConfig is the setting git config keeps called key, set to want.
func gitConfig(key, want string) setting {
	return setting{get: []string{"config", "--get", key}, set: []string{"config", key, want}, want: want}
}

// settings are git's settings rec's clone takes: git signing in through
// GitHub's CLI, for the config repository alone, as the boot clones it;
// and, kit-config starting on this Mac, the clone on main, tracking main
// alone, as kit-config's clone does, though the remote has no branch till
// the first push.
func settings(rec record) []setting {
	out := []setting{gitConfig("credential.https://github.com.helper", boot.GitCredentials)}
	if !rec.Empty {
		return out
	}
	return append(out,
		setting{get: []string{"symbolic-ref", "HEAD"}, set: []string{"symbolic-ref", "HEAD", "refs/heads/main"}, want: "refs/heads/main"},
		gitConfig("remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main"),
		gitConfig("branch.main.remote", "origin"),
		gitConfig("branch.main.merge", "refs/heads/main"),
	)
}

// unset are the settings rec's clone takes that it hasn't.
func (s *Setup) unset(ctx context.Context, rec record) []setting {
	var unset []setting
	for _, st := range settings(rec) {
		if res, err := s.git(ctx, st.get...); err != nil || strings.TrimSpace(string(res.Stdout)) != st.want {
			unset = append(unset, st)
		}
	}
	return unset
}

// set sets each of settings in the clone.
func (s *Setup) set(ctx context.Context, settings []setting) error {
	for _, st := range settings {
		if _, err := s.git(ctx, st.set...); err != nil {
			return fmt.Errorf("set git in kit-config's clone: %w", err)
		}
	}
	return nil
}

// git runs git in the config repository's clone.
func (s *Setup) git(ctx context.Context, args ...string) (runner.Result, error) {
	return s.Run.Run(ctx, runner.Command{Name: "git", Args: append([]string{"-C", s.Dirs.Config}, args...)})
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
