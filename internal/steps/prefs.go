package steps

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
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

// The keys of the settings to-do: restore its source's last save, or
// start afresh.
const (
	keyRestore = "r"
	keyAfresh  = "f"
)

// afresh is the settings to-do's key starting afresh.
var afresh = check.Key{Key: keyAfresh, Does: "starts afresh: nothing restored, saving from now"}

// settingsBack is the to-do of a Mac whose apps' settings are yet to be
// restored, or started afresh: first, so the apps opened next start with
// them; and, when the config keeps settings files beside the apps'
// settings, it says they're restored too. Readied, its keys are its
// source's save, if any, and f (restoreKeys).
func settingsBack(files bool) check.ToDo {
	why := "First, so the apps you open next start with yours. This Mac saves its own once they're restored."
	if files {
		why += " Settings files are restored too."
	}
	return check.ToDo{
		Title: "Restore application settings",
		Why:   why,
		Group: groupApps,
		Keys: []check.Key{
			{Key: keyRestore, Does: "restores the last save"},
			afresh,
		},
		First: true,
	}
}

// restoreKeys are the settings to-do's keys, the store's saves known: r,
// when there's a source (source), its date said, and f.
func restoreKeys(saves []prefs.Save, mac, from string, now time.Time) []check.Key {
	s, ok := source(saves, mac, from)
	if !ok {
		return []check.Key{afresh}
	}
	does := fmt.Sprintf("restores this machine ID's last save (%s)", day(s.At, now))
	if s.Mac != mac {
		does = fmt.Sprintf("restores %s's last save (%s) onto this Mac; %s keeps its own", s.Mac, day(s.At, now), s.Mac)
	}
	return []check.Key{{Key: keyRestore, Does: does}, afresh}
}

// source is the save the settings to-do restores: this machine ID's own,
// else that of from, the Mac it started as a copy of; or none.
func source(saves []prefs.Save, mac, from string) (prefs.Save, bool) {
	for _, m := range []string{mac, from} {
		if i := slices.IndexFunc(saves, func(s prefs.Save) bool { return m != "" && s.Mac == m }); i >= 0 {
			return saves[i], true
		}
	}
	return prefs.Save{}, false
}

// appToOpen is the to-do of a sandboxed app whose settings wait for its
// settings folder, which macOS makes only once it's opened: kit opens it
// as its card goes up, and the person quits it and says so.
func appToOpen(app prefs.App) check.ToDo {
	return check.ToDo{
		Title:    "Open " + app.Name + " and quit it",
		Why:      "Its settings go in its own folder, which macOS makes the first time it opens: quit it, then press space.",
		Group:    groupApps,
		SaysDone: true,
		First:    true,
	}
}

