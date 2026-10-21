package runner_test

import (
	"context"
	"errors"
	"slices"
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

// An exit's lines are those of its standard error that start with one of
// the prefixes, trimmed, or all of them given none: the line that carries
// the error, never one above it that only mentions it. What git prints is
// what real git does when ssh can't reach the remote.
func TestExitErrorLines(t *testing.T) {
	err := runner.NewExitError("git fetch --quiet", 128, []byte("ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"))
	if got := err.Lines("fatal:", "ssh:"); !slices.Equal(got, []string{"ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known", "fatal: Could not read from remote repository."}) {
		t.Errorf("Lines(fatal:, ssh:) = %q", got)
	}
	if got := err.Lines("error:"); got != nil {
		t.Errorf("Lines(error:) = %q, want none", got)
	}
	if got := err.Lines(); len(got) != 5 || got[2] != "" || got[4] != "and the repository exists." {
		t.Errorf("Lines() = %q, want every line", got)
	}
}

func TestSecretValues(t *testing.T) {
	ctx := runner.WithSecretEnv(t.Context(), "A=one", "EMPTY=")
	ctx = runner.WithSecretEnv(ctx, "B=two=three")
	if got := runner.SecretEnv(ctx); !slices.Equal(got, []string{"A=one", "EMPTY=", "B=two=three"}) {
		t.Errorf("SecretEnv() = %q, want each variable given, in order", got)
	}
	if got := runner.SecretValues(ctx); !slices.Equal(got, []string{"one", "two=three"}) {
		t.Errorf("SecretValues() = %q, want each value that's something", got)
	}
	if runner.WithSecretEnv(t.Context()) != t.Context() || runner.SecretEnv(t.Context()) != nil {
		t.Error("no variables changed the context, or a context without them set some")
	}
}

// only runs commands, and can't say what environment it runs them in.
type only struct{ runner.Runner }

// A runner that can't say what environment it runs a command in says so,
// wrapped in any other: never an environment without what it adds.
func TestEnvironOfARunnerThatCantSay(t *testing.T) {
	r := only{runnertest.New(t)}
	wrapped := runner.WithEnv(runner.Observed(r, func(context.Context, runner.Report) {}, time.Now), func() []string { return []string{"A=1"} })
	for name, r := range map[string]runner.Runner{"alone": r, "wrapped": wrapped} {
		if env, ok := runner.Environ(t.Context(), r, runner.Command{Name: "git"}); ok || env != nil {
			t.Errorf("%s: Environ() = %q, %t; want it unknown", name, env, ok)
		}
	}
}

// A variable's value is its last, as a program sees it; one set empty is
// set.
func TestLookupEnv(t *testing.T) {
	env := []string{"A=1", "B=", "A=2", "AB=3"}
	for _, c := range []struct {
		name, value string
		set         bool
	}{
		{"A", "2", true},
		{"B", "", true},
		{"AB", "3", true},
		{"C", "", false},
	} {
		if value, set := runner.LookupEnv(env, c.name); value != c.value || set != c.set {
			t.Errorf("LookupEnv(%s) = %q, %t; want %q, %t", c.name, value, set, c.value, c.set)
		}
	}
}
