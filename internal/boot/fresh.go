package boot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
	kitstate "github.com/leeovery/kit/internal/state"
)

// refusal is a repository refused as kit-config, saying why, and whether
// it's there: with no --config, the person's asked which, at a terminal,
// the refusal in the question's place.
type refusal struct {
	why   string
	there bool
}

func (r refusal) Error() string { return r.why }

// readConfig reads the config repository (read): the one --config names,
// else the one a boot before chose, else login's kit-config. With no
// --config, each refused, the person chooses another, created or theirs,
// asked till one isn't.
func (b *Boot) readConfig(ctx context.Context, token, login string) error {
	st, err := kitstate.Load[state](b.State, stateName)
	if err != nil {
		return err
	}
	repo := b.repo(login)
	if b.Repo == "" {
		repo = cmp.Or(st.Repo, repo)
	}
	create := st.Fresh != nil && st.Fresh.Create && repo == st.Repo
	// has is whether login's kit-config is there, refused: never offered
	// again, as join or create.
	has := false
	for chosen := false; ; chosen = true {
		err := b.read(ctx, token, login, repo, create, chosen)
		refused, isRefused := errors.AsType[refusal](err)
		switch {
		case !isRefused:
			return err
		case b.Repo != "":
			return fmt.Errorf("%s: name yours with --config owner/name", refused)
		}
		has = has || refused.there && repo == login+"/"+Suggested
		if repo, create, err = b.account().ChooseRepo(ctx, b.Ask, GitHubStep, token, login, has, refused.why); err != nil {
			return err
		}
	}
}

// read reads repo, private, into the boot's copy, unless it's empty, or to
// be created (create) and not there yet: kit-config starts on this Mac
// then (startHere). It's refused when it's not there or holds no kit.toml;
// and, chosen by the person (chosen), when it's public, or holds files
// when it was to be created. One recorded as to be created that holds
// files is read: the boot before pushed them.
func (b *Boot) read(ctx context.Context, token, login, repo string, create, chosen bool) error {
	b.doing(GitHubStep, "reading "+repo)
	private, err := b.GitHub.Private(ctx, token, repo)
	switch {
	case errors.Is(err, ErrNotFound) && create:
		return b.startHere(ctx, repo, true)
	case errors.Is(err, ErrNotFound) && b.Repo == "" && repo == login+"/"+Suggested:
		return refusal{why: login + " has no repository named " + Suggested}
	case errors.Is(err, ErrNotFound):
		return refusal{why: repo + " isn't on GitHub, or this sign-in can't see it"}
	case err != nil:
		return fmt.Errorf("find %s: %w", repo, err)
	case !private && chosen:
		return refusal{why: repo + " is public on GitHub, and a config repository is always private", there: true}
	case !private:
		return fmt.Errorf("%s is public on GitHub, and a config repository is always private: make it private, then boot again", repo)
	}
	empty, err := b.GitHub.Empty(ctx, token, repo)
	switch {
	case err != nil:
		return fmt.Errorf("read %s: %w", repo, err)
	case empty:
		return b.startHere(ctx, repo, create)
	case create && chosen:
		return refusal{why: repo + " is on GitHub already, holding files", there: true}
	}
	if err := b.GitHub.fetch(ctx, token, repo, b.archive()); err != nil {
		return fmt.Errorf("read %s: %w", repo, err)
	}
	if _, err := os.Stat(filepath.Join(b.archive(), config.File)); err != nil {
		if err := os.RemoveAll(b.archive()); err != nil {
			return err
		}
		return refusal{why: repo + " holds no " + config.File + ", so it's no config repository", there: true}
	}
	return kitstate.Update(b.State, stateName, func(st *state) { st.Repo, st.Fresh = repo, nil })
}

// startHere has kit-config start on this Mac, in repo, created (create)
// or empty: the terminal this Mac uses asked, unless a boot before asked
// it for repo, and the boot's copy of any other repository gone.
func (b *Boot) startHere(ctx context.Context, repo string, create bool) error {
	st, err := kitstate.Load[state](b.State, stateName)
	if err != nil {
		return err
	}
	if st.Fresh != nil && st.Repo == repo {
		return kitstate.Update(b.State, stateName, func(st *state) { st.Fresh.Create = create })
	}
	if err := os.RemoveAll(b.archive()); err != nil {
		return err
	}
	terminal, err := b.askTerminal(ctx)
	unasked := errors.Is(err, ErrNoTerminal)
	if err != nil && !unasked {
		return err
	}
	return kitstate.Update(b.State, stateName, func(st *state) {
		st.Repo, st.Fresh = repo, &fresh{Create: create, Terminal: terminal, Unasked: unasked}
	})
}

