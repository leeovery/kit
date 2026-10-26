package prefs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// RestoreOptions are what to restore: everything from this Mac's folder
// unless From names another Mac's; Domains, just those; Pending, what
// waits; At, the store as it was then; As, one domain into a scratch
// domain, testing a restore without touching the app; DryRun, only say
// what would be done. Unattended, with Pending, is the hourly pass, with no
// one there: it opens no app, leaving one with no settings folder for the
// person to open, and leaves what failed otherwise for them to try again.
type RestoreOptions struct {
	From       string
	Domains    []string
	Pending    bool
	Unattended bool
	At         string
	As         string
	DryRun     bool
}

// RestoreReport is what a restore did, or would do.
type RestoreReport struct {
	From string
	// At is the date of the store's history restored from, and Commit its
	// commit; both empty for its latest.
	At, Commit string
	Restored   []string
	Files      int
	// Waiting are the domains left pending, each with why: not installed,
	// or running. ToOpen, those whose apps the person opens once, each with
	// why: kit opened them and no settings folder came of it, or, unattended,
	// kit opens none.
	Waiting, ToOpen map[string]string
	// Failed are the domains that failed otherwise, each with why;
	// FilesFailed, the settings files, by their paths from ~ (or /), each
	// with why.
	Failed, FilesFailed map[string]string
	// Apps are the apps of the domains not restored, where kit found them.
	Apps map[string]App
	// Saved is where the live settings replaced were saved.
	Saved  string
	DryRun bool
	// As is the scratch domain a domain was restored into.
	As string
	// Notes are what the clone's pull did besides, worth saying, as dealing
	// with what a capture that stopped left.
	Notes []string
}

// Summary is the restore in a line, as prefsync's, then what its pull did
// besides.
func (r RestoreReport) Summary() string {
	return withNotes(r.restored(), r.Notes)
}

// restored is what the restore did, or would, in a line.
func (r RestoreReport) restored() string {
	if r.As != "" {
		if r.DryRun {
			return fmt.Sprintf("would import %s as %s", r.Restored[0], r.As)
		}
		return fmt.Sprintf("imported %s as %s", r.Restored[0], r.As)
	}
	verb := "restored"
	if r.DryRun {
		verb = "would restore"
	}
	s := fmt.Sprintf("%s %s and %s from %s", verb, plural(len(r.Restored), "domain"), plural(r.Files, "file"), r.From)
	if r.At != "" {
		s += " as it was at " + r.At
	}
	if len(r.Waiting) > 0 {
		s += fmt.Sprintf("; %d pending (not installed or running)", len(r.Waiting))
	}
	if len(r.ToOpen) > 0 {
		s += fmt.Sprintf("; %d to open once", len(r.ToOpen))
	}
	if n := r.Failures(); n > 0 {
		s += fmt.Sprintf("; %d failed", n)
	}
	if r.Saved != "" {
		s += "; previous settings saved in " + r.Saved
	}
	return s
}

// Failures counts the domains and settings files that failed.
func (r RestoreReport) Failures() int {
	return len(r.Failed) + len(r.FilesFailed)
}

// helperDirs are where apps keep helper apps with their own preferences.
var helperDirs = []string{"Contents/MacOS", "Contents/Library/LoginItems", "Contents/Helpers"}

// installedApps are the apps installed, by bundle id: in each app folder and
// a folder below, and the helper apps inside them (Keyboard Maestro's
// Engine, login items). The first found of a bundle id is kept.
func (p *Prefs) installedApps() map[string]string {
	byID := map[string]string{}
	for _, base := range p.AppDirs {
		var found []string
		for _, pattern := range []string{"*.app", "*/*.app"} {
			m, _ := filepath.Glob(filepath.Join(base, pattern))
			found = append(found, m...)
		}
		for _, app := range found {
			bundles := []string{app}
			for _, d := range helperDirs {
				h, _ := filepath.Glob(filepath.Join(app, d, "*.app"))
				bundles = append(bundles, h...)
			}
			for _, b := range bundles {
				if id := apps.BundleID(b); id != "" {
					if _, seen := byID[id]; !seen {
						byID[id] = b
					}
				}
			}
		}
	}
	return byID
}

