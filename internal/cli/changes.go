package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// syncStep names the step that commits and pushes the config's changes.
const syncStep = "kit-config"

// changes is a run of a command that changes things a name at a time, shown
// and logged as any run is: a step for each name, then one committing and
// pushing the config's changes.
type changes struct {
	run     *run
	started time.Time
	results []check.Result
	// files are the config's files changed, to commit.
	files []string
}

// startChanges starts a run of r's command over names, and the step that
// syncs the config.
func startChanges(r *run, names []string) *changes {
	steps := make([]event.Step, 0, len(names)+1)
	for _, name := range names {
		steps = append(steps, event.Step{Name: name, Title: name})
	}
	steps = append(steps, event.Step{Name: syncStep, Title: "Config repository"})
	c := &changes{run: r, started: r.now}
	r.sink.Emit(event.RunStarted{Time: r.now, Command: r.command, Machine: r.machine, Version: r.version, Steps: steps})
	return c
}

// step runs do as the step named name, emitting its start and its result.
func (c *changes) step(ctx context.Context, name string, do func(ctx context.Context) check.Result) check.Result {
	started := time.Now()
	c.run.sink.Emit(event.StepStarted{Time: started, Step: name, Doing: "changing"})
	res := do(event.WithStep(ctx, name))
	c.results = append(c.results, res)
	c.run.sink.Emit(event.StepFinished{Time: time.Now(), Step: name, Result: res, Duration: time.Since(started)})
	return res
}

// changed notes file as changed, to commit.
func (c *changes) changed(file string) {
	if !slices.Contains(c.files, file) {
		c.files = append(c.files, file)
	}
}

// sync commits the config's changed files with message, and pushes, as the
// last step: nothing to do when nothing changed.
func (c *changes) sync(ctx context.Context, message string) {
	c.step(ctx, syncStep, func(ctx context.Context) check.Result {
		if len(c.files) == 0 {
			return check.Result{State: check.OK, Summary: "nothing changed"}
		}
		slices.Sort(c.files)
		if _, err := c.run.repo.Commit(ctx, message, c.files...); err != nil {
			return check.Result{State: check.Failed, Reason: "couldn't commit " + strings.Join(c.files, ", ") + ": " + err.Error()}
		}
		err := c.run.repo.Push(ctx)
		switch {
		case errors.Is(err, gitrepo.ErrNoRemote):
			return check.Result{State: check.OK, Summary: "committed; no remote to push to"}
		case err != nil:
			return check.Result{State: check.Attention, Summary: "committed, not pushed: " + err.Error()}
		}
		return check.Result{State: check.OK, Summary: "committed and pushed " + strings.Join(c.files, ", ")}
	})
}

// finish ends the run, returning attention when a step didn't stand ok.
func (c *changes) finish() error {
	counts := event.Tally(c.results...)
	c.run.sink.Emit(event.RunFinished{Time: time.Now(), Duration: time.Since(c.started), Counts: counts})
	if counts[check.OK] != len(c.results) {
		return attention{}
	}
	return nil
}

// kindNamed is the run's kind called name, or an error naming the kinds.
func (r *run) kindNamed(name string) (kind.Kind, error) {
	k, ok := r.kindsByName[name]
	if !ok {
		var names []string
		for n := range r.kindsByName {
			names = append(names, n)
		}
		slices.Sort(names)
		return nil, fmt.Errorf("no kind named %s: one of %s", name, strings.Join(names, ", "))
	}
	return k, nil
}

// scope is whose declarations a thing is declared in: shared, or this Mac's.
func (r *run) scope(shared bool) string {
	if shared {
		return config.Shared
	}
	return r.machine
}

// where are the entries declaring name, of the kind called kindName: in
// every declarations file; or, for a kind declared outside the config
// repository, in its own file, matching as the kind matches things.
func (r *run) where(kindName, name string) ([]config.Entry, error) {
	k := r.kindsByName[kindName]
	if _, ok := k.(kind.Declarer); !ok {
		return r.cfg.Where(kindName, name)
	}
	key := func(n string) string { return n }
	if kd, ok := k.(kind.Keyed); ok {
		key = kd.Key
	}
	var found []config.Entry
	for _, e := range r.lists[kindName].Entries {
		if key(e.Name) == key(name) {
			found = append(found, e)
		}
	}
	return found, nil
}

// installed reports whether name, as given, is among what k finds
// installed: nothing is, while k's program isn't.
func installed(ctx context.Context, k kind.Kind, name string) (bool, error) {
	all, err := k.Installed(ctx)
	if errors.Is(err, runner.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(all, func(it kind.Installed) bool { return it.Name == name }), nil
}

// commitMessage says what a change was, for which Mac, and why.
func commitMessage(verb, kindName string, names []string, machine, note string) string {
	msg := fmt.Sprintf("kit %s %s %s (%s)", verb, kindName, strings.Join(names, ", "), machine)
	if note != "" {
		msg += ": " + note
	}
	return msg
}
