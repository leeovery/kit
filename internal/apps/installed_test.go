package apps_test

import (
	"bytes"
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
// installed in, or a folder below one, or a link to a folder.
func TestFolders(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	writeApp(t, filepath.Join(one, "Tool.app"), "com.example.tool")
	writeApp(t, filepath.Join(two, "Utilities", "Helper.app"), "com.example.helper")
	elsewhere := t.TempDir()
	writeApp(t, filepath.Join(elsewhere, "Linked.app"), "com.example.linked")
	if err := os.Symlink(elsewhere, filepath.Join(two, "Linked")); err != nil {
		t.Fatal(err)
	}
	folders := &apps.Folders{Dirs: []string{one, two}}
	for id, want := range map[string]bool{"com.example.tool": true, "com.example.helper": true, "com.example.linked": true, "com.example.other": false} {
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
	// A plist that doesn't read yet, as one being written, isn't taken as
	// the app's last word: once it reads, at the same time and size, it's
	// found.
	half := filepath.Join(one, "Half.app", "Contents", "Info.plist")
	writeApp(t, filepath.Join(one, "Half.app"), "com.example.half")
	whole, _ := os.ReadFile(half)
	if err := os.WriteFile(half, bytes.Repeat([]byte("x"), len(whole)), 0o600); err != nil {
		t.Fatal(err)
	}
	then := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(half, then, then); err != nil {
		t.Fatal(err)
	}
	if folders.Has("com.example.half") {
		t.Error("Has() of an app whose plist doesn't read = true")
	}
	if err := os.WriteFile(half, whole, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(half, then, then); err != nil {
		t.Fatal(err)
	}
	if !folders.Has("com.example.half") {
		t.Error("Has() didn't read again a plist that didn't read")
	}
	// A plist rewritten in the same second, by its size.
	writeApp(t, filepath.Join(one, "Half.app"), "com.example.half.longer")
	if err := os.Chtimes(half, then, then); err != nil {
		t.Fatal(err)
	}
	if !folders.Has("com.example.half.longer") {
		t.Error("Has() didn't read again a plist whose size changed")
	}
	if got := apps.BundleID(filepath.Join(one, "Missing.app")); got != "" {
		t.Errorf("BundleID() of no app = %q", got)
	}
}