// groupPrefix is a group container's or team's prefix on a domain.
var groupPrefix = regexp.MustCompile(`^(group\.|[A-Z0-9]{10}\.)`)

// owningApps are the installed apps using a domain: a [prefs apps] entry's;
// the app whose bundle id it is; the app it's a sub-domain of
// (<bundle id>.something), the longest; or every app sharing it, as
// com.stairways.keyboardmaestro is the editor's and the engine's. A group
// container's or team's prefix is looked past.
func (p *Prefs) owningApps(domain string, apps map[string]string) []string {
	for _, pair := range p.Lists.Apps {
		if fnmatch(domain, pair[0]) {
			if _, ok := apps[pair[1]]; ok {
				return []string{pair[1]}
			}
		}
	}
	names := []string{domain}
	if bare := groupPrefix.ReplaceAllString(domain, ""); bare != domain {
		names = append(names, bare)
	}
	for _, name := range names {
		if _, ok := apps[name]; ok {
			return []string{name}
		}
		parent := ""
		for id := range apps {
			if strings.HasPrefix(name, id+".") && len(id) > len(parent) {
				parent = id
			}
		}
		if parent != "" {
			return []string{parent}
		}
		var children []string
		for id := range apps {
			if strings.HasPrefix(id, name+".") {
				children = append(children, id)
			}
		}
		if len(children) > 0 {
			slices.Sort(children)
			return children
		}
	}
	return nil
}

// running reports whether the app of a bundle id is running.
func (p *Prefs) running(ctx context.Context, id string) bool {
	res, err := p.Run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-e", fmt.Sprintf("application id %q is running", id)}})
	return err == nil && strings.TrimSpace(string(res.Stdout)) == "true"
}

// Why a sandboxed app's container isn't there: kit opened it, and none came
// of it; or kit may not open it; or, opened, it didn't quit.
var (
	errNoFolder  = errors.New("opened once, it still has no settings folder")
	errNotOpened = errors.New("it has no settings folder till it's opened once")
	errDidntQuit = errors.New("opened once for its settings folder, but it didn't quit")
)

// ensureContainer makes a sandboxed app's container, where it reads its
// settings, which exists only once it has run, when open says it may:
// launched hidden once, then quit, the run's log naming it as it opens.
// One that won't quit isn't restored, as it would write its defaults back
// when it did.
func (p *Prefs) ensureContainer(ctx context.Context, id, app string, open bool) error {
	if p.hasFolder(id) || !p.sandboxed(ctx, app) {
		return nil
	}
	if !open {
		return errNotOpened
	}
	container := filepath.Join(p.Home, "Library/Containers", id)
	event.Log(ctx, event.Logged{Line: "Opening " + appName(app) + " once, for its settings folder"})
	_, _ = p.Run.Run(ctx, runner.Command{Name: "open", Args: []string{"-g", "-j", "-b", id}})
	for range 40 {
		if _, err := os.Stat(container); err == nil {
			break
		}
		p.Sleep(500 * time.Millisecond)
	}
	_, _ = p.Run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-e", fmt.Sprintf("tell application id %q to quit", id)}})
	quit := false
	for range 20 {
		if !p.running(ctx, id) {
			quit = true
			break
		}
		p.Sleep(500 * time.Millisecond)
	}
	if !quit {
		return errDidntQuit
	}
	if !p.hasFolder(id) {
		return errNoFolder
	}
	return nil
}

// sandboxed reports whether the app at app is sandboxed: its entitlements,
// as codesign reads them, turn com.apple.security.app-sandbox on. An app can
// declare it off, and then macOS makes it no container: its settings are in
// its domain, as any app's.
func (p *Prefs) sandboxed(ctx context.Context, app string) bool {
	res, _ := p.Run.Run(ctx, runner.Command{Name: "codesign", Args: []string{"-d", "--entitlements", "-", "--xml", app}})
	v, _ := plist.Decode(res.Stdout)
	entitlements, _ := v.(map[string]any)
	on, _ := entitlements["com.apple.security.app-sandbox"].(bool)
	return on
}

// appName is the app at app by the name its bundle has, as the Finder shows
// it.
func appName(app string) string {
	return strings.TrimSuffix(filepath.Base(app), ".app")
}

