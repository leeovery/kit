// Package logs is the run log: every event of a run, a JSON line each, in a
// file of its own, kept 30 days; and reading it back, for kit log.
package logs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
)

// Retention is how long a run's log is kept.
const Retention = 30 * 24 * time.Hour

// OutputCap is how much of each of a command's outputs a log keeps, unless
// it's verbose: what's past it is noted by its size alone, so hourly runs
// don't fill the disk.
const OutputCap = 64 << 10

// ext is a run log's extension.
const ext = ".jsonl"

// ErrNoLogs is returned by Latest when no run has been logged.
var ErrNoLogs = errors.New("no runs logged yet")

// Record is a log's line: one event, as its fields apply.
type Record struct {
	Time time.Time `json:"time"`
	// Event is what happened: run_started, step_started, step_finished,
	// command or run_finished.
	Event   string       `json:"event"`
	Command string       `json:"command,omitempty"`
	Machine string       `json:"machine,omitempty"`
	Version string       `json:"version,omitempty"`
	Steps   []event.Step `json:"steps,omitempty"`
	Step    string       `json:"step,omitempty"`
	// Result is a finished step's.
	Result *check.Result `json:"result,omitempty"`
	// Exit is a command's exit code: -1 when it didn't run, or was ended.
	Exit       *int                `json:"exit,omitempty"`
	DurationMS int64               `json:"duration_ms,omitempty"`
	Stdout     string              `json:"stdout,omitempty"`
	Stderr     string              `json:"stderr,omitempty"`
	StdoutSize int                 `json:"stdout_bytes,omitempty"`
	StderrSize int                 `json:"stderr_bytes,omitempty"`
	Error      string              `json:"error,omitempty"`
	Counts     map[check.State]int `json:"counts,omitempty"`
}

// File is a run's log, as a sink.
type File struct {
	mu      sync.Mutex
	f       *os.File
	enc     *json.Encoder
	path    string
	verbose bool
	err     error
}

// Open starts the log of a run of command, at now, in dir: a new file,
// named for when and what, as in 2026-01-02T030405.678-status.jsonl, so
// names sort by when. It removes logs older than Retention first. A verbose
// log keeps commands' output whole.
func Open(dir string, now time.Time, command string, verbose bool) (*File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("make the logs directory: %w", err)
	}
	prune(dir, now)
	stem := now.Format("2006-01-02T150405.000") + "-" + command
	for n := 1; ; n++ {
		name := stem + ext
		if n > 1 {
			name = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("start the run's log: %w", err)
		}
		return &File{f: f, enc: json.NewEncoder(f), path: path, verbose: verbose}, nil
	}
}

// prune removes the run logs in dir last written before Retention ago. A
// log it can't remove is left for the next run.
func prune(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		if now.Sub(info.ModTime()) > Retention {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Path is where the log is.
func (l *File) Path() string {
	return l.path
}

// Emit writes e as a line. The first failure to write stops the log, and
// Close returns it.
func (l *File) Emit(e event.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return
	}
	l.err = l.enc.Encode(l.record(e))
}

// record is e as a log's line.
func (l *File) record(e event.Event) Record {
	r := Record{Time: e.At()}
	switch e := e.(type) {
	case event.RunStarted:
		r.Event, r.Command, r.Machine, r.Version, r.Steps = "run_started", e.Command, e.Machine, e.Version, e.Steps
	case event.StepStarted:
		r.Event, r.Step = "step_started", e.Step
	case event.StepFinished:
		result := e.Result
		r.Event, r.Step, r.Result, r.DurationMS = "step_finished", e.Step, &result, e.Duration.Milliseconds()
	case event.CommandRan:
		exit := e.Exit
		r.Event, r.Step, r.Command, r.Exit, r.DurationMS, r.Error = "command", e.Step, e.Command, &exit, e.Duration.Milliseconds(), e.Error
		r.Stdout, r.StdoutSize = l.capped(e.Stdout)
		r.Stderr, r.StderrSize = l.capped(e.Stderr)
	case event.RunFinished:
		r.Event, r.DurationMS, r.Counts = "run_finished", e.Duration.Milliseconds(), e.Counts
	}
	return r
}

// capped is output as the log keeps it, and its full size when that's cut.
func (l *File) capped(output string) (string, int) {
	if l.verbose || len(output) <= OutputCap {
		return output, 0
	}
	return strings.ToValidUTF8(output[:OutputCap], ""), len(output)
}

// Close closes the log, and returns the first error writing it, if any.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.f.Close(); err != nil && l.err == nil {
		l.err = err
	}
	if l.err != nil {
		return fmt.Errorf("write the run's log: %w", l.err)
	}
	return nil
}

// Latest is the newest run log in dir: names sort by when, the newest last.
func Latest(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoLogs
	}
	if err != nil {
		return "", fmt.Errorf("read the logs directory: %w", err)
	}
	latest := ""
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ext) && e.Name() > latest {
			latest = e.Name()
		}
	}
	if latest == "" {
		return "", ErrNoLogs
	}
	return filepath.Join(dir, latest), nil
}

// Read reads the run log at path.
func Read(path string) ([]Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the run's log: %w", err)
	}
	var records []Record
	for n, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), n+1, err)
		}
		records = append(records, r)
	}
	return records, nil
}
