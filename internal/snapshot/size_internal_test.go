package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Sizing ends when its time's up, whatever a reader's waiting on, as a
// file in iCloud alone: the folder it's in says as much as was counted,
// not whole, and the rest as they stand.
func TestSizingEndsOnTime(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"Desktop/stuck", "Code"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "Code", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stuck := make(chan struct{})
	defer close(stuck)
	readDir := func(dir string) ([]os.DirEntry, error) {
		if filepath.Base(dir) == "stuck" {
			<-stuck
		}
		return os.ReadDir(dir)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	done := make(chan []Folder, 1)
	go func() { done <- sizeAll(ctx, home, []Folder{{Path: "~/Desktop"}, {Path: "~/Code"}}, readDir) }()
	select {
	case got := <-done:
		if len(got) != 2 || got[0].Whole || !got[1].Whole || got[1].Size == 0 {
			t.Errorf("folders = %+v, want the Desktop not whole, Code whole", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sizing waited for a reader stuck past its time")
	}
}
