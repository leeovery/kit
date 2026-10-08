package cli

import (
	"errors"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// logSchema is kit log --json's document's version.
const logSchema = 1

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

Each run's log is a JSON-lines file in ~/Library/Logs/kit, kept 30 days;
--json prints a run's records as one document.`,
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
					return attention{message: "no run of " + strings.Join(args, " ") + " logged"}
				}
				path = runs[i].Path
			case a.pretty(out):
				runs, err := logs.Runs(dirs.Logs)
				if err != nil {
					return err
				}
				if len(runs) == 0 {
					return attention{message: "no runs logged yet"}
				}
				path, err = a.Pick(cmd.Context(), []string{""}, render.RunList(runs), []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "open"}, {Key: "q", Does: "quit"}})
				if errors.Is(err, ask.ErrCancelled) {
					return nil
				}
				if err != nil {
					return err
				}
			default:
				path, err = logs.Latest(dirs.Logs)
				if errors.Is(err, logs.ErrNoLogs) {
					return attention{message: "no runs logged yet"}
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
