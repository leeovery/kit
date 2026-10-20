package steps

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
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
	why := "First, so the apps you open next start with yours. Bringing them back opens some apps once, never again: their windows, notifications and permission prompts may follow. This Mac saves its own once they're back."
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

// appsToOpen is the to-do of a Mac with apps whose settings wait for their
// settings folders, which macOS makes only once each app's opened, kit
// having opened it to no effect, or opening none unattended: open each
// once and quit it. kit watches for the folders.
func appsToOpen(apps []prefs.App) check.ToDo {
	t := check.ToDo{Group: groupApps, First: true}
	if len(apps) == 1 {
		name := apps[0].Name
		t.Title = "Open " + name + " once and quit it"
		t.Why = "Its settings come back after: macOS makes its settings folder only once it's opened."
		t.Steps = []string{"Open " + name, "Quit it"}
		t.Watch = name + " has its settings folder"
		return t
	}
	t.Title = "Open these apps once and quit them"
	t.Why = "Their settings come back after: macOS makes their settings folders only once they're opened."
	for _, a := range apps {
		t.Steps = append(t.Steps, "Open "+a.Name+", then quit it")
	}
	t.Watch = "each has its settings folder"
	return t
}

// Prefs checks apps' settings are being saved: capture switched on, this
// Mac owning its folder in the store, a capture in the last day without
// errors, and its commits pushed within a day. It reads kit's record
// alone: no git, no GitHub. Not switched on, it's a to-do: bring them back
// from this Mac's last save, as kit prefs restore does, its date said as it
// comes up; or start afresh, as kit prefs start-fresh does. With apps held
// for their settings folders, it's a to-do to open them, watched: once
// they're there, the settings of those not running come back at once.
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
			Settle: func(ctx context.Context, it check.Item) string {
				if it.ID != PrefsName+":"+idOpen {
					return ""
				}
				return bringBackOpened(ctx, p)
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
			case len(p.ToOpen(rec)) > 0:
				apps := p.ToOpen(rec)
				names := make([]string, len(apps))
				for i, a := range apps {
					names[i] = a.Name
				}
				summary := fmt.Sprintf("%d %s to open once", len(apps), plural(len(apps), "app", "apps"))
				return yours(PrefsName, summary, idOpen, "open once and quit: "+strings.Join(names, ", "), appsToOpen(apps))
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

// idOpen is the id, in its step, of the to-do to open apps once.
const idOpen = "open"

// bringBack brings back apps' settings from this Mac's last save, as kit
// prefs restore does, saying what it brought back, as in 214 apps'
// settings, from 15 Oct; how many wait for their apps; how many apps are
// to be opened once, which a to-do asks for next; and how many are held,
// having failed otherwise, for kit prefs restore --pending, the log naming
// each, with why. Saving starts all the same.
func bringBack(ctx context.Context, p *prefs.Prefs, now func() time.Time) (string, error) {
	event.Log(ctx, event.Logged{Line: "Bringing back apps' settings from this Mac's last save"})
	report, err := p.Restore(ctx, prefs.RestoreOptions{})
	if err != nil {
		return "", err
	}
	for _, domain := range slices.Sorted(maps.Keys(report.Failed)) {
		name := cmp.Or(report.Apps[domain].Name, domain)
		event.Log(ctx, event.Logged{Line: name + "'s settings didn't come back: " + report.Failed[domain], Alert: true})
	}
	for _, file := range slices.Sorted(maps.Keys(report.FilesFailed)) {
		event.Log(ctx, event.Logged{Line: file + " didn't come back: " + report.FilesFailed[file], Alert: true})
	}
	said := cameBack(report)
	if last, err := p.LastSave(ctx); err == nil {
		said += ", from " + day(last, now())
	}
	said += waitingFor(report)
	if n := appsOf(report.ToOpen, report.Apps); n > 0 {
		said += fmt.Sprintf("; %d %s to open once", n, plural(n, "app", "apps"))
	}
	if n := report.Failures(); n > 0 {
		said += fmt.Sprintf("; %d held for kit prefs restore --pending", n)
	}
	return said, nil
}

// bringBackOpened brings back the settings of the apps held for their
// settings folders that have them now and aren't running, opening
// nothing, as the hourly pass does: what it brought back, and how many
// still wait for their apps.
func bringBackOpened(ctx context.Context, p *prefs.Prefs) string {
	report, err := p.Restore(ctx, prefs.RestoreOptions{Pending: true, Unattended: true})
	if err != nil {
		event.Log(ctx, event.Logged{Line: "Bring back apps' settings: " + err.Error(), Alert: true})
		return ""
	}
	return cameBack(report) + waitingFor(report)
}

// cameBack is how many domains' settings report brought back, as in 214
// apps' settings.
func cameBack(report prefs.RestoreReport) string {
	n := len(report.Restored)
	return fmt.Sprintf("%d %s", n, plural(n, "app's settings", "apps' settings"))
}

// waitingFor is how many of report's domains wait for their apps, after a
// semicolon: "" for none.
func waitingFor(report prefs.RestoreReport) string {
	n := len(report.Waiting)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("; %d %s", n, plural(n, "waits for its app", "wait for their apps"))
}

// appsOf counts the apps of domains, as apps names them, each once.
func appsOf(domains map[string]string, apps map[string]prefs.App) int {
	ids := map[string]bool{}
	for d := range domains {
		ids[cmp.Or(apps[d].ID, d)] = true
	}
	return len(ids)
}

// startAfresh switches saving on with nothing brought back, as kit prefs
// start-fresh does, saying so, and how many of an earlier restore's still
// wait to come back, which saving leaves be. A restore switches saving on
// itself, so only a record from before it did can have one to tell of.
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
		n := len(rec.Pending.Domains) + len(rec.Pending.Files)
		said += fmt.Sprintf("; %d still %s to come back", n, plural(n, "waits", "wait"))
	}
	return said, nil
}

// day is the day t was, as now's clock has it: 15 Oct.
func day(t, now time.Time) string {
	return t.In(now.Location()).Format("2 Jan")
}
