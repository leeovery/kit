package steps

import (
	"context"
	"errors"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/runner"
)

// AppAccessName names the step by hand giving kit.app Full Disk Access.
const AppAccessName = "kit-app-access"

// AccessSettings is System Settings, opened at Full Disk Access's list.
const AccessSettings = "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_AllFiles"

// The card giving kit.app Full Disk Access.
const (
	appAccessTitle = "Give kit.app Full Disk Access"
	appAccessWhy   = "kit's hourly run goes through kit.app, which needs Full Disk Access to save apps' settings and read Time Machine's state."
)

var appAccessSteps = []string{
	"System Settings is open at Full Disk Access, and Finder at kit.app",
	"Drag kit.app into the list, and switch it on",
	"In the list already, from an earlier build? Remove it with −, then drag it in again: macOS keeps the old build's grant",
}

// AppAccessing is what giving kit.app Full Disk Access needs.
type AppAccessing struct {
	Run runner.Runner
	// App is kit.app's path, and Built the fingerprint of the kit.app
	// there, "" when there's none (macapp.Built).
	App   string
	Built func() string
	// StateDir is kit's state, holding the scheduled runs' record.
	StateDir string
	// Raise raises its card, at a terminal: nil elsewhere, where no one's
	// there to give it.
	Raise *Raise
	Now   func() time.Time
}

// AppAccess is the step by hand giving kit.app Full Disk Access, which its
// hourly run needs: done as the record of the scheduled runs last found it
// for the kit.app there, by a run reading what only it shows, or as the
// person said once they'd given it. One built from other files is another
// app to macOS, which has none till it's given again. Applied at a
// terminal, it opens System Settings at the list and Finder at kit.app,
// raises its card, and records it given once the person says so, for the
// next hourly run to find out.
func AppAccess(a AppAccessing) engine.Step {
	step := engine.Step{
		Name: AppAccessName, Title: AppAccessName, Area: AreaManual,
		ByHand: []event.Hand{{Name: AppAccessName, Title: appAccessTitle, Why: appAccessWhy}},
		Check: func(context.Context) check.Result {
			rec, err := nightly.Load(a.StateDir)
			if err != nil {
				return failed(err)
			}
			said, ok := appAccess(rec.Access, a.Built(), a.Now().Location())
			if ok {
				return check.Result{State: check.OK, Summary: said}
			}
			return check.Result{State: check.Attention, Summary: said, Items: []check.Item{{
				ID: AppAccessName + ":" + AppAccessName, Name: appAccessTitle, State: check.ByHand, Detail: "a whole kit apply at a terminal asks for it",
			}}}
		},
	}
	if a.Raise == nil {
		return step
	}
	step.Apply = func(ctx context.Context, _ check.Result) error {
		built := a.Built()
		if built == "" {
			return engine.NotYet("kit.app isn't built yet: the hourly launch's step builds it")
		}
		c := &ownCard{
			says: event.NeedsYou{Item: AppAccessName, Title: appAccessTitle, Why: appAccessWhy, Steps: appAccessSteps},
			show: func(ctx context.Context) {
				_, _ = a.Run.Run(ctx, runner.Command{Name: "open", Args: []string{AccessSettings}})
				_, _ = a.Run.Run(ctx, runner.Command{Name: "open", Args: []string{"-R", a.App}})
			},
		}
		_, _, err := a.Raise.up(ctx, AppAccessName, c, midRun(ctx, AppAccessName))
		if err != nil && !errors.Is(err, errDoneThenStop) {
			if errors.Is(err, errStopHere) {
				return nil
			}
			return err
		}
		return nightly.Update(a.StateDir, func(r *nightly.Record) {
			r.Access = nightly.Access{At: a.Now(), App: built, Granted: true, Given: true}
		})
	}
	return step
}

// appAccess says how kit.app's Full Disk Access stands, as last found, for
// the kit.app built from the files whose fingerprint is built, its times in
// loc, and whether that's done.
func appAccess(last nightly.Access, built string, loc *time.Location) (said string, ok bool) {
	switch {
	case built == "" || last.App == "":
		return "not given", false
	case last.App != built:
		if last.Granted {
			return "not given to this build of kit.app", false
		}
		return "not given", false
	case !last.Granted:
		return "not granted: the hourly run at " + last.At.In(loc).Format("15:04") + " couldn't read what it protects", false
	case last.Given:
		return "given: the next hourly run checks it", true
	}
	return "granted", true
}
