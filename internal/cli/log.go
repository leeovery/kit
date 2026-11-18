package cli

import (
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/render"
)

// logSchema is the version of kit log --json's documents: a run's, and the
// runs'.
const logSchema = 2

func newLogCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:         "log [command]",
		Short:       "Show past runs: every check and command, with timings",
		Annotations: map[string]string{brief: "past runs, every check and command"},
		Long: `Show what a run did: each step's check, with how it stood, and every
command it ran, with its exit code and how long it took.

At a terminal, kit log lists the runs, newest first, a day at a time, to
open one; kit log <command> opens the last run of a command, as in kit log
apply. Otherwise it shows the last run, of the command named if one is.

Each run's log is a JSON-lines file in ~/Library/Logs/kit, kept 30 days.
--json prints a run's records as one document, of the command named; with
none named, it lists the runs, newest first.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs, err := a.dirs()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			var path string
			switch {
			case len(args) > 0:
				runs, err := logs.Runs(dirs.Logs)
				if err != nil {
					return err
				}
				i := slices.IndexFunc(runs, func(r logs.Run) bool { return isOf(r, args) })
				if i < 0 {
					return notFound{message: "no run of " + strings.Join(args, " ") + " logged"}
				}
				path = runs[i].Path
			case a.json:
				runs, err := logs.Runs(dirs.Logs)
				if err != nil {
					return err
				}
				return writeRuns(out, runs)
			case a.pretty(out):
				runs, err := logs.Runs(dirs.Logs)
				if err != nil {
					return err
				}
				if len(runs) == 0 {
					return notFound{message: "no runs logged yet"}
				}
				path, err = a.Pick(cmd.Context(), []string{""}, render.RunList(runs), render.RunListKeys)
				if errors.Is(err, ask.ErrCancelled) {
					return nil
				}
				if err != nil {
					return err
				}
			default:
				path, err = logs.Latest(dirs.Logs)
				if errors.Is(err, logs.ErrNoLogs) {
					return notFound{message: "no runs logged yet"}
				}
				if err != nil {
					return err
				}
			}
			records, err := logs.Read(path)
			if err != nil {
				return err
			}
			if a.json {
				return render.WriteJSON(out, struct {
					Schema  int           `json:"schema"`
					Log     string        `json:"log"`
					Records []logs.Record `json:"records"`
				}{logSchema, path, records})
			}
			if a.pretty(out) {
				return render.LogRun(a.colors(out), a.Width(out), records)
			}
			return render.LogView(a.colors(out), path, records)
		},
	}
}

// isOf is whether r was of the command words name, as in apply, or brew add:
// what it was of begins with them.
func isOf(r logs.Run, words []string) bool {
	of := strings.Fields(r.Of())
	return len(of) >= len(words) && slices.Equal(of[:len(words)], words)
}

// loggedRun is a run, as kit log --json lists it: its log, what it was of,
// when it started and finished, and how it ended (result).
type loggedRun struct {
	Log      string              `json:"log"`
	Command  string              `json:"command"`
	Started  time.Time           `json:"started"`
	Finished time.Time           `json:"finished,omitzero"`
	Result   string              `json:"result"`
	Counts   map[check.State]int `json:"counts,omitempty"`
}

// writeRuns writes runs to out as one document, newest first.
func writeRuns(out io.Writer, runs []logs.Run) error {
	listed := make([]loggedRun, len(runs))
	for i, r := range runs {
		listed[i] = loggedRun{Log: r.Path, Command: r.Of(), Started: r.Started.Time, Result: result(r)}
		if r.Done() {
			listed[i].Finished, listed[i].Counts = r.Finished.Time, r.Finished.Counts
		}
	}
	return render.WriteJSON(out, struct {
		Schema int         `json:"schema"`
		Runs   []loggedRun `json:"runs"`
	}{logSchema, listed})
}

// result is how a run ended, in a word: unfinished, or the worst state its
// steps ended in, failed first, then attention, deferred and ok.
func result(r logs.Run) string {
	if !r.Done() {
		return "unfinished"
	}
	for _, s := range []check.State{check.Failed, check.Attention, check.Deferred} {
		if r.Finished.Counts[s] > 0 {
			return string(s)
		}
	}
	return string(check.OK)
}
