// Package logs is the run log: every event of a run, a JSON line each, in a
// file of its own, kept 30 days; and reading it back, for kit log.
package logs

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	// Only are the steps a run was of, when it was of steps named.
	Only []string `json:"only,omitempty"`
	Step string   `json:"step,omitempty"`
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
	// A command's output, as it comes, is in its command's line whole; a run
	// getting ready is in its start.
	switch e.(type) {
	case event.Output, event.Preparing:
		return
	}
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
		r.Event, r.Command, r.Machine, r.Version, r.Steps, r.Only = "run_started", e.Command, e.Machine, e.Version, e.Steps, e.Only
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

// Run is a logged run, as its log begins and ends: where its log is, how it
// started, and how it finished, when it did.
type Run struct {
	Path    string
	Started Record
	// Finished is the run's last record: run_finished when it finished.
	Finished Record
}

// Done is whether the run finished.
func (r Run) Done() bool { return r.Finished.Event == "run_finished" }

// Of is what the run was of: its command, after kit, with the steps it was
// of, if it was of steps named, as in status brew; kit alone, kit's home.
func (r Run) Of() string {
	return strings.Join(append([]string{cmp.Or(r.Started.Command, "kit")}, r.Started.Only...), " ")
}

// Runs are the runs logged in dir, newest first: each from its start, read
// line by line from the log's beginning (a run can run commands before it
// starts), and its last line, read from its end, so a long log is quick to
// list. A log with no start, as kit list's, isn't a run.
func Runs(dir string) ([]Run, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the logs directory: %w", err)
	}
	var runs []Run
	for _, e := range slices.Backward(entries) {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if run, ok := readRun(path); ok {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

// readRun is the run logged at path, when it started.
func readRun(path string) (Run, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Run{}, false
	}
	defer func() { _ = f.Close() }()
	start, ok := started(bufio.NewReaderSize(f, 64<<10))
	if !ok {
		return Run{}, false
	}
	return Run{Path: path, Started: start, Finished: last(f)}, true
}

// startLine is how a run's start begins, after its time: a record's event
// follows its time, so it's in a line's first hundred bytes.
var startLine = []byte(`"event":"run_started"`)

// started is the run's start, the first run_started line in what r reads:
// of other lines, only as much is read as says what they are.
func started(r *bufio.Reader) (Record, bool) {
	for {
		line, err := r.ReadSlice('\n')
		if bytes.Contains(line[:min(len(line), 100)], startLine) {
			whole := bytes.Clone(line)
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = r.ReadSlice('\n')
				whole = append(whole, line...)
			}
			var start Record
			return start, json.Unmarshal(whole, &start) == nil
		}
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = r.ReadSlice('\n')
		}
		if err != nil {
			return Record{}, false
		}
	}
}

// last is the last record in f, read from its end: none when it's longer
// than a run's end ever is, as a command's whole output, when the run didn't
// finish.
func last(f *os.File) Record {
	var end Record
	info, err := f.Stat()
	if err != nil {
		return end
	}
	from := max(info.Size()-8<<10, 0)
	tail := make([]byte, info.Size()-from)
	if _, err := f.ReadAt(tail, from); err != nil && !errors.Is(err, io.EOF) {
		return end
	}
	tail = bytes.TrimRight(tail, "\n")
	_ = json.Unmarshal(tail[bytes.LastIndexByte(tail, '\n')+1:], &end)
	return end
}
