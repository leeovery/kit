package askpass

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/testguard"
)

func TestMain(m *testing.M) { os.Exit(testguard.Main(m)) }

// kept is the password the server keeps.
func (s *Server) kept() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.password
}

// bySudo is the helper here taken as run by sudo: the helper is this test
// itself, and its parent stands for sudo.
func bySudo(pid int) bool { return pid == os.Getppid() }

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
	s, err := start("", "/usr/bin/true", func(_ context.Context, n int) (string, error) {
		asked = append(asked, n)
		if notGiven {
			return "", errors.New("cancelled")
		}
		return "typed" + strconv.Itoa(n), nil
	}, bySudo)
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(s.Helper()); err != nil || target != "/usr/bin/true" {
		t.Errorf("the helper links to %q, %v; want kit", target, err)
	}
	defer s.Running(os.Getpid())()
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

// A program that connects to ask for itself, not through the helper run by
// sudo, is refused: the person isn't asked, and nothing's given, however
// the run holds the password.
func TestRefusesAnAskerNotSudos(t *testing.T) {
	asked := false
	s, err := Start("", "/usr/bin/true", func(context.Context, int) (string, error) {
		asked = true
		return "typed", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	defer s.Running(os.Getpid())()
	s.Keep("hunter2")
	if got, err := ask(t, s); got != "" || err == nil || asked {
		t.Errorf("asked by a program, not sudo: %q, %v, the person asked %v; want nothing given", got, err, asked)
	}
}

// Only what's asked from beneath a command running that was given the
// helper is answered: not beneath none, sudo or not, nor once that command
// has exited.
func TestAnswersOnlyBeneathACommandRunning(t *testing.T) {
	s, err := start("", "/usr/bin/true", func(context.Context, int) (string, error) {
		return "", errors.New("the person was asked")
	}, bySudo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Keep("hunter2")
	if got, err := ask(t, s); got != "" || err == nil {
		t.Errorf("beneath no command running: %q, %v; want nothing given", got, err)
	}
	exited := s.Running(os.Getpid())
	if got, err := ask(t, s); got != "hunter2\n" || err != nil {
		t.Errorf("beneath a command running: %q, %v; want the password kept", got, err)
	}
	exited()
	if got, err := ask(t, s); got != "" || err == nil {
		t.Errorf("beneath a command that's exited: %q, %v; want nothing given", got, err)
	}
}

// A program other than kit's helper is refused, however it's run: nc
// connecting from beneath a command running, its parent taken as sudo.
func TestRefusesAnythingButTheHelper(t *testing.T) {
	s, err := start("", "/usr/bin/true", func(context.Context, int) (string, error) {
		return "", errors.New("the person was asked")
	}, func(int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	defer s.Running(os.Getpid())()
	s.Keep("hunter2")
	r := runner.Exec{Now: time.Now}
	res, err := r.Run(t.Context(), runner.Command{Name: "/bin/sh", Args: []string{"-c", `printf 'Password:\n' | /usr/bin/nc -U "$1"`, "sh", filepath.Join(s.dir, socketName)}})
	if strings.Contains(string(res.Stdout), "hunter2") {
		t.Errorf("nc was given %q (%v); want nothing", res.Stdout, err)
	}
}

// The run's end gives up asking the person for a sudo still waiting for
// their answer: Close doesn't wait for it, and the helper's given nothing.
func TestCloseGivesUpAsking(t *testing.T) {
	asking := make(chan struct{})
	s, err := start("", "/usr/bin/true", func(ctx context.Context, _ int) (string, error) {
		close(asking)
		<-ctx.Done()
		return "", ctx.Err()
	}, bySudo)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Running(os.Getpid())()
	given := make(chan error, 1)
	go func() {
		_, err := ask(t, s)
		given <- err
	}()
	<-asking
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close is waiting for the person's answer")
	}
	if err := <-given; err == nil {
		t.Error("the helper was answered after the run ended: want nothing given")
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

// A sudo kit runs itself is answered as the helper answers a program's: the
// password kept, the first time it asks, then the person's; what they give
// isn't kept, for kit to keep once sudo's taken it.
func TestAnswersASudoKitRuns(t *testing.T) {
	var asked []int
	s, err := Start("", "/usr/bin/true", func(_ context.Context, n int) (string, error) {
		asked = append(asked, n)
		return "typed" + strconv.Itoa(n), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Keep("hunter2")

	answer := s.Answer()
	for i, want := range []string{"hunter2", "typed0", "typed1"} {
		if got, err := answer(t.Context(), i); got != want || err != nil {
			t.Errorf("asked %d: %q, %v; want %q", i, got, err, want)
		}
	}
	if got, err := s.Answer()(t.Context(), 0); got != "hunter2" || err != nil {
		t.Errorf("the next sudo: %q, %v; want the password kept, as it was", got, err)
	}
	if len(asked) != 2 {
		t.Errorf("the person asked %v, want twice", asked)
	}
}

// With nothing kept, a sudo kit runs is answered by the person at once.
func TestAnswersASudoKitRunsFromThePerson(t *testing.T) {
	s, err := Start("", "/usr/bin/true", func(context.Context, int) (string, error) { return "typed", nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if got, err := s.Answer()(t.Context(), 0); got != "typed" || err != nil || s.kept() != "" {
		t.Errorf("answered %q, %v, kept %q; want the person's, not kept", got, err, s.kept())
	}
}

// What a program kit runs is given has its sudo ask the run: through the
// helper, with -A; and without it, as sudo runs the helper without a
// terminal only while DISPLAY is set. What's given is taken back out for
// what outlives the run, a display of the person's own kept.
func TestEnvHasSudoAskTheRun(t *testing.T) {
	s, err := Start("", "/usr/bin/true", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	env := s.Env()
	if want := []string{"SUDO_ASKPASS=" + s.Helper(), "DISPLAY="}; !slices.Equal(env, want) {
		t.Errorf("Env() = %q, want %q", env, want)
	}
	environ := slices.Concat([]string{"HOME=/Users/someone"}, env)
	if got := Without(environ); !slices.Equal(got, []string{"HOME=/Users/someone"}) {
		t.Errorf("Without(%q) = %q, want the run's taken out", environ, got)
	}
	if got := Without([]string{"DISPLAY=:0", "SUDO_ASKPASS="}); !slices.Equal(got, []string{"DISPLAY=:0"}) {
		t.Errorf("Without() = %q, want a display of the person's own kept", got)
	}
}

// What kit asks the person in its turn is asked alone: a program's sudo
// asking meanwhile is pending, waiting its turn; then, the password the
// person gave kept, as kit keeps it once its own sudo has taken it, is
// given it, the person asked nothing more.
func TestAsksOneThingAtATime(t *testing.T) {
	asking := make(chan struct{}, 2)
	s, err := start("", "/usr/bin/true", func(context.Context, int) (string, error) {
		asking <- struct{}{}
		return "", errors.New("the person was asked twice")
	}, bySudo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	defer s.Running(os.Getpid())()
	up, answer, turned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(turned)
		s.Turn(func() {
			close(up)
			<-answer
			s.Keep("typed")
		})
	}()
	<-up
	given := make(chan string, 1)
	go func() {
		password, err := ask(t, s)
		if err != nil {
			t.Error(err)
		}
		given <- password
	}()
	deadline := time.Now().Add(time.Second)
	for !s.Pending() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !s.Pending() {
		t.Error("the sudo waiting its turn isn't pending")
	}
	close(answer)
	<-turned
	if sudo := <-given; sudo != "typed\n" || len(asking) != 0 || s.Pending() {
		t.Errorf("the sudo waiting was given %q, the person asked again %d times, pending %v; want typed, given once", sudo, len(asking), s.Pending())
	}
}
