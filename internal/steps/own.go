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
	words, err := config.Words(e.Value)
	if err != nil || len(words) < 2 || words[0] != "--" {
		return "its line isn't a name, then -- and a command"
	}
	name := words[1]
	if rest, ok := strings.CutPrefix(name, "~/"); ok {
		name = filepath.Join(home, rest)
	}
	res, err := run.Run(ctx, runner.Command{Name: name, Args: words[2:], Timeout: ownTimeout})
	_, exited := errors.AsType[*runner.ExitError](err)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, runner.ErrNotFound):
		return words[1] + " isn't there"
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
