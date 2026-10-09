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
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// ManualName names the step of what a person does by hand.
const ManualName = "manual"

// manualRecord names the record of the manual steps marked done, on this
// Mac.
const manualRecord = "manual.json"

type manualDone struct {
	Done map[string]time.Time `json:"done,omitempty"`
}

// ManualStep is a [manual] line: what to do; what it comes after (a step,
// or one of a step's things, as in cask:1password) and the steps it comes
// before; and the command saying whether it's done, if it has one.
type ManualStep struct {
	Name, Do      string
	After, Before []string
	Command       *runner.Command
	// Pos is where its line is, as in laptop/declarations:12.
	Pos string
}

// Ordered reports whether the line says what it comes after or before: a
// step of its own, then, rather than one of the rest.
func (m ManualStep) Ordered() bool { return len(m.After) > 0 || len(m.Before) > 0 }

// ParseManual reads a [manual] line's value: what to do, in quotes, then
// --after and --before, each as often as wanted, and --test, then the rest
// of the line, the command saying whether it's done (exit 0: done), a word
// starting ~/ starting at home.
func ParseManual(home string, e config.Entry) (ManualStep, error) {
	m := ManualStep{Name: e.Name, Pos: e.Pos()}
	words, err := config.Words(e.Value)
	if err != nil {
		return m, err
	}
	if len(words) == 0 || strings.HasPrefix(words[0], "--") {
		return m, errors.New("what to do comes after the name, in quotes")
	}
	m.Do = words[0]
	var cmd []string
	for i := 1; i < len(words) && cmd == nil; i++ {
		switch w := words[i]; w {
		case "--after", "--before":
			name, err := orderedBy(w, words, i)
			if err != nil {
				return m, err
			}
			i++
			if w == "--after" {
				m.After = append(m.After, name)
			} else {
				m.Before = append(m.Before, name)
			}
		case "--test":
			if cmd = words[i+1:]; len(cmd) == 0 {
				return m, errors.New("--test needs the command saying whether it's done after it")
			}
		case "--":
			return m, errors.New("the command saying whether it's done comes after --test now")
		default:
			return m, fmt.Errorf("%q isn't one of a step by hand's options: --after, --before and --test", w)
		}
	}
	if cmd == nil {
		return m, nil
	}
	for i, w := range cmd {
		if rest, ok := strings.CutPrefix(w, "~/"); ok {
			cmd[i] = filepath.Join(home, rest)
		}
	}
	m.Command = &runner.Command{Name: cmd[0], Args: cmd[1:], Timeout: ownTimeout}
	return m, nil
}

// SplitManual splits [manual]'s lines: those saying what they come after
// or before, each a step of its own, and the rest, checked together. One
// that doesn't read stays with the rest, whose check says so.
func SplitManual(home string, list config.List) (rest config.List, own []ManualStep) {
	rest = config.List{Kind: list.Kind}
	for _, e := range list.Entries {
		if m, err := ParseManual(home, e); err == nil && m.Ordered() {
			own = append(own, m)
			continue
		}
		rest.Entries = append(rest.Entries, e)
	}
	return rest, own
}

// Raise is how a step by hand that's come due is raised while kit applies
// at a terminal, where someone can act on it: said in its row, what to do
// under it, and a notification, in case they're away from it.
type Raise struct {
	Sink   event.Sink
	Now    func() time.Time
	Notify func(ctx context.Context, title, text string)
}

// manualPoll is how often a raised step by hand's command is run, to see
// whether it's done.
var manualPoll = 3 * time.Second

