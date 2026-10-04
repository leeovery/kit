package runner

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// waitDelay is how long a command's output is waited for once it has exited
// or been ended: a child it started could hold its output open for good.
const waitDelay = 5 * time.Second

// Exec is the runner that runs real programs: each found on Path, kit's own,
// and run in Env alone, never the environment kit inherited.
type Exec struct {
	Path []string
	Env  []string
	Now  func() time.Time
}

func (e Exec) Run(ctx context.Context, cmd Command) (Result, error) {
	program, err := e.find(cmd.Name)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(cmd.Timeout, DefaultTimeout))
	defer cancel()
	started := e.Now()
	stdout, stderr, err := e.start(ctx, program, cmd)
	res := Result{Stdout: stdout, Stderr: stderr, ExitCode: -1, Duration: e.Now().Sub(started)}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.Exited() {
		res.ExitCode = exit.ExitCode()
		return res, &ExitError{Command: cmd.String(), Code: res.ExitCode, Stderr: lastLine(stderr)}
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return res, fmt.Errorf("%s: %w", cmd, ctxErr)
		}
		return res, fmt.Errorf("run %s: %w", cmd, err)
	}
	res.ExitCode = 0
	return res, nil
}

// start runs program as cmd says, returning what it printed.
func (e Exec) start(ctx context.Context, program string, cmd Command) (stdout, stderr []byte, err error) {
	c := exec.CommandContext(ctx, program, cmd.Args...)
	c.Env = e.Env
	c.Dir = cmd.Dir
	if cmd.Input != "" {
		c.Stdin = strings.NewReader(cmd.Input)
	}
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	// The command runs in a process group of its own, and is ended with
	// everything it started, as os/exec ends the command alone: a child left
	// running would outlive it, holding its output open until waitDelay
	// passed.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = waitDelay
	err = c.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// find finds the program name on Path, or checks a path to it.
func (e Exec) find(name string) (string, error) {
	if strings.Contains(name, "/") {
		if executable(name) {
			return name, nil
		}
		return "", fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	for _, dir := range e.Path {
		if program := filepath.Join(dir, name); executable(program) {
			return program, nil
		}
	}
	return "", fmt.Errorf("%s: %w on kit's PATH", name, ErrNotFound)
}

// executable reports whether path is a file anyone may run.
func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
