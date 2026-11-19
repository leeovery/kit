package snapshot

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/tilde"
)

// PathsLabel labels the folders a snapshot declares in the Mac's [paths].
const PathsLabel = "PATH directories"

// along is m as its kinds are looked at, and the part declaring the folders
// that takes, nil for none: a kind whose program Run doesn't find is looked
// for along Beyond, in order, and the first folder holding it is declared
// in the Mac's [paths], as kit path add declares it, those declared in
// Beyond's order; Run then finds it there (RunOn), as kit's later runs
// will.
func (m Mac) along() (Mac, *Part) {
	if m.RunOn == nil {
		return m, nil
	}
	deps := &kind.Deps{Run: m.Run, Home: m.Home, ConfigHome: m.ConfigHome, StateDir: m.Dirs.State, AppDirs: m.AppDirs}
	beyond := m.beyond()
	needed := make([]bool, len(beyond))
	for _, d := range kinds.All {
		if !snapshotted(d) {
			continue
		}
		program := d.New(deps).Program()
		if runner.Has(m.Run, program) {
			continue
		}
		if at := slices.IndexFunc(beyond, func(dir string) bool { return runner.Has(m.RunOn([]string{dir}), program) }); at >= 0 {
			needed[at] = true
		}
	}
	var dirs []string
	for i, dir := range beyond {
		if needed[i] {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return m, nil
	}
	m.Run = m.RunOn(dirs)
	p := &Part{Kind: config.PathsKind, Label: PathsLabel}
	for _, dir := range dirs {
		if shown := tilde.Shown(m.Home, dir); strings.HasPrefix(shown, "~/") {
			dir = shown
		}
		p.Lines = append(p.Lines, config.Entry{Name: dir})
		p.names = append(p.names, dir)
	}
	m.look(Look{Label: PathsLabel, Looked: true, Found: len(p.Lines), Names: p.names})
	return m, p
}

// beyond are Beyond's folders a program may be declared from, each once:
// none of kit's own, nor a temporary one (Temp), nor one that isn't
// absolute, which only a shell in a project folder finds anything in.
func (m Mac) beyond() []string {
	own := []string{m.Dirs.Config, m.Dirs.State, m.Dirs.Logs, m.Dirs.Data}
	var out []string
	for _, dir := range m.Beyond {
		if !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if !within(dir, own) && !within(dir, m.Temp) && !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return out
}

// within reports whether dir is one of folders, or inside one.
func within(dir string, folders []string) bool {
	return slices.ContainsFunc(folders, func(f string) bool {
		if f == "" {
			return false
		}
		f = filepath.Clean(f)
		return dir == f || strings.HasPrefix(dir, f+"/")
	})
}

// snapshotted reports whether a snapshot declares the kind d describes: a
// package's, or one of the settings' it declares.
func snapshotted(d kind.Descriptor) bool {
	return d.Group == kind.Packages || slices.Contains(settingsKinds, d.Name)
}
