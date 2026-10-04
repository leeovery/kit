package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
)

func newMachineCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "machine [<name>]",
		Short: "Show this Mac's name, or set it to one of the config's Macs",
		Long: `Show this Mac's name, or set it to one of the Macs kit.toml knows.

kit reads a Mac's own files (brew.<name> and the rest) by this name, so it's
settled before anything else. A replacement Mac can take the name of the one
it replaces.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs, err := a.dirs()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return a.showMachine(cmd, dirs)
			}
			return a.setMachine(cmd, dirs, args[0])
		},
	}
}

func (a *app) showMachine(cmd *cobra.Command, dirs config.Dirs) error {
	name, err := config.ReadMachine(dirs.State)
	if err != nil {
		return err
	}
	if name == "" {
		hint := "run kit machine <name>"
		if cfg, err := a.loadConfig(dirs); err == nil {
			hint += ", one of " + strings.Join(cfg.MacNames(), ", ")
		}
		return attention{message: "this Mac has no name yet: " + hint}
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), name)
	return err
}

func (a *app) setMachine(cmd *cobra.Command, dirs config.Dirs, name string) error {
	cfg, err := a.loadConfig(dirs)
	if err != nil {
		return err
	}
	if !cfg.Knows(name) {
		return fmt.Errorf("no Mac named %s in %s: one of %s", name, config.File, strings.Join(cfg.MacNames(), ", "))
	}
	was, err := config.ReadMachine(dirs.State)
	if err != nil {
		return err
	}
	if was == name {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "this Mac is %s already\n", name)
		return err
	}
	if err := config.WriteMachine(dirs.State, name); err != nil {
		return err
	}
	if was == "" {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "this Mac is %s\n", name)
	} else {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "this Mac was %s; it's %s now\n", was, name)
	}
	return err
}
