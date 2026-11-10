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
	// VerdictSetAside is a path the restore's first run listed, holding what
	// isn't kit's, all of it written in set-up's window: set aside, then
	// restored.
	VerdictSetAside Verdict = "set-aside"
	// VerdictHere is a path recorded here, or holding anything that isn't
	// kit's and can't be set aside: never touched.
	VerdictHere Verdict = "here"
)

// Judgement is a listed path's verdict and, for one to set aside, what:
// the files in it that aren't kit's, each from ~.
type Judgement struct {
	Path    string
	Verdict Verdict
	New     []string
}

// Judge judges f's line's path, in the home folder home: by its record in
// rec first, done, here, or its copy started; else, with no record, or not
// in its source, by what it holds that isn't kit's, as f leaves out,
// and set-up's window, a file's time its modification time. The window
// opens when this Mac's set-up began, began, and closes when the restore
// first settled (rec's Settled), and it's open only to the paths the
// restore's first run listed (rec's First), before their copies start or
// their source is found without them: it's shut on a Mac in use, nothing
// recording began, and to a line added after the first run, which is here
// if it holds anything that isn't kit's. Whether to restore one
// absent then is the caller's. Judging moves nothing, and stops at the first
// file that's neither kit's nor written in the window.
func Judge(home string, f *Filter, rec Record, began time.Time) (Judgement, error) {
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
	if !rec.mayAside(j.Path) {
		began = time.Time{}
	}
	written, older, err := f.holds(home, began)
	switch {
	case err != nil:
		return j, err
	case older:
		j.Verdict = VerdictHere
	case len(written) > 0:
		j.Verdict, j.New = VerdictSetAside, written
	default:
		j.Verdict = VerdictRestore
	}
	return j, nil
}

// holds finds what f's line's path, in home, holds that isn't kit's:
// whether any of it is older than began, stopping at the first that is;
// else all of it, each from ~. Folders alone aren't anything held.
func (f *Filter) holds(home string, began time.Time) (written []string, older bool, err error) {
	root := filepath.Join(home, strings.TrimPrefix(f.Line.Path, "~/"))
	info, err := os.Lstat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("read %s: %w", f.Line.Path, err)
	case !info.IsDir():
		if before(info, began) {
			return nil, true, nil
		}
		return []string{f.Line.Path}, false, nil
	}
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
		info, err := d.Info()
		if err != nil {
			return err
		}
		if before(info, began) {
			older = true
			return filepath.SkipAll
		}
		written = append(written, f.Line.Path+"/"+rel)
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", f.Line.Path, err)
	}
	if older {
		return nil, true, nil
	}
	return written, false, nil
}

// before reports whether info's file was last written before began: always,
// when began is zero.
func before(info fs.FileInfo, began time.Time) bool {
	return began.IsZero() || info.ModTime().Before(began)
}

// asideFolder is where in kit's state a restore sets files aside, a folder
// a time.
const asideFolder = "restore/set-aside"

// SetAside moves what j found written in its path in set-up's window into
// kit's state folder, stateDir: into a folder of its own under
// restore/set-aside, named by the time, now (asideDir), each keeping its
// path from the home folder, home. It records what it moved, and where,
// even when it fails partway. A file gone since j was judged is passed
// over. It's applying's to call: judging moves nothing.
func SetAside(stateDir, home string, j Judgement, now time.Time) error {
	if len(j.New) == 0 {
		return nil
	}
	dir, err := asideDir(stateDir, now)
	if err != nil {
		return fmt.Errorf("set aside what's in %s: %w", j.Path, err)
	}
	var moved []string
	for _, p := range j.New {
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
		err = fmt.Errorf("set aside what's in %s: %w", j.Path, err)
	}
	if len(moved) == 0 {
		return err
	}
	aside := Aside{Time: now, Dir: dir, Paths: moved}
	return errors.Join(err, UpdateRecord(stateDir, func(r *Record) {
		r.Change(j.Path, func(p *Path) { p.SetAside = append(p.SetAside, aside) })
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
