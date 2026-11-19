package snapshot

import (
	"fmt"
	"path/filepath"
	"regexp"
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
// will. A folder named for a version is passed over, as the next upgrade
// takes it away: when it alone holds a program, it's refused, saying so.
func (m Mac) along() (Mac, *Part) {
	if m.RunOn == nil {
		return m, nil
	}
	deps := &kind.Deps{Run: m.Run, Home: m.Home, ConfigHome: m.ConfigHome, StateDir: m.Dirs.State, AppDirs: m.AppDirs}
	beyond := m.beyond()
	needed := make([]bool, len(beyond))
	versioned := make([][]string, len(beyond))
	for _, d := range kinds.All {
		if !snapshotted(d) {
			continue
		}
		program := d.New(deps).Program()
		if runner.Has(m.Run, program) {
			continue
		}
		switch at, tied := m.place(beyond, program); {
		case at >= 0:
			needed[at] = true
		case tied >= 0:
			versioned[tied] = append(versioned[tied], program)
		}
	}
	p := &Part{Kind: config.PathsKind, Label: PathsLabel}
	var dirs []string
	for i, dir := range beyond {
		if needed[i] {
			dirs = append(dirs, dir)
			p.Lines = append(p.Lines, config.Entry{Name: m.declared(dir)})
			p.names = append(p.names, m.declared(dir))
		}
		if len(versioned[i]) > 0 {
			p.Refused = append(p.Refused, config.Refusal{Kind: config.PathsKind, Entry: config.Entry{Name: m.declared(dir)},
				Err: fmt.Errorf("it holds %s, but its name holds a version, which the next upgrade changes: kit path add a folder that doesn't change", strings.Join(versioned[i], " and "))})
		}
	}
	if len(dirs) == 0 && len(p.Refused) == 0 {
		return m, nil
	}
	if len(dirs) > 0 {
		m.Run = m.RunOn(dirs)
	}
	m.look(Look{Label: PathsLabel, Looked: true, Found: len(p.Lines), Names: p.names})
	return m, p
}

// place is where along beyond program is found, at its first folder not
// named for a version, -1 for none; and, when that's none, the first
// folder named for a version that holds it, -1 for none.
func (m Mac) place(beyond []string, program string) (at, versioned int) {
	versioned = -1
	for i, dir := range beyond {
		switch {
		case !runner.Has(m.RunOn([]string{dir}), program):
		case !forVersion(dir):
			return i, -1
		case versioned < 0:
			versioned = i
		}
	}
	return -1, versioned
}

// version is a folder's name that's a version: digits and dots, as in
// 22.11.0, perhaps led by a v. A dot at least: digits alone name folders
// of other things too, as a numbered one.
var version = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)+$`)

// forVersion reports whether a folder of dir's is named for a version, as
// a version manager's folder for each version is.
func forVersion(dir string) bool {
	return slices.ContainsFunc(strings.Split(dir, "/"), version.MatchString)
}

// declared is dir as kit path add declares it: from ~ in the home.
func (m Mac) declared(dir string) string {
	if dir == filepath.Clean(m.Home) {
		return "~"
	}
	if shown := tilde.Shown(m.Home, dir); strings.HasPrefix(shown, "~/") {
		return shown
	}
	return dir
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
