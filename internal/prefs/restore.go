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

	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// RestoreOptions are what to restore: everything from this Mac's folder
// unless From names another Mac's; Domains, just those; Pending, the
// domains waiting; At, the store as it was then; As, one domain into a
// scratch domain, testing a restore without touching the app; DryRun, only
// say what would be done.
type RestoreOptions struct {
	From    string
	Domains []string
	Pending bool
	At      string
	As      string
	DryRun  bool
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
	// or running.
	Waiting map[string]string
	// Failed are the domains that failed, each with why.
	Failed map[string]string
	// Saved is where the live settings replaced were saved.
	Saved  string
	DryRun bool
	// As is the scratch domain a domain was restored into.
	As string
}

// Summary is the restore in a line, as prefsync's.
func (r RestoreReport) Summary() string {
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
	if len(r.Failed) > 0 {
		s += fmt.Sprintf("; %d failed", len(r.Failed))
	}
	if r.Saved != "" {
		s += "; previous settings saved in " + r.Saved
	}
	return s
}

// helperDirs are where apps keep helper apps with their own preferences.
var helperDirs = []string{"Contents/MacOS", "Contents/Library/LoginItems", "Contents/Helpers"}

// installedApps are the apps installed, by bundle id: in each app folder and
// a folder below, and the helper apps inside them (Keyboard Maestro's
// Engine, login items). The first found of a bundle id is kept.
func (p *Prefs) installedApps() map[string]string {
	apps := map[string]string{}
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
				if id := bundleID(b); id != "" {
					if _, seen := apps[id]; !seen {
						apps[id] = b
					}
				}
			}
		}
	}
	return apps
}

// bundleID is an app's bundle id, from its Info.plist: "" when it doesn't
// say.
func bundleID(app string) string {
	data, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	v, err := plist.Decode(data)
	if err != nil {
		return ""
	}
	m, _ := v.(map[string]any)
	id, _ := m["CFBundleIdentifier"].(string)
	return id
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

// ensureContainer makes a sandboxed app's container, where it reads its
// settings, which exists only once it has run: launched hidden once, then
// quit. One that won't quit isn't restored, as it would write its defaults
// back when it did.
func (p *Prefs) ensureContainer(ctx context.Context, id, app string) error {
	container := filepath.Join(p.Home, "Library/Containers", id)
	if _, err := os.Stat(container); err == nil {
		return nil
	}
	res, _ := p.Run.Run(ctx, runner.Command{Name: "codesign", Args: []string{"-d", "--entitlements", "-", "--xml", app}})
	if !bytes.Contains(res.Stdout, []byte("com.apple.security.app-sandbox")) {
		return nil
	}
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
		return errors.New("launched once to make its container, but it didn't quit")
	}
	if _, err := os.Stat(container); err != nil {
		return errors.New("its container didn't appear after launching it once")
	}
	return nil
}

// restoreDomain replaces a domain's live settings (target's, when testing
// into a scratch domain) with its stored copy, through cfprefsd: the stored
// copy checked first, the live settings saved, then read back and compared.
func (p *Prefs) restoreDomain(ctx context.Context, stored, target, saved string) error {
	data, err := os.ReadFile(stored)
	if err != nil {
		return err
	}
	v, err := plist.Decode(data)
	wanted, _ := v.(map[string]any)
	if err != nil || len(wanted) == 0 {
		return errors.New("its stored copy is empty or not a dictionary")
	}
	if current, err := p.export(ctx, target); err == nil && len(current) > 0 {
		backup, err := plist.Encode(current)
		if err != nil {
			return fmt.Errorf("save its live settings: %w", err)
		}
		if err := writeIfChanged(filepath.Join(saved, target+".plist"), backup); err != nil {
			return fmt.Errorf("save its live settings: %w", err)
		}
	}
	// Import merges: deleted first, for an exact restore.
	_, _ = p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"delete", target}})
	if _, err := p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"import", target, stored}}); err != nil {
		return fmt.Errorf("import: %w", err)
	}
	back, err := p.export(ctx, target)
	if err != nil || !plist.Equal(stripVolatile(back), stripVolatile(wanted)) {
		return errors.New("imported, but reads back different (settings may be in the wrong place)")
	}
	return nil
}

