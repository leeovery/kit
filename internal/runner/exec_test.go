package runner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
