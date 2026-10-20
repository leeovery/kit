package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/steps"
)

// folderMove is what becomes of a step's folders as its line moves to the
// shared file: the Macs' folders, those there, which go; and whether the
// shared one's there already, the same as theirs.
type folderMove struct {
	macs  []string
	there bool
}

// planStepFolder is how the folder of s, the shared one, comes to be as its
// line moves from the files of the Macs from: theirs, all the same, moved
// in, or left for one there already that's the same; or, none of them
// having one, a template written. Theirs differing, or one there already
// otherwise, is refused, naming them.
func (r *run) planStepFolder(s steps.Script, from []string) (folderMove, error) {
	var m folderMove
	for _, mac := range from {
		folder := config.StepFolder(mac, s.Name)
		there, err := exists(filepath.Join(r.cfg.Dir, folder))
		if err != nil {
			return m, err
		}
		if there {
			m.macs = append(m.macs, folder)
		}
	}
	there, err := exists(s.Dir)
	if err != nil {
		return m, err
	}
	m.there = there
	if len(m.macs) == 0 {
		if there {
			return m, fmt.Errorf("%s is there already: remove it, or name the step otherwise", s.Folder)
		}
		return m, nil
	}
	others := m.macs[1:]
	if there {
		others = append(others, s.Folder)
	}
	for _, other := range others {
		same, err := sameTree(filepath.Join(r.cfg.Dir, m.macs[0]), filepath.Join(r.cfg.Dir, other))
		if err != nil {
			return m, err
		}
		if !same {
			return m, fmt.Errorf("%s and %s differ: make them the same, or take one out first", m.macs[0], other)
		}
	}
	return m, nil
}

// makeStepFolder makes the folder of s, the shared one, as m says, before
// its line's declared: the first Mac's moved in, or, none having one, a
// template written. It returns what it did, and undo, putting things back
// as they were, should the declare fail: nothing to undo till something's
// made, and the folder of steps it made, once empty, gone too.
func (r *run) makeStepFolder(s steps.Script, does string, m folderMove) (string, func() error, error) {
	nothing := func() error { return nil }
	parent := filepath.Dir(s.Dir)
	hadParent, err := exists(parent)
	if err != nil {
		return "", nothing, err
	}
	tidy := func() error {
		if hadParent {
			return nil
		}
		if err := os.Remove(parent); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if len(m.macs) == 0 {
		undo := func() error { return errors.Join(os.RemoveAll(s.Dir), tidy()) }
		if err := os.MkdirAll(s.Dir, 0o755); err != nil {
			return "", undo, err
		}
		err := os.WriteFile(filepath.Join(s.Dir, steps.ScriptName), []byte(steps.Template(s.Name, does)), 0o755)
		return "its script, " + s.Folder + "/" + steps.ScriptName + ", is a template: write its check and apply", undo, err
	}
	said := "its folder moved from " + strings.Join(m.macs, " and ")
	if m.there {
		return said, nothing, nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", tidy, err
	}
	first := filepath.Join(r.cfg.Dir, m.macs[0])
	if err := os.Rename(first, s.Dir); err != nil {
		return "", tidy, err
	}
	return said, func() error { return errors.Join(os.Rename(s.Dir, first), tidy()) }, nil
}

// finishStepFolder takes the Macs' folders out, once the line's moved,
// noting each folder changed, to commit, the shared one too.
func (r *run) finishStepFolder(c *changes, s steps.Script, m folderMove) error {
	c.changed(s.Folder)
	for _, folder := range m.macs {
		if err := os.RemoveAll(filepath.Join(r.cfg.Dir, folder)); err != nil {
			return err
		}
		c.changed(folder)
	}
	return nil
}

// exists reports whether there's anything at path.
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// sameTree reports whether the folders a and b hold the same: the same
// paths, each of the same type and permissions, files with the same
// contents and links to the same.
func sameTree(a, b string) (bool, error) {
	ta, err := tree(a)
	if err != nil {
		return false, err
	}
	tb, err := tree(b)
	if err != nil {
		return false, err
	}
	return maps.Equal(ta, tb), nil
}

// tree is what the folder at root holds, by path within it: each one's
// type and permissions, and a file's contents, or where a link points.
func tree(root string) (map[string]string, error) {
	held := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		what := info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			what += " " + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			what += " " + string(data)
		}
		held[rel] = what
		return nil
	})
	return held, err
}
