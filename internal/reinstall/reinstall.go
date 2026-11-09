package reinstall

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// recordName names the reinstall's record, in kit's state.
const recordName = "reinstall.json"

// How many installs run at once; how long one may take; and how often,
// at most, the step running them says how far it's got.
const (
	AtOnce         = 4
	installTimeout = 15 * time.Minute
	sayEvery       = time.Second
)

// Outcome is how an install came out.
type Outcome string

const (
	// Installed is its dependencies installed; There, found there already
	// as the search found it, a folder copied whole or installed by hand.
	Installed Outcome = "installed"
	There     Outcome = "there"
	// Yours is its install the person's to do, as its tool can't, its
	// Error saying what to do.
	Yours Outcome = "yours"
	// Failed is its install failed: tried again only when a run names the
	// step (Reinstaller's Retry), as some old projects never install.
	Failed Outcome = "failed"
	// NoTool is its tool not installed: tried again once it is.
	NoTool Outcome = "no-tool"
)

// Record is the reinstall's record on this Mac: the paths the restore has
// done that have been searched for projects, from ~; and the installs found
// in them, each with its outcome once it has one.
type Record struct {
	Searched []string  `json:"searched,omitempty"`
	Installs []Install `json:"installs,omitempty"`
}

// Install is a project's dependencies to install: its folder, from ~; the
// tool its lockfile is for, by its program; whether it's been started, so
// one stopped part-way, its dependency folder begun, runs again; how it came
// out, none till it's run; and, failed, the last line it said, or, the
// person's, what to do.
type Install struct {
	Dir     string  `json:"dir"`
	Tool    string  `json:"tool"`
	Started bool    `json:"started,omitempty"`
	Outcome Outcome `json:"outcome,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// same reports whether other is the same install as in: its folder and
// tool.
func (in Install) same(other Install) bool {
	return in.Dir == other.Dir && in.Tool == other.Tool
}

// Records reads the restore's record and the reinstall's, in stateDir,
// kit's state: each empty when there's none.
func Records(stateDir string) (restore.Record, Record, error) {
	restored, err := restore.LoadRecord(stateDir)
	if err != nil {
		return restored, Record{}, err
	}
	rec, err := state.Load[Record](stateDir, recordName)
	return restored, rec, err
}

// Unsearched are the paths the restore has done that rec hasn't searched
// for projects, sorted.
func Unsearched(restored restore.Record, rec Record) []string {
	var paths []string
	for path, p := range restored.Paths {
		if p.State == restore.Done && !slices.Contains(rec.Searched, path) {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

// Reinstaller reinstalls the dependencies of the projects the restore
// brought back, in the home folder Home, running each install with Run, its
// record in StateDir, kit's state; Now its clock, time.Now when nil. With
// Retry, as when a run names the step, failed installs run again.
type Reinstaller struct {
	Run      runner.Runner
	Home     string
	StateDir string
	Now      func() time.Time
	Retry    bool
}

// ToRun are rec's installs to run: those with no outcome, started or not;
// those whose tool has come since; and, with Retry, those that failed.
func (r Reinstaller) ToRun(rec Record) []Install {
	var todo []Install
	for _, in := range rec.Installs {
		switch {
		case in.Outcome == "",
			in.Outcome == NoTool && runner.Has(r.Run, in.Tool),
			in.Outcome == Failed && r.Retry:
			todo = append(todo, in)
		}
	}
	return todo
}

// Search searches each path the restore has done that hasn't been for
// projects (Find), recording the installs found, each once, and the path
// searched.
func (r Reinstaller) Search() error {
	restored, rec, err := Records(r.StateDir)
	if err != nil {
		return err
	}
	for _, path := range Unsearched(restored, rec) {
		found, err := Find(r.Home, path)
		if err != nil {
			return err
		}
		err = state.Update(r.StateDir, recordName, func(rec *Record) {
			for _, in := range found {
				if !slices.ContainsFunc(rec.Installs, in.same) {
					rec.Installs = append(rec.Installs, in)
				}
			}
			rec.Searched = append(rec.Searched, path)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Install runs todo, AtOnce folders at a time, a folder's installs one
// after another, recording each outcome as it comes, and having the step
// ctx is running say, every sayEvery at most, how many of them are done.
// An install the run's stop cut short records no outcome, so it runs again.
func (r Reinstaller) Install(ctx context.Context, todo []Install) error {
	if len(todo) == 0 {
		return nil
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	c := &counting{ctx: ctx, of: len(todo), now: now}
	c.say()
	var mu sync.Mutex
	var errs []error
	folders := make(chan []Install)
	var wg sync.WaitGroup
	for range AtOnce {
		wg.Go(func() {
			for folder := range folders {
				for _, in := range folder {
					if ctx.Err() != nil {
						break
					}
					in, err := r.install(ctx, in)
					if ctx.Err() != nil {
						break
					}
					if err == nil {
						err = r.record(in)
					}
					if err != nil {
						mu.Lock()
						errs = append(errs, err)
						mu.Unlock()
					}
					c.done()
				}
			}
		})
	}
	for _, folder := range byFolder(todo) {
		folders <- folder
	}
	close(folders)
	wg.Wait()
	return errors.Join(append(errs, ctx.Err())...)
}

// install runs in, recorded as started first, saying how it came out: its
// tool missing, installed, or failed with the last line it said, the run's
// log told. What's in its folder is never judged here: the search did that.
func (r Reinstaller) install(ctx context.Context, in Install) (Install, error) {
	in.Outcome, in.Error = "", ""
	t, ok := toolNamed(in.Tool)
	if !ok {
		in.Outcome, in.Error = Failed, in.Tool+" isn't a tool kit reinstalls with"
		return in, nil
	}
	if !runner.Has(r.Run, t.program) {
		in.Outcome = NoTool
		return in, nil
	}
	in.Started = true
	if err := r.record(in); err != nil {
		return in, err
	}
	dir := filepath.Join(r.Home, strings.TrimPrefix(in.Dir, "~/"))
	// Run as a check's commands are, so what an installer prints stays in
	// the log, never shown as the step's output.
	_, err := r.Run.Run(event.WithChecking(ctx), runner.Command{Name: t.program, Args: t.args, Dir: dir, Timeout: installTimeout})
	in.Outcome = Installed
	if err != nil {
		in.Outcome, in.Error = Failed, engine.Reason(err)
		event.Log(ctx, event.Logged{Line: fmt.Sprintf("%s's dependencies didn't install (%s): %s", in.Dir, in.Tool, in.Error), Alert: true})
	}
	return in, nil
}

// record records in's outcome.
func (r Reinstaller) record(in Install) error {
	return state.Update(r.StateDir, recordName, func(rec *Record) {
		if i := slices.IndexFunc(rec.Installs, in.same); i >= 0 {
			rec.Installs[i] = in
		}
	})
}

// byFolder is todo by folder, in the order each folder first comes.
func byFolder(todo []Install) [][]Install {
	var folders [][]Install
	at := map[string]int{}
	for _, in := range todo {
		i, ok := at[in.Dir]
		if !ok {
			i = len(folders)
			at[in.Dir] = i
			folders = append(folders, nil)
		}
		folders[i] = append(folders[i], in)
	}
	return folders
}

// counting is how many of the installs a step runs are done, of how many,
// and when it last said so.
type counting struct {
	ctx context.Context
	now func() time.Time

	mu       sync.Mutex
	got, of  int
	saidLast time.Time
}

// done counts an install done, saying so when it's the last, or the step
// hasn't for sayEvery.
func (c *counting) done() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got++
	if c.got == c.of || c.now().Sub(c.saidLast) >= sayEvery {
		c.say()
	}
}

// say says how many installs are done, of how many. c.mu must be held
// once installs run.
func (c *counting) say() {
	c.saidLast = c.now()
	event.Say(c.ctx, event.Doing{Says: "installing", Done: c.got, Of: c.of})
}
