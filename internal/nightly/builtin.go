package nightly

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// The clean-up's ages: an item untouched for a week goes; a Claude Code
// session's folder, for 30 days, as sessions can sit idle for weeks and be
// resumed.
const (
	scratchKeep = 7 * 24 * time.Hour
	sessionKeep = 30 * 24 * time.Hour
)

// captureTimeout is how long settings capture may take: seconds, usually;
// a hang (Dropbox, no Full Disk Access) is cut off so it can't hold up the
// next night's run.
const captureTimeout = 15 * time.Minute

// The built-in jobs' names, as their steps and the record name them.
const (
	CleanScratchJob    = "clean-scratch"
	CaptureSettingsJob = "capture-settings"
)

// CleanScratch is the job clearing tmp, the Scratch volume's throwaway
// folder, of what's untouched for a week. Claude Code's own folder there
// (claude-<uid>, always in use) is judged a session at a time,
// claude-<uid>/<project>/<session>, over 30 days, a session staying while
// its transcript in home's ~/.claude/projects changed in that time; a
// project's folder goes once it's empty. Nothing to do when tmp isn't
// there: the scratch check says so.
func CleanScratch(tmp, home string, uid int, now func() time.Time) Job {
	return Job{Name: CleanScratchJob, Title: "Scratch clean-up", Run: func(context.Context) error {
		if info, err := os.Stat(tmp); err != nil || !info.IsDir() {
			return nil
		}
		var errs []error
		remove := func(path string) {
			if err := os.RemoveAll(path); err != nil {
				errs = append(errs, err)
			}
		}
		claude := filepath.Join(tmp, fmt.Sprintf("claude-%d", uid))
		sessionsBefore := now().Add(-sessionKeep)
		projects, _ := os.ReadDir(claude)
		for _, p := range projects {
			if !p.IsDir() {
				continue
			}
			project := filepath.Join(claude, p.Name())
			sessions, _ := os.ReadDir(project)
			for _, s := range sessions {
				if !s.IsDir() {
					continue
				}
				transcript := filepath.Join(home, ".claude", "projects", p.Name(), s.Name()+".jsonl")
				if info, err := os.Stat(transcript); err == nil && info.ModTime().After(sessionsBefore) {
					continue
				}
				if session := filepath.Join(project, s.Name()); !touchedSince(session, sessionsBefore) {
					remove(session)
				}
			}
			// Only once empty.
			_ = os.Remove(project)
		}
		entries, err := os.ReadDir(tmp)
		if err != nil {
			return err
		}
		before := now().Add(-scratchKeep)
		for _, e := range entries {
			if entry := filepath.Join(tmp, e.Name()); !touchedSince(entry, before) {
				remove(entry)
			}
		}
		return errors.Join(errs...)
	}}
}

// touchedSince reports whether anything at path, or under it, changed
// since t: links aren't followed.
func touchedSince(path string, t time.Time) bool {
	touched := false
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(t) {
			touched = true
			return filepath.SkipAll
		}
		return nil
	})
	return touched
}

// CaptureSettings is the job capturing app settings: prefsync capture,
// niced, stopped after 15 minutes. prefsync says how it went in its last
// line.
func CaptureSettings(run runner.Runner) Job {
	return Job{Name: CaptureSettingsJob, Title: "Settings capture", Run: func(ctx context.Context) error {
		res, err := run.Run(ctx, runner.Command{Name: "nice", Args: []string{"-n", "10", "prefsync", "capture"}, Timeout: captureTimeout})
		if err == nil {
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("stopped after %d minutes without finishing", int(captureTimeout.Minutes()))
		}
		if _, exited := errors.AsType[*runner.ExitError](err); exited {
			for _, out := range [][]byte{res.Stdout, res.Stderr} {
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
					return errors.New(last)
				}
			}
		}
		return err
	}}
}
