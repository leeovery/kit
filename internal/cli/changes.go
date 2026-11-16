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
	"github.com/leeovery/kit/internal/look"
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
	// wrote are the steps that changed any of them, by name; current is the
	// step running, "" between steps.
	wrote   map[string]bool
	current string
	// doings say what each step does as it runs, when the command doesn't.
	doings map[string]string
}

// startChanges starts a run of r's command over names, and the step that
// syncs the config.
func startChanges(r *run, names []string) *changes {
	steps := make([]event.Step, 0, len(names))
	for _, name := range names {
		steps = append(steps, event.Step{Name: name, Title: name})
	}
	return startChangesOf(r, steps, nil)
}

// startChangesOf starts a run of r's command over steps, each doing what
// doings says as it runs, and the step that syncs the config.
func startChangesOf(r *run, steps []event.Step, doings map[string]string) *changes {
	steps = append(slices.Clone(steps), event.Step{Name: syncStep, Title: syncStep})
	c := &changes{run: r, started: time.Now(), wrote: map[string]bool{}, doings: doings}
	r.sink.Emit(event.RunStarted{Time: r.now, Command: r.command, Machine: r.machine, Version: r.version, Steps: steps})
	return c
}

// step runs do as the step named name, emitting its start and its result.
func (c *changes) step(ctx context.Context, name string, do func(ctx context.Context) check.Result) check.Result {
	started := time.Now()
	c.run.sink.Emit(event.StepStarted{Time: started, Step: name, Doing: c.doing(name)})
	c.current = name
	res := do(event.WithSaying(event.WithStep(ctx, name), func(d event.Doing) {
		d.Time, d.Step = time.Now(), name
		c.run.sink.Emit(d)
	}))
	c.current = ""
	c.results = append(c.results, res)
	c.run.sink.Emit(event.StepFinished{Time: time.Now(), Step: name, Result: res, Duration: time.Since(started)})
	return res
}

// doing is what the step called name does, as its row says while it runs:
// adding or removing, as the command does (its verb, after its noun, as in
// brew add jq), committing the config's changes; else changing.
func (c *changes) doing(name string) string {
	if doing, ok := c.doings[name]; ok {
		return doing
	}
	if name == syncStep {
		return "committing"
	}
	if words := strings.Fields(c.run.command); len(words) > 1 {
		switch words[1] {
		case "add":
			return "adding"
		case "remove":
			return "removing"
		}
	}
	return "changing"
}

// changed notes file as changed, to commit, by the step running.
func (c *changes) changed(file string) {
	if c.current != "" {
		c.wrote[c.current] = true
	}
	if !slices.Contains(c.files, file) {
		c.files = append(c.files, file)
	}
}

// whoWrote are those of names whose steps changed the config's files,
// whether or not they went on to fail, in turn: what a commit of them is
// of.
func (c *changes) whoWrote(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool { return !c.wrote[name] })
}

// sync commits the config's changed files with message, and pushes, as the
// last step.
func (c *changes) sync(ctx context.Context, message string) {
	c.step(ctx, syncStep, func(ctx context.Context) check.Result {
		slices.Sort(c.files)
		return syncing{repo: c.run.repo, files: c.files, what: strings.Join(c.files, ", ")}.commit(ctx, message)
	})
}

// syncing is committing files of the config's, said as what, and pushing
// them.
type syncing struct {
	repo  gitrepo.Repo
	files []string
	what  string
}

// commit commits the files with message, and pushes. With nothing to
// commit, it pushes only what was committed before and is waiting, as when
// that push failed: nothing else would.
func (s syncing) commit(ctx context.Context, message string) check.Result {
	if len(s.files) == 0 {
		return s.pushWaiting(ctx)
	}
	committed, err := s.repo.Commit(ctx, message, s.files...)
	return s.committed(ctx, committed, err)
}

// committed is how syncing stands once the commit's done, as it went:
// pushed, unless it failed.
func (s syncing) committed(ctx context.Context, committed bool, err error) check.Result {
	switch {
	case err != nil:
		return check.Result{State: check.Failed, Reason: "couldn't commit " + s.what + ": " + err.Error()}
	case !committed:
		return s.pushWaiting(ctx)
	}
	return s.push(ctx)
}

// push pushes what was committed.
func (s syncing) push(ctx context.Context) check.Result {
	err := s.repo.Push(ctx)
	switch {
	case errors.Is(err, gitrepo.ErrNoRemote):
		return check.Result{State: check.OK, Summary: "committed; no remote to push to"}
	case err != nil:
		return check.Result{State: check.Attention, Summary: "committed, not pushed: " + err.Error()}
	}
	return check.Result{State: check.OK, Summary: "committed and pushed " + s.what}
}

