package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/dialog"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/macapp"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/notice"
	"github.com/leeovery/kit/internal/redact"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/steps"
)

func newStatusCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:         "status [step...]",
		Short:       "Show how this Mac stands against the config: what needs attention",
		Annotations: map[string]string{brief: "how this Mac stands against its config"},
		Long: `Show how this Mac stands against its config: each step's check, and what needs
attention, such as packages declared but missing, or installed but not declared.

Name steps, as kit status shows them (homebrew, brew, cask, config-private),
to check only those, with what they need. A kind is checked only while the
config has its section for this Mac, the shared file's or its own, even an
empty one. Exits 0 when nothing needs attention, 1 when something does, and
2 when kit couldn't check. Each run is logged: see kit log.`,
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
	// steps are the pipeline's steps, as given it.
	steps    []engine.Step
	sink     event.Sink
	face     render.Face
	log      *logs.File
	stateDir string
	// kinds are the kinds the run checks, by name, in pipeline order: their
	// items are drift. A kind is checked only while the config has its
	// section for this Mac, even an empty one.
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
	// byHandLast is whether the steps by hand with no place of their own
	// come last, raised at a terminal (placeByHand).
	byHandLast bool
	// admin is whether an administrator's password is at hand, for the
	// steps whose apply needs one; adminSteps are those steps.
	admin      *steps.Admin
	adminSteps []engine.Step
	// askpass answers, at a terminal, what the programs the run starts ask
	// for an administrator's password: nil when nothing will.
	askpass *askpass.Server
	// hold is how the password stands while kit asks for it as the run goes
	// on: nil when it doesn't. settled are the things the kinds are done
	// with for now, which nothing waits behind.
	hold    *adminHold
	settled settled
	// held is whether the person at the Mac is being asked something, which
	// the run's time limits wait on, at a terminal: by macOS, in a window of
	// its own, or for a program's sudo, through askpass, which holds up the
	// command it's in; unwatch stops watching macOS.
	held    func() bool
	unwatch func()
	// notices is kit's app, which posts the run's notifications, once the
	// run has any to post; raise raises the steps by hand that come due, at
	// a terminal, their notifications among them.
	notices *macapp.App
	raise   *steps.Raise
	now     time.Time
	run     runner.Runner
	// homeDir is the user's home, logsDir where kit's logs go, and dataDir
	// kit's data folder.
	homeDir, logsDir, dataDir string
	// cfg, kindsByName, lists and repo are what changing the config needs.
	cfg         *config.Config
	kindsByName map[string]kind.Kind
	lists       map[string]config.List
	// placing is how each kind's lines install, by kind, as they're placed
	// once the run's steps are known.
	placing map[string]kind.Placing
	repo    gitrepo.Repo
	record  drift.Record
}

// remember notes, in the drift record, the drift the run's checks found:
// what's new, and what's gone; and it forgets the kinds the run doesn't
// look after.
func (r *run) remember(report engine.Report) error {
	results := make(map[string]check.Result)
	for _, name := range r.drift {
		if res, ok := report.Results[name]; ok {
			results[name] = res
		}
	}
	unmanaged := slices.DeleteFunc(slices.Clone(r.allKinds), func(k string) bool { return slices.Contains(r.kinds, k) })
	return drift.Update(r.stateDir, func(rec *drift.Record) {
		rec.Seen(results, r.now)
		rec.Forget(unmanaged)
	})
}

func (r *run) options(a *app, only []string) engine.Options {
	return engine.Options{Command: r.command, Machine: r.machine, Version: a.Version, Only: only, Now: a.Now, Held: r.held}
}

// close finishes the run's face and log, returning the first error, and
// stops answering for a password. Kit's app is closed before the cards'
// notifications are waited for, stopping a build under way, so that none
// waits for it; by then each was taken away, as was macOS asking's, as its
// watch ended, so none waiting for the build is posted.
func (r *run) close() error {
	if r.unwatch != nil {
		r.unwatch()
	}
	if r.notices != nil {
		r.notices.Close()
	}
	if r.raise != nil {
		r.raise.Wait()
	}
	if r.askpass != nil {
		_ = r.askpass.Close()
	}
	faceErr := r.face.Close()
	if err := r.log.Close(); faceErr == nil {
		return err
	}
	return faceErr
}

