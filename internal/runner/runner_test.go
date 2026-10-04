package runner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

func TestCommandString(t *testing.T) {
	tests := []struct {
		cmd  runner.Command
		want string
	}{
		{cmd: runner.Command{Name: "brew", Args: []string{"list", "--formula", "--full-name", "-1"}}, want: "brew list --formula --full-name -1"},
		{cmd: runner.Command{Name: "brew", Args: []string{"info", "--json=v2", "owner/tap/node@24"}}, want: "brew info --json=v2 owner/tap/node@24"},
		{cmd: runner.Command{Name: "defaults", Args: []string{"write", "it's", "a b", ""}}, want: `defaults write 'it'\''s' 'a b' ''`},
	}
	for _, tt := range tests {
		if got := tt.cmd.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestObservedTellsOfEveryCommand(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "leaves").Prints("jq\n")
	fake.On("brew", "info", "nope").Exits(1).PrintsToStderr("Error: No available formula")
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	type key struct{}
	var reports []runner.Report
	var steps []any
	r := runner.Observed(fake, func(ctx context.Context, rep runner.Report) {
		reports = append(reports, rep)
		steps = append(steps, ctx.Value(key{}))
	}, func() time.Time { return clock })

	ctx := context.WithValue(t.Context(), key{}, "brew")
	if _, err := r.Run(ctx, runner.Command{Name: "brew", Args: []string{"leaves"}}); err != nil {
		t.Fatal(err)
	}
	_, err := r.Run(ctx, runner.Command{Name: "brew", Args: []string{"info", "nope"}})

	if len(reports) != 2 || steps[0] != "brew" || steps[1] != "brew" {
		t.Fatalf("observed %d commands, with contexts %v; want both, in the context each ran in", len(reports), steps)
	}
	if reports[0].Command.String() != "brew leaves" || string(reports[0].Result.Stdout) != "jq\n" || reports[0].Err != nil || !reports[0].Started.Equal(clock) {
		t.Errorf("first report = %+v, want brew leaves, its output, no error, started now", reports[0])
	}
	if reports[1].Err != err || reports[1].Result.ExitCode != 1 {
		t.Errorf("second report = %+v, want its error and exit code", reports[1])
	}
}

func TestExitErrorWithoutStderr(t *testing.T) {
	err := &runner.ExitError{Command: "brew leaves", Code: 1}
	if err.Error() != "brew leaves exited 1" {
		t.Errorf("Error() = %q", err)
	}
	if _, ok := errors.AsType[*runner.ExitError](error(err)); !ok {
		t.Error("errors.AsType should find the ExitError")
	}
}
