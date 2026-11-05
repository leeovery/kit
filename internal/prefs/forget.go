package prefs

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// SavedFor are the domains this Mac's folder in the store's clone holds
// that belong to the installed app of a bundle id, as a restore finds
// whose a domain is, sorted.
func (p *Prefs) SavedFor(id string) []string {
	entries, _ := os.ReadDir(filepath.Join(p.Clone, p.Mac, "domains"))
	installed := p.installedApps()
	var owned []string
	for _, e := range entries {
		if d, ok := strings.CutSuffix(e.Name(), ".plist"); ok && slices.Contains(p.owningApps(d, installed), id) {
			owned = append(owned, d)
		}
	}
	slices.Sort(owned)
	return owned
}

// Forget deletes domains from the Mac, through cfprefsd, which keeps an
// app's settings once their file has gone, and takes them off what's held
// to be restored: the next capture drops them from the store, as it
// mirrors the Mac, the store's history keeping them.
func (p *Prefs) Forget(ctx context.Context, domains []string) error {
	for _, d := range domains {
		// One that's gone already is what's wanted.
		_, _ = p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"delete", d}})
	}
	rec, err := p.Load()
	if err != nil || rec.Pending == nil || !slices.ContainsFunc(domains, func(d string) bool { return slices.Contains(rec.Pending.Domains, d) }) {
		return err
	}
	return p.update(func(r *Record) {
		if r.Pending == nil {
			return
		}
		h := heldIn(r.Pending)
		for _, d := range domains {
			h.drop(d)
		}
		r.Pending = h.pending(r.Pending.From)
	})
}
