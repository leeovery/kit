package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// minRsync is the oldest rsync that copies: 3.2.4 is the first to pass a
// path to the remote's shell safely without being asked, spaces and all, as
// Application Support's have.
var minRsync = []int{3, 2, 4}

// partialDir is the folder, in each folder copied into, that keeps what's
// been copied of a file not finished, for a copy run again to carry on from:
// rsync's own name for it, which rsync leaves out of what it copies.
const partialDir = ".rsync-partial"

// ioTimeout ends a copy that's sent and received nothing for so long, as an
// SSH connection gone quiet (rsync's --timeout, its exit 30): the runner's
// own timeout can't tell a long copy from a stuck one.
const ioTimeout = 10 * time.Minute

// mostRecopied is how many of the paths the second pass copied again it
// names.
const mostRecopied = 20

// vanished is rsync's exit when files went from the source as it copied.
const vanished = 24

// ErrVanished is in the error of a copy some of whose files went from the
// source as it copied them, rsync's exit 24: from a Mac in use, where files
// come and go, it isn't a failure.
var ErrVanished = errors.New("files vanished from the source as they were copied")

// exitSays is what each of rsync's exits means, in its manual's words, and
// its own for those its manual leaves out.
var exitSays = map[int]string{
	1:   "syntax or usage error",
	2:   "protocol incompatibility",
	3:   "errors selecting input/output files, dirs",
	4:   "requested action not supported",
	5:   "error starting client-server protocol",
	6:   "daemon unable to append to log-file",
	10:  "error in socket I/O",
	11:  "error in file I/O",
	12:  "error in rsync protocol data stream",
	13:  "errors with program diagnostics",
	14:  "error in IPC code",
	20:  "received SIGUSR1 or SIGINT",
	21:  "some error returned by waitpid()",
	22:  "error allocating core memory buffers",
	23:  "partial transfer due to error",
	24:  "partial transfer due to vanished source files",
	25:  "the --max-delete limit stopped deletions",
	30:  "timeout in data send/receive",
	35:  "timeout waiting for daemon connection",
	124: "remote shell failed",
	125: "remote shell killed",
	126: "remote command could not be run",
	127: "remote command not found",
	255: "unexplained error",
}

var (
	rsyncVersion = regexp.MustCompile(`^rsync\s+version\s+v?(\d+)\.(\d+)\.(\d+)`)
	// progressLine is rsync's --info=progress2 line: bytes copied, the
	// percent of all it knows of, the rate and the time left; at the end of a
	// file, how many it's copied (xfr#) and how many it's still to check.
	progressLine = regexp.MustCompile(`^\s*([\d,.]+)\s+(\d+)%\s+\S+\s+\S+(?:\s+\(xfr#(\d+), [a-z]+-chk=\d+/\d+\))?\s*$`)
	statFiles    = regexp.MustCompile(`(?m)^Number of regular files transferred: ([\d,.]+)\s*$`)
	statBytes    = regexp.MustCompile(`(?m)^Total transferred file size: ([\d,.]+) bytes\s*$`)
	// itemized is a line of --itemize-changes: the update (> received, c
	// created, h hard-linked, . unchanged but for attributes), the kind of
	// thing, nine attribute columns, then the path.
	itemized = regexp.MustCompile(`^([<>ch.*])([fdLDS])\S{9} (.+)$`)
)

// Rsync is rsync 3, as kit's PATH finds it, Homebrew's, run through a
// runner.
type Rsync struct {
	run runner.Runner
}

// NewRsync returns rsync, run through run.
func NewRsync(run runner.Runner) Rsync {
	return Rsync{run: run}
}

// Found is the rsync kit's PATH finds.
type Found struct {
	// Version is the version it says it is, as 3.4.1: "" when it isn't
	// rsync 3, or there's none.
	Version string
	// Usable is whether it can copy; Why says why not.
	Usable bool
	Why    string
}

// Find finds the rsync on kit's PATH, and whether it's one that can copy:
// rsync 3, at least minRsync, never macOS's own, openrsync, which keeps no
// extended attributes.
func (r Rsync) Find(ctx context.Context) (Found, error) {
	res, err := r.run.Run(ctx, runner.Command{Name: "rsync", Args: []string{"--version"}})
	if errors.Is(err, runner.ErrNotFound) {
		return Found{Why: "rsync isn't installed"}, nil
	}
	if err != nil {
		return Found{}, fmt.Errorf("ask rsync its version: %w", err)
	}
	return found(string(res.Stdout)), nil
}

// found is what rsync --version's output says of the rsync that printed it.
func found(out string) Found {
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if strings.HasPrefix(first, "openrsync") {
		return Found{Why: "it's macOS's own, openrsync, not rsync 3"}
	}
	m := rsyncVersion.FindStringSubmatch(first)
	if m == nil {
		return Found{Why: fmt.Sprintf("it says it's %q, not rsync 3", first)}
	}
	parts := make([]int, 3)
	for i, s := range m[1:] {
		parts[i], _ = strconv.Atoi(s)
	}
	version := strings.Join(m[1:], ".")
	if parts[0] != 3 || slices.Compare(parts, minRsync) < 0 {
		return Found{Version: version, Why: fmt.Sprintf("it's rsync %s, not rsync %d.%d.%d or later", version, minRsync[0], minRsync[1], minRsync[2])}
	}
	return Found{Version: version, Usable: true}
}