// restoreDomain replaces a domain's live settings (target's, when testing
// into a scratch domain) with its stored copy, through cfprefsd: the stored
// copy checked first, the live settings saved, then read back and compared.
func (p *Prefs) restoreDomain(ctx context.Context, stored, target, saved string) error {
	data, err := os.ReadFile(stored)
	if err != nil {
		return errors.New("its saved copy couldn't be read")
	}
	v, err := plist.Decode(data)
	wanted, _ := v.(map[string]any)
	if err != nil || len(wanted) == 0 {
		return errors.New("its saved copy is empty")
	}
	if current, err := p.export(ctx, target); err == nil && len(current) > 0 {
		backup, err := plist.Encode(current)
		if err == nil {
			err = writeIfChanged(filepath.Join(saved, target+".plist"), backup)
		}
		if err != nil {
			return errors.New("its live settings couldn't be saved first")
		}
	}
	// Import merges: deleted first, for an exact restore.
	_, _ = p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"delete", target}})
	if _, err := p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"import", target, stored}}); err != nil {
		return errors.New("macOS didn't take them")
	}
	back, err := p.export(ctx, target)
	if err != nil || !plist.Equal(stripVolatile(back), stripVolatile(wanted)) {
		return errors.New("macOS took them, but they read back different")
	}
	return nil
}

// Restore puts settings from the store back on the Mac: every stored domain
// whose app is installed and closed (the rest wait, pending), then the
// settings files that differ, each live copy saved first. A full restore
// switches capture on, what it couldn't restore held aside, so capture
// leaves it be: waiting for its app; its app for the person to open once;
// or, failed otherwise, for kit prefs restore --pending. Never quits an
// app. From another Mac's folder, or onto other hardware, the settings
// bound to one Mac are left out.
func (p *Prefs) Restore(ctx context.Context, opts RestoreOptions) (RestoreReport, error) {
	report := RestoreReport{From: opts.From, DryRun: opts.DryRun, As: opts.As, Waiting: map[string]string{}, ToOpen: map[string]string{}, Failed: map[string]string{}, FilesFailed: map[string]string{}, Apps: map[string]App{}}
	if report.From == "" {
		report.From = p.Mac
	}
	rec, err := p.Load()
	if err != nil {
		return report, err
	}
	named := slices.Clone(opts.Domains)
	full := len(named) == 0 && !opts.Pending
	var files map[string]bool
	before := heldIn(rec.Pending)
	if opts.Unattended && !opts.Pending {
		return report, errors.New("an unattended restore restores what's waiting alone")
	}
	if opts.Pending {
		if len(named) > 0 || opts.As != "" || opts.At != "" {
			return report, errors.New("--pending restores what's waiting: no domains, --as or --at with it")
		}
		if rec.Pending == nil || len(rec.Pending.Domains)+len(rec.Pending.Files) == 0 {
			return report, nil
		}
		if opts.Unattended && !before.anyToTry() {
			return report, nil
		}
		report.From, named = rec.Pending.From, slices.Clone(rec.Pending.Domains)
		if !opts.Unattended {
			files = maps.Clone(before.files)
		}
	}
	if !opts.DryRun && !p.HasFullDiskAccess() {
		return report, errNoAccess
	}
	if err := p.ensureClone(ctx); err != nil {
		return report, err
	}
	// Offline, or refused, the clone's restored from as it is.
	report.Notes, _ = p.repo().Pull(ctx)
	store := filepath.Join(p.Clone, report.From)
	if opts.At != "" {
		dir, commit, date, cleanup, err := p.snapshot(ctx, report.From, opts.At)
		if err != nil {
			return report, err
		}
		defer cleanup()
		store, report.Commit, report.At = dir, commit, date
	}
	domainsDir, filesDir := filepath.Join(store, "domains"), filepath.Join(store, "files")
	var stored []string
	if entries, err := os.ReadDir(domainsDir); err == nil {
		for _, e := range entries {
			if d, ok := strings.CutSuffix(e.Name(), ".plist"); ok {
				stored = append(stored, d)
			}
		}
	}
	if len(stored) == 0 {
		return report, fmt.Errorf("no settings stored for %s", report.From)
	}
	saved := filepath.Join(p.State, backupsDir, p.Now().Format("2006-01-02-150405"))

	if opts.As != "" {
		if len(named) != 1 || !slices.Contains(stored, named[0]) {
			return report, errors.New("--as takes exactly one stored domain")
		}
		report.Restored = named
		if opts.DryRun {
			return report, nil
		}
		err := p.restoreDomain(ctx, filepath.Join(domainsDir, named[0]+".plist"), opts.As, saved)
		report.Saved = savedIfAny(saved)
		return report, err
	}

	bound, err := p.machineBound(ctx, report.From, store)
	if err != nil {
		return report, err
	}
	domains := named
	if full {
		for _, d := range stored {
			if !matches(d, p.Lists.Deny) && !matches(d, bound) {
				domains = append(domains, d)
			}
		}
	} else if !opts.Pending {
		var missing []string
		for _, d := range named {
			if !slices.Contains(stored, d) {
				missing = append(missing, d)
			}
		}
		if len(missing) > 0 {
			return report, fmt.Errorf("not in the store: %s", strings.Join(missing, ", "))
		}
	}

	installed, containers := p.installedApps(), map[string]error{}
	for _, domain := range domains {
		if opts.Unattended && before.failed[domain] {
			continue
		}
		if !slices.Contains(stored, domain) {
			report.Failed[domain] = "not in the store"
			continue
		}
		// A domain named by hand is restored even without an app; pending
		// ones wait until their app is installed.
		owners := p.owningApps(domain, installed)
		if len(owners) == 0 && (full || opts.Pending) {
			report.Waiting[domain] = "not installed"
			continue
		}
		p.restoreOwned(ctx, &report, domain, owners, installed, restoring{
			stored: filepath.Join(domainsDir, domain+".plist"), saved: saved, open: !opts.Unattended, dryRun: opts.DryRun, containers: containers,
		})
	}

	if full || len(files) > 0 {
		report.Files, report.FilesFailed = p.restoreFiles(filesDir, bound, files, saved, opts.DryRun)
	}
	report.Saved = savedIfAny(saved)
	if opts.DryRun || opts.At != "" {
		return report, nil
	}
	commit := p.head(ctx)
	return report, p.update(func(r *Record) {
		switch {
		case full:
			r.Pending = report.held(heldIn(nil), true, true).pending(report.From)
			r.Restored = &Restored{From: report.From, Commit: commit, At: p.Now()}
			r.On, r.OnAt = "restored from "+report.From+" "+p.Now().Format("2006-01-02 15:04"), p.Now()
		case r.Pending != nil && r.Pending.From == report.From:
			r.Pending = report.held(heldIn(r.Pending), false, files != nil).pending(report.From)
		}
	})
}