// Restore puts settings from the store back on the Mac: every stored domain
// whose app is installed and closed (the rest wait, pending), then the
// settings files that differ, each live copy saved first. A full restore
// with no failures switches capture on. Never quits an app. From another
// Mac's folder, or onto other hardware, the settings bound to one Mac are
// left out.
func (p *Prefs) Restore(ctx context.Context, opts RestoreOptions) (RestoreReport, error) {
	report := RestoreReport{From: opts.From, DryRun: opts.DryRun, As: opts.As, Waiting: map[string]string{}, Failed: map[string]string{}}
	if report.From == "" {
		report.From = p.Mac
	}
	rec, err := p.Load()
	if err != nil {
		return report, err
	}
	named := slices.Clone(opts.Domains)
	if opts.Pending {
		if len(named) > 0 || opts.As != "" || opts.At != "" {
			return report, errors.New("--pending restores what's waiting: no domains, --as or --at with it")
		}
		if rec.Pending == nil || len(rec.Pending.Domains) == 0 {
			return report, nil
		}
		report.From, named = rec.Pending.From, slices.Clone(rec.Pending.Domains)
	}
	if !opts.DryRun && !p.HasFullDiskAccess() {
		return report, errNoAccess
	}
	if err := p.ensureClone(ctx); err != nil {
		return report, err
	}
	_ = p.pull(ctx)
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

	full := len(named) == 0
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

	apps := p.installedApps()
	for _, domain := range domains {
		if !slices.Contains(stored, domain) {
			report.Failed[domain] = "not in the store"
			continue
		}
		owners := p.owningApps(domain, apps)
		// A domain named by hand is restored even without an app; pending
		// ones wait until their app is installed.
		if len(owners) == 0 && (full || opts.Pending) {
			report.Waiting[domain] = "not installed"
			continue
		}
		if i := slices.IndexFunc(owners, func(id string) bool { return p.running(ctx, id) }); i >= 0 {
			report.Waiting[domain] = owners[i] + " is running"
			continue
		}
		if opts.DryRun {
			report.Restored = append(report.Restored, domain)
			continue
		}
		err := func() error {
			for _, id := range owners {
				if err := p.ensureContainer(ctx, id, apps[id]); err != nil {
					return err
				}
			}
			return p.restoreDomain(ctx, filepath.Join(domainsDir, domain+".plist"), domain, saved)
		}()
		if err != nil {
			report.Failed[domain] = err.Error()
			continue
		}
		report.Restored = append(report.Restored, domain)
	}

	if full {
		n, err := p.restoreFiles(filesDir, bound, saved, opts.DryRun)
		report.Files = n
		if err != nil {
			return report, err
		}
	}
	report.Saved = savedIfAny(saved)
	if opts.DryRun || opts.At != "" {
		return report, nil
	}
	commit := p.head(ctx)
	return report, p.update(func(r *Record) {
		// What's still waiting: after a full restore, what this one left;
		// otherwise what was waiting, less what was restored now.
		switch {
		case full && !opts.Pending:
			left := slices.Concat(slices.Collect(maps.Keys(report.Waiting)), slices.Collect(maps.Keys(report.Failed)))
			r.Pending = pendingOf(report.From, left)
		case r.Pending != nil && r.Pending.From == report.From:
			left := slices.DeleteFunc(slices.Clone(r.Pending.Domains), func(d string) bool { return slices.Contains(report.Restored, d) })
			r.Pending = pendingOf(report.From, left)
		}
		if full && !opts.Pending {
			r.Restored = &Restored{From: report.From, Commit: commit, At: p.Now()}
			if len(report.Failed) == 0 {
				r.On = "restored from " + report.From + " " + p.Now().Format("2006-01-02 15:04")
			}
		}
	})
}

// pendingOf is a pending list, nil when nothing's left.
func pendingOf(from string, domains []string) *Pending {
	if len(domains) == 0 {
		return nil
	}
	slices.Sort(domains)
	return &Pending{From: from, Domains: slices.Compact(domains)}
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
// live ones, saving each live one first, those bound to one Mac left out:
// how many.
func (p *Prefs) restoreFiles(files string, bound []string, saved string, dryRun bool) (int, error) {
	var expanded []string
	for _, b := range bound {
		expanded = append(expanded, p.expand(b))
	}
	n := 0
	for _, stored := range storedFiles(files) {
		target := p.livePath(files, stored)
		if matches(target, expanded) {
			continue
		}
		want, err := os.ReadFile(stored)
		if err != nil {
			return n, err
		}
		current, err := os.ReadFile(target)
		if err == nil && bytes.Equal(current, want) {
			continue
		}
		n++
		if dryRun {
			continue
		}
		mode := fs.FileMode(0o644)
		if err == nil {
			info, statErr := os.Stat(target)
			if statErr == nil {
				mode = info.Mode().Perm()
			}
			rel, _ := filepath.Rel(files, stored)
			if err := writeIfChanged(filepath.Join(saved, "files", rel), current); err != nil {
				return n, err
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return n, err
		}
		tmp := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".kit")
		if err := os.WriteFile(tmp, want, mode); err != nil {
			return n, err
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			return n, err
		}
	}
	return n, nil
}
