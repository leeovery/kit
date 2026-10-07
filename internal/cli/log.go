package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/render"
)

// logSchema is kit log --json's document's version.
const logSchema = 1

func newLogCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "log",
		Short: "Show what the last run did: every check and command, with timings",
		Long: `Show what the last run did: each step's check, with how it stood, and every
command it ran, with its exit code and how long it took.

Each run's log is a JSON-lines file in ~/Library/Logs/kit, kept 30 days;
--json prints the last one's records as one document.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dirs, err := a.dirs()
			if err != nil {
				return err
			}
			path, err := logs.Latest(dirs.Logs)
			if errors.Is(err, logs.ErrNoLogs) {
				return attention{message: "no runs logged yet"}
			}
			if err != nil {
				return err
			}
			records, err := logs.Read(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
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