// held is what h holds once this restore's done: what it restored taken
// off; what it couldn't, of what h held, or, every, of all it tried, put on
// as it found it, but an app to open that's running, open still, as it
// stays to be quit; and, when it tried them (files), the settings files that
// failed in place of those held.
func (r RestoreReport) held(h held, every, files bool) held {
	for _, d := range r.Restored {
		h.drop(d)
	}
	for d, why := range r.Waiting {
		app, toOpen := h.open[d]
		switch {
		case toOpen && running(why):
			h.toOpen(d, app, why)
		case every || h.domains[d]:
			h.hold(d, why)
		}
	}
	for d, why := range r.ToOpen {
		if every || h.domains[d] {
			h.toOpen(d, r.Apps[d], why)
		}
	}
	for d, why := range r.Failed {
		if every || h.domains[d] {
			h.fail(d, why)
		}
	}
	if files {
		for f := range h.files {
			h.drop(f)
		}
		for f, why := range r.FilesFailed {
			h.failFile(f, why)
		}
	}
	return h
}

// restoring is how a domain is restored: from its stored copy, the live
// settings saved first in saved; open, whether its app may be opened once
// for its settings folder; dryRun, only saying it would be. containers
// are what making each app's container came to, by bundle id, in this
// restore, so an app with several domains is opened once.
type restoring struct {
	stored, saved string
	open, dryRun  bool
	containers    map[string]error
}

