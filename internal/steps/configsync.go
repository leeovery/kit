package steps

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/look"
)

// syncWithin is how long a commit may wait unpushed: kit pushes its own
// commits at once, so one left over is a push that failed.
const syncWithin = time.Hour

// ConfigSyncName is the name of the step that checks the config repository
// is pushed.
const ConfigSyncName = "config-sync"

// ConfigPullID is the id of the config-sync item saying the remote's
// commits weren't brought in as the run began, and BroughtIn its count of
// those that were.
const (
	ConfigPullID = ConfigSyncName + ":pull"
	BroughtIn    = "brought_in"
)

// Pulled is how bringing in kit-config's remote commits went as the run
// began (gitrepo's BringIn): how many came in; or why none could, since
// when that's been so, and why it doesn't need attention yet, as drift's
// Quieten has an item, "" once it's been so a day.
type Pulled struct {
	Brought int
	Missed  error
	Since   time.Time
	Quiet   string
}

// into is res, config-sync's, with what p says: the commits brought in
// after its summary, and counted; or an item saying why none could be,
// needing attention once it isn't quiet. A check that failed still keeps
// the item, so the drift record keeps when it was first seen, but stays
// failed, its summary its own.
func (p Pulled) into(res check.Result) check.Result {
	failed := res.State == check.Failed
	switch {
	case p.Brought > 0 && !failed:
		res = res.Adding(fmt.Sprintf("%d %s brought in", p.Brought, look.Plural(p.Brought, "commit", "commits")))
		res.Counts = map[string]int{BroughtIn: p.Brought}
	case p.Missed != nil:
		res.Items = append(res.Items, check.Item{ID: ConfigPullID, Name: "the remote's commits not brought in", State: Problem, Detail: p.Missed.Error(), Since: p.Since, Quiet: p.Quiet})
		if p.Quiet == "" && !failed {
			res.State = check.Attention
		}
	}
	return res
}

// ConfigSync checks the config repository has no commit unpushed for over
// an hour, nor any kit holds back, saying why (gitrepo's Holding), nor
// stands where kit can't tell where to push (gitrepo's ErrNowhere); and
// says how bringing in the remote's commits went as the run began
// (pulled). Edits not committed are drift (ConfigEdits), not a problem.
func ConfigSync(repo gitrepo.Repo, now func() time.Time, pulled Pulled) engine.Step {
	return engine.Step{
		Name: ConfigSyncName, Title: "Sync", Area: AreaConfig,
		Check: func(ctx context.Context) check.Result {
			return pulled.into(pushes(ctx, repo, now))
		},
	}
}

// pushes is how the config repository stands with its remote, as
// ConfigSync checks it.
func pushes(ctx context.Context, repo gitrepo.Repo, now func() time.Time) check.Result {
	made, err := repo.Waiting(ctx)
	switch {
	case errors.Is(err, gitrepo.ErrNoRemote):
		return check.Result{State: check.OK, Summary: "nothing to push to", Glance: "nothing to push to"}
	case errors.Is(err, gitrepo.ErrNowhere):
		return problem(ConfigSyncName, "nowhere to push to", [3]string{"nowhere", "kit can't tell where to push", err.Error()})
	case err != nil:
		return failed(err)
	}
	if len(made) == 0 {
		return check.Result{State: check.OK, Summary: "pushed", Glance: "pushed"}
	}
	waiting := fmt.Sprintf("%d %s not pushed", len(made), look.Plural(len(made), "commit", "commits"))
	if held := repo.Holding(ctx); held != nil {
		return problem(ConfigSyncName, "pushes held back", [3]string{"held", waiting, held.Error()})
	}
	if now().Sub(slices.MinFunc(made, time.Time.Compare)) > syncWithin {
		return problem(ConfigSyncName, "commits waiting", [3]string{"unpushed", waiting + ", the oldest over an hour old", "push them: git -C " + repo.Dir + " push (kit rebases onto the remote first when it pushes)"})
	}
	return check.Result{State: check.OK, Summary: "pushed", Glance: "pushed"}
}

// The states of kit-config's edits, as the config step's items carry them:
// a repository is one of its own, nested in kit-config, which it can't
// keep.
const (
	ConfigEdited     = gitrepo.Edited
	ConfigAdded      = gitrepo.Added
	ConfigDeleted    = gitrepo.Deleted
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
				res.Summary = fmt.Sprintf("%d %s not committed", n, look.Plural(n, "file", "files"))
			}
			return res
		},
	}
}

// editDetail says more of an edit: the lines it adds and removes, or what
// to do about a repository nested in kit-config.
func editDetail(c gitrepo.Change) string {
	if c.State == ConfigRepository {
		return "kit-config can't keep it"
	}
	return fmt.Sprintf("+%d −%d lines", c.Added, c.Removed)
}
