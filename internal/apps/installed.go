package apps

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/leeovery/kit/internal/plist"
)

// Installed reports whether an app whose bundle id is id is in one of
// dirs, the folders apps are installed in, or a folder below one, as
// Utilities is.
func Installed(dirs []string, id string) bool {
	for _, dir := range dirs {
		var found []string
		for _, pattern := range []string{"*.app", "*/*.app"} {
			m, _ := filepath.Glob(filepath.Join(dir, pattern))
			found = append(found, m...)
		}
		if slices.ContainsFunc(found, func(app string) bool { return BundleID(app) == id }) {
			return true
		}
	}
	return false
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
