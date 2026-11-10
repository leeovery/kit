package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind/gitconfig"
	"github.com/leeovery/kit/internal/steps"
)

// newMacName names the step committing the new Mac the boot added to
// kit-config.
const newMacName = "new-mac"

// newMacStep commits and pushes the new Mac the boot added to repo, mac,
// left uncommitted as git couldn't commit yet: its table in kit.toml and its
// folder, those alone, as kit machine add commits them. Once they're
// committed and nothing's left to push, the boot's record forgets the Mac,
// so later runs don't make the step; till then each apply commits or
// pushes what's left. Applying waits for git's name and email; a commit
// git can't sign yet, as the key it signs with comes later, is left for
// the next run, saying what git said.
func newMacStep(repo gitrepo.Repo, stateDir, mac string) engine.Step {
	paths := []string{config.File, mac}
	what := mac + "'s folder"
	if _, err := os.Stat(filepath.Join(repo.Dir, mac)); err != nil {
		what = mac + "'s table in " + config.File
	}
	settled := check.Result{State: check.OK, Summary: what + " committed"}
	// done is what applying did, which the check after it says.
	var done *check.Result
	return engine.Step{
		Name: newMacName, Title: "New Mac", Area: steps.AreaConfig, AlwaysApply: true,
		Waits: []engine.Wait{gitSetting(repo, "user.name", "git's name"), gitSetting(repo, "user.email", "git's email")},
		Check: func(ctx context.Context) check.Result {
			if done != nil {
				return *done
			}
			changes, err := repo.Changes(ctx)
			switch {
			case err != nil:
				return check.Result{State: check.Failed, Reason: err.Error()}
			case slices.ContainsFunc(changes, func(c gitrepo.Change) bool { return c.Path == config.File || strings.HasPrefix(c.Path, mac+"/") }):
				return check.Result{State: check.Attention, Summary: what + " not committed"}
			}
			return settled
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			out, err := repo.Commit(ctx, commitMessage("add", machineCommand, []string{mac}, mac, ""), paths...)
			if errors.Is(err, gitrepo.ErrUnsigned) {
				return engine.NotYet(err.Error() + "; the next kit apply tries again")
			}
			sync := syncing{repo: repo, files: paths, what: what}
			res := settled
			switch {
			case out.Committed || err != nil:
				res = sync.committed(ctx, out.Committed, err)
			case repo.Unpushed(ctx) > 0:
				res = sync.pushWaiting(ctx)
			}
			res = noted(res, out.Notes)
			if res.State == check.OK {
				if err := boot.ForgetNewMac(stateDir); err != nil {
					return err
				}
			}
			done = &res
			return nil
		},
	}
}

// gitSetting is a wait, said as what, till git has a value for its setting
// key, whatever sets it.
func gitSetting(repo gitrepo.Repo, key, what string) engine.Wait {
	return engine.Wait{Thing: what, Ready: func(ctx context.Context) bool { return repo.IsSet(ctx, key) }}
}

// commitSteps are the steps of kit apply's that commit to kit-config, in
// the order they come: the new Mac the boot added, then its host key, both
// in kit.toml.
var commitSteps = []string{newMacName, steps.HostKeyName}

// placeCommits places the steps committing to kit-config (commitSteps), those
// all, the run's steps, have: each after git's settings and own, the steps
// of the user's own, which can bring what git commits with, as the key it
// signs with, but for those that come after it, which it can't wait for;
// and after the one before it. The config's edits are looked at after them,
// so a run doesn't end listing the files they committed as edits.
func placeCommits(all, own []engine.Step) {
	before := ""
	for _, name := range commitSteps {
		i := slices.IndexFunc(all, func(s engine.Step) bool { return s.Name == name })
		if i < 0 {
			continue
		}
		placeCommit(all, own, i)
		if before != "" && !following(all, name)[before] {
			all[i].After = append(all[i].After, before)
		}
		before = name
	}
}

// placeCommit places all[i], a step committing to kit-config, as
// placeCommits says.
func placeCommit(all, own []engine.Step, i int) {
	name := all[i].Name
	later := following(all, name)
	var waits []string
	if git := gitconfig.Descriptor.Name; stepNames(all)[git] && !later[git] {
		all[i].After = append(all[i].After, git)
		waits = append(waits, "git's settings")
	}
	yours := 0
	for _, s := range own {
		if !later[s.Name] {
			all[i].After = append(all[i].After, s.Name)
			yours++
		}
	}
	if yours > 0 {
		waits = append(waits, "your steps")
	}
	if len(waits) > 0 {
		all[i].Waiting = "waiting for " + strings.Join(waits, " and ")
	}
	edits := slices.IndexFunc(all, func(s engine.Step) bool { return s.Name == steps.ConfigEditsName })
	if edits >= 0 && !following(all, steps.ConfigEditsName)[name] {
		all[edits].After = append(all[edits].After, name)
	}
}