// pushWaiting is how syncing stands with nothing committed: the commits
// waiting for the upstream pushed, if there are any.
func (s syncing) pushWaiting(ctx context.Context) check.Result {
	nothing := check.Result{State: check.OK, Summary: "nothing changed"}
	n := s.repo.Unpushed(ctx)
	if n == 0 {
		return nothing
	}
	waiting := fmt.Sprintf("%d %s waiting", n, look.Plural(n, "commit", "commits"))
	err := s.repo.Push(ctx)
	switch {
	case errors.Is(err, gitrepo.ErrNoRemote):
		return nothing
	case err != nil:
		return check.Result{State: check.Attention, Summary: "nothing changed; " + waiting + ", not pushed: " + err.Error()}
	}
	return check.Result{State: check.OK, Summary: "nothing changed; pushed " + waiting}
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

// declare declares e in scope's declarations, noting each file it writes
// changed: in the shared one, it takes e out of each Mac's file declaring
// it, in the same edit, as a thing is in the shared declarations or a
// Mac's, never both (config.DeclareShared). It returns what to say after
// where it's declared: the Macs it's moved out of.
func (c *changes) declare(kindName, scope string, e config.Entry) (string, error) {
	r := c.run
	if scope != config.Shared {
		if err := r.cfg.Declare(kindName, scope, e); err != nil {
			return "", err
		}
		c.changed(config.DeclFile(scope))
		return "", nil
	}
	moved, err := r.macsDeclaring(kindName, e.Name)
	if err != nil {
		return "", err
	}
	written, err := r.cfg.DeclareShared(kindName, e, moved)
	for _, scope := range written {
		c.changed(config.DeclFile(scope))
	}
	return outOf(moved), err
}

// undeclareIn takes name, of the kind called kindName, out of scope's
// declarations, noting the file changed.
func (c *changes) undeclareIn(kindName, scope, name string) error {
	if err := c.run.cfg.Undeclare(kindName, scope, name); err != nil {
		return err
	}
	c.changed(config.DeclFile(scope))
	return nil
}

// undeclareHere takes name, of the kind called kindName, out of this Mac's
// declarations, and the shared ones with shared, and says whose it was
// taken out of. One declared for every Mac, without shared, is refused
// (*everyMacError) before anything's written, as the shared file's entries
// come first.
func (c *changes) undeclareHere(kindName, name string, shared bool) ([]string, error) {
	r := c.run
	where, err := r.cfg.Where(kindName, name)
	if err != nil {
		return nil, err
	}
	var from []string
	for _, e := range where {
		switch {
		case e.Scope == config.Shared && !shared:
			return nil, &everyMacError{at: e}
		case e.Scope != config.Shared && e.Scope != r.machine:
			continue
		}
		if err := c.undeclareIn(kindName, e.Scope, name); err != nil {
			return from, err
		}
		from = append(from, e.Scope)
	}
	return from, nil
}

// everyMacError is undeclaring refused a thing declared for every Mac, at,
// without --shared.
type everyMacError struct{ at config.Entry }

func (e *everyMacError) Error() string {
	return "declared for every Mac, in " + e.at.Pos() + ": --shared takes it out of every Mac's"
}

// checkDeclare says why declare would refuse e in scope's declarations,
// writing nothing: nil when it would declare it.
func (r *run) checkDeclare(kindName, scope string, e config.Entry) error {
	if scope != config.Shared {
		return r.cfg.CheckDeclare(kindName, scope, e)
	}
	moved, err := r.macsDeclaring(kindName, e.Name)
	if err != nil {
		return err
	}
	return r.cfg.CheckDeclareShared(kindName, e, moved)
}

// macsDeclaring are the Macs whose own files declare name, of the kind
// called kindName: those a shared declare takes it out of.
func (r *run) macsDeclaring(kindName, name string) ([]string, error) {
	where, err := r.cfg.Where(kindName, name)
	if err != nil {
		return nil, err
	}
	var macs []string
	for _, w := range where {
		if w.Scope != config.Shared {
			macs = append(macs, w.Scope)
		}
	}
	return macs, nil
}

// movedValue is the value of a Mac's line declaring name, of the kind
// called kindName, for a shared declare given the name alone, which moves
// the Macs' line as written; refused, saying usage, when no Mac's file
// declares it.
func (r *run) movedValue(kindName, name, usage string) (string, error) {
	where, err := r.cfg.Where(kindName, name)
	if err != nil {
		return "", err
	}
	for _, w := range where {
		if w.Scope != config.Shared {
			return w.Value, nil
		}
	}
	return "", fmt.Errorf("no Mac declares %s to move: say it as %s", name, usage)
}

// outOf says the Macs a thing declared shared was moved out of, after what
// it says it did: nothing for none.
func outOf(moved []string) string {
	if len(moved) == 0 {
		return ""
	}
	return ", out of " + strings.Join(moved, " and ")
}

// installed reports whether name, as given, is among what k finds
// installed: nothing is, while k's program isn't.
func installed(ctx context.Context, k kind.Kind, name string) (bool, error) {
	all, err := kind.InstalledOf(ctx, k, []string{name})
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
	msg := fmt.Sprintf("kit %s %s %s (%s)", kindName, verb, strings.Join(names, ", "), machine)
	if note != "" {
		msg += ": " + note
	}
	return msg
}
