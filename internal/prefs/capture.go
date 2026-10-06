package prefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// volatilePrefixes and volatileKeys are the keys that change by themselves:
// window geometry, list views' state, file dialogs' history, update checks'
// times. Stripped, a domain's capture stays the same until a setting
// changes. prefsync's, unchanged, so the store doesn't change at the switch.
var (
	volatilePrefixes = []string{"NSWindow Frame ", "NSSplitView Subview Frames ", "NSTableView ", "NSOutlineView Items ", "NSNavPanel", "NSNavLast"}
	volatileKeys     = []string{"SULastCheckTime", "SULastProfileSubmissionDate", "NSWindowLastPosition"}
)

// appleOrSystem reports whether a domain is Apple's own or a system
// daemon's: macOS settings declare Apple's; daemons have bare names.
func appleOrSystem(domain string) bool {
	if !strings.Contains(domain, ".") {
		return true
	}
	for _, prefix := range []string{"com.apple.", "group.com.apple.", "systemgroup.", ".GlobalPreferences", "org.cups."} {
		if strings.HasPrefix(domain, prefix) {
			return true
		}
	}
	return false
}

// stripVolatile is a domain's settings without the keys that change by
// themselves.
func stripVolatile(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if slices.Contains(volatileKeys, k) || slices.ContainsFunc(volatilePrefixes, func(p string) bool { return strings.HasPrefix(k, p) }) {
			continue
		}
		out[k] = v
	}
	return out
}

// liveDomains are the domains on the Mac to capture, sorted: none of
// Apple's or the system's, none denied.
func (p *Prefs) liveDomains(ctx context.Context) ([]string, error) {
	res, err := p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"domains"}})
	if err != nil {
		return nil, fmt.Errorf("list the preferences domains: %w", err)
	}
	var out []string
	for d := range strings.SplitSeq(strings.TrimSpace(string(res.Stdout)), ", ") {
		if d = strings.TrimSpace(d); d != "" && !appleOrSystem(d) && !matches(d, p.Lists.Deny) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out, nil
}

// export is a domain's live settings.
func (p *Prefs) export(ctx context.Context, domain string) (map[string]any, error) {
	res, err := p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"export", domain, "-"}})
	if err != nil {
		return nil, err
	}
	v, err := plist.Decode(res.Stdout)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a dictionary")
	}
	return m, nil
}

// normalised is a domain's settings as the store keeps them: the keys that
// change by themselves stripped, sorted XML; nil when nothing's left, so an
// empty capture never overwrites a good one.
func normalised(m map[string]any) ([]byte, error) {
	m = stripVolatile(m)
	if len(m) == 0 {
		return nil, nil
	}
	return plist.Encode(m)
}

// settingsFiles are the files [prefs files] names: each file, and every file
// under each folder (.DS_Store left out); an entry with *, ? or [ is a glob.
func (p *Prefs) settingsFiles() []string {
	var out []string
	for _, entry := range p.Lists.Files {
		expanded := p.expand(entry)
		roots := []string{expanded}
		if hasMagic(expanded) {
			roots = glob(expanded)
		}
		for _, root := range roots {
			info, err := os.Stat(root)
			switch {
			case err != nil:
				continue
			case !info.IsDir():
				out = append(out, root)
			default:
				out = append(out, walk(root)...)
			}
		}
	}
	return out
}

// walk is every file under dir, a folder's in name order, as Python's
// os.walk finds them: linked folders not followed, linked files included.
func walk(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files, dirs []string
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				continue
			}
		}
		switch {
		case isDir:
			dirs = append(dirs, path)
		case e.Name() != ".DS_Store":
			files = append(files, path)
		}
	}
	for _, d := range dirs {
		files = append(files, walk(d)...)
	}
	return files
}

// storedPath is where in the store's files a settings file goes: its path
// under the home folder, or under _root for one outside it.
func (p *Prefs) storedPath(files, source string) string {
	if rel, err := filepath.Rel(p.Home, source); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		return filepath.Join(files, rel)
	}
	return filepath.Join(files, "_root", strings.TrimPrefix(source, "/"))
}

// livePath is where a stored settings file goes back to.
func (p *Prefs) livePath(files, stored string) string {
	rel, _ := filepath.Rel(files, stored)
	if after, ok := strings.CutPrefix(rel, "_root/"); ok {
		return "/" + after
	}
	return filepath.Join(p.Home, rel)
}