// Prefs checks apps' settings are being saved: capture switched on, this
// Mac owning its folder in the store, a capture in the last day without
// errors, and its commits pushed within a day. It reads kit's record
// alone: no git, no GitHub. Not switched on, it's a to-do: restore the
// last save of its source, this machine ID's own or else from's, the Mac
// it started as a copy of, as kit prefs restore does, its date said as it
// comes up; or start afresh, as kit prefs start-fresh does. Either takes
// this Mac's folder over when other hardware owns it, the card saying so.
// Applied, with no source in the store, it starts afresh by itself. With
// sandboxed apps held for their settings folders, it's a to-do for each,
// one at a time: kit opens the app, once a run, and once the person's quit
// it, restores its settings.
func Prefs(p *prefs.Prefs, from string, now func() time.Time) engine.Step {
	var mu sync.Mutex
	opened := map[string]bool{}
	return engine.Step{
		Name: PrefsName, Title: "App settings", Area: AreaBackups,
		Choice: &engine.Choice{
			Ready: func(ctx context.Context, it check.Item) check.Item {
				if it.ToDo == nil {
					return it
				}
				saves, err := p.Saves(ctx)
				if err != nil {
					return it
				}
				t := *it.ToDo
				t.Keys = restoreKeys(saves, p.Mac, from, now())
				if owner, taking, _ := p.TakingOver(ctx); taking {
					t.Why = fmt.Sprintf("This Mac takes over %s from %s, which stops saving to it. %s", p.Mac, owner.Called(), t.Why)
				}
				it.ToDo = &t
				return it
			},
			Answer: func(ctx context.Context, it check.Item, key string) (string, error) {
				if id, ok := strings.CutPrefix(it.ID, PrefsName+":"+idOpen+":"); ok {
					return restoreOpened(ctx, p, id)
				}
				said, err := answerSettings(ctx, p, from, key, now)
				if err != nil {
					return "", err
				}
				return said, p.TakeOver(ctx)
			},
			Open: func(ctx context.Context, it check.Item) {
				id, ok := strings.CutPrefix(it.ID, PrefsName+":"+idOpen+":")
				mu.Lock()
				first := ok && !opened[id]
				opened[id] = true
				mu.Unlock()
				if first {
					p.Open(ctx, id)
				}
			},
		},
		Check: func(ctx context.Context) check.Result {
			rec, err := p.Load()
			if err != nil {
				return failed(err)
			}
			switch {
			case rec.NotOwner != "":
				return problem(PrefsName, "another Mac owns this Mac's folder in the store", [3]string{"not-owner", rec.NotOwner, "another Mac took this folder over, so this Mac doesn't save to it"})
			case rec.On == "":
				return yours(PrefsName, "paused: capture isn't switched on for this Mac", idPaused, "kit prefs restore, or kit prefs start-fresh", settingsBack(len(p.Lists.Files) > 0))
			}
			return withHeld(ctx, p, rec, saving(rec, now))
		},
		Apply: func(ctx context.Context, found check.Result) error {
			if !slices.ContainsFunc(found.Items, func(it check.Item) bool { return it.ID == PrefsName+":"+idPaused }) {
				return nil
			}
			// A store that can't be read leaves the card as it stands.
			if saves, err := p.Saves(ctx); err == nil {
				if _, ok := source(saves, p.Mac, from); !ok {
					said, err := startAfresh(p)
					if err == nil {
						event.Log(ctx, event.Logged{Line: "No saved application settings to restore: " + said})
					}
					return err
				}
			}
			return nil
		},
	}
}

// idPaused is the id, in its step, of the settings to-do.
const idPaused = "paused"

// answerSettings does what key, of the settings to-do, says: r restores
// its source's last save (source); f starts afresh.
func answerSettings(ctx context.Context, p *prefs.Prefs, from, key string, now func() time.Time) (string, error) {
	switch key {
	case keyAfresh:
		return startAfresh(p)
	case keyRestore:
		saves, err := p.Saves(ctx)
		if err != nil {
			return "", err
		}
		if s, ok := source(saves, p.Mac, from); ok {
			return restoreSave(ctx, p, s, now)
		}
		return "", fmt.Errorf("no settings stored for %s", strings.Join(slices.DeleteFunc([]string{p.Mac, from}, func(m string) bool { return m == "" }), " or "))
	}
	return "", fmt.Errorf("%s isn't one of its keys", key)
}

