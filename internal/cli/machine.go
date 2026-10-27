package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
)

// machineCommand names kit machine, which adds Macs to the config.
const machineCommand = "machine"

func newMachineCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "machine [<name>]",
		Short:       "Show this Mac's name, or set it to one of the config's Macs",
		Annotations: map[string]string{brief: "this Mac's name"},
		Long: `Show this Mac's name, or set it to one of the Macs kit.toml knows.

kit reads a Mac's own files (brew.<name> and the rest) by this name, so it's
settled before anything else. A replacement Mac can take the name of the one
it replaces; a new one is added to the config with kit machine add.`,
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
	var from, description string
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a Mac to the config, a copy of another's (--from) or empty",
		Long: `Add a Mac to kit-config: its table in kit.toml, after the last Mac's, and its
folder, a copy of another Mac's with --from (every file as it is), or, without
it, empty but for its declarations file. This Mac's name is left as it is: the
new Mac takes the name, as kit machine <name>, when it's set up. The change is
committed and pushed to the config repository.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, machineCommand+" add", func(ctx context.Context, r *run) error {
				return addMachine(ctx, r, args[0], from, description)
			})
		},
	}
	add.Flags().StringVar(&from, "from", "", "the Mac whose folder it starts as a copy of")
	add.Flags().StringVar(&description, "description", "", "what the Mac is, as in \"Mac Studio\"")
	cmd.AddCommand(add)
	return cmd
}

// addMachine adds the Mac called name to the config, a copy of from's
// folder unless from is "", and commits it.
func addMachine(ctx context.Context, r *run, name, from, description string) error {
	if name == "add" {
		return fmt.Errorf("a Mac can't be called add: that's kit machine add")
	}
	if err := r.cfg.AddMac(name, from, description); err != nil {
		return err
	}
	c := startChanges(r, []string{name})
	c.step(ctx, name, func(context.Context) check.Result {
		c.changed(config.File)
		c.changed(name)
		said := "added to kit-config"
		if from != "" {
			said += ", a copy of " + from
		}
		return check.Result{State: check.OK, Summary: said}
	})
	c.sync(ctx, commitMessage("add", machineCommand, []string{name}, r.machine, ""))
	return c.finish()
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
