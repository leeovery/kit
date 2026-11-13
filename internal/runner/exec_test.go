package runner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// programs writes shell scripts, by name, into a new directory, and returns
// a runner whose PATH is that directory alone.
func programs(t *testing.T, scripts map[string]string) (runner.Exec, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return runner.Exec{Path: []string{dir}, Env: []string{"PATH=" + dir, "KIT_TEST=given"}, Now: time.Now}, dir
}

// clock keeps a command's time for Exec only as the test says: its time
// limit is up, and the grace kit gives it once ended over, when the test
// ends them, never by themselves.
type clock struct {
	mu     sync.Mutex
	limits []context.CancelFunc
	graces chan grace
}

// grace is a grace kit gave a command it ended: how long, and what ends it
// now, unless kit's stopped it.
type grace struct {
	took time.Duration
	over func()
}

// timed is r keeping time by a clock of the test's, its commands' output
// waited for wait.
func timed(r runner.Exec, wait time.Duration) (runner.Exec, *clock) {
	c := &clock{graces: make(chan grace, 8)}
	return runner.Timed(r, c.limit, c.after, wait), c
}

func (c *clock) limit(ctx context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limits = append(c.limits, cancel)
	return ctx, cancel
}

// up has every time limit given so far run out.
func (c *clock) up() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cancel := range c.limits {
		cancel()
	}
}

func (c *clock) after(d time.Duration, f func()) func() bool {
	const pending, stopped, over = 0, 1, 2
	var state atomic.Int32
	c.graces <- grace{took: d, over: func() {
		if state.CompareAndSwap(pending, over) {
			f()
		}
	}}
	return func() bool { return state.CompareAndSwap(pending, stopped) }
}

// given is the next grace kit gave a command it ended, failing the test
// unless it's as long as want: one that ends nothing, given none. It's
// called from the goroutine reading a command's output, too, so never
// ends the test.
func (c *clock) given(t *testing.T, want time.Duration) grace {
	t.Helper()
	select {
	case g := <-c.graces:
		if g.took != want {
			t.Errorf("given a grace of %v, want %v", g.took, want)
		}
		return g
	case <-time.After(5 * time.Second):
		t.Error("given no grace")
		return grace{over: func() {}}
	}
}

