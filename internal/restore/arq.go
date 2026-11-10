package restore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// arqFolder is where in kit's state the person restores the listed paths
// in Arq's app, for kit to copy from, as arqc can't restore: a holding
// folder each, numbered, so it's quick to pick in Arq.
const arqFolder = "restore/arq"

// ArqFolder is the folder in kit's state, stateDir, holding Arq's
// restores.
func ArqFolder(stateDir string) string {
	return filepath.Join(stateDir, filepath.FromSlash(arqFolder))
}

// Holding is the holding folder numbered n, in kit's state, stateDir.
func Holding(stateDir string, n int) string {
	return filepath.Join(ArqFolder(stateDir), strconv.Itoa(n))
}

// Restored is where Arq puts p, a line's path, from ~, restored into the
// holding folder hold: in it, under its own name, as Arq restores a folder
// into the one it's given.
func Restored(hold, p string) string {
	return filepath.Join(hold, path.Base(p))
}

// Hold gives each of paths, lines' paths, a holding folder for Arq's
// restore, in kit's state, stateDir, recording its number and making it:
// one that has one keeps it; the rest, in order, the lowest numbers no
// other path holds, from 1. It returns each's holding folder, by path.
func Hold(stateDir string, paths []string) (map[string]string, error) {
	numbers := map[string]int{}
	err := UpdateRecord(stateDir, func(r *Record) {
		var used []int
		for _, p := range r.Paths {
			used = append(used, p.Holding)
		}
		next := 1
		for _, p := range paths {
			n := r.Paths[p].Holding
			for ; n == 0; next++ {
				if !slices.Contains(used, next) {
					n = next
				}
			}
			r.Change(p, func(rp *Path) { rp.Holding = n })
			used, numbers[p] = append(used, n), n
		}
	})
	if err != nil {
		return nil, err
	}
	holds := make(map[string]string, len(numbers))
	for p, n := range numbers {
		holds[p] = Holding(stateDir, n)
		if err := os.MkdirAll(holds[p], 0o700); err != nil {
			return nil, fmt.Errorf("make %s's holding folder: %w", p, err)
		}
	}
	return holds, nil
}

// Holds reports whether the holding folder hold holds Arq's restore of p, a
// line's path, under its own name (Restored); else what it holds instead,
// by name, Finder's files aside: nothing, for one empty or not there, as
// one Arq's still restoring into can be.
func Holds(hold, p string) (held bool, instead []string, err error) {
	_, err = os.Lstat(Restored(hold, p))
	if err == nil {
		return true, nil, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false, nil, err
	}
	entries, err := os.ReadDir(hold)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	for _, e := range entries {
		if !slices.Contains(finder, e.Name()) {
			instead = append(instead, e.Name())
		}
	}
	return false, instead, nil
}

// heldIn is an error unless the holding folder hold holds Arq's restore of
// p, a line's path, saying what it holds instead: never that its source
// hasn't p, as Arq may still be restoring it, or have put it elsewhere.
func heldIn(hold, p string) error {
	held, instead, err := Holds(hold, p)
	switch {
	case err != nil, held:
		return err
	case len(instead) == 0:
		return fmt.Errorf("%s holds nothing: Arq's restore of %s isn't in it", hold, p)
	}
	return fmt.Errorf("%s holds %s, not %s: restore %s itself into it, as Arq puts a folder in the one it's given", hold, strings.Join(instead, ", "), path.Base(p), p)
}

// Arq is the listed paths Arq restored, each into its holding folder, by
// path (Hold), to open as a source: in kit's state, stateDir, Home the home
// folder; never changing as it's copied, as a backup's restore; each
// path's holding folder going once it's restored, so its room's freed.
type Arq struct {
	StateDir, Home string
	Holds          map[string]string
}

// Origin is what the record keeps of Arq's restore: the folder holding its
// holding folders, from ~ when it's in the home folder.
func (a Arq) Origin() Origin {
	dir := ArqFolder(a.StateDir)
	if rest, ok := strings.CutPrefix(dir, a.Home+"/"); ok && a.Home != "" {
		dir = "~/" + rest
	}
	return Origin{Kind: FromArq, Detail: dir}
}

// Open opens Arq's restore: nothing to close.
func (a Arq) Open(context.Context) (*Opened, error) {
	folders := make(map[string]string, len(a.Holds))
	for p, hold := range a.Holds {
		folders[p] = Restored(hold, p)
	}
	return &Opened{
		Folders: folders, Frees: true, Origin: a.Origin(),
		Close: func(context.Context) error { return nil },
	}, nil
}
