package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/steps"
)

// prefsNoun names apps' settings' command.
const prefsNoun = "prefs"

// newPrefsCommand is kit prefs: apps' settings, saved to the prefs
// repository and restored from it.
func newPrefsCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     prefsNoun,
		Short:   "Apps' settings, saved nightly to a private repository and restored from it",
		GroupID: groupFiles,
		Long: `Apps' settings: every preferences domain that isn't Apple's or the system's,
through defaults export and import, and the settings files [prefs files] names.
Each Mac saves them nightly into its own folder of the repository kit.toml's
prefs_repo names (private on GitHub, or kit doesn't push), cloned in kit's data
folder; each capture that changed anything is a commit, so restore can go back
to a date. [prefs deny] leaves domains out; [prefs apps] says whose a domain
is when its name doesn't; [prefs machine-bound] are never copied to another
Mac, or onto other hardware.`,
	}
	capture := &cobra.Command{
		Use:   "capture",
		Short: "Save apps' settings now, as the nightly run does",
		Long: `Save apps' settings now, as the nightly run does: only what changed is
written, what's gone from the Mac leaves the store (history keeps it), and
one commit is pushed. Needs Full Disk Access, capture switched on for this
Mac, and this Mac owning its folder in the store.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, prefsNoun+" capture", a.prefsCapture)
		},
	}
	var opts prefs.RestoreOptions
	restore := &cobra.Command{
		Use:   "restore [<domain>...]",
		Short: "Put apps' settings back from the store: all, or those domains",
		Long: `Put apps' settings back from the store: every saved domain whose app is
installed and not running, then the settings files that differ, the live
copy of each saved first. A sandboxed app with no settings folder yet is
opened once, hidden, to make one. Name domains to restore just those, even
without their app. A full restore switches capture on. What it can't
restore is held, left alone by capture (kit prefs pending): kept till its app
is installed, as kit restores it once it is; waiting for its app to quit; for
you to open an app once, as no settings folder came of kit opening it; or,
failed otherwise, in a full restore, for --pending. Never quits an app
but a helper with no window, inside another app, which it quits, opens
hidden for its settings folder if need be, and starts again if it was
running.

--from restores another Mac's settings, leaving out [prefs machine-bound]
(as onto other hardware); --at the store as it was then (2026-09-28, "3 days
ago"); --as one domain into a scratch domain, to test without touching the
app; --pending what's waiting, failed or not, opening an app once for its
settings folder; --dry-run says what it would do. The hourly pass restores
what's waiting too, but opens only such helpers, and leaves what failed to
--pending.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Domains = args
			return a.prepared(cmd, prefsNoun+" restore", func(ctx context.Context, r *run) error {
				return a.prefsRestore(ctx, r, opts)
			})
		},
	}
	f := restore.Flags()
	f.StringVar(&opts.From, "from", "", "restore another Mac's settings, by its name")
	f.StringVar(&opts.At, "at", "", `the store as it was then, any date git reads: 2026-09-28, "3 days ago"`)
	f.StringVar(&opts.As, "as", "", "restore one domain into this scratch domain, leaving the app alone")
	f.BoolVar(&opts.Pending, "pending", false, "restore what's waiting, failed or not, opening an app once for its settings folder")
	f.BoolVar(&opts.DryRun, "dry-run", false, "say what it would restore, and change nothing")
	var from string
	var count int
	history := &cobra.Command{
		Use:   "history [<domain>|<path>]",
		Short: "The captures that changed anything, or one domain's or file's changes",
		Long: `The captures that changed anything, newest first; or, named, one domain's
or one settings file's (~/...) changes over time, as diffs: settings are
sorted XML, so a diff is the setting that changed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, prefsNoun+" history", func(ctx context.Context, r *run) error {
				p, err := a.prefs(r)
				if err != nil {
					return err
				}
				target := ""
				if len(args) == 1 {
					target = args[0]
				}
				out, err := p.History(ctx, from, target, count)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(a.Stdout, out)
				return err
			})
		},
	}
	history.Flags().StringVar(&from, "from", "", "another Mac's history, by its name")
	history.Flags().IntVarP(&count, "count", "n", 20, "how many captures")
	var clear bool
	pending := &cobra.Command{
		Use:   "pending",
		Short: "What's held to be restored, each with why (--clear: drop it, and capture what's there)",
		Long: `What's held to be restored, each with why: kept till its app is installed,
which kit restores once it installs the app, as the hourly pass does; an app
running, which the hourly pass brings back once it's quit; an app to open
once, which it brings back once the app has its settings folder; or a
restore that failed otherwise, which only kit prefs restore --pending tries
again. --clear drops it all, so capture saves what's on the Mac.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, prefsNoun+" pending", func(_ context.Context, r *run) error {
				return a.prefsPending(r, clear)
			})
		},
	}
	pending.Flags().BoolVar(&clear, "clear", false, "drop what's held, so capture saves what's on the Mac for it")
	startFresh := &cobra.Command{
		Use:   "start-fresh",
		Short: "Switch capture on without restoring: the next capture starts this Mac's store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, prefsNoun+" start-fresh", func(_ context.Context, r *run) error {
				p, err := a.prefs(r)
				if err != nil {
					return err
				}
				if err := p.StartFresh(); err != nil {
					return err
				}
				_, err = fmt.Fprintln(a.Stdout, "Capture is switched on for this Mac: the next capture starts its store.")
				return err
			})
		},
	}
	cmd.AddCommand(capture, restore, history, pending, startFresh)
	return cmd
}

