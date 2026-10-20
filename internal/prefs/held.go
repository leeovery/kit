package prefs

import (
	"cmp"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// heldIn is what p holds: nothing, for none. A record from before kit
// sorted what it held has its failed domains among those waiting: those
// whose why isn't a wait's.
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
	if p.Failed == nil && p.Open == nil {
		for d := range h.domains {
			if why := h.why[d]; why != "" && !waits(why) {
				h.failed[d] = true
			}
		}
	}
	return h
}

// waits reports whether why is a wait's: not installed, running, or not
// quitting once opened.
func waits(why string) bool {
	return why == "not installed" || strings.HasSuffix(why, " is running") || why == errDidntQuit.Error()
}

// anyToTry reports whether h holds anything an unattended pass might bring
// back: a domain that didn't fail otherwise.
func (h held) anyToTry() bool {
	for d := range h.domains {
		if !h.failed[d] {
			return true
		}
	}
	return false
}

// Failures counts what rec holds that failed otherwise, domains and
// settings files, which only kit prefs restore --pending, by hand, tries
// again.
func (p *Prefs) Failures(rec Record) int {
	h := heldIn(rec.Pending)
	return len(h.failed) + len(h.files)
}

// drop takes name, a domain or a file, off.
func (h held) drop(name string) {
	delete(h.domains, name)
	delete(h.files, name)
	delete(h.failed, name)
	delete(h.open, name)
	delete(h.why, name)
}

// hold puts domain on, as waiting for its app, with why.
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
// them: those installed that have no settings folder yet, or are still
// running, each once, by name.
func (p *Prefs) ToOpen(ctx context.Context, rec Record) []App {
	if rec.Pending == nil || len(rec.Pending.Open) == 0 {
		return nil
	}
	installed := p.installedApps()
	seen := map[string]bool{}
	var out []App
	for _, app := range rec.Pending.Open {
		if _, ok := installed[app.ID]; seen[app.ID] || !ok {
			continue
		}
		seen[app.ID] = true
		if !p.hasFolder(app.ID) || p.running(ctx, app.ID) {
			out = append(out, app)
		}
	}
	slices.SortFunc(out, func(a, b App) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// hasFolder reports whether the sandboxed app of a bundle id has its
// settings folder, its container.
func (p *Prefs) hasFolder(id string) bool {
	_, err := os.Stat(filepath.Join(p.Home, "Library/Containers", id))
	return err == nil
}
