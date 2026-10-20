// Package askpass lets a program kit runs ask the run for an administrator's
// password, through sudo: SUDO_ASKPASS names a link to kit in a directory
// of the run's own, beside a socket the run answers on. sudo runs kit
// through the link, with its prompt, as sudo -A does, and plain sudo
// without a terminal (Env); kit, run so, asks the run over the socket and
// prints the answer, for sudo to read. Only a program the run started may
// ask, and the password is never on a command line, in a file or in the
// log.
package askpass

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Name is the link's name: kit run through a link so named is the helper
// sudo runs for a password.
const Name = "kit-askpass"

// socketName is the socket's name, beside the link.
const socketName = "socket"

// Asker asks the person for the password: asked is how many times this
// sudo has asked them already.
type Asker func(ctx context.Context, asked int) (string, error)

// Server answers the helper, for a run.
type Server struct {
	dir string
	ln  net.Listener
	ask Asker
	wg  sync.WaitGroup
	// asking has the person asked one thing at a time.
	asking sync.Mutex

	mu       sync.Mutex
	password string
	// sudos are how each sudo that asked, by its process, has been
	// answered.
	sudos map[int]*answered
}

// answered is how a sudo has been answered: with the password kept, and
// how many times the person was asked.
type answered struct {
	kept  bool
	asked int
}

// Start readies the run's directory, in base, with its link to kit, at
// self, and its socket, and answers on it until Close: with the password
// the person gave earlier in the run (Keep), the first time a sudo asks;
// else, and when that sudo asks again, as it does when a password's wrong,
// with what ask gets from them.
func Start(base, self string, ask Asker) (*Server, error) {
	dir, err := os.MkdirTemp(base, "kit-askpass-")
	if err != nil {
		return nil, fmt.Errorf("make a directory for asking: %w", err)
	}
	s := &Server{dir: dir, ask: ask, sudos: map[int]*answered{}}
	if err := os.Symlink(self, s.Helper()); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("link kit for asking: %w", err)
	}
	if s.ln, err = net.Listen("unix", filepath.Join(dir, socketName)); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("listen for asking: %w", err)
	}
	s.wg.Go(s.serve)
	return s, nil
}

// Helper is the link sudo runs: what SUDO_ASKPASS names.
func (s *Server) Helper() string {
	return filepath.Join(s.dir, Name)
}

// Env is what a program kit runs is given, for its sudo to ask the run:
// SUDO_ASKPASS, naming the helper, and DISPLAY, as sudo without -A runs the
// helper only when it has no terminal and DISPLAY is set, to anything
// (sudo 1.9.17, proven on macOS 26). Empty, DISPLAY tells everything else
// there's no display.
func (s *Server) Env() []string {
	return []string{"SUDO_ASKPASS=" + s.Helper(), "DISPLAY="}
}

// Without is environ without what Env adds, for what outlives the run, as
// the shell kit becomes.
func Without(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		return strings.HasPrefix(kv, "SUDO_ASKPASS=") || kv == "DISPLAY="
	})
}

// Keep keeps the password the person gave, for the rest of the run: ""
// forgets it.
func (s *Server) Keep(password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password = password
}

// Close stops answering, forgets the password, and removes the directory.
func (s *Server) Close() error {
	_ = s.ln.Close()
	s.wg.Wait()
	s.Keep("")
	return os.RemoveAll(s.dir)
}

// serve answers each asking, one at a time: a person answers one question
// at a time.
func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.answer(c)
	}
}

// answer answers an asking, from a program the run started, through the
// helper; anything else, it closes on.
func (s *Server) answer(c net.Conn) {
	defer func() { _ = c.Close() }()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return
	}
	helper, err := peer(uc)
	if err != nil || !descends(helper, os.Getpid()) {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := bufio.NewReader(c).ReadString('\n'); err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	password, err := s.passwordFor(parent(helper))
	if err != nil {
		return
	}
	_, _ = io.WriteString(c, password+"\n")
}

// passwordFor is the answer for the sudo whose process is sudo.
func (s *Server) passwordFor(sudo int) (string, error) {
	s.mu.Lock()
	a := s.sudos[sudo]
	if a == nil {
		a = &answered{}
		s.sudos[sudo] = a
	}
	s.mu.Unlock()
	return s.next(context.Background(), a)
}

// Answer answers a sudo kit runs itself, reading the password on its input
// (runner.Command's Answer), each time it asks, as the helper answers a
// program's sudo.
func (s *Server) Answer() func(ctx context.Context, asked int) (string, error) {
	a := &answered{}
	return func(ctx context.Context, _ int) (string, error) {
		return s.next(ctx, a)
	}
}

// next is the answer for a sudo answered so far as a says: the password
// kept, the first time it asks, should the person give it while this one
// waits its turn to ask too; else the person's, which is kept.
func (s *Server) next(ctx context.Context, a *answered) (string, error) {
	if password, ok := s.keptFor(a); ok {
		return password, nil
	}
	s.asking.Lock()
	defer s.asking.Unlock()
	if password, ok := s.keptFor(a); ok {
		return password, nil
	}
	s.mu.Lock()
	asked := a.asked
	a.asked++
	s.mu.Unlock()
	password, err := s.ask(ctx, asked)
	if err == nil {
		s.Keep(password)
	}
	return password, err
}

// keptFor is the password kept, for a sudo answered so far as a says, the
// first time it asks.
func (s *Server) keptFor(a *answered) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.kept || s.password == "" {
		return "", false
	}
	a.kept = true
	return s.password, true
}

// Ask asks the person with ask, asked how many times they've been asked
// already, as nothing else asks meanwhile, a program's sudo through the
// helper waiting its turn, and keeps what they give for the rest of the
// run.
func (s *Server) Ask(ctx context.Context, ask Asker, asked int) (string, error) {
	s.asking.Lock()
	defer s.asking.Unlock()
	password, err := ask(ctx, asked)
	if err == nil {
		s.Keep(password)
	}
	return password, err
}

// Ask is kit run as the helper, through the link at path, with sudo's
// prompt: it asks the run whose socket is beside the link, and writes the
// answer to out, for sudo to read.
func Ask(path, prompt string, out io.Writer) error {
	c, err := net.DialTimeout("unix", filepath.Join(filepath.Dir(path), socketName), 5*time.Second)
	if err != nil {
		return fmt.Errorf("the run that asks for kit's password has ended: %w", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := io.WriteString(c, strings.ReplaceAll(prompt, "\n", " ")+"\n"); err != nil {
		return err
	}
	answer, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return errors.New("no password given")
	}
	_, err = io.WriteString(out, answer)
	return err
}