// prefs is apps' settings on this Mac, with kit-config's lists for it.
func (a *app) prefs(r *run) (*prefs.Prefs, error) {
	var lists prefs.Lists
	for kind, into := range map[string]*[]string{config.PrefsFilesKind: &lists.Files, config.PrefsDenyKind: &lists.Deny, config.PrefsMachineBoundKind: &lists.MachineBound} {
		list, err := r.cfg.List(kind, r.machine)
		if err != nil {
			return nil, err
		}
		*into = list.Names()
	}
	apps, err := r.cfg.List(config.PrefsAppsKind, r.machine)
	if err != nil {
		return nil, err
	}
	for _, e := range apps.Entries {
		lists.Apps = append(lists.Apps, [2]string{e.Name, e.Value})
	}
	p := prefs.New(r.run, r.homeDir, r.stateDir, filepath.Join(r.dataDir, prefsNoun), r.cfg.PrefsRepo, r.machine, lists, a.Now)
	p.Probes = append(p.Probes[:len(p.Probes)-1], a.TCC)
	p.AppDirs = a.appFolders(r.homeDir).Dirs
	return p, nil
}

// oneStep runs do as a run of one step, saying what it's doing as it
// starts, and whatever do says meanwhile: the run needs attention unless it
// stands ok.
func (a *app) oneStep(ctx context.Context, r *run, step event.Step, doing string, do func(ctx context.Context) check.Result) error {
	r.sink.Emit(event.RunStarted{Time: r.now, Command: r.command, Machine: r.machine, Version: r.version, Steps: []event.Step{step}})
	started := time.Now()
	r.sink.Emit(event.StepStarted{Time: started, Step: step.Name, Doing: doing})
	ctx = event.WithSaying(event.WithStep(ctx, step.Name), func(e event.Doing) {
		e.Time, e.Step = time.Now(), step.Name
		r.sink.Emit(e)
	})
	res := do(event.WithDoing(ctx, doing))
	r.sink.Emit(event.StepFinished{Time: time.Now(), Step: step.Name, Result: res, Duration: time.Since(started)})
	r.sink.Emit(event.RunFinished{Time: time.Now(), Duration: time.Since(started), Counts: event.Tally(res)})
	if res.State != check.OK {
		return attention{}
	}
	return nil
}

// prefsStep is kit prefs' commands' one step.
var prefsStep = event.Step{Name: steps.PrefsName, Title: "Settings", Area: steps.AreaBackups}

// prefsCapture captures apps' settings, as a run of one step.
func (a *app) prefsCapture(ctx context.Context, r *run) error {
	p, err := a.prefs(r)
	if err != nil {
		return err
	}
	return a.oneStep(ctx, r, prefsStep, "capturing", func(ctx context.Context) check.Result {
		return captureResult(p.Capture(ctx))
	})
}

