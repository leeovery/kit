package testguard

import (
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// kit's directories in a home, and the file in its state directory holding
// the Mac's name.
var (
	configDir   = filepath.Join(".config", "kit")
	stateDir    = filepath.Join(".local", "state", "kit")
	logsDir     = filepath.Join("Library", "Logs", "kit")
	machineFile = "machine"
)

// watcher watches something of the real system's, as closely as a live kit
// running alongside the tests allows, and says how the tests changed it, a
// line each.
type watcher interface {
	changes() []string
}

// watchers are what testguard watches of the real system.
type watchers []watcher

// changes are how the tests changed what's watched, a line each, and once:
// two places can be one directory.
func (ws watchers) changes() []string {
	var lines []string
	for _, w := range ws {
		for _, line := range w.changes() {
			if !slices.Contains(lines, line) {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// watchReal notes, before the tests begin, what the real system holds of
// kit's: its config, by default in home, and wherever KIT_CONFIG and
// XDG_CONFIG_HOME put it, as getenv reads them; its state, by default in
// home, and wherever XDG_STATE_HOME puts it; and its logs, in home.
//
// The config is a repository the user edits, and kit edits only when told
// to, so any change to it is a test's, but for its .git directory, which a
// pull or a commit made alongside the tests changes. A live kit writes its
// state and logs as it runs, so of those only a directory appearing is a
// test's, and a change to the Mac's name, which only kit machine writes; the
// OS sandbox denies a test any write there anyway.
func watchReal(home string, getenv func(string) string) watchers {
	var ws watchers
	for _, p := range configPlaces(home, getenv) {
		ws = append(ws, watchContents(p, outsideGit, followed))
	}
	for _, p := range statePlaces(home, getenv) {
		if !exists(p.path) {
			ws = append(ws, absence{p})
			continue
		}
		ws = append(ws, watchContents(p.in(machineFile), nil, followed))
	}
	if home != "" {
		if p := inHome(home, logsDir); !exists(p.path) {
			ws = append(ws, absence{p})
		}
	}
	return ws
}

// configPlaces are where kit's config is: by default in home, and where
// KIT_CONFIG and XDG_CONFIG_HOME say. kit ignores a relative XDG directory,
// and a relative KIT_CONFIG names none testguard can know, as it's relative
// to wherever kit runs.
func configPlaces(home string, getenv func(string) string) []place {
	var places []place
	if home != "" {
		places = append(places, inHome(home, configDir))
	}
	if dir := getenv("KIT_CONFIG"); filepath.IsAbs(dir) {
		places = append(places, named(dir))
	}
	if dir := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		places = append(places, named(filepath.Join(dir, "kit")))
	}
	return places
}

// statePlaces are where kit's state is: by default in home, and where
// XDG_STATE_HOME says.
func statePlaces(home string, getenv func(string) string) []place {
	var places []place
	if home != "" {
		places = append(places, inHome(home, stateDir))
	}
	if dir := getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		places = append(places, named(filepath.Join(dir, "kit")))
	}
	return places
}

// place is somewhere of the real system's: where it is, its symlinks
// resolved as the tests began, so a link changed since changes nothing, and
// how a report names it.
type place struct {
	path  string
	shown string
}

// inHome is the place dir is in home, named from the home, as in
// ~/.config/kit.
func inHome(home, dir string) place {
	return place{path: resolve(filepath.Join(home, dir)), shown: "~/" + filepath.ToSlash(dir)}
}

// named is the place at where, named as the environment names it.
func named(where string) place {
	return place{path: resolve(where), shown: filepath.ToSlash(where)}
}

// in is the place named name in p, named from p.
func (p place) in(name string) place {
	return place{path: resolve(filepath.Join(p.path, name)), shown: path.Join(p.shown, filepath.ToSlash(name))}
}

// contents watches what's at a place, and everything under it, that keep
// keeps, or all of it when keep is nil, each noted as note notes it.
type contents struct {
	place
	keep   func(name string) bool
	note   func(path string) (entry, bool)
	before snapshot
}

func watchContents(p place, keep func(name string) bool, note func(path string) (entry, bool)) contents {
	c := contents{place: p, keep: keep, note: note}
	c.before = c.take()
	return c
}

func (c contents) changes() []string {
	return diff(c.before, c.take())
}

// take notes what the place holds. One that isn't there holds nothing.
func (c contents) take() snapshot {
	s := make(snapshot)
	_ = filepath.WalkDir(c.path, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		e, ok := c.note(p)
		if !ok {
			return nil
		}
		rel, _ := filepath.Rel(c.path, p)
		if shown := path.Join(c.shown, filepath.ToSlash(rel)); c.keep == nil || c.keep(shown) {
			s[shown] = e
		}
		return nil
	})
	return s
}

// outsideGit reports whether name is outside any .git directory.
func outsideGit(name string) bool {
	return !slices.Contains(strings.Split(name, "/"), ".git")
}

// followed notes what's at path, links followed: a config kept elsewhere and
// linked in is the real one all the same. A directory is noted by its type
// alone: its time changes with every entry made or removed in it, each
// noted on its own.
func followed(path string) (entry, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return entry{}, false
	}
	if info.IsDir() {
		return entry{kind: info.Mode().Type()}, true
	}
	return entry{kind: info.Mode().Type(), size: info.Size(), modTime: info.ModTime().UnixNano()}, true
}

// absence watches a place that wasn't there as the tests began: a state or
// logs directory, which a live kit writes in only once it's there, so its
// appearing is a test's.
type absence struct {
	place
}

func (a absence) changes() []string {
	if !exists(a.path) {
		return nil
	}
	return []string{"the real " + a.shown + " appeared"}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// snapshot is what a place holds, by how a report names each path.
type snapshot map[string]entry

// entry is what's at a path: its type, size and modification time.
type entry struct {
	kind    fs.FileMode
	size    int64
	modTime int64
}

// diff lists what differs from before to after, a line a path, in order.
func diff(before, after snapshot) []string {
	paths := make(map[string]bool)
	for p := range before {
		paths[p] = true
	}
	for p := range after {
		paths[p] = true
	}
	var lines []string
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		was, wasThere := before[p]
		is, isThere := after[p]
		switch {
		case !wasThere:
			lines = append(lines, "the real "+p+" was created")
		case !isThere:
			lines = append(lines, "the real "+p+" was removed")
		case was != is:
			lines = append(lines, "the real "+p+" was modified")
		}
	}
	return lines
}