func TestExecRunsAProgramFoundOnItsPath(t *testing.T) {
	r, _ := programs(t, map[string]string{"greet": `echo "hello $1"; echo careful >&2`})

	res, err := r.Run(t.Context(), runner.Command{Name: "greet", Args: []string{"world"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(res.Stdout) != "hello world\n" || string(res.Stderr) != "careful\n" || res.ExitCode != 0 {
		t.Errorf("Run() = %q, %q, exit %d; want what it printed, exit 0", res.Stdout, res.Stderr, res.ExitCode)
	}
}

// A command told it's running is told its process, as it runs, and told
// once, after, that it's exited.
func TestExecTellsACommandItsRunning(t *testing.T) {
	r, dir := programs(t, map[string]string{"own": `echo $$ > "$1"`})
	said := filepath.Join(dir, "pid")
	var events []string
	res, err := r.Run(t.Context(), runner.Command{Name: "own", Args: []string{said}, Running: func(pid int) func() {
		events = append(events, "running "+strconv.Itoa(pid))
		return func() { events = append(events, "exited") }
	}})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := os.ReadFile(said)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"running " + strings.TrimSpace(string(pid)), "exited"}; !slices.Equal(events, want) || strconv.Itoa(res.Group) != strings.TrimSpace(string(pid)) {
		t.Errorf("told %q, its group %d; want %q", events, res.Group, want)
	}
}

func TestExecReportsAFailingExit(t *testing.T) {
	r, _ := programs(t, map[string]string{"fail": "echo partial; echo 'first' >&2; echo 'Error: no such formula' >&2; exit 3"})

	res, err := r.Run(t.Context(), runner.Command{Name: "fail", Args: []string{"some thing"}})
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok || exit.Code != 3 || res.ExitCode != 3 || string(res.Stdout) != "partial\n" {
		t.Fatalf("Run() = %+v, %v; want an ExitError with code 3, and the output", res, err)
	}
	if want := "fail 'some thing' exited 3: Error: no such formula"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestExecFindsOnlyWhatsOnItsPath(t *testing.T) {
	r, dir := programs(t, map[string]string{"here": "echo here"})
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("not a program"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"elsewhere", "plain", filepath.Join(dir, "missing")} {
		res, err := r.Run(t.Context(), runner.Command{Name: name})
		if !errors.Is(err, runner.ErrNotFound) || res.ExitCode != -1 {
			t.Errorf("Run(%s) = exit %d, %v; want ErrNotFound", name, res.ExitCode, err)
		}
	}
	res, err := r.Run(t.Context(), runner.Command{Name: filepath.Join(dir, "here")})
	if err != nil || string(res.Stdout) != "here\n" {
		t.Errorf("Run() of a path = %q, %v; want it run", res.Stdout, err)
	}
	for name, want := range map[string]bool{"here": true, filepath.Join(dir, "here"): true, "elsewhere": false, "plain": false} {
		if got := runner.Has(r, name); got != want {
			t.Errorf("Has(%s) = %v, want %v", name, got, want)
		}
	}
}

func TestExecRunsInItsOwnEnvironmentAndDirectory(t *testing.T) {
	r, dir := programs(t, map[string]string{"show": "pwd; /usr/bin/env"})
	work := t.TempDir()
	t.Setenv("KIT_INHERITED", "leaked")

	res, err := r.Run(t.Context(), runner.Command{Name: "show", Dir: work})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	wantDir, _ := filepath.EvalSymlinks(work)
	if gotDir, _ := filepath.EvalSymlinks(lines[0]); gotDir != wantDir {
		t.Errorf("ran in %s, want %s", lines[0], work)
	}
	env := lines[1:]
	for _, want := range []string{"PATH=" + dir, "KIT_TEST=given"} {
		if !slices.Contains(env, want) {
			t.Errorf("environment %q lacks %s", env, want)
		}
	}
	for _, v := range env {
		if strings.HasPrefix(v, "KIT_INHERITED=") {
			t.Errorf("environment holds %s, which it inherited: want the runner's alone", v)
		}
	}
}

// The environment a runner says a command runs in is the one the program
// sees, through every wrapper: Exec's, a Prepared's variables over it, the
// command's own over those, then its context's; a variable nothing sets,
// unset.
func TestExecSaysTheEnvironmentACommandRunsIn(t *testing.T) {
	exec, _ := programs(t, map[string]string{"show": "/usr/bin/env"})
	exec.Env = append(exec.Env, "TOKEN=the runner's", "ADDED=the runner's")
	added := runner.Prepared(exec, func(cmd runner.Command) runner.Command {
		cmd.Env = append([]string{"ADDED=added", "OTHER=added"}, cmd.Env...)
		return cmd
	})
	r := runner.Streamed(runner.Observed(added, func(context.Context, runner.Report) {}, time.Now), func(context.Context) bool { return true }, func(context.Context, runner.Command, string) {})
	ctx := runner.WithSecretEnv(t.Context(), "TOKEN=the context's")
	cmd := runner.Command{Name: "show", Env: []string{"OTHER=the command's"}}

	env, ok := runner.Environ(ctx, r, cmd)
	res, err := r.Run(ctx, cmd)
	if !ok || err != nil {
		t.Fatalf("Environ() said %t, Run() failed: %v", ok, err)
	}
	seen := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	for _, name := range []string{"KIT_TEST", "TOKEN", "ADDED", "OTHER", "UNSET"} {
		said, saidSet := runner.LookupEnv(env, name)
		got, gotSet := runner.LookupEnv(seen, name)
		if said != got || saidSet != gotSet {
			t.Errorf("%s: the runner says %q (set %t), the program saw %q (set %t)", name, said, saidSet, got, gotSet)
		}
	}
}

// With no variable set, by the runner, the command or its context, a
// command runs in an empty environment, as the runner says, never kit's
// own.
func TestExecRunsInAnEmptyEnvironmentWithNoneSet(t *testing.T) {
	r := runner.Exec{Now: time.Now}
	cmd := runner.Command{Name: "/usr/bin/env"}
	env, ok := runner.Environ(t.Context(), r, cmd)
	res, err := r.Run(t.Context(), cmd)
	if !ok || err != nil {
		t.Fatalf("Environ() said %t, Run() failed: %v", ok, err)
	}
	if env == nil || len(env) != 0 || len(res.Stdout) != 0 {
		t.Errorf("Environ() = %#v; the program saw %q; want both empty", env, res.Stdout)
	}
}

// A variable a command's context sets is set for that command alone, over
// the runner's value and the command's own.
func TestExecSetsTheVariablesItsContextSets(t *testing.T) {
	r, _ := programs(t, map[string]string{"show": "/usr/bin/env"})
	r.Env = append(r.Env, "TOKEN=the runner's")
	ctx := runner.WithSecretEnv(t.Context(), "TOKEN=the context's")

	res, err := r.Run(ctx, runner.Command{Name: "show", Env: []string{"TOKEN=the command's"}})
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	for line := range strings.Lines(string(res.Stdout)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TOKEN="); ok {
			tokens = append(tokens, v)
		}
	}
	if !slices.Equal(tokens, []string{"the context's"}) {
		t.Errorf("TOKEN = %q, want the context's alone", tokens)
	}
	res, err = r.Run(t.Context(), runner.Command{Name: "show"})
	if err != nil || strings.Contains(string(res.Stdout), "the context's") {
		t.Errorf("a command run outside that context got its variable, or failed: %v", err)
	}
}

func TestExecGivesInputAndNothingElse(t *testing.T) {
	r, _ := programs(t, map[string]string{"echoes": "/bin/cat"})

	res, err := r.Run(t.Context(), runner.Command{Name: "echoes", Input: "a secret\n"})
	if err != nil || string(res.Stdout) != "a secret\n" {
		t.Errorf("Run() with input = %q, %v; want the input echoed", res.Stdout, err)
	}
	res, err = r.Run(t.Context(), runner.Command{Name: "echoes"})
	if err != nil || len(res.Stdout) != 0 {
		t.Errorf("Run() without input = %q, %v; want nothing to read", res.Stdout, err)
	}
}

func TestExecEndsACommandThatRunsTooLong(t *testing.T) {
	// sleep, started in the background, outlives a shell ended alone, holding
	// the command's output open. A second is long enough for the shell to
	// start it.
	r, _ := programs(t, map[string]string{"slow": "/bin/sleep 10 & wait"})

	start := time.Now()
	res, err := r.Run(t.Context(), runner.Command{Name: "slow", Timeout: time.Second})
	if !errors.Is(err, context.DeadlineExceeded) || res.ExitCode != -1 {
		t.Errorf("Run() = exit %d, %v; want the deadline exceeded", res.ExitCode, err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("Run() took %v, want it ended at its timeout, with everything it started", took)
	}
}

func TestExecEndsACommandWhenItsContextIsDone(t *testing.T) {
	r, _ := programs(t, map[string]string{"slow": `/bin/sleep 10 & echo started > "$1"; wait`})
	marker := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(t.Context())
	var cancelled time.Time
	go func() {
		for {
			if _, err := os.Stat(marker); err == nil {
				cancelled = time.Now()
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	_, err := r.Run(ctx, runner.Command{Name: "slow", Args: []string{marker}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error = %v, want it cancelled", err)
	}
	if took := time.Since(cancelled); took > 2*time.Second {
		t.Errorf("Run() returned %v after its context was done, want it ended then, with everything it started", took)
	}
}

// A command ended, at its time limit or as the run stops, is sent SIGTERM
// first, it and what it started, for it to clean up, as git takes its lock
// away, even what it started that outlives it, its output elsewhere; one
// that ignores it is killed, with what it started, once its grace is up;
// and what it started that ignores it is killed then too, though the
// command's gone.
func TestExecEndsACommandWithSIGTERMFirst(t *testing.T) {
	dir := t.TempDir()
	lock, child, left := filepath.Join(dir, "index.lock"), filepath.Join(dir, "child"), filepath.Join(dir, "left")
	inner := filepath.Join(dir, "inner.lock")
	r, _ := programs(t, map[string]string{
		"wraps":    `/bin/sh -c 'trap "/bin/sleep 0.05; /bin/rm -f \"\$1\"; exit 143" TERM; : > "$1"; while :; do /bin/sleep 0.1; done' - "$1" > /dev/null 2>&1 & until [ -e "$1" ]; do :; done; echo ready; wait`,
		"tidy":     `: > "$1"; trap '/bin/rm -f "$1"; exit 143' TERM; echo ready; while :; do /bin/sleep 0.1; done`,
		"stubborn": `trap '' TERM; /bin/sleep 30 & echo $! > "$1"; echo ready; wait`,
		"leaves":   `/bin/sh -c 'trap "" TERM; echo $$ > "$1"; exec /bin/sleep 30' - "$1" > /dev/null 2>&1 & until [ -s "$1" ]; do :; done; echo ready; wait`,
	})
	r, clock := timed(r, 2*time.Second)
	if took := endOnceReady(t, r, nil, "tidy", lock); took > time.Second {
		t.Errorf("tidy took %v to end, want it ended at once", took)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock is left (%v): want SIGTERM, and the lock taken away", err)
	}
	clock.given(t, 2*time.Second)

	endOnceReady(t, r, func() { clock.given(t, 2*time.Second).over() }, "stubborn", child)
	if pid, runs := stillRuns(t, child, 2*time.Second); runs {
		t.Errorf("what it started, ignoring SIGTERM, still runs (%d), want its group killed", pid)
	}

	endOnceReady(t, r, nil, "wraps", inner)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(inner); errors.Is(err, os.ErrNotExist) {
			break
		}
	}
	if _, err := os.Stat(inner); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock what it started holds is left (%v): want it given its SIGTERM's grace, though the command's gone", err)
	}
	clock.given(t, 2*time.Second)

	endOnceReady(t, r, nil, "leaves", left)
	clock.given(t, 2*time.Second).over()
	if pid, runs := stillRuns(t, left, 2*time.Second); runs {
		t.Errorf("what it left, ignoring SIGTERM, still runs (%d), want it killed once its grace is up", pid)
	}
}

// A command that exits by itself, a child it left holding its output, has
// that output waited for as long as any command's, no longer: the grace
// kit gives a command it ends isn't added to it.
func TestExecWaitsForTheOutputOfACommandThatExits(t *testing.T) {
	r, _ := programs(t, map[string]string{"exits": "/bin/sleep 5 & exit 0"})
	const wait = 500 * time.Millisecond
	r, _ = timed(r, wait)
	start := time.Now()
	if _, err := r.Run(t.Context(), runner.Command{Name: "exits"}); err == nil {
		t.Error("Run() = nil, want its output's wait to have run out")
	}
	// With the grace, 2s, added, it would take longer.
	if took := time.Since(start); took < wait || took > wait+time.Second {
		t.Errorf("Run() took %v, want the output waited for %v", took, wait)
	}
}

// endOnceReady runs the program called name in r, ending it once it says
// it's ready, then calling ended, when given, before it's read anything
// more: how long it took to end then.
func endOnceReady(t *testing.T, r runner.Exec, ended func(), name string, args ...string) time.Duration {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var at time.Time
	ready := func(line string) {
		if line == "ready" {
			at = time.Now()
			cancel()
			if ended != nil {
				ended()
			}
		}
	}
	if _, err := r.Run(ctx, runner.Command{Name: name, Args: args, Lines: ready}); !errors.Is(err, context.Canceled) {
		t.Errorf("Run(%s) = %v, want it ended", name, err)
	}
	return time.Since(at)
}

// stillRuns reports whether the process whose pid is in file still runs,
// once given as long as within to go, and its pid.
func stillRuns(t *testing.T, file string, within time.Duration) (int, bool) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	// Killed, it's there till launchd reaps it, which can take a moment.
	for deadline := time.Now().Add(within); pid != 0 && syscall.Kill(pid, 0) == nil && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	return pid, pid == 0 || syscall.Kill(pid, 0) == nil
}

// kit's own sudo, once ended, is sent SIGTERM, which sudo passes on to
// what it runs as root, and which kit can't signal itself; ignored, as long
// as kit waits for a command's output once ended, the group's killed.
func TestExecEndsKitsOwnSudoThroughSudo(t *testing.T) {
	dir := t.TempDir()
	relayed, child := filepath.Join(dir, "relayed"), filepath.Join(dir, "child")
	r, _ := programs(t, map[string]string{
		"sudo":     `/bin/sleep 30 & pid=$!; trap 'echo relayed > "$2"; kill $pid; exit 143' TERM; echo ready; wait`,
		"stubborn": `trap '' TERM; /bin/sleep 30 & echo $! > "$2"; echo ready; wait`,
	})
	// Unlike any other command's grace, 2s.
	const wait = 3 * time.Second
	r, clock := timed(r, wait)
	if took := endOnceReady(t, r, nil, "sudo", "-n", relayed); took > wait {
		t.Errorf("sudo wasn't ended at once (%v)", took)
	}
	if _, err := os.Stat(relayed); err != nil {
		t.Errorf("sudo wasn't sent SIGTERM to pass on (%v)", err)
	}
	clock.given(t, wait)

	if err := os.Rename(filepath.Join(r.Path[0], "stubborn"), filepath.Join(r.Path[0], "sudo")); err != nil {
		t.Fatal(err)
	}
	endOnceReady(t, r, func() { clock.given(t, wait).over() }, "sudo", "-n", child)
	if pid, runs := stillRuns(t, child, 2*time.Second); runs {
		t.Errorf("what sudo ran, ignoring SIGTERM, still runs (%d), want its group killed", pid)
	}
}

// A command kit runs detached says the process group it ran in, its own.
func TestExecSaysTheGroupACommandRanIn(t *testing.T) {
	r, _ := programs(t, map[string]string{"group": "echo $$"})
	res, err := r.Run(t.Context(), runner.Command{Name: "group"})
	if pid, _ := strconv.Atoi(strings.TrimSpace(string(res.Stdout))); err != nil || pid == 0 || res.Group != pid {
		t.Errorf("Run() = group %d, %v; want the group it led, %q", res.Group, err, res.Stdout)
	}
}

// A command that fails on its own as its time limit runs out, kit not
// having ended it, says how it exited; one that does as the run stops says
// it was stopped: here each as its last line's read, once it's exited.
func TestExecSaysAnExitKitDidntCause(t *testing.T) {
	r, _ := programs(t, map[string]string{"fails": "printf bye; exit 3"})
	r, clock := timed(r, 2*time.Second)
	res, err := r.Run(t.Context(), runner.Command{Name: "fails", Lines: func(string) { clock.up() }})
	if exit, ok := errors.AsType[*runner.ExitError](err); !ok || exit.Code != 3 || res.ExitCode != 3 {
		t.Errorf("Run(), its limit up = exit %d, %v; want exit 3", res.ExitCode, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := r.Run(ctx, runner.Command{Name: "fails", Lines: func(string) { cancel() }}); !errors.Is(err, context.Canceled) {
		t.Errorf("Run(), the run stopping = %v; want it stopped", err)
	}
}

// terminalCheck prints whether the program has a terminal to ask at: one
// it can open, as sudo opens /dev/tty to ask for a password.
const terminalCheck = `if (exec 3</dev/tty) 2>/dev/null; then echo terminal; else echo none; fi`

// leads reports whether the process pid leads a session of its own, and
// with it a process group: one without kit's terminal.
func leads(pid int) bool {
	sid, err := syscall.Getsid(pid)
	pgid, groupErr := syscall.Getpgid(pid)
	return err == nil && groupErr == nil && sid == pid && pgid == pid
}

// inKitsSession reports whether the process pid is in this process's
// session, leading a process group of its own.
func inKitsSession(pid int) bool {
	sid, err := syscall.Getsid(pid)
	own, ownErr := syscall.Getsid(0)
	pgid, groupErr := syscall.Getpgid(pid)
	return err == nil && ownErr == nil && groupErr == nil && sid == own && sid != pid && pgid == pid
}

// whereRun runs cmd, a program that prints its process id, then waits, and
// reports, while it runs, whether it leads a session of its own, or is in
// kit's, leading a process group there.
func whereRun(t *testing.T, r runner.Exec, cmd runner.Command) (leader, inSession bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cmd.Lines = func(line string) {
		if pid, err := strconv.Atoi(line); err == nil {
			leader, inSession = leads(pid), inKitsSession(pid)
			cancel()
		}
	}
	if _, err := r.Run(ctx, cmd); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(%s) = %v, want it ended once it had said where it runs", cmd, err)
	}
	return leader, inSession
}

// kit's own sudo, which never asks at the terminal, stays in kit's session,
// where sudo holds the password kit gave it; any other sudo runs as any
// program does, in a session of its own.
func TestExecRunsKitsOwnSudoInKitsSession(t *testing.T) {
	r, _ := programs(t, map[string]string{"sudo": "echo $$; exec /bin/sleep 10"})
	for _, tc := range []struct {
		args []string
		kits bool
	}{
		{[]string{"-n", "true"}, true},
		{[]string{"-S", "-p", "ASK>", "-v"}, true},
		{[]string{"-A", "true"}, true},
		{[]string{"true"}, false},
		{[]string{"-u", "someone", "-n", "true"}, false},
	} {
		leader, inSession := whereRun(t, r, runner.Command{Name: "sudo", Args: tc.args})
		if inSession != tc.kits || leader == tc.kits {
			t.Errorf("sudo %q: in kit's session %v, leading its own %v; want in kit's %v", tc.args, inSession, leader, tc.kits)
		}
	}
}

// A program sharing kit's session, as Homebrew does for sudo, and op for
// 1Password's approval, runs in kit's session, its own process group's
// leader, where sudo holds the password kit gave it.
func TestExecRunsWhatSharesItsSessionInKitsSession(t *testing.T) {
	r, _ := programs(t, map[string]string{"brew": "echo $$; exec /bin/sleep 10"})
	for _, shares := range []bool{true, false} {
		leader, inSession := whereRun(t, r, runner.Command{Name: "brew", Args: []string{"install"}, SharesSession: shares})
		if inSession != shares || leader == shares {
			t.Errorf("sharing its session %v: in kit's session %v, leading its own %v", shares, inSession, leader)
		}
	}
}

// Nothing kit runs has kit's terminal, but kit's own sudo: each leads a
// session of its own, so a program that asks at a terminal, as a program's
// own sudo does, fails at once, where in the background of kit's terminal
// it would stop, unseen, for good.
func TestExecRunsACommandWithoutKitsTerminal(t *testing.T) {
	r, _ := programs(t, map[string]string{"alone": "echo $$; " + terminalCheck + "; exec /bin/sleep 10"})
	ctx, cancel := context.WithCancel(t.Context())
	var said []string
	leader := false

	_, err := r.Run(ctx, runner.Command{Name: "alone", Lines: func(line string) {
		said = append(said, line)
		if pid, err := strconv.Atoi(line); err == nil {
			leader = leads(pid)
		}
		if line == "none" || line == "terminal" {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) || !leader || !slices.Contains(said, "none") {
		t.Errorf("Run() = %v, said %q, leading a session of its own %v; want no terminal to ask at", err, said, leader)
	}
}

// A command kit answers runs without kit's terminal too, but kit's own
// sudo, answered on its input.
func TestExecAnswersACommandWithoutKitsTerminal(t *testing.T) {
	asks := `echo $$ > "$PIDFILE"; ` + terminalCheck + `; printf 'ASK>' >&2; read a; echo "$a"`
	r, _ := programs(t, map[string]string{"asks": asks, "sudo": asks})
	for _, tc := range []struct {
		cmd  runner.Command
		kits bool
	}{
		{runner.Command{Name: "asks"}, false},
		{runner.Command{Name: "sudo", Args: []string{"-S", "-p", "ASK>", "-v"}}, true},
	} {
		marker := filepath.Join(t.TempDir(), "pid")
		var leader, inSession bool
		tc.cmd.Env, tc.cmd.Asks = []string{"PIDFILE=" + marker}, "ASK>"
		tc.cmd.Answer = func(context.Context, int) (string, error) {
			data, err := os.ReadFile(marker)
			if err != nil {
				return "", err
			}
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				leader, inSession = leads(pid), inKitsSession(pid)
			}
			return "given", nil
		}
		res, err := r.Run(t.Context(), tc.cmd)
		if err != nil || !strings.HasSuffix(string(res.Stdout), "given\n") || inSession != tc.kits || leader == tc.kits {
			t.Errorf("%s: %q, %v, in kit's session %v, leading its own %v; want the answer, in kit's %v", tc.cmd, res.Stdout, err, inSession, leader, tc.kits)
		}
		if !tc.kits && string(res.Stdout) != "none\ngiven\n" {
			t.Errorf("%s printed %q, want no terminal to ask at", tc.cmd, res.Stdout)
		}
	}
}

// A command's lines are given as it prints them, its output and its errors,
// a line a carriage return starts again given as it ends; all it printed is
// still kept.
func TestExecGivesLinesAsTheyCome(t *testing.T) {
	r, _ := programs(t, map[string]string{"install": `echo "==> Fetching"; printf '10%%\r50%%\r100%%\n'; echo careful >&2; printf 'no newline'`})
	var got []string
	res, err := r.Run(t.Context(), runner.Command{Name: "install", Lines: func(line string) { got = append(got, line) }})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"100%", "==> Fetching", "careful", "no newline"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if string(res.Stdout) != "==> Fetching\n10%\r50%\r100%\nno newline" || string(res.Stderr) != "careful\n" {
		t.Errorf("Run() = %q, %q; want all it printed kept", res.Stdout, res.Stderr)
	}
}

// A carriage return then a new line ends the line, as it does at a
// terminal; one before anything else starts it again, text drawn before it
// replaced, even when what comes next is printed a while later.
func TestExecGivesLinesAsATerminalShowsThem(t *testing.T) {
	r, _ := programs(t, map[string]string{"install": `printf 'ended\r\n'; printf '10%%\r'; /bin/sleep 0.2; printf '50%%\r'; /bin/sleep 0.2; printf '\n'; printf 'drawn\r'; /bin/sleep 0.2; printf 'redrawn\n'`})
	var got []string
	if _, err := r.Run(t.Context(), runner.Command{Name: "install", Lines: func(line string) { got = append(got, line) }}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ended", "50%", "redrawn"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// Asked, each line a carriage return ends is given too, as a progress bar
// draws it, as it's drawn; a carriage return then a new line ends a line
// once.
func TestExecGivesRedrawnLinesWhenAsked(t *testing.T) {
	r, _ := programs(t, map[string]string{"copy": `printf 'ended\r\n'; printf '\r10%%'; /bin/sleep 0.2; printf '\r50%%'; /bin/sleep 0.2; printf '\n'; printf 'drawn\r'; printf 'redrawn\n'`})
	var got []string
	if _, err := r.Run(t.Context(), runner.Command{Name: "copy", Redraws: true, Lines: func(line string) { got = append(got, line) }}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ended", "10%", "50%", "drawn", "redrawn"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// Asked, what a command prints on its output is given to Lines alone, none
// of it kept; what it prints on its errors is kept as ever.
func TestExecKeepsOnlyLinesWhenAsked(t *testing.T) {
	r, _ := programs(t, map[string]string{"copy": `echo one; echo two; echo careful >&2`})
	var got []string
	res, err := r.Run(t.Context(), runner.Command{Name: "copy", LinesOnly: true, Lines: func(line string) { got = append(got, line) }})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"careful", "one", "two"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if len(res.Stdout) != 0 || string(res.Stderr) != "careful\n" {
		t.Errorf("Run() = %q, %q; want no output kept, its errors kept", res.Stdout, res.Stderr)
	}
}

// What's streamed is only what's wanted: never a secret's lines.
func TestStreamedGivesWhatsWanted(t *testing.T) {
	r, _ := programs(t, map[string]string{"say": "echo said"})
	var got []string
	streamed := runner.Streamed(r, func(ctx context.Context) bool { return ctx.Value(wanted{}) != nil }, func(_ context.Context, cmd runner.Command, line string) {
		got = append(got, cmd.Name+": "+line)
	})
	ctx := context.WithValue(t.Context(), wanted{}, true)
	for _, c := range []struct {
		ctx context.Context
		cmd runner.Command
	}{
		{ctx, runner.Command{Name: "say"}},
		{t.Context(), runner.Command{Name: "say", Args: []string{"unwanted"}}},
		{ctx, runner.Command{Name: "say", Secret: true}},
	} {
		if _, err := streamed.Run(c.ctx, c.cmd); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(got, []string{"say: said"}) {
		t.Errorf("lines = %q, want the wanted command's alone", got)
	}
}

type wanted struct{}

// A command that asks on its standard error is answered on its standard
// input, each time it asks, a prompt split across its writes included; what
// it printed comes back without the prompts.
func TestExecAnswersWhatACommandAsks(t *testing.T) {
	e, _ := programs(t, map[string]string{
		"asks": `printf 'ASK' >&2; /bin/sleep 0.1; printf '>' >&2; read a; printf 'Sorry, try again.\nASK>' >&2; read b; echo "$a $b"`,
	})
	var asked []int
	res, err := e.Run(t.Context(), runner.Command{Name: "asks", Asks: "ASK>", Answer: func(_ context.Context, n int) (string, error) {
		asked = append(asked, n)
		return []string{"first", "second"}[n], nil
	}})
	if err != nil || string(res.Stdout) != "first second\n" || string(res.Stderr) != "Sorry, try again.\n" || !slices.Equal(asked, []int{0, 1}) {
		t.Errorf("Run = %q, %q, %v; asked %v", res.Stdout, res.Stderr, err, asked)
	}
}

// An answer not given ends the command's input.
func TestExecEndsTheInputOfACommandNotAnswered(t *testing.T) {
	e, _ := programs(t, map[string]string{"asks": `printf 'ASK>' >&2; read a || exit 3; echo "$a"`})
	notGiven := errors.New("cancelled")
	res, err := e.Run(t.Context(), runner.Command{Name: "asks", Asks: "ASK>", Answer: func(context.Context, int) (string, error) {
		return "", notGiven
	}})
	if exit, ok := errors.AsType[*runner.ExitError](err); !ok || exit.Code != 3 || len(res.Stdout) != 0 {
		t.Errorf("Run = %q, %v; want it ended, exit 3", res.Stdout, err)
	}
}

// A command that ends while its answer's being got, as sudo does once its
// own time to wait for a password is up, fails saying the answer came too
// late, as well as how it exited; one answered in time doesn't.
func TestExecTellsOfAnAnswerThatCameTooLate(t *testing.T) {
	e, _ := programs(t, map[string]string{"asks": `printf 'ASK>' >&2; exit 1`})
	res, err := e.Run(t.Context(), runner.Command{Name: "asks", Asks: "ASK>", Answer: func(context.Context, int) (string, error) {
		time.Sleep(300 * time.Millisecond)
		return "late", nil
	}})
	if exit, ok := errors.AsType[*runner.ExitError](err); !ok || exit.Code != 1 || !errors.Is(err, runner.ErrAnsweredLate) || res.ExitCode != 1 {
		t.Errorf("Run = exit %d, %v; want exit 1, the answer too late", res.ExitCode, err)
	}
	e, _ = programs(t, map[string]string{"asks": `printf 'ASK>' >&2; read a; exit 1`})
	if _, err := e.Run(t.Context(), runner.Command{Name: "asks", Asks: "ASK>", Answer: func(context.Context, int) (string, error) {
		return "wrong", nil
	}}); errors.Is(err, runner.ErrAnsweredLate) {
		t.Errorf("Run = %v; want an answer heard, and refused, not too late", err)
	}
}

// A command given no time limit runs as long as it takes, till its context
// is done.
func TestExecRunsAnUnlimitedCommandAsLongAsItTakes(t *testing.T) {
	e, _ := programs(t, map[string]string{"slow": "/bin/sleep 0.3"})
	if _, err := e.Run(t.Context(), runner.Command{Name: "slow", Timeout: runner.Unlimited}); err != nil {
		t.Errorf("Run = %v; want it run to its end", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := e.Run(ctx, runner.Command{Name: "slow", Timeout: runner.Unlimited}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v; want it ended with its context", err)
	}
}
