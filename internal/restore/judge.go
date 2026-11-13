package restore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Verdict is what the restore does about a listed path, judged before any
// copy.
type Verdict string

const (
	// VerdictDone is a path recorded done: never restored again.
	VerdictDone Verdict = "done"
	// VerdictResume is a path whose copy has started and not finished: its
	// copy runs again.
	VerdictResume Verdict = "resume"
	// VerdictRestore is a path absent, or holding nothing but kit's.
	VerdictRestore Verdict = "restore"
	// VerdictSetAside is a path in set-up's window, holding what isn't
	// kit's, whatever its age: all of that set aside (SetAside), then
	// restored.
	VerdictSetAside Verdict = "set-aside"
	// VerdictHere is a path recorded here, or holding anything that isn't
	// kit's and can't be set aside: never touched.
	VerdictHere Verdict = "here"
)

// Judgement is a listed path's verdict.
type Judgement struct {
	Path    string
	Verdict Verdict
}

// Judge judges f's line's path, in the home folder home: by its record in
// rec first, done, here, or its copy started; else, with no record, or not
// in its source, by whether it holds anything that isn't kit's, as f leaves
// out, looking no further than the first: holding any in set-up's window
// (rec's MaySetAside), it's to set aside, whatever its age; out of it, it's
// here. Whether to restore a path absent, or holding nothing but kit's, is
// the caller's. Judging moves nothing.
func Judge(home string, f *Filter, rec Record, setUp bool) (Judgement, error) {
	j := Judgement{Path: f.Line.Path}
	switch p := rec.Paths[j.Path]; {
	case p.State == Done:
		j.Verdict = VerdictDone
		return j, nil
	case p.State == Here:
		j.Verdict = VerdictHere
		return j, nil
	case p.started():
		j.Verdict = VerdictResume
		return j, nil
	}
	held, err := f.holds(home, false)
	switch {
	case err != nil:
		return j, err
	case len(held) == 0:
		j.Verdict = VerdictRestore
	case rec.MaySetAside(j.Path, setUp):
		j.Verdict = VerdictSetAside
	default:
		j.Verdict = VerdictHere
	}
	return j, nil
}

// holds is what f's line's path, in home, holds that isn't kit's, each
// from ~: all of it, or, all false, the first found alone, which is enough
// to say it holds anything. Folders alone aren't anything held.
func (f *Filter) holds(home string, all bool) ([]string, error) {
	root := filepath.Join(home, strings.TrimPrefix(f.Line.Path, "~/"))
	info, err := os.Lstat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", f.Line.Path, err)
	case !info.IsDir():
		return []string{f.Line.Path}, nil
	}
	var held []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, dir := filepath.ToSlash(p[len(root)+1:]), d.IsDir()
		switch {
		case dir && f.Excludes(rel, dir):
			return filepath.SkipDir
		case dir || f.Excludes(rel, dir):
			return nil
		}
		held = append(held, f.Line.Path+"/"+rel)
		if !all {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Line.Path, err)
	}
	return held, nil
}

// asideFolder is where in kit's state a restore sets files aside, a folder
// a time.
const asideFolder = "restore/set-aside"

// SetAside moves everything f's line's path, in the home folder home,
// holds that isn't kit's, as f leaves out, into kit's state folder,
// stateDir (SetAsideFiles). It's applying's to call: judging moves nothing.
func SetAside(stateDir, home string, f *Filter, now time.Time) (Aside, error) {
	held, err := f.holds(home, true)
	if err != nil {
		return Aside{}, err
	}
	return SetAsideFiles(stateDir, home, f.Line.Path, held, now)
}

// SetAsideFiles moves files, each from ~, of the listed path path, into
// kit's state folder, stateDir: into a folder of its own under
// restore/set-aside, named by the time, now (asideDir), each keeping its
// path from the home folder, home. It records what it moved, and where,
// even when it fails partway, and returns it, nothing when it moved
// nothing. A file gone since it was found is passed over.
func SetAsideFiles(stateDir, home, path string, files []string, now time.Time) (Aside, error) {
	if len(files) == 0 {
		return Aside{}, nil
	}
	dir, err := asideDir(stateDir, now)
	if err != nil {
		return Aside{}, fmt.Errorf("set aside what's in %s: %w", path, err)
	}
	var moved []string
	for _, p := range files {
		rel := strings.TrimPrefix(p, "~/")
		dst := filepath.Join(dir, rel)
		if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			break
		}
		err = os.Rename(filepath.Join(home, rel), dst)
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
			continue
		}
		if err != nil {
			break
		}
		moved = append(moved, p)
	}
	if err != nil {
		err = fmt.Errorf("set aside what's in %s: %w", path, err)
	}
	if len(moved) == 0 {
		return Aside{}, err
	}
	aside := Aside{Time: now, Dir: dir, Paths: moved}
	return aside, errors.Join(err, UpdateRecord(stateDir, func(r *Record) {
		r.Change(path, func(p *Path) { p.SetAside = append(p.SetAside, aside) })
	}))
}

// asideDir makes the folder, in kit's state folder, stateDir, that a
// set-aside at now moves into: named by the time, to the second, and, one
// of that second there already, a number after it, so a set-aside never
// replaces what another set aside.
func asideDir(stateDir string, now time.Time) (string, error) {
	named := filepath.Join(stateDir, asideFolder, now.Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Dir(named), 0o700); err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		dir := named
		if n > 1 {
			dir = fmt.Sprintf("%s-%d", named, n)
		}
		if err := os.Mkdir(dir, 0o700); !errors.Is(err, fs.ErrExist) {
			return dir, err
		}
	}
}
