package steps

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// ScriptName names a step's script, in its folder.
const ScriptName = "run"

// ActionRun is what applying does about a step of the user's own that isn't
// done: runs it.
const ActionRun = "run"

// How long a step's script may take: its check is cheap; applying may
// install something.
const (
	scriptCheckTimeout = time.Minute
	scriptApplyTimeout = 15 * time.Minute
)

// template is the script kit step add writes: one that follows the
// contract, with nothing in it yet.
//
//go:embed template.sh
var template string

// templateTitle is the template's line naming the step and saying what it
// does.
const templateTitle = "# STEP: WHAT IT DOES"

// Template is the script for a new step called name, which does what does
// says.
func Template(name, does string) string {
	return strings.Replace(template, templateTitle, "# "+name+": "+does, 1)
}

// Script is a step of the user's own, as a [steps] line declares it: what it
// does, the steps it's applied after and those it needs, whether applying it
// needs an administrator's password; and its folder, holding its script and
// its data.
type Script struct {
	Name, Does   string
	After, Needs []string
	Admin        bool
	// Folder is its folder in the config repository, as in
	// laptop/steps/fonts; Dir is the same, a full path.
	Folder, Dir string
}

// ParseScript reads a [steps] line of the config repository at configDir:
// what the step does, in quotes, then --after and --needs, each with a
// step's name and as often as wanted, and --admin.
func ParseScript(configDir string, e config.Entry) (Script, error) {
	folder := config.StepFolder(e.Scope, e.Name)
	s := Script{Name: e.Name, Folder: folder, Dir: filepath.Join(configDir, folder)}
	words, err := config.Words(e.Value)
	if err != nil {
		return s, err
	}
	if len(words) == 0 || strings.HasPrefix(words[0], "--") {
		return s, errors.New("what the step does comes after its name, in quotes")
	}
	s.Does = words[0]
	for i := 1; i < len(words); i++ {
		switch w := words[i]; w {
		case "--admin":
			s.Admin = true
		case "--after", "--needs":
			if i+1 == len(words) || strings.HasPrefix(words[i+1], "--") {
				return s, fmt.Errorf("%s needs a step's name after it", w)
			}
			i++
			if w == "--after" {
				s.After = append(s.After, words[i])
			} else {
				s.Needs = append(s.Needs, words[i])
			}
		default:
			return s, fmt.Errorf("%q isn't one of a step's options: --after, --needs and --admin", w)
		}
	}
	return s, nil
}

// ScriptStep is the step s declares. Its check runs s's script with check,
// in s's folder: exit 0 says it's done, the first line printed saying how it
// stands; exit 1 that it isn't, so applying runs the script with apply;
// anything else that the check couldn't tell. Applying is believed only
// when the check then passes. One that needs an administrator's password
// waits for admin to hold one.
func ScriptStep(run runner.Runner, admin *Admin, s Script) engine.Step {
	script := filepath.Join(s.Dir, ScriptName)
	command := func(verb string, timeout time.Duration) runner.Command {
		return runner.Command{Name: script, Args: []string{verb}, Dir: s.Dir, Timeout: timeout}
	}
	checks := func(ctx context.Context) check.Result {
		if err := runnable(script, s.Folder+"/"+ScriptName); err != nil {
			return failed(err)
		}
		res, err := run.Run(ctx, command("check", scriptCheckTimeout))
		exit, exited := errors.AsType[*runner.ExitError](err)
		switch {
		case err == nil:
			return check.Result{State: check.OK, Summary: cmp.Or(firstLine(res.Stdout), "done")}
		case exited && exit.Code == 1:
			return check.Result{State: check.Attention, Summary: cmp.Or(firstLine(res.Stdout), "not done"), Items: []check.Item{{
				ID: s.Name + ":" + s.Name, Name: s.Does, State: Problem, Detail: "kit apply " + s.Name, Action: ActionRun,
			}}}
		}
		return check.Result{State: check.Failed, Reason: why(res, err, firstLine(res.Stdout))}
	}
	return engine.Step{
		Name: s.Name, Title: s.Name, Area: AreaSteps, Needs: s.Needs, After: s.After, Admin: s.Admin,
		Check: checks,
		Apply: func(ctx context.Context, _ check.Result) error {
			if s.Admin && !admin.ok(ctx) {
				return errors.New(AdminWait)
			}
			res, err := run.Run(ctx, command("apply", scriptApplyTimeout))
			if err != nil {
				return errors.New(why(res, err, lastLine(res.Stdout)))
			}
			if after := checks(ctx); after.State != check.OK {
				return fmt.Errorf("applied, but its check still says: %s", cmp.Or(after.Summary, after.Reason))
			}
			return nil
		},
	}
}

// BrokenScript is the step of a [steps] line that doesn't read, as wrong
// says: its check fails, saying so and where.
func BrokenScript(e config.Entry, wrong error) engine.Step {
	return engine.Step{
		Name: e.Name, Title: e.Name, Area: AreaSteps,
		Check: func(context.Context) check.Result {
			return check.Result{State: check.Failed, Reason: e.Pos() + ": " + wrong.Error()}
		},
	}
}

// runnable says what's wrong with the script at path, shown as shown: nil
// when it's a file that can be run.
func runnable(path, shown string) error {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return errors.New(shown + " isn't there")
	case err != nil:
		return err
	case !info.Mode().IsRegular():
		return errors.New(shown + " isn't a file")
	case info.Mode().Perm()&0o111 == 0:
		return errors.New(shown + " can't be run: chmod +x it")
	}
	return nil
}

// why says why a step's script failed, as it ran: the last thing it said on
// its standard error, else said (from its standard output), else how it
// ended.
func why(res runner.Result, err error, said string) string {
	if line := lastLine(res.Stderr); line != "" {
		return line
	}
	if said != "" {
		return said
	}
	if _, exited := errors.AsType[*runner.ExitError](err); !exited {
		return "it didn't finish: " + err.Error()
	}
	return fmt.Sprintf("it exited %d, saying nothing", res.ExitCode)
}

// firstLine is the first line of out that says something.
func firstLine(out []byte) string {
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// lastLine is the last line of out that says something.
func lastLine(out []byte) string {
	for _, line := range slices.Backward(strings.Split(string(out), "\n")) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
