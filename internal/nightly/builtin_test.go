package nightly_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// made writes a file at path, its folders made, last changed age ago from
// now; the folders keep an old time, so only the file's age counts.
func made(t *testing.T, root, path string, now time.Time, age time.Duration) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := now.Add(-age)
	if err := os.Chtimes(full, when, when); err != nil {
		t.Fatal(err)
	}
}

// ageFolders backdates every folder under root, so a test's files alone say
// what's been touched.
func ageFolders(t *testing.T, root string, now time.Time) {
	t.Helper()
	old := now.Add(-365 * 24 * time.Hour)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			_ = os.Chtimes(path, old, old)
		}
		return nil
	})
}

func TestCleanScratch(t *testing.T) {
	now := at(5, 3, 0)
	scratch, home := t.TempDir(), t.TempDir()
	day := 24 * time.Hour
	made(t, scratch, "old-build/out.o", now, 10*day)
	made(t, scratch, "recent-build/out.o", now, 2*day)
	made(t, scratch, "mixed/old.o", now, 20*day)
	made(t, scratch, "mixed/new.o", now, time.Hour)
	made(t, scratch, ".hidden-old", now, 9*day)
	// Claude Code's sessions: over 30 days and idle; idle but resumed (its
	// transcript changed); and one inside 30 days.
	made(t, scratch, "claude-501/-proj/idle/notes.txt", now, 40*day)
	made(t, scratch, "claude-501/-proj/resumed/notes.txt", now, 40*day)
	made(t, home, ".claude/projects/-proj/resumed.jsonl", now, 3*day)
	made(t, scratch, "claude-501/-proj/fresh/notes.txt", now, 10*day)
	made(t, scratch, "claude-501/-gone/idle/notes.txt", now, 45*day)
	ageFolders(t, scratch, now)

	job := nightly.CleanScratch(scratch, home, 501, func() time.Time { return now })
	if err := job.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	var left []string
	_ = filepath.WalkDir(scratch, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(scratch, path)
			left = append(left, rel)
		}
		return nil
	})
	slices.Sort(left)
	want := []string{"claude-501/-proj/fresh/notes.txt", "claude-501/-proj/resumed/notes.txt", "mixed/new.o", "mixed/old.o", "recent-build/out.o"}
	if !slices.Equal(left, want) {
		t.Errorf("left %q\nwant %q", left, want)
	}
	if _, err := os.Stat(filepath.Join(scratch, "claude-501", "-gone")); !os.IsNotExist(err) {
		t.Errorf("an emptied project's folder: %v, want it gone", err)
	}
	if job.Name != "clean-scratch" {
		t.Errorf("job = %s", job.Name)
	}
}

func TestCleanScratchWithoutScratch(t *testing.T) {
	job := nightly.CleanScratch(filepath.Join(t.TempDir(), "nothing"), t.TempDir(), 501, time.Now)
	if err := job.Run(t.Context()); err != nil {
		t.Errorf("Run() without Scratch = %v, want nothing done", err)
	}
}

func TestCaptureSettings(t *testing.T) {
	capture := []string{"-n", "10", "prefsync", "capture"}
	fake := runnertest.New(t)
	fake.On("nice", capture...).Prints("checked 214 domains\ncaptured 3 changed domains\n")
	if err := nightly.CaptureSettings(fake).Run(t.Context()); err != nil {
		t.Errorf("Run() = %v", err)
	}
	if c := fake.Commands()[0]; c.Timeout != 15*time.Minute {
		t.Errorf("timeout %v, want 15 minutes", c.Timeout)
	}

	fake = runnertest.New(t)
	fake.On("nice", capture...).Exits(1).Prints("checking\npaused: capture isn't switched on for this Mac\n")
	if err := nightly.CaptureSettings(fake).Run(t.Context()); err == nil || err.Error() != "paused: capture isn't switched on for this Mac" {
		t.Errorf("Run() = %v, want prefsync's last line", err)
	}

	fake = runnertest.New(t)
	fake.On("nice", capture...).Fails(context.DeadlineExceeded)
	if err := nightly.CaptureSettings(fake).Run(t.Context()); err == nil || !strings.Contains(err.Error(), "stopped after 15 minutes") {
		t.Errorf("Run() = %v", err)
	}
}
