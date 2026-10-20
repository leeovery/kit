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
// once it's changed, so looking often costs a listing of the folders. Safe
// to use from several goroutines.
type Folders struct {
	Dirs []string

	mu   sync.Mutex
	read map[string]bundle
}

// bundle is an app's bundle id, as its Info.plist said when last modified
// at mod.
type bundle struct {
	mod time.Time
	id  string
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

// apps are the apps in the folders, and in the folders below them, by
// path.
func (f *Folders) apps() []string {
	var found []string
	for _, dir := range f.Dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			switch {
			case filepath.Ext(e.Name()) == ".app":
				found = append(found, path)
			case e.IsDir():
				below, _ := filepath.Glob(filepath.Join(path, "*.app"))
				found = append(found, below...)
			}
		}
	}
	return found
}

// idOf is the bundle id of the app at app, read again only once its
// Info.plist has changed: mu is held.
func (f *Folders) idOf(app string) string {
	info := filepath.Join(app, "Contents", "Info.plist")
	st, err := os.Stat(info)
	if err != nil {
		return ""
	}
	if b, ok := f.read[info]; ok && b.mod.Equal(st.ModTime()) {
		return b.id
	}
	if f.read == nil {
		f.read = map[string]bundle{}
	}
	id := BundleID(app)
	f.read[info] = bundle{mod: st.ModTime(), id: id}
	return id
}

// BundleID is the bundle id of the app at app, from its Info.plist: "" when
// it doesn't say.
func BundleID(app string) string {
	data, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	v, err := plist.Decode(data)
	if err != nil {
		return ""
	}
	m, _ := v.(map[string]any)
	id, _ := m["CFBundleIdentifier"].(string)
	return id
}
