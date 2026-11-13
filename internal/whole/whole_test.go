package whole_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/leeovery/kit/internal/whole"
)

var writers = map[string]func(string, []byte, fs.FileMode) error{"Write": whole.Write, "Synced": whole.Synced}

// The file holds what's written, with exactly the mode given, whatever the
// umask would take, in place of the file there; nothing's left beside it.
func TestWritesWithTheModeGiven(t *testing.T) {
	for name, write := range writers {
		dir := t.TempDir()
		path := filepath.Join(dir, "file")
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := write(path, []byte("new"), 0o664); err != nil {
			t.Fatalf("%s() = %v", name, err)
		}
		data, _ := os.ReadFile(path)
		info, _ := os.Stat(path)
		if string(data) != "new" || info.Mode().Perm() != 0o664 {
			t.Errorf("%s(): the file holds %q, mode %v", name, data, info.Mode().Perm())
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%s() left %v", name, entries)
		}
	}
}

// A link is replaced by the file, what it led to left as it was.
func TestReplacesALink(t *testing.T) {
	for name, write := range writers {
		dir := t.TempDir()
		target, path := filepath.Join(dir, "target"), filepath.Join(dir, "file")
		if err := os.WriteFile(target, []byte("theirs"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if err := write(path, []byte("new"), 0o600); err != nil {
			t.Fatalf("%s() = %v", name, err)
		}
		info, _ := os.Lstat(path)
		theirs, _ := os.ReadFile(target)
		if !info.Mode().IsRegular() || string(theirs) != "theirs" {
			t.Errorf("%s(): the path is %v, what it led to holds %q", name, info.Mode(), theirs)
		}
	}
}

// A write that fails leaves what was there, and nothing beside it.
func TestAFailedWriteLeavesWhatWasThere(t *testing.T) {
	for name, write := range writers {
		dir := t.TempDir()
		path := filepath.Join(dir, "folder")
		if err := os.MkdirAll(filepath.Join(path, "inside"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := write(path, []byte("new"), 0o600); err == nil {
			t.Errorf("%s() over a folder holding something succeeded", name)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%s() left %v", name, entries)
		}
		if _, err := os.Stat(filepath.Join(path, "inside")); err != nil {
			t.Errorf("%s(): the folder's gone: %v", name, err)
		}
	}
}
