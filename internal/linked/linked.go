// Package linked is the files the config repository links into the home
// folder: each scope's home folder (shared/home, then the Mac's), mirrored
// into ~ a file at a time, so an edit through a link is an edit in the
// repository. Folders are never linked whole: an app's other files beside
// a linked one stay the app's.
package linked

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// StepName names the step, and its items' kind, as in file:~/.zshrc.
const StepName = "file"

// The states of a linked file.
const (
	// Missing is a file not linked, with nothing in its place: applying
	// links it.
	Missing = "missing"
	// Changed is a file linked otherwise, with nothing lost by linking it
	// again: a copy the same as the repository's, a link to one the same,
	// or a private file others can read. Applying puts it right.
	Changed = "changed"
	// Diverged is a different file where the link belongs: a file of its
	// own, or a link elsewhere. Applying leaves it; kit reconcile settles
	// it.
	Diverged = "diverged"
	// Dead is a link into the repository whose file has gone: applying
	// removes it.
	Dead = "dead"
)

// What applying does about an item.
const (
	ActionLink   = "link"
	ActionUnlink = "unlink"
)

// recordName names the record of the files kit has linked, kept so that a
// link whose file has gone, folder and all, is still found.
const recordName = "links.json"

type record struct {
	Links []string `json:"links"`
}

// Link is a file a scope's home folder declares.
type Link struct {
	// Name is where it's linked, as it's shown: ~/.zshrc.
	Name string
	// Scope is whose home folder holds it: shared, or a Mac's.
	Scope string
	// Path is the file in the repository, as in shared/home/.zshrc.
	Path string
	// Src is the file in the repository, and Dst where it's linked.
	Src, Dst string
}

// Files are a config repository's linked files, for the scopes one Mac
// reads.
type Files struct {
	Repo gitrepo.Repo
	// Home is the home folder links go in; StateDir holds kit's record.
	Home, StateDir string
	// Scopes are the scopes the Mac reads: shared first, then its own.
	Scopes []string
}

// HomeFolder is the folder of scope's whose files are linked into the home
// folder, as a path in the repository.
func HomeFolder(scope string) string {
	return scope + "/home"
}

// Present reports whether there's anything to look after: a home folder in
// one of the scopes, or a link kit made before.
func (f *Files) Present() bool {
	for _, scope := range f.Scopes {
		if info, err := os.Stat(filepath.Join(f.Repo.Dir, HomeFolder(scope))); err == nil && info.IsDir() {
			return true
		}
	}
	rec, err := state.Load[record](f.StateDir, recordName)
	return err != nil || len(rec.Links) > 0
}

// Declared are the files the scopes' home folders hold, by where they're
// linked: the files git keeps or would keep, so what the repository
// ignores is never linked. A file in two scopes' home folders is refused.
func (f *Files) Declared(ctx context.Context) ([]Link, error) {
	var folders []string
	for _, scope := range f.Scopes {
		if info, err := os.Stat(filepath.Join(f.Repo.Dir, HomeFolder(scope))); err == nil && info.IsDir() {
			folders = append(folders, HomeFolder(scope))
		}
	}
	if len(folders) == 0 {
		return nil, nil
	}
	paths, err := f.Repo.Files(ctx, folders...)
	if err != nil {
		return nil, err
	}
	var links []Link
	for _, p := range paths {
		scope, rel, ok := f.split(p)
		if !ok || filepath.Base(rel) == ".DS_Store" {
			continue
		}
		src := filepath.Join(f.Repo.Dir, p)
		// Deleted but not yet committed, or a link of the repository's own.
		if info, err := os.Lstat(src); err != nil || !info.Mode().IsRegular() {
			continue
		}
		l := Link{Name: "~/" + rel, Scope: scope, Path: p, Src: src, Dst: filepath.Join(f.Home, rel)}
		if i := slices.IndexFunc(links, func(o Link) bool { return o.Name == l.Name }); i >= 0 {
			return nil, fmt.Errorf("%s is in %s and %s: a file goes in the shared home folder or a Mac's, not both", l.Name, links[i].Path, l.Path)
		}
		links = append(links, l)
	}
	slices.SortFunc(links, func(a, b Link) int { return strings.Compare(a.Name, b.Name) })
	return links, nil
}

// split is the scope and the path in its home folder of p, a path in the
// repository.
func (f *Files) split(p string) (scope, rel string, ok bool) {
	for _, scope := range f.Scopes {
		if rel, ok := strings.CutPrefix(p, HomeFolder(scope)+"/"); ok && rel != "" {
			return scope, rel, true
		}
	}
	return "", "", false
}

