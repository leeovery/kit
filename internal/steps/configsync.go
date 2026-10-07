package steps

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
)

// syncWithin is how long a commit may wait unpushed: kit pushes its own
// commits at once, so one left over is a push that failed.
const syncWithin = time.Hour

// ConfigSyncName is the name of the step that checks the config repository
// is pushed.
const ConfigSyncName = "config-sync"

// ConfigSync checks the config repository in dir has no commit unpushed for
// over an hour. Edits not committed are drift (ConfigEdits), not a problem.
func ConfigSync(run runner.Runner, dir string, now func() time.Time) engine.Step {
	return engine.Step{
		Name: ConfigSyncName, Title: "Sync", Area: AreaConfig,
		Check: func(ctx context.Context) check.Result {
			// What's committed and not pushed, oldest first: none when
			// there's no upstream to push to.
			unpushed, err := output(ctx, run, "git", "-C", dir, "log", "@{u}..HEAD", "--format=%ct")
			if err != nil {
				return check.Result{State: check.OK, Summary: "nothing to push to", Glance: "nothing to push to"}
			}
			var oldest int64
			count := 0
			for f := range strings.FieldsSeq(unpushed) {
				t, _ := strconv.ParseInt(f, 10, 64)
				if count == 0 || t < oldest {
					oldest = t
				}
				count++
			}
			if count > 0 && now().Sub(time.Unix(oldest, 0)) > syncWithin {
				return problem("config-sync", "commits waiting", [3]string{"unpushed", fmt.Sprintf("%d commits not pushed, the oldest over an hour old", count), "push them: git -C " + dir + " push (kit pulls with rebase first when it commits)"})
			}
			return check.Result{State: check.OK, Summary: "pushed", Glance: "pushed"}
		},
	}
}

// The states of kit-config's edits, as the config step's items carry them.
const (
	ConfigEdited  = gitrepo.Edited
	ConfigAdded   = gitrepo.Added
	ConfigDeleted = gitrepo.Deleted
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
				detail := fmt.Sprintf("+%d −%d lines", c.Added, c.Removed)
				res.Items = append(res.Items, check.Item{ID: ConfigEditsName + ":" + c.Path, Name: c.Path, State: c.State, Detail: detail})
			}
			if n := len(res.Items); n > 0 {
				res.State = check.Attention
				res.Summary = fmt.Sprintf("%d %s not committed", n, plural(n, "file", "files"))
			}
			return res
		},
	}
}

// plural is one or many, as n says.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
