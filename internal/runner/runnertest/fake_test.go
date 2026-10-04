package runnertest_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

func TestFakeAnswersAsScripted(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "leaves").Prints("jq\nripgrep\n").Takes(2 * time.Second)
	fake.On("brew", "info", "nope").PrintsToStderr("Error: No available formula").Exits(1)
	fake.On("mas", "list").Fails(runner.ErrNotFound)
	fake.On("brew", "leaves").Prints("jq\n")

	res, err := fake.Run(t.Context(), runner.Command{Name: "brew", Args: []string{"leaves"}})
	if err != nil || string(res.Stdout) != "jq\n" || res.ExitCode != 0 {
		t.Errorf("brew leaves = %q, exit %d, %v; want the later script's answer", res.Stdout, res.ExitCode, err)
	}
	res, err = fake.Run(t.Context(), runner.Command{Name: "brew", Args: []string{"info", "nope"}})
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok || exit.Code != 1 || exit.Stderr != "Error: No available formula" || string(res.Stderr) != "Error: No available formula" {
		t.Errorf("brew info nope = %+v, %v; want an ExitError with its stderr", res, err)
	}
	res, err = fake.Run(t.Context(), runner.Command{Name: "mas", Args: []string{"list"}})
	if !errors.Is(err, runner.ErrNotFound) || res.ExitCode != -1 {
		t.Errorf("mas list = exit %d, %v; want ErrNotFound", res.ExitCode, err)
	}

	want := []string{"brew leaves", "brew info nope", "mas list"}
	if got := fake.Calls(); !slices.Equal(got, want) {
		t.Errorf("Calls() = %q, want %q", got, want)
	}
	if got := len(fake.Commands()); got != 3 {
		t.Errorf("Commands() holds %d, want 3", got)
	}
}

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

func TestFakeIsSafeFromSeveralGoroutines(t *testing.T) {
	fake := runnertest.New(t)
	for i := range 10 {
		fake.On("brew", "info", fmt.Sprint(i)).Prints(fmt.Sprint(i))
	}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			res, err := fake.Run(t.Context(), runner.Command{Name: "brew", Args: []string{"info", fmt.Sprint(i)}})
			if err != nil || string(res.Stdout) != fmt.Sprint(i) {
				t.Errorf("brew info %d = %q, %v", i, res.Stdout, err)
			}
		})
	}
	wg.Wait()
	if got := len(fake.Calls()); got != 10 {
		t.Errorf("Calls() holds %d, want 10", got)
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

func TestFakeAnswersInTurn(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "leaves").Prints("jq\n").Then().Prints("jq\nripgrep\n").Then().Exits(1)
	var got []string
	for range 4 {
		res, err := fake.Run(t.Context(), runner.Command{Name: "brew", Args: []string{"leaves"}})
		got = append(got, fmt.Sprintf("%q %v", res.Stdout, err != nil))
	}
	want := []string{`"jq\n" false`, `"jq\nripgrep\n" false`, `"" true`, `"" true`}
	if !slices.Equal(got, want) {
		t.Errorf("answers = %q, want %q", got, want)
	}
}