// storedFiles are the files in the store's files folder, sorted, those
// whose names start with a dot left out (prefsync's rule).
func storedFiles(files string) []string {
	var out []string
	_ = filepath.WalkDir(files, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			out = append(out, path)
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// pruneEmpty removes the folders left empty under root, deepest first, but
// not root itself.
func pruneEmpty(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	for _, dir := range slices.Backward(dirs) {
		_ = os.Remove(dir)
	}
}

// change is an item a capture wrote: new or changed, and what it is, a
// domain's name or a file's path from ~ or /.
type change struct {
	what, name string
}

// Report is what a capture did.
type Report struct {
	Domains, Files int
	changes        []change
	Removed        []string
	// Pending counts the domains left alone, waiting to be restored.
	Pending int
	// Errors are what couldn't be saved, each with why: kept as stored.
	Errors []string
	// Claimed is whether this capture claimed the folder for this Mac.
	Claimed bool
	// Committed is whether anything changed, so a commit was made.
	Committed bool
	// PushError is why the push failed: the commit waits for the next one.
	PushError string
}

// Changes are the items written, each "new: <name>" or "changed: <name>".
func (r Report) Changes() []string {
	var out []string
	for _, c := range r.changes {
		out = append(out, c.what+": "+c.name)
	}
	return out
}

// Summary is the capture in a line, as prefsync's: "57 domains, 2 changed;
// 1056 files, 0 changed".
func (r Report) Summary() string {
	counts := func(isFile bool) string {
		changed, isNew := 0, 0
		for _, c := range r.changes {
			if (strings.HasPrefix(c.name, "~") || strings.HasPrefix(c.name, "/")) == isFile {
				changed++
				if c.what == "new" {
					isNew++
				}
			}
		}
		s := fmt.Sprintf("%d changed", changed)
		if isNew > 0 {
			s += fmt.Sprintf(" (%d new)", isNew)
		}
		return s
	}
	s := fmt.Sprintf("%s, %s; %s, %s", plural(r.Domains, "domain"), counts(false), plural(r.Files, "file"), counts(true))
	if len(r.Removed) > 0 {
		s += fmt.Sprintf("; %d removed", len(r.Removed))
	}
	if r.Pending > 0 {
		s += fmt.Sprintf("; %d pending restore", r.Pending)
	}
	if len(r.Errors) > 0 {
		s += fmt.Sprintf("; %d errors", len(r.Errors))
	}
	return s
}

// message is a capture's commit message: a line counting the changes, then
// what they were, up to 60 lines.
func (r Report) message(when string) string {
	var changed, isNew []string
	for _, c := range r.changes {
		if c.what == "new" {
			isNew = append(isNew, c.name)
		} else {
			changed = append(changed, c.name)
		}
	}
	var counts []string
	if len(changed) > 0 {
		counts = append(counts, fmt.Sprintf("%d changed", len(changed)))
	}
	if len(isNew) > 0 {
		counts = append(counts, fmt.Sprintf("%d new", len(isNew)))
	}
	if len(r.Removed) > 0 {
		counts = append(counts, fmt.Sprintf("%d removed", len(r.Removed)))
	}
	head := "Capture " + when + ": no changes"
	if len(counts) > 0 {
		head = "Capture " + when + ": " + strings.Join(counts, ", ")
	}
	var lines []string
	for _, n := range isNew {
		lines = append(lines, "new: "+n)
	}
	for _, n := range changed {
		lines = append(lines, "changed: "+n)
	}
	for _, n := range r.Removed {
		lines = append(lines, "removed: "+n)
	}
	if len(lines) > 60 {
		lines = append(lines[:60], fmt.Sprintf("... and %d more", len(lines)-60))
	}
	if r.Claimed {
		lines = append(lines, "claimed: the folder, for this Mac's hardware")
	}
	if len(lines) == 0 {
		return head
	}
	return head + "\n\n" + strings.Join(lines, "\n")
}

// ErrPaused is capture refusing to run before it's switched on for this Mac.
var ErrPaused = errors.New("paused: capture isn't switched on for this Mac: kit prefs restore, or kit prefs start-fresh")

// NotOwnerError is capture refusing a folder another Mac owns.
type NotOwnerError struct {
	Mac   string
	Owner Owner
}

func (e NotOwnerError) Error() string {
	since := ""
	if e.Owner.Since != "" {
		since = ", since " + e.Owner.Since
	}
	model := ""
	if e.Owner.Model != "" {
		model = ", a " + e.Owner.Model
	}
	return fmt.Sprintf("the store's %s folder belongs to another Mac (hardware %s%s%s): this one doesn't capture into it until it takes ownership", e.Mac, e.Owner.Hardware, model, since)
}

// gitignore is a Mac's folder's ignore file: Finder's litter and capture's
// files half written.
const gitignore = ".DS_Store\n.*.tmp\n"

// Capture saves the Mac's settings into its folder in the store, writing
// only what changed and removing what's gone from the Mac, then commits and
// pushes. It needs Full Disk Access, capture switched on, and this Mac to
// own the folder (claiming it when no Mac has). A domain or file that
// fails to read is kept as stored, and the rest go on; domains waiting to
// be restored are left alone.
func (p *Prefs) Capture(ctx context.Context) (Report, error) {
	var report Report
	rec, err := p.Load()
	if err != nil {
		return report, err
	}
	if rec.On == "" {
		return report, ErrPaused
	}
	if !p.HasFullDiskAccess() {
		return report, errNoAccess
	}
	if err := p.ensureClone(ctx); err != nil {
		return report, err
	}
	hardware, err := p.Hardware(ctx)
	if err != nil {
		return report, err
	}
	// Pulled first, so a folder another Mac has taken over is seen as its.
	// Offline, capture carries on, its commit waiting to be pushed.
	_ = p.pull(ctx)
	store := filepath.Join(p.Clone, p.Mac)
	owner, owned, err := readOwner(store)
	if err != nil {
		return report, err
	}
	if owned && owner.Hardware != hardware {
		refused := NotOwnerError{Mac: p.Mac, Owner: owner}
		_ = p.update(func(r *Record) { r.NotOwner = refused.Error() })
		return report, refused
	}
	repo := gitrepo.Repo{Dir: p.Clone, Run: p.Run}
	if !owned {
		if err := p.claim(ctx, store, hardware); err != nil {
			return report, err
		}
		report.Claimed = true
	}
	if err := writeIfChanged(filepath.Join(store, ".gitignore"), []byte(gitignore)); err != nil {
		return report, err
	}

	domainsDir, filesDir := filepath.Join(store, "domains"), filepath.Join(store, "files")
	protected := map[string]bool{}
	if rec.Pending != nil && rec.Pending.From == p.Mac {
		for _, d := range rec.Pending.Domains {
			protected[d] = true
		}
	}
	report.Pending = len(protected)
	// What stays stored: domains captured now, ones that failed to export
	// (still there, unreadable this time) and pending ones. A domain that
	// exports empty counts as gone: macOS lists a deleted domain for a
	// while, and one holding only volatile keys has nothing worth keeping.
	keep := maps.Clone(protected)
	live, err := p.liveDomains(ctx)
	if err != nil {
		return report, err
	}
	for _, domain := range live {
		if protected[domain] {
			continue
		}
		m, err := p.export(ctx, domain)
		var content []byte
		if err == nil {
			content, err = normalised(m)
		}
		if err != nil {
			report.Errors = append(report.Errors, domain+": "+err.Error())
			keep[domain] = true
			continue
		}
		if content == nil {
			continue
		}
		report.Domains++
		keep[domain] = true
		what, err := write(filepath.Join(domainsDir, domain+".plist"), content)
		if err != nil {
			report.Errors = append(report.Errors, domain+": "+err.Error())
			continue
		}
		if what != "" {
			report.changes = append(report.changes, change{what, domain})
		}
	}

	sources := p.settingsFiles()
	wanted := map[string]bool{}
	for _, source := range sources {
		stored := p.storedPath(filesDir, source)
		wanted[stored] = true
		data, err := os.ReadFile(source)
		var what string
		if err == nil {
			what, err = write(stored, data)
		}
		if err != nil {
			report.Errors = append(report.Errors, source+": "+err.Error())
			continue
		}
		report.Files++
		if what != "" {
			report.changes = append(report.changes, change{what, p.tilde(source)})
		}
	}

	// The mirror: what's stored but gone from the Mac, or no longer saved.
	var stale []string
	if entries, err := os.ReadDir(domainsDir); err == nil {
		for _, e := range entries {
			if d, ok := strings.CutSuffix(e.Name(), ".plist"); ok && !keep[d] {
				stale = append(stale, filepath.Join(domainsDir, e.Name()))
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return report, err
	}
	for _, f := range storedFiles(filesDir) {
		if !wanted[f] {
			stale = append(stale, f)
		}
	}
	for _, path := range stale {
		if err := os.Remove(path); err != nil {
			report.Errors = append(report.Errors, path+": "+err.Error())
			continue
		}
		rel, _ := filepath.Rel(store, path)
		report.Removed = append(report.Removed, rel)
	}
	pruneEmpty(filesDir)

	committed, err := repo.Commit(ctx, report.message(p.Now().Format("2006-01-02 15:04")), p.Mac)
	if err != nil {
		report.Errors = append(report.Errors, "history: "+err.Error())
	}
	report.Committed = committed
	report.PushError = p.push(ctx, repo)
	return report, p.update(func(r *Record) {
		r.Captured, r.Summary, r.Errors, r.NotOwner = p.Now(), report.Summary(), len(report.Errors), ""
		if report.PushError == "" {
			r.Unpushed, r.PushError = time.Time{}, ""
			return
		}
		if r.Unpushed.IsZero() {
			r.Unpushed = p.Now()
		}
		r.PushError = report.PushError
	})
}

// tilde is a path with the home folder shown as ~.
func (p *Prefs) tilde(path string) string {
	if rest, ok := strings.CutPrefix(path, p.Home+"/"); ok {
		return "~/" + rest
	}
	return path
}
