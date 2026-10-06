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

// ManualStep is a [manual] line: what to do, and the command saying whether
// it's done, if it has one.
type ManualStep struct {
	Name, Do string
	Command  *runner.Command
}

// ParseManual reads a [manual] line's value: what to do, in quotes, then
// optionally -- and a command (exit 0: done), a word starting ~/ starting
// at home.
func ParseManual(home string, e config.Entry) (ManualStep, error) {
	m := ManualStep{Name: e.Name}
	words, err := config.Words(e.Value)
	if err != nil {
		return m, err
	}
	if len(words) == 0 || words[0] == "--" {
		return m, errors.New("what to do comes after the name, in quotes")
	}
	m.Do = words[0]
	switch {
	case len(words) == 1:
		return m, nil
	case words[1] != "--" || len(words) == 2:
		return m, errors.New("after what to do comes -- and a command saying whether it's done, or nothing")
	}
	cmd := words[2:]
	for i, w := range cmd {
		if rest, ok := strings.CutPrefix(w, "~/"); ok {
			cmd[i] = filepath.Join(home, rest)
		}
	}
	m.Command = &runner.Command{Name: cmd[0], Args: cmd[1:], Timeout: ownTimeout}
	return m, nil
}

// Manual checks what a person must do by hand, as [manual] declares it: a
// step with a command is done when it exits 0; one without, when it's been
// marked done on this Mac (kit done). Each step not done is an item saying
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
					it.Detail = "then: kit done " + m.Name
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
