package steps

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
)

// syncWithin is how long a commit may wait unpushed: kit pushes its own
// commits at once, so one left over is a push that failed.
const syncWithin = time.Hour

// ConfigSyncName is the name of the step that checks the config repository
// is pushed.
const ConfigSyncName = "config-sync"

// ConfigSync checks the config repository has no commit unpushed for over
// an hour, nor any kit holds back, saying why (gitrepo's Holding). Edits
// not committed are drift (ConfigEdits), not a problem.
func ConfigSync(repo gitrepo.Repo, now func() time.Time) engine.Step {
	return engine.Step{
		Name: ConfigSyncName, Title: "Sync", Area: AreaConfig,
		Check: func(ctx context.Context) check.Result {
			made, err := repo.Waiting(ctx)
			if err != nil {
				return check.Result{State: check.OK, Summary: "nothing to push to", Glance: "nothing to push to"}
			}
			if len(made) == 0 {
				return check.Result{State: check.OK, Summary: "pushed", Glance: "pushed"}
			}
			waiting := fmt.Sprintf("%d %s not pushed", len(made), plural(len(made), "commit", "commits"))
			if held := repo.Holding(ctx); held != nil {
				return problem(ConfigSyncName, "pushes held back", [3]string{"held", waiting, held.Error()})
			}
			if now().Sub(slices.MinFunc(made, time.Time.Compare)) > syncWithin {
				return problem(ConfigSyncName, "commits waiting", [3]string{"unpushed", waiting + ", the oldest over an hour old", "push them: git -C " + repo.Dir + " push (kit rebases onto the remote first when it pushes)"})
			}
			return check.Result{State: check.OK, Summary: "pushed", Glance: "pushed"}
		},
	}
}

// The states of kit-config's edits, as the config step's items carry them:
// untracked is a file taken out of git's index, kept on this Mac, as git
// ignores it; a repository is one of its own, nested in kit-config, which
// it can't keep.
const (
	ConfigEdited     = gitrepo.Edited
	ConfigAdded      = gitrepo.Added
	ConfigDeleted    = gitrepo.Deleted
	ConfigUntracked  = gitrepo.Untracked
	ConfigRepository = gitrepo.Repository
)

// ConfigEditsName names the step of the config repository's edits, and its
// items' kind, as in config:shared/home/.zshrc.
const ConfigEditsName = "config"

// ConfigEdits checks the config repository for edits not committed: made
// through a linked file, by hand, or by an app. Each is drift, an item a
// file, which kit reconcile commits or undoes.
func ConfigEdits(repo gitrepo.Repo) engine.Step {
	return engine.Step{
		Name: ConfigEditsName, Title: "kit-config edits", Area: AreaDrift,
		Check: func(ctx context.Context) check.Result {
			changes, err := repo.Changes(ctx)
			if err != nil {
				return failed(err)
			}
			res := check.Result{State: check.OK, Summary: "all committed"}
			for _, c := range changes {
				res.Items = append(res.Items, check.Item{ID: ConfigEditsName + ":" + c.Path, Name: c.Path, State: c.State, Detail: editDetail(c)})
			}
			if n := len(res.Items); n > 0 {
				res.State = check.Attention
				res.Summary = fmt.Sprintf("%d %s not committed", n, plural(n, "file", "files"))
			}
			return res
		},
	}
}

// editDetail says more of an edit: the lines it adds and removes, or what
// to do about a repository nested in kit-config; nothing of a file
// untracked, whose lines are kept.
func editDetail(c gitrepo.Change) string {
	switch c.State {
	case ConfigUntracked:
		return ""
	case ConfigRepository:
		return "kit-config can't keep it"
	}
	return fmt.Sprintf("+%d −%d lines", c.Added, c.Removed)
}

// plural is one or many, as n says.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