// Source is where a path is copied from: this Mac, as a snapshot mounted on
// it or a folder Arq restored into, or another Mac, over SSH.
type Source struct {
	// Path is the path there, absolute.
	Path string
	// Host is the Mac it's on, as SSH reaches it: "" for this Mac.
	Host string
	// Shell is the command rsync reaches Host with, ssh and its options
	// (rsync's --rsh); Rsync, the path to Host's rsync 3. Both are needed
	// with a Host: Host's rsync by name would be its own openrsync.
	Shell, Rsync string
}

// Copy is a path copied, a folder or a file, from From to To, its place on
// this Mac.
type Copy struct {
	From Source
	// To is where it's copied to, absolute: the folder copied into, or the
	// file.
	To string
	// File is whether it's a file, not a folder.
	File bool
	// Filter is a file of patterns rsync leaves out, a pattern a line
	// (--exclude-from), one starting with / anchored at the folder copied:
	// "" for none.
	Filter string
}

// Stats is what a copy comes to: how many files, and how many bytes of
// them.
type Stats struct {
	Files int
	Bytes int64
}

// Progress is how far a copy has got, as rsync says it.
type Progress struct {
	// Bytes is how much it's copied; Percent, that of all it knows it's to
	// copy so far, which can fall back as it finds more: Size's Bytes is the
	// whole.
	Bytes   int64
	Percent int
	// Files is how many files it's copied.
	Files int
}

// Recopied is what the second pass found different and copied again: how
// many, and the first of their paths, mostRecopied at most.
type Recopied struct {
	Count int
	Paths []string
}

// Size is what copying c would copy: files and bytes, from a dry run, for
// the free space it needs.
func (r Rsync) Size(ctx context.Context, c Copy) (Stats, error) {
	return stated(r.rsync(ctx, c, nil, "--dry-run", "--stats"))
}

// Copy copies c, telling progress how far it's got as rsync says, and
// returns what it copied. A copy stopped part way carries on from where it
// was, run again. Files vanished from the source as it copied fail it with
// ErrVanished, and it still returns what it copied.
func (r Rsync) Copy(ctx context.Context, c Copy, progress func(Progress)) (Stats, error) {
	var read progressReader
	lines := func(line string) {
		if p, ok := read.line(line); ok && progress != nil {
			progress(p)
		}
	}
	// The names of the files copied end each progress line with a new line:
	// the runner gives a line a carriage return draws again only as it ends,
	// and progress2 alone ends none till the copy's done.
	return stated(r.rsync(ctx, c, lines, "--info=name1,progress2", "--stats"))
}

// Compare is the second pass: it compares every file of c's copy with its
// source, by checksum, and copies again those that differ, saying how many
// and which. Files vanished from the source as it compared fail it with
// ErrVanished, and it still says what it copied.
func (r Rsync) Compare(ctx context.Context, c Copy) (Recopied, error) {
	res, err := r.rsync(ctx, c, nil, "--checksum", "--itemize-changes")
	if err != nil && !errors.Is(err, ErrVanished) {
		return Recopied{}, err
	}
	return recopied(res.Stdout), err
}

// rsync runs rsync on c with options, giving its lines to lines, once c's
// destination has its parent folder: rsync makes the folder copied into, but
// not the folders it's in.
func (r Rsync) rsync(ctx context.Context, c Copy, lines func(string), options ...string) (runner.Result, error) {
	args, err := c.args(options)
	if err != nil {
		return runner.Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(c.To)), 0o755); err != nil {
		return runner.Result{}, fmt.Errorf("make the folder %s is copied into: %w", c.To, err)
	}
	res, err := r.run.Run(ctx, runner.Command{Name: "rsync", Args: args, Timeout: runner.Unlimited, Lines: lines})
	return res, failed(err)
}

// args are rsync's arguments for c, with options: archive (which, run by
// anyone but root, keeps no owners), extended attributes and hard links;
// never ACLs, as Time Machine's deny adding files to the folders it marks;
// never deleting.
func (c Copy) args(options []string) ([]string, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	args := []string{"--archive", "--xattrs", "--hard-links", "--partial-dir=" + partialDir, fmt.Sprintf("--timeout=%d", int(ioTimeout.Seconds()))}
	if c.Filter != "" {
		args = append(args, "--exclude-from="+c.Filter)
	}
	if c.From.Host != "" {
		args = append(args, "--rsh="+c.From.Shell, "--rsync-path="+c.From.Rsync)
	}
	args = append(args, options...)
	from, to := filepath.Clean(c.From.Path), filepath.Clean(c.To)
	// With trailing slashes, rsync copies what's in a folder into To, making
	// To if it isn't there, and anchors the filter at it; without, it copies
	// a file as To.
	if !c.File {
		from, to = from+"/", to+"/"
	}
	if c.From.Host != "" {
		from = c.From.Host + ":" + from
	}
	return append(args, from, to), nil
}