// askTerminal asks which terminal this Mac uses, for kit-config starting
// on it: one kit knows, by name, or "" for macOS's own, which the boot
// runs in. Without a terminal to ask at (ErrNoTerminal), no flag answers
// it: kit terminal names one later.
func (b *Boot) askTerminal(ctx context.Context) (string, error) {
	names := slices.Sorted(maps.Keys(apps.Terminals))
	answers := make([]Answer, 0, len(names)+1)
	for _, name := range names {
		answers = append(answers, Answer{Label: apps.Terminals[name].Title, Does: "installed, then kit carries on there"})
	}
	answers = append(answers, Answer{Label: "Terminal", Does: "macOS's own: kit carries on here"})
	i, err := b.Ask.Choose(ctx, GitHubStep, "kit-config starts on this Mac: which terminal will it use?", answers)
	if err != nil || i == len(names) {
		return "", err
	}
	return names[i], nil
}

// nameFirst names this Mac, the first of kit-config starting on it: as
// --mac says, else asked.
func (b *Boot) nameFirst(ctx context.Context) error {
	st, err := kitstate.Load[state](b.State, stateName)
	switch {
	case err != nil:
		return err
	case st.Fresh == nil:
		return errors.New("kit-config hasn't been read")
	}
	name := b.Mac
	if name == "" {
		if name, err = b.askName(ctx); err != nil {
			return err
		}
	}
	if !validName(name) {
		return fmt.Errorf("%q can't name a Mac: lower case letters, digits and dashes, as in studio", name)
	}
	return config.WriteMachine(b.State, name)
}

// create creates repo, private, unless GitHub has it, as when a boot that
// created it stopped before cloning it.
func (b *Boot) create(ctx context.Context, token, repo string) error {
	_, err := b.GitHub.Private(ctx, token, repo)
	switch {
	case errors.Is(err, ErrNotFound):
		return b.account().CreateRepo(ctx, ConfigStep, repo)
	case err != nil:
		return fmt.Errorf("find %s: %w", repo, err)
	}
	return nil
}

// pushFirst has kit-config start on this Mac in its clone, empty: git set
// on main (OnMain), the first files written (config.Start: this Mac the
// primary, and the terminal it uses), committed, and pushed once every URL
// git pushes to is proven private. Nothing for a config that was read.
func (b *Boot) pushFirst(ctx context.Context) error {
	st, err := kitstate.Load[state](b.State, stateName)
	if err != nil || st.Fresh == nil {
		return err
	}
	mac, err := config.ReadMachine(b.State)
	if err != nil {
		return err
	}
	for _, s := range OnMain {
		if _, err := b.Run.Run(ctx, runner.Command{Name: "git", Args: append([]string{"-C", b.Config}, s.Set...)}); err != nil {
			return fmt.Errorf("set git in kit-config's clone: %w", err)
		}
	}
	if _, err := os.Lstat(filepath.Join(b.Config, config.File)); errors.Is(err, fs.ErrNotExist) {
		if err := config.Start(b.Config, config.First{Mac: mac, Kit: b.Version, Terminal: st.Fresh.Terminal, Homebrew: b.Homebrew}); err != nil {
			return err
		}
	}
	repo := gitrepo.Repo{Dir: b.Config, Run: b.Run, BranchMayBeMissing: true}
	if err := ProvenPrivate(ctx, repo, "boot again"); err != nil {
		return err
	}
	b.doing(ConfigStep, "pushing its first files")
	if repo.Env, err = b.author(ctx, repo); err != nil {
		return err
	}
	if _, err := repo.Commit(ctx, fmt.Sprintf("kit bootstrap (%s): kit-config starts on this Mac", mac), config.File, config.Shared, mac); err != nil {
		return fmt.Errorf("commit kit-config's first files: %w", err)
	}
	if err := repo.Push(ctx); err != nil {
		return fmt.Errorf("push kit-config's first files: %w", err)
	}
	return kitstate.Update(b.State, stateName, func(st *state) {
		st.Started, st.NoTerminal, st.Fresh = true, st.Fresh.Unasked, nil
	})
}

// author is who commits kit-config's first files, as git's environment
// names them: none when git has a name and an address already; else the
// account the boot signed in to, as GitHub names it, a new Mac's git
// having neither till kit-config's settings are applied.
func (b *Boot) author(ctx context.Context, repo gitrepo.Repo) ([]string, error) {
	if repo.IsSet(ctx, "user.name") && repo.IsSet(ctx, "user.email") {
		return nil, nil
	}
	token, _ := b.signedIn()
	name, email, err := b.GitHub.Author(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("find who's signed in to GitHub, to commit kit-config's first files as: %w", err)
	}
	return []string{
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
	}, nil
}