// captureResult is how a capture stands: what it saved, each error and a
// push that waits an item; refused, why.
func captureResult(report prefs.Report, err error) check.Result {
	var notOwner prefs.NotOwnerError
	switch {
	case errors.Is(err, prefs.ErrPaused):
		return check.Result{State: check.Attention, Summary: "paused: capture isn't switched on for this Mac", Items: []check.Item{{ID: "prefs:paused", Name: "Restore application settings", State: steps.Problem, Detail: "kit prefs restore, or kit prefs start-fresh"}}}
	case errors.As(err, &notOwner):
		return check.Result{State: check.Attention, Summary: "another Mac owns this Mac's folder in the store", Items: []check.Item{{ID: "prefs:not-owner", Name: err.Error(), State: steps.Problem}}}
	case err != nil:
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	res := check.Result{State: check.OK, Summary: report.Summary()}
	for _, e := range report.Errors {
		res.Items = append(res.Items, check.Item{ID: "prefs:error:" + e, Name: e, State: "error"})
	}
	if report.PushError != "" {
		res.Items = append(res.Items, check.Item{ID: "prefs:unpushed", Name: "not pushed: " + report.PushError, State: "unpushed", Detail: "the next capture pushes again"})
	}
	if len(res.Items) > 0 {
		res.State = check.Attention
	}
	return res
}

// prefsRestore restores apps' settings, as a run of one step: what waits
// and what failed, each an item.
func (a *app) prefsRestore(ctx context.Context, r *run, opts prefs.RestoreOptions) error {
	p, err := a.prefs(r)
	if err != nil {
		return err
	}
	doing := "restoring"
	if opts.DryRun {
		doing = "checking what it would restore"
	}
	return a.oneStep(ctx, r, prefsStep, doing, func(ctx context.Context) check.Result {
		report, err := p.Restore(ctx, opts)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		if opts.Pending && report.Restored == nil && report.Files == 0 && len(report.Waiting)+len(report.Kept)+len(report.ToOpen) == 0 && report.Failures() == 0 {
			return check.Result{State: check.OK, Summary: "nothing pending"}
		}
		res := check.Result{State: check.OK, Summary: report.Summary()}
		for _, d := range slices.Sorted(maps.Keys(report.Failed)) {
			res.Items = append(res.Items, check.Item{ID: "prefs:failed:" + d, Name: d, State: "failed", Detail: report.Failed[d]})
		}
		for _, d := range slices.Sorted(maps.Keys(report.ToOpen)) {
			res.Items = append(res.Items, check.Item{ID: "prefs:open:" + d, Name: d, State: "to open", Detail: report.ToOpen[d], Quiet: "open " + report.Apps[d].Name + " once and quit it: the hourly pass brings it back"})
		}
		for _, f := range slices.Sorted(maps.Keys(report.FilesFailed)) {
			res.Items = append(res.Items, check.Item{ID: "prefs:failed:" + f, Name: f, State: "failed", Detail: report.FilesFailed[f]})
		}
		for _, d := range slices.Sorted(maps.Keys(report.Waiting)) {
			quiet := "the hourly pass tries again"
			if prefs.Running(report.Waiting[d]) {
				quiet = "until it's closed: kit prefs restore --pending"
			}
			res.Items = append(res.Items, check.Item{ID: "prefs:pending:" + d, Name: d, State: "pending", Detail: report.Waiting[d], Quiet: quiet})
		}
		for _, d := range slices.Sorted(maps.Keys(report.Kept)) {
			res.Items = append(res.Items, check.Item{ID: "prefs:kept:" + d, Name: d, State: "kept", Detail: "its app isn't installed", Quiet: "restored once it is"})
		}
		if report.Failures() > 0 {
			res.State = check.Attention
		}
		return res
	})
}

// prefsPending says what's waiting to be restored, or drops it.
func (a *app) prefsPending(r *run, clear bool) error {
	p, err := a.prefs(r)
	if err != nil {
		return err
	}
	if clear {
		n, err := p.ClearPending()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(a.Stdout, "Cleared %d %s: capture saves what's on the Mac for them now.\n", n, look.Plural(n, "pending setting", "pending settings"))
		return err
	}
	rec, err := p.Load()
	if err != nil {
		return err
	}
	if a.json {
		pending := rec.Pending
		if pending == nil {
			pending = &prefs.Pending{Domains: []string{}}
		}
		return render.WriteJSON(a.Stdout, pending)
	}
	if rec.Pending == nil {
		_, err = fmt.Fprintln(a.Stdout, "Nothing is held to be restored.")
		return err
	}
	if _, err := fmt.Fprintf(a.Stdout, "Held to be restored from %s (kit prefs restore --pending):\n", rec.Pending.From); err != nil {
		return err
	}
	for _, name := range slices.Concat(rec.Pending.Domains, rec.Pending.Files) {
		line := "  " + name
		if why := rec.Pending.Why[name]; why != "" {
			line += ": " + why
		}
		if _, err := fmt.Fprintln(a.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}
