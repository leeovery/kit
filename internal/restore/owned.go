package restore

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/exclusion"
	"github.com/leeovery/kit/internal/linked"
)

// Owned are the paths in the home folder that are kit's, each from ~: what
// a restore never carries, and never counts as a path's holding anything,
// as kit puts it in place itself.
type Owned struct {
	// Linked are the files kit links: files alone, as folders are never
	// linked whole.
	Linked []string
	// Prefs are what prefs saves, while it's on for the Mac: [prefs files],
	// each a file, a folder (everything in it) or a glob.
	Prefs []string
	// Installed are what kinds install into the home folder (kind.Homed).
	Installed []string
	// Excluded are what the backups leave out: [backup exclusions], each a
	// path or a glob.
	Excluded []string
	// Own are kit's own folders: its config repository, state, data and
	// logs.
	Own []string
}

// Mac is a Mac whose owned paths ReadOwned reads, with what they're read
// from.
type Mac struct {
	Config *config.Config
	// Name is the Mac's name, Home its home folder, and Dirs kit's own
	// folders on it.
	Name string
	Home string
	Dirs config.Dirs
	// Linked are the files the config repository links on it.
	Linked *linked.Files
	// Prefs is whether prefs is on for it, as its [features] say.
	Prefs bool
	// Kinds are its kinds: those installing into the home folder
	// (kind.Homed) say what.
	Kinds []kind.Kind
}

// ReadOwned reads what's kit's in m's home folder. What's outside it is no
// restore's, so it's left out.
func ReadOwned(ctx context.Context, m Mac) (Owned, error) {
	var o Owned
	links, err := m.Linked.Declared(ctx)
	if err != nil {
		return o, err
	}
	for _, l := range links {
		o.Linked = append(o.Linked, l.Name)
	}
	if m.Prefs {
		if o.Prefs, err = m.names(config.PrefsFilesKind); err != nil {
			return o, err
		}
	}
	for _, k := range m.Kinds {
		h, ok := k.(kind.Homed)
		if !ok {
			continue
		}
		list, err := m.Config.List(k.Name(), m.Name)
		if err != nil {
			return o, err
		}
		o.Installed = append(o.Installed, m.inHome(h.HomePaths(list.On()))...)
	}
	if o.Excluded, err = m.names(exclusion.Descriptor.Name); err != nil {
		return o, err
	}
	o.Own = m.inHome([]string{m.Dirs.Config, m.Dirs.State, m.Dirs.Data, m.Dirs.Logs})
	return o, nil
}

// names are the names kind's sections declare for m, those in its home
// folder.
func (m Mac) names(kind string) ([]string, error) {
	list, err := m.Config.List(kind, m.Name)
	if err != nil {
		return nil, err
	}
	return m.inHome(list.Names()), nil
}

// inHome are those of paths in m's home folder, each from ~.
func (m Mac) inHome(paths []string) []string {
	var out []string
	for _, p := range paths {
		if rest, ok := strings.CutPrefix(p, m.Home+"/"); ok && m.Home != "" {
			p = "~/" + rest
		}
		if strings.HasPrefix(p, "~/") {
			out = append(out, p)
		}
	}
	return out
}

// owner is a path that's kit's, from ~ and cleaned, a glob when glob, else
// a path as it is; and why it's kit's, for a line naming it.
type owner struct {
	path string
	glob bool
	why  string
}

// owners are o's paths, each with why it's kit's: [prefs files] and
// [backup exclusions] hold globs, their own readers expanding them; the
// rest are paths as they are.
func (o Owned) owners() []owner {
	var out []owner
	add := func(paths []string, globs bool, why string) {
		for _, p := range paths {
			rest, ok := strings.CutPrefix(p, "~/")
			rest = path.Clean(rest)
			if !ok || rest == "." || rest == ".." || strings.HasPrefix(rest, "../") {
				continue
			}
			p = "~/" + rest
			out = append(out, owner{path: p, glob: globs && hasMagic(p), why: fmt.Sprintf(why, p)})
		}
	}
	add(o.Linked, false, "kit links %s")
	add(o.Prefs, true, "prefs saves %s")
	add(o.Installed, false, "kit installs %s")
	add(o.Excluded, true, "the backups leave out %s")
	add(o.Own, false, "kit keeps its own files in %s")
	return out
}

// under is the rule, anchored at p, a line's path, leaving out what o names
// in it: "" when o names nothing in it. covers is whether o is p or holds
// it, so the whole of p is kit's.
func (o owner) under(p string) (text string, covers bool) {
	lineParts := strings.Split(strings.TrimPrefix(p, "~/"), "/")
	parts := strings.Split(strings.TrimPrefix(o.path, "~/"), "/")
	for i := range min(len(lineParts), len(parts)) {
		if !o.part(parts[i], lineParts[i]) {
			return "", false
		}
	}
	if len(parts) <= len(lineParts) {
		return "", true
	}
	rest := strings.Join(parts[len(lineParts):], "/")
	if o.glob {
		return "/" + oneStar(rest), false
	}
	return "/" + escape(rest), false
}

// part reports whether pattern, a part of o's path, matches name, the same
// part of a line's: a glob's part as its readers match one, alone, where a
// * never meets a /.
func (o owner) part(pattern, name string) bool {
	if o.glob && hasMagic(pattern) {
		return wildmatch(oneStar(pattern), name)
	}
	return pattern == name
}

// hasMagic reports whether p holds what rsync and kit's globs take as a
// pattern: *, ? or [.
func hasMagic(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

// oneStar is pattern with each run of * a single one: a glob kit reads
// matches within a part of a path, where rsync's ** matches across them.
func oneStar(pattern string) string {
	for strings.Contains(pattern, "**") {
		pattern = strings.ReplaceAll(pattern, "**", "*")
	}
	return pattern
}

// escape is p, a path as it is, as rsync matches it as itself: when it holds
// *, ? or [, which rsync would take as a pattern, those and \ escaped.
func escape(p string) string {
	if !hasMagic(p) {
		return p
	}
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(p)
}
