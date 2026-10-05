package steps

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// OwnName names the step of the user's own checks.
const OwnName = "checks"

// ownTimeout is how long one of the user's checks may take.
const ownTimeout = time.Minute

// Own runs the user's own checks, as their [checks] lines declare them: a
// name, then -- and a command. The contract: exit 0 when all's well;
// otherwise the first line it prints says what's wrong. A ~ starting the
// command is the home.
func Own(run runner.Runner, home string, list config.List) engine.Step {
	return engine.Step{
		Name: OwnName, Title: "Your checks", Area: AreaChecks,
		Check: func(ctx context.Context) check.Result {
			var problems [][3]string
			for _, e := range list.Entries {
				if what := own(ctx, run, home, e); what != "" {
					problems = append(problems, [3]string{e.Name, e.Name + ": " + what, fmt.Sprintf("your check, %s line %d", e.File, e.Line)})
				}
			}
			summary := fmt.Sprintf("%d checks, all passing", len(list.Entries))
			if len(list.Entries) == 1 {
				summary = "1 check, passing"
			}
			if len(problems) > 0 {
				return problem(OwnName, fmt.Sprintf("%d of %d failing", len(problems), len(list.Entries)), problems...)
			}
			return check.Result{State: check.OK, Summary: summary, Glance: summary}
		},
	}
}

// own runs the check e declares, saying what's wrong: "" when all's well.
func own(ctx context.Context, run runner.Runner, home string, e config.Entry) string {
	cmd, err := OwnCommand(home, e.Value, ownTimeout)
	if err != nil {
		return err.Error()
	}
	res, err := run.Run(ctx, cmd)
	return Outcome(cmd, res, err)
}

// OwnCommand is the command a line of the user's own declares after its
// name: -- then the command, a word starting ~/ starting at the home, as a
// shell has it.
func OwnCommand(home, value string, timeout time.Duration) (runner.Command, error) {
	words, err := config.Words(value)
	if err != nil || len(words) < 2 || words[0] != "--" {
		return runner.Command{}, errors.New("its line isn't a name, then -- and a command")
	}
	words = words[1:]
	for i, w := range words {
		if rest, ok := strings.CutPrefix(w, "~/"); ok {
			words[i] = filepath.Join(home, rest)
		}
	}
	return runner.Command{Name: words[0], Args: words[1:], Timeout: timeout}, nil
}

// Outcome says what's wrong with a command of the user's own, as it ran:
// "" when it exited 0; else the first line it printed.
func Outcome(cmd runner.Command, res runner.Result, err error) string {
	_, exited := errors.AsType[*runner.ExitError](err)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, runner.ErrNotFound):
		return filepath.Base(cmd.Name) + " isn't there"
	case !exited:
		return "it didn't finish: " + err.Error()
	}
	for _, out := range [][]byte{res.Stdout, res.Stderr} {
		if line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); line != "" {
			return strings.TrimSpace(line)
		}
	}
	return fmt.Sprintf("it exited %d, saying nothing", res.ExitCode)
}
