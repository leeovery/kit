package apps_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
func TestFolders(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	writeApp(t, filepath.Join(one, "Tool.app"), "com.example.tool")
	writeApp(t, filepath.Join(two, "Utilities", "Helper.app"), "com.example.helper")
	folders := &apps.Folders{Dirs: []string{one, two}}
	for id, want := range map[string]bool{"com.example.tool": true, "com.example.helper": true, "com.example.other": false} {
		if got := folders.Has(id); got != want {
			t.Errorf("Has(%s) = %v, want %v", id, got, want)
		}
	}
	// An app installed meanwhile is found at the next look; one changed,
	// by what it says now.
	writeApp(t, filepath.Join(two, "Other.app"), "com.example.other")
	if !folders.Has("com.example.other") {
		t.Error("Has() of an app installed since the last look = false")
	}
	info := filepath.Join(one, "Tool.app", "Contents", "Info.plist")
	writeApp(t, filepath.Join(one, "Tool.app"), "com.example.renamed")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(info, later, later); err != nil {
		t.Fatal(err)
	}
	if folders.Has("com.example.tool") || !folders.Has("com.example.renamed") {
		t.Error("Has() didn't read an app's Info.plist again once it changed")
	}
	if got := apps.BundleID(filepath.Join(one, "Missing.app")); got != "" {
		t.Errorf("BundleID() of no app = %q", got)
	}
}
