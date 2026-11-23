package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/dialog"
	"github.com/leeovery/kit/internal/drift"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
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
It brings in kit-config's remote commits first, leaving your edits as they are.

Name steps, as kit status shows them (homebrew, brew, cask, config-private),
to check only those, with what they need. A kind is checked only while the
config has its section for this Mac, the shared file's or its own, even an
empty one. Exits 0 when kit checked, even when something needs attention, 1
when a check failed, and 2 when kit couldn't check. Each run is logged: see
kit log.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			a.pull, a.pullQuietly = true, len(args) > 0
			r, err := a.prepare(cmd.Context(), "status", "status")
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
			if report.Failed() {
				return failure{}
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
	steps []engine.Step
	sink  event.Sink
	face  render.Face
	// log is the run's log: none, that logs nothing, for what isn't a run,
	// as kit agent order isn't.
	log      render.Face
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
	// path and env are kit's own PATH and the environment it runs programs
	// in.
	path, env []string
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
	record  drift.Records
}

// remember notes, in the drift record, the drift the run's checks found:
// what's new, and what's gone; and, as drift is, kit-config's remote
// commits not brought in (pullOf); and it forgets the kinds the run
// doesn't look after.
func (r *run) remember(report engine.Report) error {
	results := make(map[string]check.Result)
	for _, name := range r.drift {
		if res, ok := report.Results[name]; ok {
			results[name] = res
		}
	}
	if res, ok := report.Results[steps.ConfigSyncName]; ok {
		results[steps.ConfigSyncName] = pullOf(res)
	}
	unmanaged := slices.DeleteFunc(slices.Clone(r.allKinds), func(k string) bool { return slices.Contains(r.kinds, k) })
	return drift.Remember(r.stateDir, results, r.now, unmanaged)
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
// it; copied from the kit.app a release ships beside this kit, when there's
// one.
func (r *run) notifier(a *app) *macapp.App {
	if r.notices == nil {
		var shipped string
		if self, err := a.Self(); err == nil {
			shipped = macapp.Shipped(self)
		}
		r.notices = macapp.New(r.run, r.dataDir, shipped, a.Getenv("__CFBundleIdentifier"), a.Now)
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
func (a *app) prepare(ctx context.Context, command, logName string) (*run, error) {
	face := a.face()
	r, err := a.build(ctx, command, logName, face, a.pretty(a.Stdout))
	if err != nil {
		return nil, err
	}
	a.start(r, face)
	return r, nil
}

// build readies a run as prepare does, shown on face, its steps those of a
// run at a terminal when atTerminal says so, as kit apply's steps by hand
// raised as cards are, but starting nothing a run at one starts (start).
// Without a logName, it keeps no log. A run that pulls (a.pull) brings in
// kit-config's remote commits first, so it reads what came in. Failing, it
// closes face.
func (a *app) build(ctx context.Context, command, logName string, face render.Face, atTerminal bool) (_ *run, err error) {
	log := render.Tee()
	defer func() {
		if err != nil {
			_ = face.Close()
			_ = log.Close()
		}
	}()
	dirs, err := a.dirs()
	if err != nil {
		return nil, err
	}
	at, err := a.basis(dirs)
	if err != nil {
		if at, err = a.healing(dirs, err); err != nil {
			return nil, err
		}
	}
	now := a.Now()
	if logName != "" {
		file, err := logs.Open(dirs.Logs, now, logName, a.verbose)
		if err != nil {
			return nil, err
		}
		log = file
	}
	sink := event.NewFanout(face, log)
	var pulled steps.Pulled
	if a.pull {
		pulled = a.pullConfig(ctx, dirs.Config, at, command, now, sink)
	}
	if at.cfg == nil || pulled.Brought > 0 {
		if at, err = a.basis(dirs); err != nil {
			return nil, unhealed(err, pulled)
		}
	}
	cfg, machine, home, path := at.cfg, at.machine, at.home, at.path
	record, err := drift.Load(dirs.State)
	if err != nil {
		return nil, err
	}
	// What the run starts whose sudo asks can ask it for an administrator's
	// password, at a terminal, once the run is ready (askpass).
	var r *run
	env := childEnv(a.Getenv, home, path)
	observed := a.commands(path, env, sink, func() *askpass.Server {
		if r == nil {
			return nil
		}
		return r.askpass
	})
	r = &run{
		command: command, machine: machine, version: a.Version, sink: sink, face: face, log: log,
		stateDir: dirs.State, now: now, run: observed, path: path, env: env, cfg: cfg, homeDir: home, logsDir: dirs.Logs, dataDir: dirs.Data,
		kindsByName: map[string]kind.Kind{}, lists: map[string]config.List{}, placing: map[string]kind.Placing{},
		repo: gitrepo.Repo{Dir: dirs.Config, Run: observed}, record: record,
		drifters: map[string]drifter{}, admin: &steps.Admin{},
	}
	r.files = &linked.Files{Repo: r.repo, Home: home, StateDir: dirs.State, Scopes: []string{config.Shared, machine}}
	// setup are the steps that set up the home folder, its linked files and
	// the shell's PATH, and the features that turn on parts of macOS.
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
		return nil, err
	}
	pathFile := steps.PathFile(shellPath, filepath.Join(dirs.State, steps.PathFileName))
	addSetup(pathFile, applyDrifter{step: pathFile, words: kind.Words{Label: "Write", Does: "write it", Doing: "writing"}})
	// kindSteps are the kinds' steps; needed, the steps they need that are
	// their own, as Homebrew's.
	var kindSteps, needed []engine.Step
	backups, err := r.backups()
	if err != nil {
		return nil, err
	}
	deps := &kind.Deps{Run: observed, Home: home, ConfigHome: a.configHome(), StateDir: dirs.State, Backups: backupNames(backups), AppDirs: a.appFolders(home).Dirs}
	// granting is how what kit runs in, access, is given Full Disk Access,
	// which the kinds whose programs need it wait for (accessWaits).
	granting := steps.GrantingFor(a.Getenv, a.scheduled)
	access := &steps.Access{Home: home}
	var accessWaits []string
	for _, d := range kinds.All {
		k := d.New(deps)
		list, unread, err := kind.Declared(cfg, k, machine)
		if err != nil {
			return nil, err
		}
		r.allKinds = append(r.allKinds, d.Name)
		r.kindsByName[d.Name], r.lists[d.Name] = k, list
		accessor, needsAccess := k.(kind.FullDiskAccessor)
		if needsAccess {
			accessor.SetAccess(r.access(&granting, access, a.scheduled))
		}
		if !list.Declared && unread == nil {
			continue
		}
		step := kind.Placed(k, list, func(context.Context) kind.Placing { return r.placed(d.Name) }, d.Needs...)
		step.Title, step.Area, step.Row, step.Tool, step.Verb = d.Label, steps.AreaDrift, d.Row, d.Tool, d.Installs().Done
		if needsAccess {
			// At a terminal, after the step whose card it waits behind; the
			// hourly run's probes for itself, and kit-app-access comes after
			// it and Homebrew's formulae.
			if !a.scheduled {
				step.After = append(step.After, granting.Step)
			}
			accessWaits = append(accessWaits, d.Name)
		}
		if unread != nil {
			step.Check = func(context.Context) check.Result {
				return check.Result{State: check.Failed, Reason: unread.Error()}
			}
		} else if len(list.Entries) > 0 && slices.Contains(applies, command) {
			for _, program := range kind.Commands(k) {
				step.Waits = append(step.Waits, engine.Wait{Thing: program, Ready: func(context.Context) bool { return runner.Has(observed, program) }})
			}
			// Each line on, not what's missing of them: finding that is a
			// kind's check, its listings and brew info's look-ups, a second
			// or more a kind, before the run could start.
			step.Weight = len(list.On())
		}
		kindSteps = append(kindSteps, quietened(step, record, now))
		r.kinds = append(r.kinds, d.Name)
		r.drift = append(r.drift, d.Name)
		r.drifters[d.Name] = kindDrifter{k: k, list: list, installing: d.Installs(), removing: d.Removes(), tool: d.Tool, held: func() []kind.Held { return r.placing[d.Name].Held }}
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
			addSetup(step, applyDrifter{step: step, words: kind.Words{Label: "Turn on", Does: "turn it on", Doing: "turning on"}})
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
			return nil, err
		}
		saving := steps.Prefs(p, r.cfg.Macs[r.machine].From, a.Now)
		// Applied after the apps a run installs, it restores the settings
		// kept for them before they're first opened.
		for _, apps := range kinds.Apps() {
			if slices.Contains(r.kinds, apps) {
				saving.After = append(saving.After, apps)
			}
		}
		settings = []engine.Step{feature(steps.FeaturePrefs, saving)}
	}
	// The terminal's grant, or the SSH session's: kit-app-access looks
	// after kit.app's. Placed as [features] places prefs only while nothing
	// else waits for it, as what holds prefs back mustn't hold that back.
	granting = granting.For(accessWaits, features.on(steps.FeaturePrefs))
	if !a.scheduled && (features.on(steps.FeaturePrefs) || len(accessWaits) > 0) {
		step := steps.FullDiskAccess(granting, access)
		if features.on(steps.FeaturePrefs) && len(accessWaits) == 0 {
			step = feature(steps.FeaturePrefs, step)
		}
		settings = append(settings, step)
	}
	checks = append(checks, settings...)
	// A step by hand that comes due while kit applies at a terminal is
	// raised there, a card, and waited on: the rest, with no place of their
	// own, once everything else has finished, with what the run left the
	// person to do, so that step's there whether or not any are declared.
	// The data restore raises its own. A run naming its steps shows a
	// timeline, with no card to answer: it raises nothing, leaving the
	// steps by hand to kit manual done and the rest to a whole kit apply.
	var raise *steps.Raise
	if slices.Contains(applies, command) && atTerminal && len(a.named) == 0 {
		desk := hand.New()
		raise = &steps.Raise{Sink: sink, Now: a.Now, Notify: r.notifier(a), Desk: desk, After: a.After}
		r.raise = raise
		if f, ok := face.(*render.Pretty); ok {
			f.Hands(desk.Answer)
		}
		r.byHandLast = true
	}
	restoring, err := a.restoreStep(r, dirs, features.on(steps.FeaturePrefs))
	if err != nil {
		return nil, err
	}
	if restoring != nil {
		checks = append(checks, *restoring)
	}
	reinstalling, err := a.reinstallStep(r, dirs, restoring != nil)
	if err != nil {
		return nil, err
	}
	if reinstalling != nil {
		checks = append(checks, *reinstalling)
	}
	if restoring == nil {
		settling, err := a.settleStep(r, dirs)
		if err != nil {
			return nil, err
		}
		if settling != nil {
			checks = append(checks, *settling)
		}
	}
	edits := steps.ConfigEdits(r.repo)
	r.drift = append(r.drift, steps.ConfigEditsName)
	r.drifters[steps.ConfigEditsName] = configDrifter{repo: r.repo, home: home, step: edits}
	checks = append(checks,
		r.hostKeyStep(a.SSHDir, a.SSHAddress, dirs.State, features.on(steps.FeatureRemoteLogin)), quietened(edits, record, now),
		steps.Disk(observed), steps.Memory(observed), steps.Load(observed, a.Now),
		steps.ConfigSync(r.repo, a.Now, quietPull(pulled, record, now)), steps.ConfigPrivate(r.repo),
	)
	newMac, err := boot.NewMac(dirs.State)
	if err != nil {
		return nil, err
	}
	if newMac == machine {
		checks = append(checks, newMacStep(r.repo, dirs.State, machine))
	}
	own, err := cfg.List(config.ChecksKind, machine)
	if err != nil {
		return nil, err
	}
	if len(own.Entries) > 0 {
		checks = append(checks, steps.Own(observed, home, own))
	}
	byHand, err := cfg.List(config.ManualKind, machine)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	scheduling, err := r.schedulingSteps(a, dirs, raise, accessWaits)
	if err != nil {
		return nil, err
	}
	checks = append(checks, scheduling...)
	checks = append(checks, nightly.Check(slices.Concat(hourly, daily), len(daily) > 0, dirs.State, a.Now))
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
		r.placeKinds(r.steps)
		r.placeFeatures(r.steps, features, featured)
		r.placeBackups(r.steps, backups)
		r.placeLines(r.steps)
		r.placeByHand(r.steps)
		placeCommits(r.steps, slices.Concat(scripts, byHandOwn))
		for i, s := range r.steps {
			if s.Admin && s.Apply != nil {
				r.steps[i].Apply = r.afterAdmin(s.Apply)
			}
		}
		r.pipeline, err = engine.New(r.steps...)
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// basis is what a run stands on: the config, as this kit reads it; this
// Mac's name, settled before anything reads a Mac's own files; its home;
// and kit's own PATH, from the config's paths.
type basis struct {
	cfg     *config.Config
	machine string
	home    string
	path    []string
}

// basis reads what a run stands on, in dirs, refusing a Mac with no name,
// or one the config doesn't know. A config there that this kit can't read
// is unread.
func (a *app) basis(dirs config.Dirs) (basis, error) {
	cfg, err := a.loadConfig(dirs)
	switch {
	case errors.Is(err, config.ErrNoConfig):
		return basis{}, err
	case err != nil:
		return basis{}, unread{err}
	}
	machine, err := config.ReadMachine(dirs.State)
	if err != nil {
		return basis{}, err
	}
	switch {
	case machine == "":
		return basis{}, fmt.Errorf("this Mac has no name yet: run kit machine <name>, one of %s", strings.Join(cfg.MacNames(), ", "))
	case !cfg.Knows(machine):
		return basis{}, fmt.Errorf("this Mac is named %s, which %s doesn't know: run kit machine <name>, one of %s", machine, config.File, strings.Join(cfg.MacNames(), ", "))
	}
	home, err := a.HomeDir()
	if err != nil {
		return basis{}, fmt.Errorf("find the home directory: %w", err)
	}
	path, err := cfg.SearchPath(home, machine)
	if err != nil {
		return basis{}, err
	}
	return basis{cfg: cfg, machine: machine, home: home, path: path}, nil
}

// unread is basis's error for a config there that this kit can't read: a
// newer kit's setting, or a bad edit from another Mac.
type unread struct{ error }

func (u unread) Unwrap() error { return u.error }

// healing is what a run that pulls stands on when basis failed, as err,
// for a config this kit can't read, which another Mac may have pushed the
// fix for: this Mac's name and home, kit's base PATH (the system's folders
// and Homebrew's), and no config, to bring in kit-config's remote commits
// with, then read it again. Any other failure, a run that doesn't pull, or
// a Mac with no name yet, is err.
func (a *app) healing(dirs config.Dirs, err error) (basis, error) {
	if _, ok := errors.AsType[unread](err); !ok || !a.pull {
		return basis{}, err
	}
	machine, nameErr := config.ReadMachine(dirs.State)
	home, homeErr := a.HomeDir()
	if nameErr != nil || homeErr != nil || machine == "" {
		return basis{}, err
	}
	return basis{machine: machine, home: home, path: config.SearchPathOf(nil)}, nil
}

// unhealed is err, the config still unread once kit-config's remote
// commits were to be brought in, saying why none were, as pulled has it,
// should none have been: the fix may be waiting on the remote.
func unhealed(err error, pulled steps.Pulled) error {
	if pulled.Missed == nil {
		return err
	}
	return fmt.Errorf("%w; kit-config's remote commits weren't brought in: %v", err, pulled.Missed)
}

// schedulingSteps are the steps that have kit run by itself, on every Mac:
// the hourly launch, opening kit.app to run the kit on kit's PATH nightly,
// whichever kit applies (steps.LaunchKit), found once Homebrew's formulae
// are applied; and, after it, the step by hand
// giving kit.app Full Disk Access, raised at a terminal (raise). In the
// hourly run, that step also comes after the kinds whose programs need it
// (held): one refused records kit.app without it, which the step's check
// must read after, not before.
func (r *run) schedulingSteps(a *app, dirs config.Dirs, raise *steps.Raise, held []string) ([]engine.Step, error) {
	self, err := a.Self()
	if err != nil {
		return nil, fmt.Errorf("find kit: %w", err)
	}
	app := r.notifier(a)
	launch := steps.HourlyLaunch(steps.Launching{
		Run: r.run, Dir: filepath.Join(r.homeDir, "Library", "LaunchAgents"), Domain: fmt.Sprintf("gui/%d", os.Getuid()),
		Kit: func() (string, string) { return steps.LaunchKit(self, r.run, cmp.Or(a.Getenv("TMPDIR"), "/tmp")) },
		App: app, StateDir: dirs.State, Now: a.Now,
	})
	// After the formulae, as kit's own may be among them, whether or not
	// they all install: After waits for a step to finish, never on how.
	if slices.Contains(r.kinds, brew.FormulaeDescriptor.Name) {
		launch.After = []string{brew.FormulaeDescriptor.Name}
	}
	access := steps.AppAccess(steps.AppAccessing{
		Run: r.run, App: app.Path(), Identity: func() string { return macapp.Identity(dirs.Data) },
		StateDir: dirs.State, Raise: raise, Now: a.Now,
	})
	access.After = []string{steps.LaunchName}
	if a.scheduled {
		access.After = append(access.After, held...)
	}
	return []engine.Step{launch, access}, nil
}

// start starts what a run at a terminal starts, shown on face: answering
// what its programs ask for an administrator's password, its cards'
// notifications, and watching macOS asking the person at the Mac.
func (a *app) start(r *run, face render.Face) {
	if a.pretty(a.Stdout) && !slices.Contains(readOnly, r.command) {
		r.askpass = a.askpass(face)
	}
	if r.raise != nil {
		r.notifier(a).Prepare()
	}
	if a.WatchDialogs && a.pretty(a.Stdout) {
		r.held, r.unwatch = watchDialogs(a.Runner(r.path, r.env), r.notifier(a), r.sink, a.Now)
	}
	if asks := r.askpass; asks != nil {
		dialogs := r.held
		r.held = func() bool { return asks.Pending() || dialogs != nil && dialogs() }
	}
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

// commands is the runner a run's commands go through: on path, with env;
// a command whose sudo asks given what asks has for it (askpassFor); each
// reported to sink, and its output streamed there, redacted, the values of
// the secrets it was given too.
func (a *app) commands(path, env []string, sink event.Sink, asks func() *askpass.Server) runner.Runner {
	exec := runner.Prepared(a.Runner(path, env), askpassFor(asks))
	observed := runner.Observed(exec, func(ctx context.Context, rep runner.Report) { sink.Emit(event.Command(ctx, rep)) }, a.Now)
	return runner.Streamed(observed, event.Changing, func(ctx context.Context, cmd runner.Command, line string) {
		sink.Emit(event.Output{Time: a.Now(), Step: event.StepOf(ctx), Command: redact.Text(cmd.String()), Line: redact.Text(line, runner.SecretValues(ctx)...)})
	})
}

// askpassFor gives a command what has its sudo ask the run for an
// administrator's password, through asks, once it's started: the helper,
// and its process noted as running, for what's asked from beneath it to be
// answered. Only a command whose sudo asks so (runner.Command's SudoAsks),
// never everything the run starts, as an npm package's install script.
func askpassFor(asks func() *askpass.Server) func(cmd runner.Command) runner.Command {
	return func(cmd runner.Command) runner.Command {
		s := asks()
		if s == nil || !cmd.SudoAsks {
			return cmd
		}
		cmd.Env = append(s.Env(), cmd.Env...)
		cmd.Running = s.Running
		return cmd
	}
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
	return r.featuresOf(r.machine)
}

// featuresOf are the features switched on for mac, as features are for
// this Mac.
func (r *run) featuresOf(mac string) (featuresOn, error) {
	list, err := r.cfg.List(config.FeaturesKind, mac)
	if err != nil {
		return nil, err
	}
	on := make(featuresOn, len(list.Entries))
	for _, e := range list.Entries {
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

// configHome is XDG's config folder, when it's set to an absolute path: ""
// for the default.
func (a *app) configHome() string {
	if dir := a.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	return ""
}

// quietened is step with its items quiet while their time hasn't come, as
// the drift record says: new for a day, snoozed, or a temporary install;
// but never what applying it was given to act on in the run and left still
// to act on.
func quietened(step engine.Step, record drift.Records, now time.Time) engine.Step {
	checks, applies := step.Check, step.Apply
	acted := &tried{}
	step.Check = func(ctx context.Context) check.Result {
		return drift.Quieten(checks(ctx), record, now, acted.items())
	}
	if applies != nil {
		step.Apply = func(ctx context.Context, found check.Result) error {
			acted.note(found)
			return applies(ctx, found)
		}
	}
	return step
}

// tried are the items applying a step was given to act on, by id.
type tried struct {
	mu  sync.Mutex
	ids map[string]bool
}

// note notes found's items that applying acts on (check.Item's Action).
func (t *tried) note(found check.Result) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, it := range found.Items {
		if it.Action == "" {
			continue
		}
		if t.ids == nil {
			t.ids = map[string]bool{}
		}
		t.ids[it.ID] = true
	}
}

// items are the items noted, by id.
func (t *tried) items() map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return maps.Clone(t.ids)
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
// pushes through it), kit's own PATH, Homebrew kept from updating itself,
// nagging or colouring its output on its own, and git from asking for a
// password mid-run. A secrets store's driver passes its program what it
// needs of kit's own.
func childEnv(getenv func(string) string, home string, path []string) []string {
	env := []string{"HOME=" + home, "PATH=" + strings.Join(path, ":")}
	for _, name := range []string{"USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "LC_ALL", "SSH_AUTH_SOCK"} {
		if v := getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
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
	case steps.ConfigEditsName, steps.ConfigSyncName, steps.ConfigPrivateName, newMacName, steps.HostKeyName:
		return render.PartConfig
	}
	return ""
}

// groupParts are what each group of kinds' things count towards.
var groupParts = map[kind.Group]string{kind.Packages: render.PartPackages, kind.Settings: render.PartSettings, kind.Secrets: render.PartSecrets}