// restoreOwned restores domain, whose apps are owners, of those installed,
// into report: waiting while one's running; its app to open, or waiting,
// when its settings folder isn't there; failed, as restoreDomain fails;
// else restored.
func (p *Prefs) restoreOwned(ctx context.Context, report *RestoreReport, domain string, owners []string, installed map[string]string, how restoring) {
	app := func(id string) App { return App{ID: id, Name: appName(installed[id])} }
	if i := slices.IndexFunc(owners, func(id string) bool { return p.running(ctx, id) }); i >= 0 {
		report.Waiting[domain], report.Apps[domain] = app(owners[i]).Name+" is running", app(owners[i])
		return
	}
	if how.dryRun {
		report.Restored = append(report.Restored, domain)
		return
	}
	for _, id := range owners {
		err, made := how.containers[id]
		if !made {
			err = p.ensureContainer(ctx, id, installed[id], how.open)
			how.containers[id] = err
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, errDidntQuit):
			report.Waiting[domain] = err.Error()
		default:
			report.ToOpen[domain] = err.Error()
		}
		report.Apps[domain] = app(id)
		return
	}
	if err := p.restoreDomain(ctx, how.stored, domain, how.saved); err != nil {
		report.Failed[domain] = err.Error()
		if len(owners) > 0 {
			report.Apps[domain] = app(owners[0])
		}
		return
	}
	report.Restored = append(report.Restored, domain)
}

// savedIfAny is saved when a restore saved anything there, else "".
func savedIfAny(saved string) string {
	if _, err := os.Stat(saved); err == nil {
		return saved
	}
	return ""
}

// machineBound are the settings left out of a restore from store, the
// folder of the Mac from: none when it's this Mac's own folder, owned by
// this hardware; [prefs machine-bound] from another Mac's, or onto other
// hardware (a replacement Mac given the same name).
func (p *Prefs) machineBound(ctx context.Context, from, store string) ([]string, error) {
	if from != p.Mac {
		return p.Lists.MachineBound, nil
	}
	owner, owned, err := readOwner(store)
	if err != nil || !owned {
		return nil, err
	}
	hardware, err := p.Hardware(ctx)
	if err != nil || hardware != owner.Hardware {
		return p.Lists.MachineBound, nil
	}
	return nil, nil
}

// restoreFiles puts back the stored settings files that differ from the
// live ones, saving each live one first, those bound to one Mac left out,
// and, given only, just those, by their paths from ~ (or /): how many it
// put back, and those that failed, each with why.
func (p *Prefs) restoreFiles(files string, bound []string, only map[string]bool, saved string, dryRun bool) (int, map[string]string) {
	var expanded []string
	for _, b := range bound {
		expanded = append(expanded, p.expand(b))
	}
	n, failed := 0, map[string]string{}
	for _, stored := range storedFiles(files) {
		target := p.livePath(files, stored)
		name := p.tilde(target)
		if matches(target, expanded) || only != nil && !only[name] {
			continue
		}
		back, err := p.restoreFile(files, stored, target, saved, dryRun)
		switch {
		case err != nil:
			failed[name] = err.Error()
		case back:
			n++
		}
	}
	return n, failed
}

// restoreFile puts the stored settings file back at target, unless it's the
// same there already, the live one saved first: whether it did, or would.
func (p *Prefs) restoreFile(files, stored, target, saved string, dryRun bool) (bool, error) {
	want, err := os.ReadFile(stored)
	if err != nil {
		return false, errors.New("its saved copy couldn't be read")
	}
	current, err := os.ReadFile(target)
	if err == nil && bytes.Equal(current, want) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	mode := fs.FileMode(0o644)
	if err == nil {
		if info, statErr := os.Stat(target); statErr == nil {
			mode = info.Mode().Perm()
		}
		rel, _ := filepath.Rel(files, stored)
		if err := writeIfChanged(filepath.Join(saved, "files", rel), current); err != nil {
			return false, errors.New("its live copy couldn't be saved first")
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return false, errors.New("it couldn't be put in place")
	}
	tmp := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".kit")
	if err := os.WriteFile(tmp, want, mode); err != nil {
		return false, errors.New("it couldn't be put in place")
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return false, errors.New("it couldn't be put in place")
	}
	return true, nil
}
