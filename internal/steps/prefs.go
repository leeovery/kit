package steps

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/prefs"
)

// PrefsName names the step checking apps' settings are saved.
const PrefsName = "prefs"

// prefsEvery is how stale the last capture may be: a day and a little,
// as the nightly run captures once a day.
const prefsEvery = 26 * time.Hour

// The keys of apps' settings' to-do: bring them back, or start afresh.
const (
	keyBringBack = "r"
	keyAfresh    = "f"
)

// settingsBack is the to-do of a Mac whose apps' settings are yet to be
// brought back, or started afresh: first, so the apps opened next start
// with them. It says up front that bringing them back opens some apps;
// and, when the config keeps settings files beside the apps' settings,
// that they come back too.
func settingsBack(files bool) check.ToDo {
	why := "First, so the apps you open next start with yours. Bringing them back opens some apps once: their windows, notifications and permission prompts may follow. This Mac saves its own once they're back."
	if files {
		why += " Settings files come back too."
	}
	return check.ToDo{
		Title: "Bring back apps' settings",
		Why:   why,
		Group: groupApps,
		Keys: []check.Key{
			{Key: keyBringBack, Does: "brings them back from this Mac's last save"},
			{Key: keyAfresh, Does: "starts afresh: nothing back, saving from now"},
		},
		First: true,
	}
}

// Prefs checks apps' settings are being saved: capture switched on, this
// Mac owning its folder in the store, a capture in the last day without
// errors, and its commits pushed within a day. It reads kit's record
// alone: no git, no GitHub. Not switched on, it's a to-do: bring them back
// from this Mac's last save, as kit prefs restore does, its date said as it
// comes up; or start afresh, as kit prefs start-fresh does.
func Prefs(p *prefs.Prefs, now func() time.Time) engine.Step {
	return engine.Step{
		Name: PrefsName, Title: "App settings", Area: AreaBackups,
		Choice: &engine.Choice{
			Ready: func(ctx context.Context, it check.Item) check.Item {
				if it.ToDo == nil {
					return it
				}
				last, err := p.LastSave(ctx)
				if err != nil {
					return it
				}
				t := *it.ToDo
				t.Keys = slices.Clone(t.Keys)
				for i, k := range t.Keys {
					if k.Key == keyBringBack {
						t.Keys[i].Does += ", " + day(last, now())
					}
				}
				it.ToDo = &t
				return it
			},
			Answer: func(ctx context.Context, _ check.Item, key string) (string, error) {
				switch key {
				case keyBringBack:
					return bringBack(ctx, p, now)
				case keyAfresh:
					return startAfresh(p)
				}
				return "", fmt.Errorf("%s isn't one of its keys", key)
			},
		},
		Check: func(context.Context) check.Result {
			rec, err := p.Load()
			if err != nil {
				return failed(err)
			}
			switch {
			case rec.NotOwner != "":
				return problem(PrefsName, "another Mac owns this Mac's folder in the store", [3]string{"not-owner", rec.NotOwner, "this Mac doesn't capture until it takes ownership (kit takeover, to come)"})
			case rec.On == "":
				return yours(PrefsName, "paused: capture isn't switched on for this Mac", "paused", "kit prefs restore, or kit prefs start-fresh", settingsBack(len(p.Lists.Files) > 0))
			case rec.Captured.IsZero() && !rec.OnAt.IsZero() && now().Sub(rec.OnAt) <= prefsEvery:
				// Just switched on, it's fine for now: the nightly run
				// saves them.
				return check.Result{State: check.OK, Summary: "saving from tonight", Glance: "app settings from tonight"}
			case rec.Captured.IsZero():
				return problem(PrefsName, "not captured yet", [3]string{"never", "apps' settings haven't been saved yet", "the nightly run captures them; or kit prefs capture"})
			case now().Sub(rec.Captured) > prefsEvery:
				return problem(PrefsName, "last capture "+check.Ago(rec.Captured, now()), [3]string{"stale", "apps' settings haven't been saved for over a day", "kit prefs capture says why"})
			case rec.Errors > 0:
				return problem(PrefsName, rec.Summary, [3]string{"errors", fmt.Sprintf("the last capture couldn't save %d", rec.Errors), "kit prefs capture lists them"})
			case !rec.Unpushed.IsZero() && now().Sub(rec.Unpushed) > prefsEvery:
				return problem(PrefsName, "not pushed since "+check.Ago(rec.Unpushed, now()), [3]string{"unpushed", rec.PushError, "the next capture pushes again; kit prefs capture shows it"})
			}
			at := check.When(rec.Captured, now())
			return check.Result{State: check.OK, Summary: "saved " + at, Glance: "app settings " + at}
		},
	}
}

// bringBack brings back apps' settings from this Mac's last save, as kit
// prefs restore does, saying what it brought back, as in 214 apps'
// settings, from 15 Oct, how many wait for their apps, and how many didn't
// come back, the log naming each, with why. Those that didn't are held
// aside with those waiting, and tried again with them; saving starts all
// the same.
func bringBack(ctx context.Context, p *prefs.Prefs, now func() time.Time) (string, error) {
	event.Log(ctx, event.Logged{Line: "Bringing back apps' settings from this Mac's last save"})
	report, err := p.Restore(ctx, prefs.RestoreOptions{})
	if err != nil {
		return "", err
	}
	for _, domain := range slices.Sorted(maps.Keys(report.Failed)) {
		name := cmp.Or(report.Apps[domain], domain)
		event.Log(ctx, event.Logged{Line: name + "'s settings didn't come back: " + report.Failed[domain], Alert: true})
	}
	said := fmt.Sprintf("%d apps' settings", len(report.Restored))
	if last, err := p.LastSave(ctx); err == nil {
		said += ", from " + day(last, now())
	}
	if n := len(report.Waiting); n > 0 {
		said += fmt.Sprintf("; %d %s", n, plural(n, "waits for its app", "wait for their apps"))
	}
	if n := len(report.Failed); n > 0 {
		said += fmt.Sprintf("; %d didn't come back, tried again hourly", n)
	}
	return said, nil
}

// startAfresh switches saving on with nothing brought back, as kit prefs
// start-fresh does, saying so, and how many of an earlier restore's still
// wait to come back, which saving leaves be.
func startAfresh(p *prefs.Prefs) (string, error) {
	if err := p.StartFresh(); err != nil {
		return "", err
	}
	rec, err := p.Load()
	if err != nil {
		return "", err
	}
	said := "saving from now, nothing brought back"
	if rec.Restored != nil {
		said = "saving from now"
	}
	if rec.Pending != nil {
		n := len(rec.Pending.Domains)
		said += fmt.Sprintf("; %d still %s to come back", n, plural(n, "waits", "wait"))
	}
	return said, nil
}

// day is the day t was, as now's clock has it: 15 Oct.
func day(t, now time.Time) string {
	return t.In(now.Location()).Format("2 Jan")
}
