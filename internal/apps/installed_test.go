package apps_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leeovery/kit/internal/apps"
)

// writeApp puts an app whose bundle id is id at path.
func writeApp(t *testing.T, path, id string) {
	t.Helper()
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>` + id + `</string></dict></plist>
`
	if err := os.MkdirAll(filepath.Join(path, "Contents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Contents", "Info.plist"), []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
}

// An app's installed when one with its bundle id is in a folder apps are
// installed in, or a folder below one.
func TestInstalled(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	writeApp(t, filepath.Join(one, "Tool.app"), "com.example.tool")
	writeApp(t, filepath.Join(two, "Utilities", "Helper.app"), "com.example.helper")
	for id, want := range map[string]bool{"com.example.tool": true, "com.example.helper": true, "com.example.other": false} {
		if got := apps.Installed([]string{one, two}, id); got != want {
			t.Errorf("Installed(%s) = %v, want %v", id, got, want)
		}
	}
	if got := apps.BundleID(filepath.Join(one, "Missing.app")); got != "" {
		t.Errorf("BundleID() of no app = %q", got)
	}
}
