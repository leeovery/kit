package runner

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	// Stdin, Stdout and Stderr are kit's terminal, which an interactive
	// command runs at.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
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
	if cmd.Answer != nil {
		return converse(ctx, c, cmd)
	}
	if cmd.Interactive {
		// In kit's own process group, as the terminal's foreground group is
		// the only one that may read from it.
		c.Stdin, c.Stdout, c.Stderr = e.Stdin, e.Stdout, e.Stderr
		c.WaitDelay = waitDelay
		return nil, nil, c.Run()
	}
	if cmd.Input != "" {
		c.Stdin = strings.NewReader(cmd.Input)
	}
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	if cmd.Lines != nil {
		l := &lines{each: cmd.Lines}
		stdout, stderr := l.stream(), l.stream()
		c.Stdout, c.Stderr = io.MultiWriter(&out, stdout), io.MultiWriter(&errOut, stderr)
		defer func() {
			stdout.end()
			stderr.end()
		}()
	}
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

// converse runs c, the command cmd, in a process group of its own, answering
// what it asks on its standard error, as cmd says, on its standard input;
// what it printed, its prompts taken out.
func converse(ctx context.Context, c *exec.Cmd, cmd Command) (stdout, stderr []byte, err error) {
	in, err := c.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	errs, err := c.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	var out bytes.Buffer
	c.Stdout = &out
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = waitDelay
	if err := c.Start(); err != nil {
		return nil, nil, err
	}
	prompt := []byte(cmd.Asks)
	var said, pending []byte
	var answerErr error
	buf := make([]byte, 4096)
	for asked := 0; ; {
		n, readErr := errs.Read(buf)
		pending = append(pending, buf[:n]...)
		for answerErr == nil {
			i := bytes.Index(pending, prompt)
			if i < 0 {
				break
			}
			said, pending = append(said, pending[:i]...), pending[i+len(prompt):]
			answer, err := cmd.Answer(ctx, asked)
			asked++
			if err != nil {
				answerErr = err
				_ = in.Close()
				break
			}
			_, _ = io.WriteString(in, answer+"\n")
		}
		// What could begin a prompt waits for what follows it.
		keep := min(len(pending), len(prompt)-1)
		said, pending = append(said, pending[:len(pending)-keep]...), pending[len(pending)-keep:]
		if readErr != nil {
			break
		}
	}
	said = append(said, pending...)
	_ = in.Close()
	err = c.Wait()
	if answerErr != nil && err == nil {
		err = answerErr
	}
	return out.Bytes(), said, err
}

// Become replaces kit with the program at path, run with args in env, at
// kit's own terminal, as if it had been run there: it returns only when it
// can't.
func Become(path string, args, env []string) error {
	return syscall.Exec(path, append([]string{filepath.Base(path)}, args...), env)
}

// Has reports whether the program name is on Path, or is a path to one.
func (e Exec) Has(name string) bool {
	_, err := e.find(name)
	return err == nil
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

// lines splits what a command prints into lines as they come, giving each
// to each, one at a time: its output's and its errors' apart.
type lines struct {
	mu   sync.Mutex
	each func(string)
}

func (l *lines) stream() *lineStream { return &lineStream{lines: l} }

// lineStream is one of a command's streams, split into lines: a carriage
// return starts the line again, as a progress bar redraws it.
type lineStream struct {
	lines *lines
	line  []byte
}

func (s *lineStream) Write(p []byte) (int, error) {
	s.lines.mu.Lock()
	defer s.lines.mu.Unlock()
	for _, b := range p {
		switch b {
		case '\n':
			s.give()
		case '\r':
			s.line = s.line[:0]
		default:
			s.line = append(s.line, b)
		}
	}
	return len(p), nil
}

// end gives what's left of the line, once the command is done.
func (s *lineStream) end() {
	s.lines.mu.Lock()
	defer s.lines.mu.Unlock()
	s.give()
}

func (s *lineStream) give() {
	if line := strings.TrimSpace(string(s.line)); line != "" {
		s.lines.each(string(s.line))
	}
	s.line = s.line[:0]
}