// notifier is kit's app, which posts the run's notifications: one a run, a
// click on one bringing forward the terminal kit runs in, when macOS names
// it.
func (r *run) notifier(getenv func(string) string, now func() time.Time) *macapp.App {
	if r.notices == nil {
		r.notices = macapp.New(r.run, r.dataDir, getenv("__CFBundleIdentifier"), now)
	}
	return r.notices
}

// watchDialogs watches, till unwatch, for macOS asking the person at the
// Mac something in a window of its own, through run, a runner whose commands
// aren't logged: the run says so once the window's stayed, and as it's
// answered, and notices posts a notification while it's up, in case the
// window's out of sight, aside, so the watch never waits for one to go
// out. held is whether such a window is on screen, from the first look
// that saw it. unwatch waits for the notifications to go out.
func watchDialogs(run runner.Runner, notices notice.Notifier, sink event.Sink, now func() time.Time) (held func() bool, unwatch func()) {
	var watch dialog.Watch
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	queue := notice.New(notices)
	asking := askingNotice{notices: queue}
	go func() {
		defer close(done)
		watch.Run(ctx, run, func(what string) {
			sink.Emit(event.Asking{Time: now(), Says: what})
			asking.changed(ctx, what)
		})
		asking.end(ctx)
	}()
	return watch.Up, func() {
		stop()
		<-done
		queue.Wait()
	}
}

// askingNoticeID is the id of the notification that macOS is asking.
const askingNoticeID = "dialog"

// askingNotice keeps a notification up while macOS is asking: posted as it
// asks, taken away once it's answered, or the watch has ended.
type askingNotice struct {
	notices notice.Notifier
	up      bool
}

// changed is told what macOS is asking, each time that changes: nothing,
// once it's answered.
func (n *askingNotice) changed(ctx context.Context, what string) {
	switch {
	case what != "":
		n.notices.Notify(ctx, askingNoticeID, "macOS is "+what, "Answer it in the window macOS shows")
		n.up = true
	case n.up:
		n.notices.Clear(ctx, askingNoticeID)
		n.up = false
	}
}

