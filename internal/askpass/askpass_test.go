package askpass

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/leeovery/kit/internal/testguard"
)

func TestMain(m *testing.M) { os.Exit(testguard.Main(m)) }

// kept is the password the server keeps.
func (s *Server) kept() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.password
}

// ask is the helper asking, as sudo runs it: what it was given, or its
// error.
func ask(t *testing.T, s *Server) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := Ask(s.Helper(), "Password:", &out)
	return out.String(), err
}

// A sudo is answered first with the password the person gave earlier in the
// run, then, asking again as it does when that's wrong, with the person's
// answer, each time; the person's answer is kept. Nothing is answered once
// the run has ended.
func TestAnswersASudo(t *testing.T) {
	var asked []int
	notGiven := false
	s, err := Start("", "/usr/bin/true", func(_ context.Context, n int) (string, error) {
		asked = append(asked, n)
		if notGiven {
			return "", errors.New("cancelled")
		}
		return "typed" + strconv.Itoa(n), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(s.Helper()); err != nil || target != "/usr/bin/true" {
		t.Errorf("the helper links to %q, %v; want kit", target, err)
	}
	s.Keep("hunter2")
	for _, want := range []string{"hunter2\n", "typed0\n", "typed1\n"} {
		if got, err := ask(t, s); got != want || err != nil {
			t.Errorf("asked: %q, %v; want %q", got, err, want)
		}
	}
	if kept := s.kept(); kept != "typed1" || len(asked) != 2 {
		t.Errorf("kept %q, the person asked %v", kept, asked)
	}
	notGiven = true
	if got, err := ask(t, s); got != "" || err == nil {
		t.Errorf("not given: %q, %v; want no answer", got, err)
	}
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) || s.kept() != "" {
		t.Errorf("after Close: the directory %v, the password %q; want both gone", err, s.kept())
	}
	if _, err := ask(t, s); err == nil {
		t.Error("asked after the run ended: want an error")
	}
}

// Only a process the run started, however far down, may ask.
func TestDescends(t *testing.T) {
	if !descends(os.Getpid(), os.Getpid()) || !descends(os.Getpid(), os.Getppid()) {
		t.Error("this process, from itself and its parent: want it descends")
	}
	if descends(os.Getppid(), os.Getpid()) || descends(1, os.Getpid()) {
		t.Error("its parent, and launchd, from this process: want they don't")
	}
}
