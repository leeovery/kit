// Package askpass lets a program kit runs ask the run for an administrator's
// password, as sudo -A asks: SUDO_ASKPASS names a link to kit in a directory
// of the run's own, beside a socket the run answers on. sudo runs kit
// through the link, with its prompt; kit, run so, asks the run over the
// socket and prints the answer, for sudo to read. Only a program the run
// started may ask, and the password is never on a command line, in a file
// or in the log.
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

// passwordFor is the answer for the sudo whose process is sudo: the
// password kept, the first time it asks; else the person's.
func (s *Server) passwordFor(sudo int) (string, error) {
	s.mu.Lock()
	a := s.sudos[sudo]
	if a == nil {
		a = &answered{}
		s.sudos[sudo] = a
	}
	if !a.kept && s.password != "" {
		a.kept = true
		password := s.password
		s.mu.Unlock()
		return password, nil
	}
	asked := a.asked
	a.asked++
	s.mu.Unlock()
	password, err := s.ask(context.Background(), asked)
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
