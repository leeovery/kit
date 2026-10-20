package apps

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/plist"
)

// Folders are the folders apps are installed in, which it looks in for an
// app by its bundle id: each app's Info.plist read once, and again only
// once it's changed (its time or size), or when it didn't read, as one
// being written, so looking often costs a listing of the folders. Safe to
// use from several goroutines.
type Folders struct {
	Dirs []string

	mu   sync.Mutex
	read map[string]bundle
}

// bundle is an app's bundle id, as its Info.plist said when last modified
// at mod, size bytes long.
type bundle struct {
	mod  time.Time
	size int64
	id   string
}

// Has reports whether an app whose bundle id is id is in one of the
// folders, or a folder below one, as Utilities is.
func (f *Folders) Has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, app := range f.apps() {
		if f.idOf(app) == id {
			return true
		}
	}
	return false
}

// apps are the apps in the folders, and in the folders below them, a link
// to a folder followed, by path.
func (f *Folders) apps() []string {
	var found []string
	for _, dir := range f.Dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			switch {
			case filepath.Ext(e.Name()) == ".app":
				found = append(found, path)
			case isDir(path, e):
				below, _ := filepath.Glob(filepath.Join(path, "*.app"))
				found = append(found, below...)
			}
		}
	}
	return found
}

// isDir reports whether e, at path, is a folder, or a link to one.
func isDir(path string, e os.DirEntry) bool {
	if e.Type()&os.ModeSymlink == 0 {
		return e.IsDir()
	}
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// idOf is the bundle id of the app at app, read again only once its
// Info.plist has changed, or when it last didn't read: mu is held.
func (f *Folders) idOf(app string) string {
	info := filepath.Join(app, "Contents", "Info.plist")
	st, err := os.Stat(info)
	if err != nil {
		return ""
	}
	if b, ok := f.read[info]; ok && b.mod.Equal(st.ModTime()) && b.size == st.Size() {
		return b.id
	}
	id, err := bundleID(app)
	if err != nil {
		return ""
	}
	if f.read == nil {
		f.read = map[string]bundle{}
	}
	f.read[info] = bundle{mod: st.ModTime(), size: st.Size(), id: id}
	return id
}

// BundleID is the bundle id of the app at app, from its Info.plist: "" when
// it doesn't say.
func BundleID(app string) string {
	id, _ := bundleID(app)
	return id
}

// bundleID is BundleID, or why its Info.plist doesn't read.
func bundleID(app string) (string, error) {
	data, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return "", err
	}
	v, err := plist.Decode(data)
	if err != nil {
		return "", err
	}
	m, _ := v.(map[string]any)
	id, _ := m["CFBundleIdentifier"].(string)
	return id, nil
}
