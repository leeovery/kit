package runner_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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

// Nothing kit runs has kit's terminal, but an interactive command and kit's
// own sudo: each leads a session of its own, so a program that asks at a
// terminal, as a program's own sudo does, fails at once, where in the
// background of kit's terminal it would stop, unseen, for good.
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

func TestExecRunsAnInteractiveCommandAtTheTerminal(t *testing.T) {
	r, _ := programs(t, map[string]string{"asks": `read answer; echo "got $answer"; echo "to stderr" >&2`})
	var out, errOut bytes.Buffer
	r.Stdin, r.Stdout, r.Stderr = strings.NewReader("yes\n"), &out, &errOut

	res, err := r.Run(t.Context(), runner.Command{Name: "asks", Interactive: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.String() != "got yes\n" || errOut.String() != "to stderr\n" || len(res.Stdout) != 0 {
		t.Errorf("the terminal got %q and %q, the result %q; want everything at the terminal, nothing captured", out.String(), errOut.String(), res.Stdout)
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

// What's streamed is only what's wanted: never a secret's lines, nor an
// interactive command's.
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