// OwnManual is a step by hand that says what it comes after or before, as
// a step of its own: done when its command exits 0, or once marked done.
// Applying it with raise raises it, then waits till it's done, running its
// command every few seconds; one without a command kit can't tell about,
// so it's raised and left. Without raise, with no one to act, applying
// leaves it as it stands.
func OwnManual(run runner.Runner, stateDir string, m ManualStep, raise *Raise) engine.Step {
	done := func(ctx context.Context) bool {
		if m.Command != nil {
			r, err := run.Run(ctx, *m.Command)
			return Outcome(*m.Command, r, err) == ""
		}
		rec, err := state.Load[manualDone](stateDir, manualRecord)
		return err == nil && !rec.Done[m.Name].IsZero()
	}
	return engine.Step{
		Name: m.Name, Title: m.Name, Area: AreaManual,
		Check: func(ctx context.Context) check.Result {
			if done(ctx) {
				return check.Result{State: check.OK, Summary: "done"}
			}
			it := check.Item{ID: ManualName + ":" + m.Name, Name: m.Do, State: "manual"}
			if m.Command == nil {
				it.Detail = "then: kit manual done " + m.Name
			}
			return check.Result{State: check.Attention, Summary: "not done", Items: []check.Item{it}}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if raise == nil {
				return nil
			}
			raise.Sink.Emit(event.Doing{Time: raise.Now(), Step: m.Name, Says: "needs you", Todo: m.Do})
			if raise.Notify != nil {
				raise.Notify(ctx, m.Name, m.Do)
			}
			if m.Command == nil {
				return nil
			}
			tick := time.NewTicker(manualPoll)
			defer tick.Stop()
			for !done(ctx) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-tick.C:
				}
			}
			return nil
		},
	}
}

// Notify posts a macOS notification from kit: about a step by hand that
// needs someone, its name and what to do.
func Notify(run runner.Runner) func(ctx context.Context, title, text string) {
	quote := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
	return func(ctx context.Context, title, text string) {
		script := "display notification " + quote(text) + " with title " + quote("kit") + " subtitle " + quote(title)
		_, _ = run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-e", script}})
	}
}

// Manual checks what a person must do by hand, as [manual] declares it: a
// step with a command is done when it exits 0; one without, when it's been
// marked done on this Mac (kit manual done). Each step not done is an item saying
// what to do.
func Manual(run runner.Runner, home, stateDir string, list config.List) engine.Step {
	return engine.Step{
		Name: ManualName, Title: "By hand", Area: AreaManual,
		Check: func(ctx context.Context) check.Result {
			rec, err := state.Load[manualDone](stateDir, manualRecord)
			if err != nil {
				return failed(err)
			}
			res := check.Result{State: check.OK}
			for _, e := range list.Entries {
				m, err := ParseManual(home, e)
				if err != nil {
					return failed(fmt.Errorf("%s: %s: %w", e.Pos(), e.Name, err))
				}
				it := check.Item{ID: ManualName + ":" + m.Name, Name: m.Do, State: "manual"}
				switch {
				case m.Command != nil:
					if r, err := run.Run(ctx, *m.Command); Outcome(*m.Command, r, err) == "" {
						continue
					}
				case !rec.Done[m.Name].IsZero():
					continue
				default:
					it.Detail = "then: kit manual done " + m.Name
				}
				res.Items = append(res.Items, it)
			}
			done := len(list.Entries) - len(res.Items)
			res.Summary = fmt.Sprintf("%d of %d done", done, len(list.Entries))
			res.Glance = res.Summary
			if len(res.Items) == 0 {
				res.Summary = fmt.Sprintf("all %d done", len(list.Entries))
				res.Glance = "all done"
			} else {
				res.State = check.Attention
			}
			return res
		},
	}
}

// MarkDone marks the manual steps named done on this Mac, at now, or not
// done, with undo.
func MarkDone(stateDir string, names []string, now time.Time, undo bool) error {
	return state.Update(stateDir, manualRecord, func(rec *manualDone) {
		if rec.Done == nil {
			rec.Done = map[string]time.Time{}
		}
		for _, name := range names {
			if undo {
				delete(rec.Done, name)
			} else {
				rec.Done[name] = now
			}
		}
	})
}