// end takes the notification away, when it's up, as the watch ends: ctx,
// the watch's, is done by then.
func (n *askingNotice) end(ctx context.Context) {
	if n.up {
		n.notices.Clear(context.WithoutCancel(ctx), askingNoticeID)
		n.up = false
	}
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
	// Everything the run starts can ask it for an administrator's password,
	// at a terminal, once the run is ready (askpass).
	var asks *askpass.Server
	env := childEnv(a.Getenv, a.Environ(), home, path)
	exec := runner.WithEnv(a.Runner(path, env), func() []string {
		if asks == nil {
			return nil
		}
		return asks.Env()
	})
	observed := runner.Observed(exec, func(ctx context.Context, rep runner.Report) { sink.Emit(event.Command(ctx, rep)) }, a.Now)
	observed = runner.Streamed(observed, event.Changing, func(ctx context.Context, cmd runner.Command, line string) {
		sink.Emit(event.Output{Time: a.Now(), Step: event.StepOf(ctx), Command: redact.Text(cmd.String()), Line: redact.Text(line, runner.SecretValues(ctx)...)})
	})
	r := &run{
		command: command, machine: machine, version: a.Version, sink: sink, face: face, log: log,
		stateDir: dirs.State, now: now, run: observed, cfg: cfg, homeDir: home, logsDir: dirs.Logs, dataDir: dirs.Data,
		kindsByName: map[string]kind.Kind{}, lists: map[string]config.List{}, placing: map[string]kind.Placing{},
		repo: gitrepo.Repo{Dir: dirs.Config, Run: observed, Home: home}, record: record,
		drifters: map[string]drifter{}, admin: &steps.Admin{},
	}
	r.files = &linked.Files{Repo: r.repo, Home: home, StateDir: dirs.State, Scopes: []string{config.Shared, machine}}
	// setup are the steps that set up the home folder: its linked files,
	// the shell's PATH, Oh My Zsh.
	var setup []engine.Step
	addSetup := func(step engine.Step, d drifter) {
		setup = append(setup, quietened(step, record, now))
		r.drift = append(r.drift, step.Name)
		r.drifters[step.Name] = d
		if step.Admin {
			r.adminSteps = append(r.adminSteps, step)
		}
	}
	if r.files.Present() {
		addSetup(engine.Step{Name: linked.StepName, Title: "Linked files", Area: steps.AreaDrift, Check: r.files.Check, Apply: r.files.Apply}, fileDrifter{files: r.files})
	}
	shellPath, err := cfg.ShellPath(home, machine)
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	if len(shellPath) > 0 {
		step := steps.PathFile(shellPath, filepath.Join(dirs.State, steps.PathFileName))
		addSetup(step, applyDrifter{step: step, label: "write it"})
	}
	// kindSteps are the kinds' steps; needed, the steps they need that are
	// their own, as Homebrew's.
	var kindSteps, needed []engine.Step
	backups, err := r.backups()
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	deps := &kind.Deps{Run: observed, Home: home, ConfigHome: a.configHome(), StateDir: dirs.State, Backups: backupNames(backups), AppDirs: a.appFolders(home).Dirs}
	for _, d := range kinds.All {
		k := d.New(deps)
		list, unread, err := declared(cfg, k, machine)
		if err != nil {
			_ = face.Close()
			_ = log.Close()
			return nil, err
		}
		r.allKinds = append(r.allKinds, d.Name)
		r.kindsByName[d.Name], r.lists[d.Name] = k, list
		if !list.Declared && unread == nil {
			continue
		}
		step := kind.Placed(k, list, func(context.Context) kind.Placing { return r.placed(d.Name) }, d.Needs...)
		step.Title, step.Area, step.Row, step.Tool = d.Label, steps.AreaDrift, d.Row, d.Tool
		if unread != nil {
			step.Check = func(context.Context) check.Result {
				return check.Result{State: check.Failed, Reason: unread.Error()}
			}
		} else if len(list.Entries) > 0 && slices.Contains(applies, command) {
			for _, program := range kind.Commands(k) {
				step.Waits = append(step.Waits, engine.Wait{Thing: program, Ready: func(context.Context) bool { return runner.Has(observed, program) }})
			}
		}
		for _, after := range d.After {
			if slices.Contains(r.kinds, after) {
				step.After = append(step.After, after)
			}
		}
		kindSteps = append(kindSteps, quietened(step, record, now))
		r.kinds = append(r.kinds, d.Name)
		r.drift = append(r.drift, d.Name)
		r.drifters[d.Name] = kindDrifter{k: k, list: list, held: func() []kind.Held { return r.placing[d.Name].Held }}
		needed = withNeeded(needed, d, deps)
	}
	// One needing an administrator's password, as installing Homebrew
	// does, asks for it with the rest.
	for _, step := range needed {
		if step.Admin {
			r.adminSteps = append(r.adminSteps, step)
		}
	}
	features, err := r.features()
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	// featured is the feature each of the features' steps is for, by the
	// step's name, to place them as the feature's section says.
	featured := map[string]string{}
	feature := func(name string, step engine.Step) engine.Step {
		featured[step.Name] = name
		return step
	}
	for _, system := range []struct {
		name string
		step func() engine.Step
	}{
		{steps.FeatureTouchIDSudo, func() engine.Step { return steps.TouchIDSudo(observed, r.admin, a.SudoLocal) }},
		{steps.FeatureRemoteLogin, func() engine.Step { return steps.RemoteLogin(observed, r.admin) }},
		{steps.FeatureFileSharing, func() engine.Step { return steps.FileSharing(observed, r.admin) }},
	} {
		if features.on(system.name) {
			step := feature(system.name, system.step())
			addSetup(step, applyDrifter{step: step, label: "turn it on"})
		}
	}
	var checks []engine.Step
	for _, b := range backups {
		checks = append(checks, steps.Backup(observed, b.Driver, a.Now, r.packages))
	}
	var settings []engine.Step
	if features.on(steps.FeaturePrefs) {
		p, err := a.prefs(r)
		if err != nil {
			_ = face.Close()
			_ = log.Close()
			return nil, err
		}
		settings = []engine.Step{feature(steps.FeaturePrefs, steps.Prefs(p, a.Now)), feature(steps.FeaturePrefs, steps.FullDiskAccess(home))}
	}
	checks = append(checks, settings...)
	edits := steps.ConfigEdits(r.repo)
	r.drift = append(r.drift, steps.ConfigEditsName)
	r.drifters[steps.ConfigEditsName] = configDrifter{repo: r.repo, home: home, step: edits}
	checks = append(checks,
		quietened(edits, record, now),
		steps.Disk(observed), steps.Memory(observed), steps.FileEvents(observed), steps.Load(observed, a.Now),
		steps.ConfigSync(r.repo, a.Now), steps.ConfigPrivate(r.repo),
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
	byHand, err := cfg.List(config.ManualKind, machine)
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	// A step by hand that comes due while kit applies at a terminal is
	// raised there, a card, and waited on: the rest, with no place of their
	// own, once everything else has finished, with what the run left the
	// person to do, so that step's there whether or not any are declared.
	var raise *steps.Raise
	if slices.Contains(applies, command) && a.pretty(a.Stdout) {
		desk := hand.New()
		raise = &steps.Raise{Sink: sink, Now: a.Now, Notify: r.notifier(a.Getenv, a.Now), Desk: desk, After: a.After}
		r.raise = raise
		if f, ok := face.(*render.Pretty); ok {
			f.Hands(desk.Answer)
		}
		r.byHandLast = true
	}
	plain, ordered := steps.SplitManual(home, byHand)
	if len(plain.Entries) > 0 || raise != nil {
		checks = append(checks, steps.Manual(observed, home, dirs.State, plain, raise))
	}
	if raise != nil {
		checks = append(checks, r.cardSteps(raise, a.appFolders(home), kindSteps)...)
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
	base := slices.Concat(needed, setup, kindSteps, checks)
	for i := range base {
		base[i].Part = partOf(base[i].Name)
	}
	scripts, err := r.scriptSteps(base)
	var byHandOwn []engine.Step
	if err == nil {
		byHandOwn, err = r.manualSteps(ordered, slices.Concat(base, scripts), raise)
	}
	if err == nil {
		r.steps = slices.Concat(base, scripts, byHandOwn)
		r.placeFeatures(r.steps, features, featured)
		r.placeBackups(r.steps, backups)
		r.placeLines(r.steps)
		r.placeByHand(r.steps)
		for i, s := range r.steps {
			if s.Admin && s.Apply != nil {
				r.steps[i].Apply = r.afterAdmin(s.Apply)
			}
		}
		r.pipeline, err = engine.New(r.steps...)
	}
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	if a.pretty(a.Stdout) && !slices.Contains(readOnly, command) {
		asks = a.askpass(face)
		r.askpass = asks
	}
	if raise != nil {
		r.notifier(a.Getenv, a.Now).Prepare()
	}
	if a.WatchDialogs && a.pretty(a.Stdout) {
		r.held, r.unwatch = watchDialogs(a.Runner(path, env), r.notifier(a.Getenv, a.Now), sink, a.Now)
	}
	if asks != nil {
		dialogs := r.held
		r.held = func() bool { return asks.Pending() || dialogs != nil && dialogs() }
	}
	return r, nil
}

// withNeeded is needed, the steps the run's kinds need that are their own,
// with those d's Setup makes that it hasn't already, each in d's row.
func withNeeded(needed []engine.Step, d kind.Descriptor, deps *kind.Deps) []engine.Step {
	if d.Setup == nil {
		return needed
	}
	for _, step := range d.Setup(deps) {
		if slices.ContainsFunc(needed, func(s engine.Step) bool { return s.Name == step.Name }) {
			continue
		}
		step.Area, step.Row, step.Tool = steps.AreaDrift, d.Row, d.Tool
		needed = append(needed, step)
	}
	return needed
}

// lead puts steps at the head of the run: the bootstrap's own steps lead
// kit apply's.
func (r *run) lead(steps []engine.Step) error {
	all := slices.Concat(steps, r.steps)
	r.placeByHand(all)
	p, err := engine.New(all...)
	if err != nil {
		return err
	}
	r.pipeline, r.steps = p, all
	return nil
}

// applies are the runs that apply the whole config.
var applies = []string{"apply", "bootstrap"}

// readOnly are the runs that change nothing, so that nothing they run asks
// for an administrator's password.
var readOnly = []string{"", "status", "list", "why", "apply --plan", "nightly --plan"}

// askpass starts answering what the programs a run starts ask for an
// administrator's password, through sudo: from the password the person
// gave earlier in the run, or by asking them in kit's field, the live part
// of the run stepping aside meanwhile. Nil when it can't start: sudo asks
// then as it would.
func (a *app) askpass(face render.Face) *askpass.Server {
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	s, err := askpass.Start("", self, func(ctx context.Context, asked int) (string, error) {
		about := look.Row{State: look.NeedsYou, Name: "sudo", Says: look.Orange("needs an administrator's password")}
		if f, ok := face.(interface{ Running() []string }); ok {
			if running := f.Running(); len(running) > 0 {
				about.Name = strings.Join(running, ", ")
			}
		}
		if asked > 0 {
			about.Says = look.Orange("that wasn't it: try again")
		}
		if f, ok := face.(interface{ StepAside() func() }); ok {
			defer f.StepAside()()
		}
		return a.ReadSecret(ctx, about)
	})
	if err != nil {
		return nil
	}
	return s
}

// placed is how the kind called name's lines install, as the run places
// them: while kit asks for an administrator's password, what needs it waits
// for the answer, and it's left out once the password's not to be had, as
// a check made while kit asked took it as coming. What the kind's done
// with for now, nothing waits behind (settled).
func (r *run) placed(name string) kind.Placing {
	p := r.placing[name]
	p.Settled = func(things []string) { r.settled.note(name, things) }
	if r.hold != nil {
		p.Admin = r.hold
	}
	return p
}

// isAdminStep reports whether the step named name needs an administrator's
// password to apply.
func (r *run) isAdminStep(name string) bool {
	return slices.ContainsFunc(r.adminSteps, func(s engine.Step) bool { return s.Name == name })
}

// features are the features switched on for this Mac: the shared file's,
// then its own. One kit doesn't know is refused, saying where.
func (r *run) features() (featuresOn, error) {
	list, err := r.cfg.List(config.FeaturesKind, r.machine)
	if err != nil {
		return nil, err
	}
	on := make(featuresOn, len(list.Entries))
	for _, e := range list.Entries {
		if err := movedToBackups(e); err != nil {
			return nil, err
		}
		if !slices.Contains(steps.Features, e.Name) {
			return nil, fmt.Errorf("%s: kit doesn't know the feature %s: one of %s", e.Pos(), e.Name, strings.Join(steps.Features, ", "))
		}
		on[e.Name] = e
	}
	return on, nil
}

// featuresOn are the features switched on for a Mac, each by the line
// switching it on.
type featuresOn map[string]config.Entry

// on reports whether the feature named name is on.
func (f featuresOn) on(name string) bool {
	_, ok := f[name]
	return ok
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
	if e, ok := k.(kind.Expander); ok {
		expanded, unread := e.Expand(list)
		return expanded, unread, nil
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
	face := render.NewPretty(a.colors(out), a.Width(out), true).Sized(func() (int, int) { return a.Width(out), a.Height(out) })
	a.unwinds = append(a.unwinds, face.Unwind)
	if a.underHome {
		face.UnderHome()
	}
	if t, ok := apps.Running(a.Getenv); ok && t.Driver.TabProgress() {
		face.ShowTabProgress()
	}
	return face
}

// stoppable is ctx for a run shown on face, stopped too by what applying's
// screen reads at a terminal, as the terminal's raw mode sends kit ctrl+c as
// a key, not an interrupt: ctrl+c, and q's Stop.
func (a *app) stoppable(ctx context.Context, face render.Face) (context.Context, context.CancelFunc) {
	ctx, stop := context.WithCancel(ctx)
	if f, ok := face.(*render.Pretty); ok && a.Listen != nil {
		f.Keys(a.Listen, stop)
	}
	return ctx, stop
}

// childEnv is the environment kit runs programs in, never the one it
// inherited whole: who and where the user is, the SSH agent (git signs and
// pushes through it), kit's own PATH, what the secrets stores' programs need
// (secret.Env), Homebrew kept from updating itself, nagging or colouring its
// output on its own, and git from asking for a password mid-run.
func childEnv(getenv func(string) string, environ []string, home string, path []string) []string {
	env := []string{"HOME=" + home, "PATH=" + strings.Join(path, ":")}
	for _, name := range []string{"USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "LC_ALL", "SSH_AUTH_SOCK"} {
		if v := getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	env = append(env, secret.Env(getenv, environ)...)
	return append(env, "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
}

// partOf is what the step called name counts towards in a view rolling its
// area up: a kind's things, and the steps it needs, are its group's; the
// home folder's set-up and the Mac's switches are packages or settings too;
// the config repository's steps are its own.
func partOf(name string) string {
	if d, ok := kinds.Of(name); ok {
		return groupParts[d.Group]
	}
	switch name {
	case steps.PathName, steps.FeatureTouchIDSudo, steps.FeatureRemoteLogin, steps.FeatureFileSharing:
		return render.PartSettings
	case linked.StepName:
		return render.PartFiles
	case steps.ConfigEditsName, steps.ConfigSyncName, steps.ConfigPrivateName:
		return render.PartConfig
	}
	return ""
}

// groupParts are what each group of kinds' things count towards.
var groupParts = map[kind.Group]string{kind.Packages: render.PartPackages, kind.Settings: render.PartSettings, kind.Secrets: render.PartSecrets}
