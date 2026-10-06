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
installed and not running (the rest wait: kit prefs pending), then the
settings files that differ, the live copy of each saved first. Name domains
to restore just those, even without their app. A full restore with nothing
failed switches capture on. Never quits an app.

--from restores another Mac's settings, leaving out [prefs machine-bound]
(as onto other hardware); --at the store as it was then (2026-09-28, "3 days
ago"); --as one domain into a scratch domain, to test without touching the
app; --pending what's waiting; --dry-run says what it would do.`,
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
	f.BoolVar(&opts.Pending, "pending", false, "restore what's waiting, now its apps are installed and closed")
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
		Short: "What's waiting to be restored (--clear: drop it, and capture what's there)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, prefsNoun+" pending", func(_ context.Context, r *run) error {
				return a.prefsPending(r, clear)
			})
		},
	}
	pending.Flags().BoolVar(&clear, "clear", false, "drop what's waiting, so capture saves what's on the Mac for it")
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
	return p, nil
}

// oneStep runs do as a run of one step, name, in area, saying what it's
// doing meanwhile: the run needs attention unless it stands ok.
func (a *app) oneStep(ctx context.Context, r *run, name, title, area, doing string, do func(ctx context.Context) check.Result) error {
	r.sink.Emit(event.RunStarted{Time: r.now, Command: r.command, Machine: r.machine, Version: r.version, Steps: []event.Step{{Name: name, Title: title, Area: area}}})
	started := time.Now()
	r.sink.Emit(event.StepStarted{Time: started, Step: name, Doing: doing})
	res := do(event.WithStep(ctx, name))
	r.sink.Emit(event.StepFinished{Time: time.Now(), Step: name, Result: res, Duration: time.Since(started)})
	r.sink.Emit(event.RunFinished{Time: time.Now(), Duration: time.Since(started), Counts: event.Tally(res)})
	if res.State != check.OK {
		return attention{}
	}
	return nil
}

// prefsCapture captures apps' settings, as a run of one step.
func (a *app) prefsCapture(ctx context.Context, r *run) error {
	p, err := a.prefs(r)
	if err != nil {
		return err
	}
	return a.oneStep(ctx, r, steps.PrefsName, "Settings", steps.AreaBackups, "capturing", func(ctx context.Context) check.Result {
		return captureResult(p.Capture(ctx))
	})
}

// captureResult is how a capture stands: what it saved, each error and a
// push that waits an item; refused, why.
func captureResult(report prefs.Report, err error) check.Result {
	var notOwner prefs.NotOwnerError
	switch {
	case errors.Is(err, prefs.ErrPaused):
		return check.Result{State: check.Attention, Summary: "paused: capture isn't switched on for this Mac", Items: []check.Item{{ID: "prefs:paused", Name: "apps' settings aren't being saved", State: steps.Problem, Detail: "kit prefs restore, or kit prefs start-fresh"}}}
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
	return a.oneStep(ctx, r, steps.PrefsName, "Settings", steps.AreaBackups, doing, func(ctx context.Context) check.Result {
		report, err := p.Restore(ctx, opts)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		if opts.Pending && report.Restored == nil && len(report.Waiting) == 0 && len(report.Failed) == 0 {
			return check.Result{State: check.OK, Summary: "nothing pending"}
		}
		res := check.Result{State: check.OK, Summary: report.Summary()}
		for _, d := range slices.Sorted(maps.Keys(report.Failed)) {
			res.Items = append(res.Items, check.Item{ID: "prefs:failed:" + d, Name: d, State: "failed", Detail: report.Failed[d]})
		}
		for _, d := range slices.Sorted(maps.Keys(report.Waiting)) {
			res.Items = append(res.Items, check.Item{ID: "prefs:pending:" + d, Name: d, State: "pending", Detail: report.Waiting[d], Quiet: "until it's installed and closed: kit prefs restore --pending"})
		}
		if len(report.Failed) > 0 {
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
		_, err = fmt.Fprintf(a.Stdout, "Cleared %s: capture saves what's on the Mac for them now.\n", plural(n, "pending domain"))
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
		_, err = fmt.Fprintln(a.Stdout, "Nothing is waiting to be restored.")
		return err
	}
	if _, err := fmt.Fprintf(a.Stdout, "Waiting to be restored from %s (kit prefs restore --pending):\n", rec.Pending.From); err != nil {
		return err
	}
	for _, d := range rec.Pending.Domains {
		if _, err := fmt.Fprintln(a.Stdout, "  "+d); err != nil {
			return err
		}
	}
	return nil
}

// plural is n and a word, made plural unless n is 1.
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