// saving is how saving apps' settings stands, as rec has it, capture
// switched on.
func saving(rec prefs.Record, now func() time.Time) check.Result {
	switch {
	case rec.Captured.IsZero() && !rec.OnAt.IsZero() && now().Sub(rec.OnAt) <= prefsEvery:
		// Just switched on, it's fine for now: the nightly run saves them.
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
}

// withHeld is res, how saving stands, with what rec holds that needs the
// person: a to-do for each sandboxed app to open and quit; and what failed
// otherwise, which only kit prefs restore --pending tries again. Saving
// standing fine, they're what it says.
func withHeld(ctx context.Context, p *prefs.Prefs, rec prefs.Record, res check.Result) check.Result {
	var items []check.Item
	var says []string
	if apps := p.ToOpen(ctx, rec); len(apps) > 0 {
		for _, app := range apps {
			t := appToOpen(app)
			items = append(items, check.Item{ID: PrefsName + ":" + idOpen + ":" + app.ID, Name: t.Title, State: Problem, Detail: "its settings are restored once it's been opened and quit", ToDo: &t})
		}
		says = append(says, fmt.Sprintf("%d %s to open once", len(apps), plural(len(apps), "app", "apps")))
	}
	if domains, files := p.Failures(rec); domains+files > 0 {
		var what []string
		if domains > 0 {
			what = append(what, fmt.Sprintf("%d %s", domains, plural(domains, "app's settings", "apps' settings")))
		}
		if files > 0 {
			what = append(what, fmt.Sprintf("%d %s", files, plural(files, "settings file", "settings files")))
		}
		items = append(items, check.Item{ID: PrefsName + ":held", Name: strings.Join(what, " and ") + " didn't come back", State: Problem, Detail: "kit prefs restore --pending tries again; kit prefs pending says why"})
		says = append(says, fmt.Sprintf("%d held for kit prefs restore --pending", domains+files))
	}
	if len(items) == 0 {
		return res
	}
	if res.State == check.OK {
		res = check.Result{State: check.Attention, Summary: strings.Join(says, "; ")}
	}
	res.Items = append(res.Items, items...)
	return res
}

// idOpen is the id, in its step, of the to-dos to open apps once, each
// followed by its app's bundle id.
const idOpen = "open"

// restoreSave restores apps' settings from save, as kit prefs restore
// does, saying what it restored, as in 214 apps' settings, from 15 Oct; how
// many wait for their apps; how many apps are to be opened once, which a
// to-do each asks for next; and how many are held, having failed
// otherwise, for kit prefs restore --pending, the log naming each, with
// why. Saving starts all the same.
func restoreSave(ctx context.Context, p *prefs.Prefs, save prefs.Save, now func() time.Time) (string, error) {
	whose := save.Mac + "'s"
	if save.Mac == p.Mac {
		whose = "this machine ID's"
	}
	event.Log(ctx, event.Logged{Line: "Restoring application settings from " + whose + " last save"})
	report, err := p.Restore(ctx, prefs.RestoreOptions{From: save.Mac})
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
	said := cameBack(report) + ", from " + day(save.At, now()) + waitingFor(report)
	if n := appsOf(report.ToOpen, report.Apps); n > 0 {
		said += fmt.Sprintf("; %d %s to open once", n, plural(n, "app", "apps"))
	}
	if n := report.Failures(); n > 0 {
		said += fmt.Sprintf("; %d held for kit prefs restore --pending", n)
	}
	return said, nil
}

// restoreOpened restores the settings of the app of a bundle id, id, once
// the person's opened and quit it, with those of any other app held that
// can be now, opening nothing but a helper with no window, as the hourly
// pass does: what it restored, and how many still wait for their apps.
// Still running, or with no settings folder yet, it says so, and restores
// nothing.
func restoreOpened(ctx context.Context, p *prefs.Prefs, id string) (string, error) {
	if err := p.Opened(ctx, id, cmp.Or(openedName(p, id), id)); err != nil {
		return "", err
	}
	report, err := p.Restore(ctx, prefs.RestoreOptions{Pending: true, Unattended: true})
	if err != nil {
		return "", err
	}
	return cameBack(report) + waitingFor(report), nil
}

// openedName is the name of the app of a bundle id that's held to be
// opened: "" when none is.
func openedName(p *prefs.Prefs, id string) string {
	rec, err := p.Load()
	if err != nil || rec.Pending == nil {
		return ""
	}
	for _, app := range rec.Pending.Open {
		if app.ID == id {
			return app.Name
		}
	}
	return ""
}

// cameBack is how many domains' settings report brought back, as in 214
// apps' settings.
func cameBack(report prefs.RestoreReport) string {
	switch n := len(report.Restored); n {
	case 0:
		return "no apps' settings"
	case 1:
		return "1 app's settings"
	default:
		return fmt.Sprintf("%d apps' settings", n)
	}
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

// startAfresh switches saving on with nothing restored, as kit prefs
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
	said := "saving from now, nothing restored"
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
