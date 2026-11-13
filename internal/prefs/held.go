package prefs

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// App is an app as kit tells of it: its bundle id, and its name, as its
// bundle has it.
type App struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// held is what waits to be restored, as a restore changes it: domains, and
// of them those whose apps the person opens once (open) and those whose
// restore failed otherwise (failed); settings files, whose restore failed;
// and why each waits.
type held struct {
	domains, files, failed map[string]bool
	open                   map[string]App
	why                    map[string]string
}

// heldIn is what p holds: nothing, for none.
func heldIn(p *Pending) held {
	h := held{domains: map[string]bool{}, files: map[string]bool{}, failed: map[string]bool{}, open: map[string]App{}, why: map[string]string{}}
	if p == nil {
		return h
	}
	for _, d := range p.Domains {
		h.domains[d] = true
	}
	for _, f := range p.Files {
		h.files[f] = true
	}
	for _, d := range p.Failed {
		h.failed[d] = true
	}
	maps.Copy(h.open, p.Open)
	maps.Copy(h.why, p.Why)
	return h
}

// keptWhy is why a domain whose app isn't installed is held: nothing to
// wait for, as its app may never come, but kept, so capture leaves the
// saved copy be.
const keptWhy = "kept till its app is installed"

// running reports whether why is that the app is running.
func running(why string) bool {
	return strings.HasSuffix(why, " is running")
}

// anyToTry reports whether h holds anything an unattended pass might bring
// back: a domain that didn't fail otherwise, and, kept till its app is
// installed, has an app now (owned).
func (h held) anyToTry(owned func(domain string) bool) bool {
	for d := range h.domains {
		if !h.failed[d] && (h.why[d] != keptWhy || owned(d)) {
			return true
		}
	}
	return false
}

// Kept counts the domains p keeps till their apps are installed.
func (p *Pending) Kept() int {
	n := 0
	for _, d := range p.Domains {
		if p.Why[d] == keptWhy {
			n++
		}
	}
	return n
}

// Running reports whether why, a domain's left waiting for, is that its
// app is running, rather than a windowless helper's folder not come yet.
func Running(why string) bool {
	return running(why)
}

// Failures counts what rec holds that failed otherwise, domains and
// settings files, which only kit prefs restore --pending, by hand, tries
// again.
func (p *Prefs) Failures(rec Record) (domains, files int) {
	h := heldIn(rec.Pending)
	return len(h.failed), len(h.files)
}

// drop takes name, a domain or a file, off.
func (h held) drop(name string) {
	delete(h.domains, name)
	delete(h.files, name)
	delete(h.failed, name)
	delete(h.open, name)
	delete(h.why, name)
}

// hold puts domain on, as waiting for its app, or kept till it's
// installed, with why.
func (h held) hold(domain, why string) {
	h.drop(domain)
	h.domains[domain], h.why[domain] = true, why
}

// toOpen puts domain on, its app, app, for the person to open once: why
// kept as it was, if it was on for that already.
func (h held) toOpen(domain string, app App, why string) {
	if _, ok := h.open[domain]; ok {
		why = h.why[domain]
	}
	h.hold(domain, why)
	h.open[domain] = app
}

// fail puts domain on, as failed otherwise.
func (h held) fail(domain, why string) {
	h.hold(domain, why)
	h.failed[domain] = true
}

// failFile puts a settings file on, as failed.
func (h held) failFile(file, why string) {
	h.drop(file)
	h.files[file], h.why[file] = true, why
}

// pending is what's held, from the Mac from: nil when nothing is.
func (h held) pending(from string) *Pending {
	if len(h.domains)+len(h.files) == 0 {
		return nil
	}
	p := &Pending{From: from, Domains: slices.Sorted(maps.Keys(h.domains)), Files: slices.Sorted(maps.Keys(h.files)), Failed: slices.Sorted(maps.Keys(h.failed))}
	if len(h.open) > 0 {
		p.Open = maps.Clone(h.open)
	}
	if len(h.why) > 0 {
		p.Why = maps.Clone(h.why)
	}
	return p
}

// ToOpen are the apps the person is to open once and quit, as rec holds
// them, till their settings are restored: those installed and sandboxed,
// each once, by name. One held that isn't sandboxed, as kit once took an
// app declaring its sandbox off to be, never gets a folder: its settings
// come back as any app's, as the next pass tries them. Nor is a windowless
// helper, held so before kit opened them itself: the next pass opens it.
func (p *Prefs) ToOpen(ctx context.Context, rec Record) []App {
	if rec.Pending == nil || len(rec.Pending.Open) == 0 {
		return nil
	}
	installed := p.installedApps()
	seen := map[string]bool{}
	var out []App
	for _, app := range rec.Pending.Open {
		path, ok := installed[app.ID]
		if seen[app.ID] || !ok || windowless(path) || !p.sandboxed(ctx, path) {
			continue
		}
		seen[app.ID] = true
		out = append(out, app)
	}
	slices.SortFunc(out, func(a, b App) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// Opened reports why the sandboxed app of a bundle id, called name, isn't
// ready for its settings: still running, or no settings folder yet; nil
// once it's been opened and quit.
func (p *Prefs) Opened(ctx context.Context, id, name string) error {
	switch {
	case p.running(ctx, id):
		return fmt.Errorf("%s is still running", name)
	case !p.hasFolder(id):
		return fmt.Errorf("%s has no settings folder yet", name)
	}
	return nil
}

// Open opens the app of a bundle id, in view, for the person to quit once
// macOS has made its settings folder. What opening says, the run's log has.
func (p *Prefs) Open(ctx context.Context, id string) {
	_, _ = p.Run.Run(ctx, runner.Command{Name: "open", Args: []string{"-b", id}})
}

// hasFolder reports whether the sandboxed app of a bundle id has its
// settings folder, its container.
func (p *Prefs) hasFolder(id string) bool {
	_, err := os.Stat(filepath.Join(p.Home, "Library/Containers", id))
	return err == nil
}