// check says what's wrong with c, if anything.
func (c Copy) check() error {
	switch {
	case !filepath.IsAbs(c.From.Path):
		return fmt.Errorf("copy from %q: the path isn't absolute", c.From.Path)
	case !filepath.IsAbs(c.To):
		return fmt.Errorf("copy to %q: the path isn't absolute", c.To)
	case c.From.Host == "":
		return nil
	case strings.HasPrefix(c.From.Host, "-"):
		return fmt.Errorf("copy from %q: that's no host", c.From.Host)
	case c.From.Shell == "" || c.From.Rsync == "":
		return fmt.Errorf("copy from %s: a copy over SSH needs its shell and the host's rsync", c.From.Host)
	}
	return nil
}

// ExitError is rsync exiting other than 0, in its words.
type ExitError struct {
	Code int
	// Says is what the code means, as rsync's manual says it.
	Says string
	// Problem is the first problem rsync printed above its summary, the
	// summary if that's all; More, how many more it printed.
	Problem string
	More    int
	err     *runner.ExitError
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("rsync exited %d, %s", e.Code, e.Says)
	if e.Problem != "" {
		msg += ": " + e.Problem
	}
	if e.More > 0 {
		msg += fmt.Sprintf(" (and %d more)", e.More)
	}
	return msg
}

// Unwrap is the runner's error, with all rsync printed on its standard
// error.
func (e *ExitError) Unwrap() error { return e.err }

// Is has an exit 24 be ErrVanished.
func (e *ExitError) Is(target error) bool { return target == ErrVanished && e.Code == vanished }

// failed is err, rsync's, as an ExitError when rsync exited other than 0.
func failed(err error) error {
	if err == nil {
		return nil
	}
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return fmt.Errorf("rsync: %w", err)
	}
	says, ok := exitSays[exit.Code]
	if !ok {
		says = "an exit rsync doesn't explain"
	}
	e := &ExitError{Code: exit.Code, Says: says, err: exit}
	var problems []string
	for _, line := range exit.Lines() {
		if line != "" && !strings.HasPrefix(line, "rsync error:") && !strings.HasPrefix(line, "rsync warning:") {
			problems = append(problems, line)
		}
	}
	if len(problems) == 0 {
		e.Problem = exit.Stderr
		return e
	}
	e.Problem, e.More = problems[0], len(problems)-1
	return e
}

// progressReader reads progress from a copy's lines, keeping how many files
// it's copied, which rsync says only as each ends.
type progressReader struct {
	files int
}

// line is the progress line says, if it's a progress line.
func (p *progressReader) line(line string) (Progress, bool) {
	m := progressLine.FindStringSubmatch(line)
	if m == nil {
		return Progress{}, false
	}
	if m[3] != "" {
		p.files, _ = strconv.Atoi(m[3])
	}
	percent, _ := strconv.Atoi(m[2])
	return Progress{Bytes: number(m[1]), Percent: percent, Files: p.files}, true
}

// stated is what the statistics of a run of rsync's that ended in err say
// it copied, or would: none, when it failed but for files vanished.
func stated(res runner.Result, err error) (Stats, error) {
	if err != nil && !errors.Is(err, ErrVanished) {
		return Stats{}, err
	}
	stats, statsErr := statsOf(res.Stdout)
	if statsErr != nil {
		return Stats{}, statsErr
	}
	return stats, err
}

// statsOf is what rsync's --stats says it copied, or would.
func statsOf(out []byte) (Stats, error) {
	files, bytes := statFiles.FindSubmatch(out), statBytes.FindSubmatch(out)
	if files == nil || bytes == nil {
		return Stats{}, errors.New("rsync's statistics say nothing of the files it copied")
	}
	return Stats{Files: int(number(string(files[1]))), Bytes: number(string(bytes[1]))}, nil
}

// recopied is what the second pass, itemizing its changes, copied again: a
// file received, or a link or other thing made, but not a folder made, or
// attributes alone set.
func recopied(out []byte) Recopied {
	var r Recopied
	for line := range strings.Lines(string(out)) {
		m := itemized.FindStringSubmatch(strings.TrimRight(line, "\n"))
		if m == nil {
			continue
		}
		update, kind, path := m[1], m[2], m[3]
		if !copiedAgain(update, kind) {
			continue
		}
		switch {
		case update == "h":
			path, _, _ = strings.Cut(path, " => ")
		case kind == "L":
			path, _, _ = strings.Cut(path, " -> ")
		}
		r.Count++
		if len(r.Paths) < mostRecopied {
			r.Paths = append(r.Paths, path)
		}
	}
	return r
}

// copiedAgain reports whether an item itemized as update, of kind, was
// copied again.
func copiedAgain(update, kind string) bool {
	switch update {
	case ">":
		return true
	case "c", "h":
		return kind != "d"
	}
	return false
}

// number is the number rsync printed as s, its thousands separated by
// commas, or by points where the locale's decimals take commas.
func number(s string) int64 {
	n, _ := strconv.ParseInt(strings.NewReplacer(",", "", ".", "").Replace(s), 10, 64)
	return n
}
