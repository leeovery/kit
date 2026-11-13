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
	for _, app := range Installed(f.Dirs) {
		if f.idOf(app) == id {
			return true
		}
	}
	return false
}

// In are the apps in dir, by path, in name order: none when it can't be
// read.
func In(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var found []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".app" {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	return found
}

// Installed are the apps in dirs, by path: each folder's own, then those
// in each folder inside it, as Utilities holds them, a link to a folder
// followed.
func Installed(dirs []string) []string {
	var found []string
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		var below []string
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			switch {
			case filepath.Ext(e.Name()) == ".app":
				found = append(found, path)
			case isDir(path, e):
				below = append(below, In(path)...)
			}
		}
		found = append(found, below...)
	}
	return found
}

// helperDirs are the folders inside an app where the apps that come with
// it are: helpers and login items, with settings of their own.
var helperDirs = []string{"Contents/MacOS", "Contents/Library/LoginItems", "Contents/Helpers"}

// WithHelpers is app, then the apps inside it that come with it (Keyboard
// Maestro's Engine, login items).
func WithHelpers(app string) []string {
	found := []string{app}
	for _, dir := range helperDirs {
		found = append(found, In(filepath.Join(app, dir))...)
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
