package cli

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/steps"
)

// pullTimeout is how long a run waits to bring in kit-config's remote
// commits before it carries on with the copy it has: as long as kit gives
// a look at the prefs store's remote.
const pullTimeout = 20 * time.Second

// pullConfig brings in the remote commits of kit-config, in dir, before the
// run of command reads it again (gitrepo's BringIn), so what another Mac
// pushed is applied within the hour: on the PATH at, the config as it read
// before, gives this Mac, its commands reported to sink, and said under
// the run's heading meanwhile, unless a.pullQuietly. One that can't happen
// never fails the run, config-sync saying why; no remote, or several,
// config-sync says already.
func (a *app) pullConfig(ctx context.Context, dir string, at basis, command string, now time.Time, sink event.Sink) steps.Pulled {
	if !a.pullQuietly {
		sink.Emit(event.Preparing{Time: now, Command: command, Machine: at.machine, Doing: "bringing in kit-config's changes"})
	}
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	repo := gitrepo.Repo{Dir: dir, Run: a.commands(at.path, childEnv(a.Getenv, at.home, at.path), sink, func() *askpass.Server { return nil })}
	n, err := repo.BringIn(ctx)
	switch {
	case errors.Is(err, gitrepo.ErrNoRemote), errors.Is(err, gitrepo.ErrNowhere):
		return steps.Pulled{}
	case err != nil:
		return steps.Pulled{Missed: err}
	}
	return steps.Pulled{Brought: n}
}

// quietPull is pulled with, when nothing was brought in, how long that's
// been so, as the drift record has it, and whether it's quiet yet, as
// drift is for its first day.
func quietPull(pulled steps.Pulled, record drift.Record, now time.Time) steps.Pulled {
	if pulled.Missed == nil {
		return pulled
	}
	missed := check.Result{State: check.Attention, Items: []check.Item{{ID: steps.ConfigPullID}}}
	it := drift.Quieten(missed, record, now, nil).Items[0]
	pulled.Since, pulled.Quiet = it.Since, it.Quiet
	return pulled
}

// pullOf is res, config-sync's, with its item saying the remote's commits
// weren't brought in alone, as the drift record keeps it.
func pullOf(res check.Result) check.Result {
	res.Items = slices.DeleteFunc(slices.Clone(res.Items), func(it check.Item) bool { return it.ID != steps.ConfigPullID })
	return res
}
