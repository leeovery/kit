package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/steps"
)

func newDoneCommand(a *app) *cobra.Command {
	var undo bool
	cmd := &cobra.Command{
		Use:   "done <name>...",
		Short: "Mark steps done by hand, on this Mac",
		Long: `Mark steps of kit-config's [manual] done on this Mac: those kit can't check
itself, such as signing in somewhere. --undo marks them not done.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, names []string) error {
			r, err := a.prepare("done", "done")
			if err != nil {
				return err
			}
			defer func() { _ = r.close() }()
			list, err := r.cfg.List(config.ManualKind, r.machine)
			if err != nil {
				return err
			}
			for _, name := range names {
				if !slices.Contains(list.Names(), name) {
					return fmt.Errorf("no step by hand named %s: kit-config's [manual] has %v", name, list.Names())
				}
			}
			if err := steps.MarkDone(r.stateDir, names, r.now, undo); err != nil {
				return err
			}
			verb := "done"
			if undo {
				verb = "not done"
			}
			_, err = fmt.Fprintf(a.Stdout, "Marked %s on this Mac: %v\n", verb, names)
			return err
		},
	}
	cmd.Flags().BoolVar(&undo, "undo", false, "mark them not done")
	return cmd
}
