package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, as --version does",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := cmd.Root()
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s version %s\n", root.DisplayName(), root.Version)
			return err
		},
	}
}
