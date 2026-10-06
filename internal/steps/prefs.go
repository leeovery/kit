package steps

import (
	"context"
	"fmt"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/prefs"
)

// PrefsName names the step checking apps' settings are saved.
const PrefsName = "prefs"

// prefsEvery is how stale the last capture may be: a day and a little,
// as the nightly run captures once a day.
const prefsEvery = 26 * time.Hour

// Prefs checks apps' settings are being saved: capture switched on, this
// Mac owning its folder in the store, a capture in the last day without
// errors, and its commits pushed within a day. It reads kit's record
// alone: no git, no GitHub.
func Prefs(p *prefs.Prefs, now func() time.Time) engine.Step {
	return engine.Step{
		Name: PrefsName, Title: "Settings", Area: AreaBackups,
		Check: func(context.Context) check.Result {
			rec, err := p.Load()
			if err != nil {
				return failed(err)
			}
			switch {
			case rec.NotOwner != "":
				return problem(PrefsName, "another Mac owns this Mac's folder in the store", [3]string{"not-owner", rec.NotOwner, "this Mac doesn't capture until it takes ownership (kit takeover, to come)"})
			case rec.On == "":
				return problem(PrefsName, "paused: capture isn't switched on for this Mac", [3]string{"paused", "apps' settings aren't being saved", "kit prefs restore, or kit prefs start-fresh"})
			case rec.Captured.IsZero():
				return problem(PrefsName, "not captured yet", [3]string{"never", "apps' settings haven't been saved yet", "the nightly run captures them; or kit prefs capture"})
			case now().Sub(rec.Captured) > prefsEvery:
				return problem(PrefsName, "last capture "+ago(rec.Captured, now()), [3]string{"stale", "apps' settings haven't been saved for over a day", "kit prefs capture says why"})
			case rec.Errors > 0:
				return problem(PrefsName, rec.Summary, [3]string{"errors", fmt.Sprintf("the last capture couldn't save %d", rec.Errors), "kit prefs capture lists them"})
			case !rec.Unpushed.IsZero() && now().Sub(rec.Unpushed) > prefsEvery:
				return problem(PrefsName, "not pushed since "+ago(rec.Unpushed, now()), [3]string{"unpushed", rec.PushError, "the next capture pushes again; kit prefs capture shows it"})
			}
			at := when(rec.Captured, now())
			return check.Result{State: check.OK, Summary: "captured " + at + ": " + rec.Summary, Glance: "Settings " + at}
		},
	}
}
