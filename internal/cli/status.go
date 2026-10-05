package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/appstore"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/kind/claudeplugin"
	"github.com/leeovery/kit/internal/kind/composer"
	"github.com/leeovery/kit/internal/kind/ghext"
	"github.com/leeovery/kit/internal/kind/gotool"
	"github.com/leeovery/kit/internal/kind/login"
	"github.com/leeovery/kit/internal/kind/mcp"
	"github.com/leeovery/kit/internal/kind/npm"
	"github.com/leeovery/kit/internal/kind/tmux"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/steps"
)

func newStatusCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status [step...]",
		Short: "Show how this Mac stands against the config: what needs attention",
		Long: `Show how this Mac stands against its config: each step's check, and what needs
attention, such as packages declared but missing, or installed but not declared.

Name steps, as kit status shows them (homebrew, brew, cask, config-private),
to check only those, with what they need. A kind with nothing declared for
this Mac, whose program isn't installed, has nothing to check, and isn't
shown. Exits 0 when nothing needs attention, 1 when something does, and 2
when kit couldn't check. Each run is logged: see kit log.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := a.prepare("status", "status")
			if err != nil {
				return err
			}
			report, err := r.pipeline.Check(cmd.Context(), r.sink, r.options(a, args))
			if err == nil {
				err = r.remember(report)
			}
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
			if report.Attention() {
				return attention{}
			}
			return nil
		},
	}
}

// run is a run of the engine, ready to go: its pipeline, where its events
// go, and what it remembers of drift.
type run struct {
	command  string
	machine  string
	version  string
	pipeline *engine.Pipeline
	sink     event.Sink
	face     render.Face
	log      *logs.File
	stateDir string
	// kinds are the kinds the run checks, by name, in pipeline order: their
	// items are drift. A kind with nothing declared for this Mac, whose
	// program isn't installed, has nothing to check.
	kinds []string
	// allKinds are every kind kit knows, by name, in pipeline order.
	allKinds []string
	// drift are the steps whose items are drift, by name, in pipeline order,
	// and drifters what checks and settles each: the kinds, and the linked
	// files.
	drift    []string
	drifters map[string]drifter
	// files are the config repository's linked files.
	files *linked.Files
	now   time.Time
	run   runner.Runner
	// homeDir is the user's home, and logsDir where kit's logs go.
	homeDir, logsDir string
	// cfg, kindsByName, lists and repo are what changing the config needs.
	cfg         *config.Config
	kindsByName map[string]kind.Kind
	lists       map[string]config.List
	repo        gitrepo.Repo
	record      drift.Record
}

// kindStep is a kind as a run takes it: the steps it needs, and the steps
// it's applied after, as they install its program.
type kindStep struct {
	kind  kind.Kind
	needs []string
	after []string
}

// kindSteps are every kind kit knows, in pipeline order, driven through
// run, for the user whose home is home and XDG config folder configHome.
// The kinds whose programs are formulae come after the formulae, so a new
// Mac has them before it needs them.
func kindSteps(hb *brew.Homebrew, run runner.Runner, home, configHome string) []kindStep {
	return []kindStep{
		{kind: hb.Formulae(), needs: []string{brew.StepName}},
		{kind: hb.Casks(), needs: []string{brew.StepName}},
		{kind: appstore.New(run), after: []string{"brew"}},
		{kind: npm.New(run, home), after: []string{"brew"}},
		{kind: composer.New(run), after: []string{"brew"}},
		{kind: gotool.New(run), after: []string{"brew"}},
		{kind: ghext.New(run), after: []string{"brew"}},
		{kind: tmux.New(run, home, configHome), after: []string{"brew"}},
		{kind: login.New(run, home), after: []string{"cask", "app"}},
		{kind: mcp.New(run, home), after: []string{"brew"}},
		{kind: claudeplugin.New(run, home), after: []string{"brew"}},
	}
}

// remember notes, in the drift record, the drift the run's checks found:
// what's new, and what's gone.
func (r *run) remember(report engine.Report) error {
	results := make(map[string]check.Result)
	for _, name := range r.drift {
		if res, ok := report.Results[name]; ok {
			results[name] = res
		}
	}
	return drift.Update(r.stateDir, func(rec *drift.Record) { rec.Seen(results, r.now) })
}

func (r *run) options(a *app, only []string) engine.Options {
	return engine.Options{Command: r.command, Machine: r.machine, Version: a.Version, Only: only, Now: a.Now}
}

// close finishes the run's face and log, returning the first error.
func (r *run) close() error {
	faceErr := r.face.Close()
	if err := r.log.Close(); faceErr == nil {
		return err
	}
	return faceErr
}

// prepare readies a run of command, logged as logName: the config, this
// Mac's name, settled before anything reads a Mac's own files, kit's own
// PATH, the runner, the faces and the log, and the pipeline.
func (a *app) prepare(command, logName string) (*run, error) {
	return a.prepareWith(command, logName, a.face())
}

// prepareWith readies a run as prepare does, shown on face.
func (a *app) prepareWith(command, logName string, face render.Face) (*run, error) {
	dirs, err := a.dirs()
	if err != nil {
		return nil, err
	}
	cfg, err := a.loadConfig(dirs)
	if err != nil {
		return nil, err
	}
	machine, err := config.ReadMachine(dirs.State)
	if err != nil {
		return nil, err
	}
	switch {
	case machine == "":
		return nil, fmt.Errorf("this Mac has no name yet: run kit machine <name>, one of %s", strings.Join(cfg.MacNames(), ", "))
	case !cfg.Knows(machine):
		return nil, fmt.Errorf("this Mac is named %s, which %s doesn't know: run kit machine <name>, one of %s", machine, config.File, strings.Join(cfg.MacNames(), ", "))
	}
	home, err := a.HomeDir()
	if err != nil {
		return nil, fmt.Errorf("find the home directory: %w", err)
	}
	path, err := cfg.SearchPath(home, machine)
	if err != nil {
		return nil, err
	}
	record, err := drift.Load(dirs.State)
	if err != nil {
		return nil, err
	}
	now := a.Now()
	log, err := logs.Open(dirs.Logs, now, logName, a.verbose)
	if err != nil {
		return nil, err
	}
	sink := event.NewFanout(face, log)
	exec := a.Runner(path, childEnv(a.Getenv, home, path))
	observed := runner.Observed(exec, func(ctx context.Context, rep runner.Report) { sink.Emit(event.Command(ctx, rep)) }, a.Now)
	hb := brew.New(observed)
	r := &run{
		command: command, machine: machine, version: a.Version, sink: sink, face: face, log: log,
		stateDir: dirs.State, now: now, run: observed, cfg: cfg, homeDir: home, logsDir: dirs.Logs,
		kindsByName: map[string]kind.Kind{}, lists: map[string]config.List{},
		repo: gitrepo.Repo{Dir: dirs.Config, Run: observed}, record: record,
		drifters: map[string]drifter{},
	}
	r.files = &linked.Files{Repo: r.repo, Home: home, StateDir: dirs.State, Scopes: []string{config.Shared, machine}}
	var files []engine.Step
	if r.files.Present() {
		step := engine.Step{Name: linked.StepName, Title: "Linked files", Area: steps.AreaDrift, Check: r.files.Check, Apply: r.files.Apply}
		files = append(files, quietened(step, record, now))
		r.drift = append(r.drift, linked.StepName)
		r.drifters[linked.StepName] = fileDrifter{files: r.files}
	}
	var kinds []engine.Step
	for _, ks := range kindSteps(hb, observed, home, a.configHome()) {
		name := ks.kind.Name()
		list, unread, err := declared(cfg, ks.kind, machine)
		if err != nil {
			_ = face.Close()
			_ = log.Close()
			return nil, err
		}
		r.allKinds = append(r.allKinds, name)
		r.kindsByName[name], r.lists[name] = ks.kind, list
		if len(list.Entries) == 0 && unread == nil && !runner.Has(observed, ks.kind.Program()) {
			continue
		}
		step := kind.Step(ks.kind, list, ks.needs...)
		step.Area = steps.AreaDrift
		if unread != nil {
			step.Check = func(context.Context) check.Result {
				return check.Result{State: check.Failed, Reason: unread.Error()}
			}
		}
		for _, after := range ks.after {
			if slices.Contains(r.kinds, after) {
				step.After = append(step.After, after)
			}
		}
		kinds = append(kinds, quietened(step, record, now))
		r.kinds = append(r.kinds, name)
		r.drift = append(r.drift, name)
		r.drifters[name] = kindDrifter{k: ks.kind, list: list}
	}
	homebrew := hb.Step()
	homebrew.Area = steps.AreaDrift
	features, err := r.features()
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	var checks []engine.Step
	if features[steps.FeatureTimeMachine] {
		checks = append(checks, steps.TimeMachine(observed, a.Now))
	}
	if features[steps.FeatureArq] {
		checks = append(checks, steps.Arq(observed, a.Now))
	}
	if features[steps.FeatureScratch] {
		checks = append(checks, steps.Scratch(observed, a.Scratch, home))
	}
	if features[steps.FeatureSettingsCapture] {
		checks = append(checks, steps.FullDiskAccess(home))
	}
	edits := steps.ConfigEdits(r.repo)
	r.drift = append(r.drift, steps.ConfigEditsName)
	r.drifters[steps.ConfigEditsName] = configDrifter{repo: r.repo, home: home, step: edits}
	checks = append(checks,
		quietened(edits, record, now),
		steps.Disk(observed), steps.Memory(observed), steps.FileEvents(observed), steps.Load(observed, a.Now),
		steps.ConfigSync(observed, dirs.Config, a.Now), steps.ConfigPrivate(observed, dirs.Config),
	)
	own, err := cfg.List(config.ChecksKind, machine)
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	if len(own.Entries) > 0 {
		checks = append(checks, steps.Own(observed, home, own))
	}
	hourly, daily, err := a.jobs(r)
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	if jobs := slices.Concat(hourly, daily); len(jobs) > 0 {
		checks = append(checks, nightly.Check(jobs, len(hourly) > 0, len(daily) > 0, dirs.State, a.Now))
	}
	r.pipeline, err = engine.New(slices.Concat([]engine.Step{homebrew}, files, kinds, checks)...)
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	return r, nil
}

// features are the features switched on for this Mac: the shared file's,
// then its own. One kit doesn't know is refused, saying where.
func (r *run) features() (map[string]bool, error) {
	list, err := r.cfg.List(config.FeaturesKind, r.machine)
	if err != nil {
		return nil, err
	}
	on := make(map[string]bool, len(list.Entries))
	for _, e := range list.Entries {
		if !slices.Contains(steps.Features, e.Name) {
			return nil, fmt.Errorf("%s: kit doesn't know the feature %s: one of %s", e.Pos(), e.Name, strings.Join(steps.Features, ", "))
		}
		on[e.Name] = true
	}
	return on, nil
}

// declared is what k declares for the Mac named mac: its sections in cfg,
// their values read by a kind whose lines carry them; or, for a kind
// declared outside the config repository, what its own file says. What
// doesn't read is unread: the kind's check fails, saying so, rather than
// kit.
func declared(cfg *config.Config, k kind.Kind, mac string) (list config.List, unread, err error) {
	if d, ok := k.(kind.Declarer); ok {
		list, unread = d.Declared()
		return list, unread, nil
	}
	if list, err = cfg.List(k.Name(), mac); err != nil {
		return list, nil, err
	}
	if v, ok := k.(kind.Valued); ok {
		read, unread := v.Values(list)
		return read, unread, nil
	}
	return list, nil, nil
}

// configHome is XDG's config folder, when it's set to an absolute path: ""
// for the default.
func (a *app) configHome() string {
	if dir := a.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	return ""
}

// quietened is step with its items quiet while their time hasn't come, as
// the drift record says: new for a day, snoozed, or a temporary install.
func quietened(step engine.Step, record drift.Record, now time.Time) engine.Step {
	checks := step.Check
	step.Check = func(ctx context.Context) check.Result {
		return drift.Quieten(checks(ctx), record, now)
	}
	return step
}

// face is the face a run shows on: JSON with --json, plain with --plain or
// without a terminal, pretty at one.
func (a *app) face() render.Face {
	out := a.Stdout
	switch {
	case a.json:
		return render.NewJSON(out)
	case !a.pretty(out):
		return render.NewPlain(out)
	}
	return render.NewPretty(a.colors(out), a.Width(out), true)
}

// childEnv is the environment kit runs programs in, never the one it
// inherited whole: who and where the user is, the SSH agent (git signs and
// pushes through it), kit's own PATH, Homebrew kept from updating itself,
// nagging or colouring its output on its own, and git from asking for a
// password mid-run.
func childEnv(getenv func(string) string, home string, path []string) []string {
	env := []string{"HOME=" + home, "PATH=" + strings.Join(path, ":")}
	for _, name := range []string{"USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "LC_ALL", "SSH_AUTH_SOCK"} {
		if v := getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return append(env, "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
}