// Check finds how each declared file stands, and the dead links into the
// repository.
func (f *Files) Check(ctx context.Context) check.Result {
	links, err := f.Declared(ctx)
	switch {
	case errors.Is(err, runner.ErrNotFound):
		return check.Result{State: check.Deferred, Reason: "needs git, which isn't installed"}
	case err != nil:
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	var items []check.Item
	linked := 0
	for _, l := range links {
		it, ok := f.state(l)
		if ok {
			items = append(items, it)
		}
		if !ok || it.State == Changed && IsLinked(l) {
			linked++
		}
	}
	dead, err := f.dead(links)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	items = append(items, dead...)
	res := check.Result{
		State:   check.OK,
		Summary: fmt.Sprintf("%d linked", len(links)),
		Counts:  map[string]int{"declared": len(links), "linked": linked},
		Items:   items,
	}
	switch {
	case len(links) == 0:
		res.Summary = "none declared"
	case linked < len(links):
		res.Summary = fmt.Sprintf("%d of %d linked", linked, len(links))
	}
	for _, it := range items {
		res.Counts[it.State]++
	}
	if len(items) > 0 {
		res.State = check.Attention
	}
	return res
}

// IsLinked reports whether l's place holds its link.
func IsLinked(l Link) bool {
	target, err := os.Readlink(l.Dst)
	return err == nil && resolve(l.Dst, target) == l.Src
}

// state is how l stands, as an item: none when it's linked as it should
// be.
func (f *Files) state(l Link) (check.Item, bool) {
	it := check.Item{ID: StepName + ":" + l.Name, Name: l.Name}
	info, err := os.Lstat(l.Dst)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		it.State, it.Action = Missing, ActionLink
	case err != nil:
		it.State, it.Detail = Diverged, err.Error()
	case info.Mode()&fs.ModeSymlink != 0:
		target, _ := os.Readlink(l.Dst)
		target = resolve(l.Dst, target)
		switch {
		case target == l.Src:
			return f.privacy(l, it)
		case same(l.Src, l.Dst):
			it.State, it.Detail, it.Action = Changed, "linked to "+f.Tilde(target)+", the same file", ActionLink
		default:
			it.State, it.Detail = Diverged, "linked to "+f.Tilde(target)+", which differs"
		}
	case info.Mode().IsRegular():
		if same(l.Src, l.Dst) {
			it.State, it.Detail, it.Action = Changed, "a copy, the same as "+l.Path, ActionLink
		} else {
			it.State, it.Detail = Diverged, "a file of its own, different from "+l.Path
		}
	default:
		it.State, it.Detail = Diverged, "a folder is there"
	}
	return it, true
}

// privacy is l, linked, as an item when it's private and others can read
// it or open its folder: none otherwise.
func (f *Files) privacy(l Link, it check.Item) (check.Item, bool) {
	dir, ok := f.privateDir(l)
	if !ok {
		return it, false
	}
	if info, err := os.Stat(l.Src); err == nil && !strings.HasSuffix(l.Name, ".pub") && info.Mode().Perm()&0o077 != 0 {
		it.State, it.Detail, it.Action = Changed, "others can read it", ActionLink
		return it, true
	}
	if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0o077 != 0 {
		it.State, it.Detail, it.Action = Changed, "others can open its folder", ActionLink
		return it, true
	}
	return it, false
}

// privateDir is the folder of l's that's for its owner alone, as ssh and
// GnuPG insist, when it's in one.
func (f *Files) privateDir(l Link) (string, bool) {
	for _, dir := range []string{".ssh", ".gnupg"} {
		if strings.HasPrefix(l.Dst, filepath.Join(f.Home, dir)+"/") {
			return filepath.Join(f.Home, dir), true
		}
	}
	return "", false
}

