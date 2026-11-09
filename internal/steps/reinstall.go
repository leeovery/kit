package steps

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/reinstall"
)

// ReinstallName names the step reinstalling restored projects' dependencies.
const ReinstallName = "reinstall"

// ReinstallOn reports whether the reinstall step is there, by the records
// in stateDir, kit's state: once the restore has done a path not yet
// searched for projects, or projects have been found; or, restoring, the
// restore in the run and yet to settle, as a new Mac's first run
// reinstalls what it restores.
func ReinstallOn(stateDir string, restoring bool) (bool, error) {
	restored, rec, err := reinstall.Records(stateDir)
	if err != nil {
		return false, err
	}
	return len(rec.Installs) > 0 || len(reinstall.Unsearched(restored, rec)) > 0 || restoring && restored.Settled.IsZero(), nil
}

// Reinstall is the step reinstalling the dependencies of the projects the
// restore brought back, as r does. Applied, it searches each path the
// restore has done for projects, once, then runs the installs with no
// outcome, and those whose tool has come since. Its check reads the records
// alone: ok once every install has an outcome, those that failed or wait
// for their tool named, never a problem; nothing restored yet, deferred.
// Failed installs run again only with r's Retry, which the caller sets
// when the run names the step. The caller places it after the restore, and
// after the secrets, which write what private packages need.
func Reinstall(r reinstall.Reinstaller) engine.Step {
	return engine.Step{
		Name: ReinstallName, Title: "Dependencies", Area: AreaBackups,
		Check: func(context.Context) check.Result {
			restored, rec, err := reinstall.Records(r.StateDir)
			if err != nil {
				return failed(err)
			}
			paths, todo := reinstall.Unsearched(restored, rec), r.ToRun(rec)
			switch {
			case len(paths) > 0 || len(todo) > 0:
				return check.Result{State: check.Attention, Summary: toReinstall(paths, len(todo))}
			case len(rec.Searched) == 0:
				return check.Result{State: check.Deferred, Reason: "waits for the restore"}
			case len(rec.Installs) == 0:
				return check.Result{State: check.OK, Summary: "no projects restored"}
			}
			return reinstalled(rec)
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if err := r.Search(); err != nil {
				return err
			}
			_, rec, err := reinstall.Records(r.StateDir)
			if err != nil {
				return err
			}
			return r.Install(ctx, r.ToRun(rec))
		},
	}
}

// toReinstall says what's left to do: the restored paths to find projects
// in, and how many installs are to run.
func toReinstall(paths []string, todo int) string {
	var says []string
	if len(paths) > 0 {
		says = append(says, "projects to find in "+strings.Join(paths, ", "))
	}
	if todo > 0 {
		says = append(says, fmt.Sprintf("%d to install", todo))
	}
	return strings.Join(says, "; ")
}

// The items of the installs left, as check.Item's Quiet has it: failed,
// left as they are; waiting for their tool; and the person's to do.
const (
	quietLeft    = "left"
	quietWaiting = "waiting"
	quietYours   = "yours"
)

// reinstalled is how rec's installs stand, each with an outcome: how many
// are in place, failed, the person's, and wait for each tool; and an item
// each for those failed, with the last line it said, those the person's,
// saying what to do, and those waiting.
func reinstalled(rec reinstall.Record) check.Result {
	res := check.Result{State: check.OK}
	waiting := map[string]int{}
	placed, failures, yours := 0, 0, 0
	for _, in := range rec.Installs {
		item := check.Item{ID: ReinstallName + ":" + in.Dir + ":" + in.Tool, Name: in.Dir, State: string(in.Outcome)}
		switch in.Outcome {
		case reinstall.Failed:
			failures++
			item.Detail, item.Quiet = in.Tool+": "+in.Error, quietLeft
		case reinstall.Yours:
			yours++
			item.Detail, item.Quiet = in.Error, quietYours
		case reinstall.NoTool:
			waiting[in.Tool]++
			item.Detail, item.Quiet = in.Tool+" isn't installed", quietWaiting
		default:
			placed++
			continue
		}
		res.Items = append(res.Items, item)
	}
	says := []string{fmt.Sprintf("%d installed", placed)}
	if failures > 0 {
		says = append(says, fmt.Sprintf("%d failed", failures))
	}
	if yours > 0 {
		says = append(says, fmt.Sprintf("%d for you to install", yours))
	}
	for _, t := range slices.Sorted(maps.Keys(waiting)) {
		says = append(says, fmt.Sprintf("%d %s for %s", waiting[t], plural(waiting[t], "waits", "wait"), t))
	}
	res.Summary = strings.Join(says, "; ")
	return res
}
