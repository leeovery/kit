package runnertest_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

func TestFakeFailsTheTestOnAnUnscriptedCommand(t *testing.T) {
	rec := &recorder{TB: t}
	fake := runnertest.New(rec)
	fake.On("brew", "leaves")

	_, err := fake.Run(t.Context(), runner.Command{Name: "brew", Args: []string{"leaves", "--installed-on-request"}})
	if !errors.Is(err, runner.ErrNotFound) {
		t.Errorf("an unscripted command: error = %v, want ErrNotFound", err)
	}
	if want := []string{"runnertest: unscripted command brew leaves --installed-on-request"}; !slices.Equal(rec.errors, want) {
		t.Errorf("the test was failed with %q, want %q", rec.errors, want)
	}
}

// recorder is a test that notes what it's failed with, rather than failing.
type recorder struct {
	testing.TB
	mu     sync.Mutex
	errors []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// A command scripted to write a file writes it at the path after its flag,
// a relative one in the folder it runs in, and only when it succeeds,
// unless it writes partly; one scripted by a match answers any arguments
// the match accepts.
func TestFakeWritesAFile(t *testing.T) {
	fake := runnertest.New(t)
	dir := t.TempDir()
	read := func(args []string) bool { return len(args) == 4 && args[0] == "read" && args[1] == "--out-file" }
	fake.OnMatch("op", read).Writes("--out-file", "\xff\n").Then().Writes("--out-file", "late").Exits(1).Then().WritesPartly("--out-file", "part").Exits(1)
	for _, path := range []string{filepath.Join(dir, "a"), "b", "c"} {
		if _, err := fake.Run(t.Context(), runner.Command{Name: "op", Args: []string{"read", "--out-file", path, "op://vault/item/file"}, Dir: dir}); (err != nil) != (path != filepath.Join(dir, "a")) {
			t.Errorf("writing %s: %v", path, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(dir, "c")); err != nil || string(got) != "part" {
		t.Errorf("a command that failed partway left %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "a")); err != nil || string(got) != "\xff\n" {
		t.Errorf("the file holds %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a command that failed wrote its file: %v", err)
	}
}

// A command scripted to ask is given what its Answer gives, each time; one
// answered too late ends first, saying so, and keeps nothing it was given
// last.
func TestFakeAsks(t *testing.T) {
	fake := runnertest.New(t)
	sudo := fake.On("sudo", "-S", "-v").Asks(2)
	answer := func(_ context.Context, asked int) (string, error) { return fmt.Sprint("try ", asked), nil }
	if _, err := fake.Run(t.Context(), runner.Command{Name: "sudo", Args: []string{"-S", "-v"}, Answer: answer}); err != nil {
		t.Fatal(err)
	}
	if got := sudo.Answered(); len(got) != 2 || got[1] != "try 1" {
		t.Errorf("Answered() = %q, want each try", got)
	}
	late := fake.On("sudo", "-S", "-k").Asks(1).AnswersLate()
	_, err := fake.Run(t.Context(), runner.Command{Name: "sudo", Args: []string{"-S", "-k"}, Answer: answer})
	if exit, ok := errors.AsType[*runner.ExitError](err); !ok || exit.Code != 1 || !errors.Is(err, runner.ErrAnsweredLate) || len(late.Answered()) != 0 {
		t.Errorf("Run() = %v, answered %q; want exit 1, the answer too late, none kept", err, late.Answered())
	}
}

// A command scripted to hang prints what it prints, then waits till its
// context is done, failing with the context's error; one scripted to run in
// a process group says so.
func TestFakeHangs(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "install", "1").Prints("==> Downloading Bear\n").Hangs().InGroup(4242)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	var heard []string
	res, err := fake.Run(ctx, runner.Command{Name: "mas", Args: []string{"install", "1"}, Lines: func(line string) { heard = append(heard, line) }})
	if !errors.Is(err, context.DeadlineExceeded) || !slices.Equal(heard, []string{"==> Downloading Bear"}) || res.Group != 4242 {
		t.Errorf("Run() = %v, printing %q, in group %d; want what it prints, then the deadline exceeded, in its group", err, heard, res.Group)
	}
}

// Asked, the fake gives each line a carriage return ends too, and keeps
// none of the command's output, Lines alone given it, as a real runner.
func TestFakeGivesRedrawsAndKeepsOnlyLinesWhenAsked(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("rsync").Prints("a/big\n\r 10%\r 50%\r\nb\n").PrintsToStderr("careful\n")
	var heard []string
	res, err := fake.Run(t.Context(), runner.Command{Name: "rsync", Redraws: true, LinesOnly: true, Lines: func(line string) { heard = append(heard, line) }})
	if want := []string{"a/big", " 10%", " 50%", "b", "careful"}; err != nil || !slices.Equal(heard, want) {
		t.Errorf("Run() = %v, giving %q; want %q", err, heard, want)
	}
	if len(res.Stdout) != 0 || string(res.Stderr) != "careful\n" {
		t.Errorf("Run() = %q, %q; want no output kept, its errors kept", res.Stdout, res.Stderr)
	}
}

// The fake says a command runs in its Env, the command's own variables
// over it, then those the context sets, as a real runner.
func TestFakeSaysTheEnvironmentACommandRunsIn(t *testing.T) {
	f := runnertest.New(t)
	f.Env = []string{"A=fake", "B=fake"}
	ctx := runner.WithSecretEnv(t.Context(), "B=context")
	env, ok := runner.Environ(ctx, f, runner.Command{Name: "git", Env: []string{"A=command"}})
	if want := []string{"A=fake", "B=fake", "A=command", "B=context"}; !ok || !slices.Equal(env, want) {
		t.Errorf("Environ() = %q, %t; want %q", env, ok, want)
	}
}