// dead are the links into the repository whose files have gone: among
// those kit made, and in every folder a home folder mirrors.
func (f *Files) dead(links []Link) ([]check.Item, error) {
	declared := make(map[string]bool, len(links))
	for _, l := range links {
		declared[l.Dst] = true
	}
	rec, err := state.Load[record](f.StateDir, recordName)
	if err != nil {
		return nil, err
	}
	candidates := slices.Clone(rec.Links)
	for _, scope := range f.Scopes {
		root := filepath.Join(f.Repo.Dir, HomeFolder(scope))
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			mirror := filepath.Join(f.Home, rel)
			entries, err := os.ReadDir(mirror)
			if err != nil {
				return nil
			}
			for _, e := range entries {
				if e.Type()&fs.ModeSymlink != 0 {
					candidates = append(candidates, filepath.Join(mirror, e.Name()))
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(candidates)
	var items []check.Item
	for _, dst := range slices.Compact(candidates) {
		if declared[dst] || !f.deadLink(dst) {
			continue
		}
		name := f.Tilde(dst)
		items = append(items, check.Item{ID: StepName + ":" + name, Name: name, State: Dead, Detail: "its file in kit-config has gone", Action: ActionUnlink})
	}
	return items, nil
}

// deadLink reports whether dst is a link into the repository to nothing.
func (f *Files) deadLink(dst string) bool {
	target, err := os.Readlink(dst)
	if err != nil {
		return false
	}
	target = resolve(dst, target)
	if !strings.HasPrefix(target, f.Repo.Dir+"/") {
		return false
	}
	_, err = os.Stat(target)
	return errors.Is(err, fs.ErrNotExist)
}

// Apply links what found says to link, and removes the dead links, each on
// its own, so one failing leaves the others; then records what's linked.
func (f *Files) Apply(ctx context.Context, found check.Result) error {
	links, err := f.Declared(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, it := range found.Items {
		switch it.Action {
		case ActionLink:
			i := slices.IndexFunc(links, func(l Link) bool { return l.Name == it.Name })
			if i < 0 {
				continue
			}
			if err := f.link(links[i]); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", it.Name, err))
			}
		case ActionUnlink:
			dst := f.untilde(it.Name)
			if !f.deadLink(dst) {
				continue
			}
			if err := os.Remove(dst); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", it.Name, err))
			}
		}
	}
	if err := f.remember(links); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// remember records which of links are linked.
func (f *Files) remember(links []Link) error {
	var dsts []string
	for _, l := range links {
		if IsLinked(l) {
			dsts = append(dsts, l.Dst)
		}
	}
	return state.Update(f.StateDir, recordName, func(rec *record) { rec.Links = dsts })
}

// link links l: in place of nothing, or of a file the same as the
// repository's, never of one that differs; its folders made as needed, and
// a private file kept private.
func (f *Files) link(l Link) error {
	if info, err := os.Lstat(l.Dst); err == nil {
		target, _ := os.Readlink(l.Dst)
		if info.Mode()&fs.ModeSymlink == 0 && !info.Mode().IsRegular() || resolve(l.Dst, target) != l.Src && !same(l.Src, l.Dst) {
			return fmt.Errorf("it differs from %s: kit reconcile settles it", l.Path)
		}
	}
	mode := os.FileMode(0o755)
	if dir, ok := f.privateDir(l); ok {
		mode = 0o700
		if err := os.MkdirAll(dir, mode); err != nil {
			return err
		}
		if err := os.Chmod(dir, mode); err != nil {
			return err
		}
		if !strings.HasSuffix(l.Name, ".pub") {
			if err := os.Chmod(l.Src, 0o600); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(l.Dst), mode); err != nil {
		return err
	}
	if target, err := os.Readlink(l.Dst); err == nil && resolve(l.Dst, target) == l.Src {
		return nil
	}
	tmp := l.Dst + ".kit-link"
	_ = os.Remove(tmp)
	if err := os.Symlink(l.Src, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.Dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// find is the declared file linked at name.
func (f *Files) find(ctx context.Context, name string) (Link, error) {
	links, err := f.Declared(ctx)
	if err != nil {
		return Link{}, err
	}
	i := slices.IndexFunc(links, func(l Link) bool { return l.Name == name })
	if i < 0 {
		return Link{}, fmt.Errorf("%s isn't a linked file", name)
	}
	return links[i], nil
}

// Adopt takes the Mac's copy of the file linked at name into the
// repository, in place of the repository's, then links it: a file of its
// own there, or the file a link elsewhere leads to.
func (f *Files) Adopt(ctx context.Context, name string) (Link, error) {
	l, err := f.find(ctx, name)
	if err != nil {
		return l, err
	}
	data, err := os.ReadFile(l.Dst)
	if err != nil {
		return l, err
	}
	info, err := os.Stat(l.Src)
	if err != nil {
		return l, err
	}
	if err := writeFile(l.Src, data, info.Mode().Perm()); err != nil {
		return l, err
	}
	return l, f.link(l)
}

// Revert moves the Mac's copy of the file linked at name to the Bin, then
// links the repository's: where the Mac's copy went.
func (f *Files) Revert(ctx context.Context, name string) (string, error) {
	l, err := f.find(ctx, name)
	if err != nil {
		return "", err
	}
	binned, err := ToBin(f.Home, l.Dst)
	if err != nil {
		return "", fmt.Errorf("couldn't move it to the Bin: %w", err)
	}
	return binned, f.link(l)
}

// Add moves the file at path, or every file in the folder at path, into
// scope's home folder in the repository, and links each back: the links it
// made. Nothing moves unless all can: a file already declared, a link, or
// one outside the home folder is refused.
func (f *Files) Add(ctx context.Context, path, scope string) ([]Link, error) {
	rel, err := filepath.Rel(f.Home, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path == f.Repo.Dir || strings.HasPrefix(path, f.Repo.Dir+"/") {
		return nil, fmt.Errorf("%s isn't in the home folder, outside kit-config", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	var files []string
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		if target, _ := os.Readlink(path); strings.HasPrefix(resolve(path, target), f.Repo.Dir+"/") {
			return nil, fmt.Errorf("%s is linked already", f.Tilde(path))
		}
		return nil, fmt.Errorf("%s is a link: add the file it leads to", f.Tilde(path))
	case info.IsDir():
		err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case p == f.Repo.Dir:
				return filepath.SkipDir
			}
			if d.Type().IsRegular() && d.Name() != ".DS_Store" {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%s has no files of its own to add", f.Tilde(path))
		}
	case info.Mode().IsRegular():
		files = []string{path}
	default:
		return nil, fmt.Errorf("%s isn't a file", f.Tilde(path))
	}
	declared, err := f.Declared(ctx)
	if err != nil {
		return nil, err
	}
	links := make([]Link, 0, len(files))
	for _, p := range files {
		rel, _ := filepath.Rel(f.Home, p)
		l := Link{Name: "~/" + rel, Scope: scope, Path: HomeFolder(scope) + "/" + rel, Dst: p}
		l.Src = filepath.Join(f.Repo.Dir, l.Path)
		if i := slices.IndexFunc(declared, func(d Link) bool { return d.Name == l.Name }); i >= 0 {
			return nil, fmt.Errorf("%s is declared already, in %s", l.Name, declared[i].Path)
		}
		if _, err := os.Lstat(l.Src); err == nil {
			return nil, fmt.Errorf("%s is in kit-config already", l.Path)
		}
		links = append(links, l)
	}
	for _, l := range links {
		data, err := os.ReadFile(l.Dst)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(l.Dst)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(l.Src), 0o755); err != nil {
			return nil, err
		}
		if err := writeFile(l.Src, data, info.Mode().Perm()); err != nil {
			return nil, err
		}
		if err := f.link(l); err != nil {
			return nil, fmt.Errorf("%s: %w", l.Name, err)
		}
	}
	return links, f.remember(append(declared, links...))
}

// Remove puts a copy of the repository's file back where the link at name
// is, then takes the file out of the repository, and any folder that
// leaves empty: the link it removed.
func (f *Files) Remove(ctx context.Context, name string) (Link, error) {
	l, err := f.find(ctx, name)
	if err != nil {
		return l, err
	}
	if !IsLinked(l) {
		return l, fmt.Errorf("%s isn't linked: kit reconcile settles it first", name)
	}
	data, err := os.ReadFile(l.Src)
	if err != nil {
		return l, err
	}
	info, err := os.Stat(l.Src)
	if err != nil {
		return l, err
	}
	if err := writeFile(l.Dst, data, info.Mode().Perm()); err != nil {
		return l, err
	}
	if err := os.Remove(l.Src); err != nil {
		return l, err
	}
	root := filepath.Join(f.Repo.Dir, HomeFolder(l.Scope))
	for dir := filepath.Dir(l.Src); dir != root && strings.HasPrefix(dir, root+"/"); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	links, err := f.Declared(ctx)
	if err != nil {
		return l, err
	}
	return l, f.remember(links)
}

// writeFile writes data to path whole or not at all, with mode: beside it,
// then renamed into place, replacing a file or a link.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".kit-new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ToBin moves path into the Bin, under a name of its own there: where it
// went.
func ToBin(home, path string) (string, error) {
	bin := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return "", err
	}
	base := filepath.Base(path)
	dst := filepath.Join(bin, base)
	for n := 2; ; n++ {
		if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
			break
		}
		dst = filepath.Join(bin, fmt.Sprintf("%s %d", base, n))
	}
	return dst, os.Rename(path, dst)
}

// same reports whether the files at a and b, links followed, hold the same.
func same(a, b string) bool {
	da, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	db, err := os.ReadFile(b)
	return err == nil && bytes.Equal(da, db)
}

// resolve is a link's target as a full path: one relative to the link's
// folder made absolute.
func resolve(link, target string) string {
	if target == "" || filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(filepath.Dir(link), target)
}

// Tilde is path as it's shown, ~ for the home folder.
func (f *Files) Tilde(path string) string {
	if rest, ok := strings.CutPrefix(path, f.Home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// untilde is a path shown with ~ in full.
func (f *Files) untilde(name string) string {
	if rest, ok := strings.CutPrefix(name, "~/"); ok {
		return filepath.Join(f.Home, rest)
	}
	return name
}
