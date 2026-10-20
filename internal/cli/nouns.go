package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/redact"
)

// kit's help lists its commands in groups: those about the whole Mac, then
// what kit manages, a command each, by sort. A command about one kind of
// thing names it first, then what to do with it, as in kit brew add.
const (
	groupMac      = "mac"
	groupPackages = "packages"
	groupSettings = "settings"
	groupFiles    = "files"
	groupOwn      = "own"
)

// groups are the groups of kit's help, in order.
var groups = []*cobra.Group{
	{ID: groupMac, Title: "This Mac:"},
	{ID: groupPackages, Title: "Packages:"},
	{ID: groupSettings, Title: "Settings:"},
	{ID: groupFiles, Title: "Files, secrets and features:"},
	{ID: groupOwn, Title: "Your own:"},
}

// helpGroups are the groups of kit's help the kinds' commands are in, by the
// kinds' groups.
var helpGroups = map[kind.Group]string{kind.Packages: groupPackages, kind.Settings: groupSettings}

// nounCommands are the commands of what kit manages: a command each for the
// kinds whose things are added and removed alike, installed and declared,
// or uninstalled and undeclared, in the order a run takes them; then the
// rest.
func nounCommands(a *app) []*cobra.Command {
	var cmds []*cobra.Command
	for _, d := range kinds.All {
		if d.Help != nil {
			cmds = append(cmds, newKindCommand(a, d))
		}
	}
	return append(cmds,
		newFileCommand(a),
		newPathCommand(a),
		newSecretCommand(a),
		newPrefsCommand(a),
		newFeatureCommand(a),
		newBackupCommand(a),
		newLineCommand(a, "check", "Checks of your own, each a command: exit 0, all's well", newLineAdd(a, "check", "Declare a check of your own, and run it once")),
		newLineCommand(a, config.HourlyKind, "Jobs of your own, run every hour by kit nightly", newLineAdd(a, config.HourlyKind, "Declare an hourly job")),
		newLineCommand(a, config.ManualKind, "Steps done by hand, each saying what to do", newLineAdd(a, config.ManualKind, "Declare a step done by hand"), newDoneCommand(a)),
		newStepCommand(a),
	)
}

// prepared runs do in a run prepared for command, as in brew add, then
// closes the run. The run is of the command and what it was given, as in
// brew add jq, which its log keeps.
func (a *app) prepared(cmd *cobra.Command, command string, do func(ctx context.Context, r *run) error) error {
	r, err := a.prepare(redact.Text(strings.Join(append([]string{command}, cmd.Flags().Args()...), " ")), strings.ReplaceAll(command, " ", "-"))
	if err != nil {
		return err
	}
	err = do(cmd.Context(), r)
	if closeErr := r.close(); err == nil {
		err = closeErr
	}
	return err
}

// declaredWhere is how adding says where a thing's declared.
const declaredWhere = `

It's declared in this Mac's declarations file, named after it, unless --shared
declares it for every Mac, in the shared file, which takes it out of each
Mac's own. The change is committed and pushed to the config repository.`

// undeclaredWhere is how removing says where a thing's undeclared.
const undeclaredWhere = `

It's taken out of this Mac's declarations file; one declared for every Mac
needs --shared, which takes it out of the shared file, every Mac's list. The
change is committed and pushed to the config repository.`

// newKindCommand is kit <kind>: adding and removing d's things, and turning
// them off and on, when they can be.
func newKindCommand(a *app, d kind.Descriptor) *cobra.Command {
	n := d.Help
	cmd := &cobra.Command{
		Use:     d.Name,
		Short:   n.What,
		GroupID: helpGroups[d.Group],
	}
	var opts addOptions
	add := &cobra.Command{
		Use:   "add <name>...",
		Short: n.Add + " for this Mac (--shared: every Mac)",
		Long: n.Add + "." + declaredWhere + `

Each goes in its sorted place. --note records why, after the name. --temp
installs without declaring, for a throwaway: it's quiet for 7 days, then kit
reconcile asks.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, d.Name+" add", func(ctx context.Context, r *run) error {
				return a.add(ctx, r, d.Name, args, opts)
			})
		},
	}
	add.Flags().BoolVar(&opts.shared, "shared", false, "declare for every Mac, not this one alone")
	add.Flags().BoolVar(&opts.temp, "temp", false, "install without declaring: quiet for 7 days, then reconcile asks")
	add.Flags().StringVar(&opts.note, "note", "", "why it's declared, kept after its name")
	var shared bool
	remove := &cobra.Command{
		Use:     "remove <name>...",
		Aliases: []string{"rm"},
		Short:   n.Remove,
		Long: n.Remove + "." + undeclaredWhere + `

When something installed still needs it, nothing changes, and why is passed
on.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, d.Name+" remove", func(ctx context.Context, r *run) error {
				return a.remove(ctx, r, d.Name, args, shared)
			})
		},
	}
	remove.Flags().BoolVar(&shared, "shared", false, "take it out of the shared file, every Mac's list")
	cmd.AddCommand(add, remove)
	if n.Switch != nil {
		cmd.AddCommand(switchCommands(a, d)...)
	}
	return cmd
}

// newFileCommand is kit file: the files linked into the home folder from
// kit-config.
func newFileCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     linked.StepName,
		Short:   "Files linked into the home folder from kit-config",
		GroupID: groupFiles,
	}
	var opts addOptions
	add := &cobra.Command{
		Use:   "add <path>...",
		Short: "Move files into kit-config, and link them back (--shared: every Mac's)",
		Long: `Move a file, or every file in a folder, into kit-config's home folder for
this Mac (--shared: every Mac's), and link it back in its place. An edit
through the link is an edit in kit-config. The change is committed and
pushed to the config repository.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, linked.StepName+" add", func(ctx context.Context, r *run) error {
				return a.addFiles(ctx, r, args, opts)
			})
		},
	}
	add.Flags().BoolVar(&opts.shared, "shared", false, "every Mac's, in the shared home folder")
	add.Flags().StringVar(&opts.note, "note", "", "why, in the commit")
	var shared bool
	remove := &cobra.Command{
		Use:     "remove <path>...",
		Aliases: []string{"rm"},
		Short:   "Put a copy of linked files back in place of their links, out of kit-config",
		Long: `Put a copy of a linked file back in place of its link, and take it out of
kit-config. One of every Mac's needs --shared. The change is committed and
pushed to the config repository.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, linked.StepName+" remove", func(ctx context.Context, r *run) error {
				return a.removeFiles(ctx, r, args, shared)
			})
		},
	}
	remove.Flags().BoolVar(&shared, "shared", false, "one of every Mac's, in the shared home folder")
	cmd.AddCommand(add, remove)
	return cmd
}

// newPathCommand is kit path: the directories on the PATH, kit's and the
// shell's.
func newPathCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     pathsKind,
		Short:   "Directories on the PATH, kit's and the shell's",
		GroupID: groupFiles,
	}
	var opts addOptions
	add := &cobra.Command{
		Use:   "add <dir>...",
		Short: "Put directories last on the PATH, for this Mac (--shared: every Mac)",
		Long:  "Put directories last on the PATH, kit's and the shell's." + declaredWhere,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, pathsKind+" add", func(ctx context.Context, r *run) error {
				return a.addPaths(ctx, r, args, opts)
			})
		},
	}
	add.Flags().BoolVar(&opts.shared, "shared", false, "declare for every Mac, not this one alone")
	add.Flags().StringVar(&opts.note, "note", "", "why it's declared, kept after it")
	var shared bool
	remove := &cobra.Command{
		Use:     "remove <dir>...",
		Aliases: []string{"rm"},
		Short:   "Take directories off the PATH",
		Long:    "Take directories off the PATH, kit's and the shell's." + undeclaredWhere,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, pathsKind+" remove", func(ctx context.Context, r *run) error {
				return a.removePaths(ctx, r, args, shared)
			})
		},
	}
	remove.Flags().BoolVar(&shared, "shared", false, "take it out of the shared file, every Mac's list")
	cmd.AddCommand(add, remove)
	return cmd
}

// newLineCommand is kit <name> for a section of lines of your own (checks,
// jobs, steps by hand): add, then remove, and extra.
func newLineCommand(a *app, name, short string, add *cobra.Command, extra ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: short, GroupID: groupOwn}
	cmd.AddCommand(append([]*cobra.Command{add, newLineRemove(a, name)}, extra...)...)
	return cmd
}

// lineUsage is how a line of name's section is added.
func lineUsage(name string) string {
	if name == config.ManualKind {
		return `add <name> "<what to do>" [-- <command saying whether it's done>]`
	}
	return "add <name> -- <command>"
}

// newLineAdd is kit <name> add, for a section of lines of your own.
func newLineAdd(a *app, name, short string) *cobra.Command {
	var opts addOptions
	long := map[string]string{
		"check":            "Declare a check of your own, a command whose exit 0 says all's well, and run\nit once.",
		config.HourlyKind:  "Declare a job of your own, a command kit nightly runs every hour.",
		config.NightlyKind: "Declare a job of your own, a command kit nightly runs in the nightly run,\nonce a day.",
		config.ManualKind:  "Declare a step done by hand: what to do, and a command saying whether it's\ndone (else kit manual done marks it). The command runs once, to say how it\nstands.",
	}[name]
	cmd := &cobra.Command{
		Use:   lineUsage(name),
		Short: short + " (--shared: every Mac)",
		Long:  long + declaredWhere,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, name+" add", func(ctx context.Context, r *run) error {
				return a.addLine(ctx, r, name, args, cmd.ArgsLenAtDash(), opts)
			})
		},
	}
	cmd.Flags().BoolVar(&opts.shared, "shared", false, "declare for every Mac, not this one alone")
	cmd.Flags().StringVar(&opts.note, "note", "", "why it's declared, kept after it")
	if name == config.ManualKind {
		cmd.Flags().StringVar(&opts.group, "group", "", "the group it's in")
	}
	return cmd
}

// lineThings are what each section of lines holds, by its command's name.
var lineThings = map[string]string{"check": "checks", config.HourlyKind: "hourly jobs", config.NightlyKind: "nightly jobs", config.ManualKind: "steps by hand", stepCommand: "steps of your own, and their folders", backupCommand: "backup tools"}

// newLineRemove is kit <name> remove, for a section of lines of your own.
func newLineRemove(a *app, name string) *cobra.Command {
	var shared bool
	cmd := &cobra.Command{
		Use:     "remove <name>...",
		Aliases: []string{"rm"},
		Short:   "Undeclare " + lineThings[name],
		Long:    "Undeclare " + lineThings[name] + "." + undeclaredWhere,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, name+" remove", func(ctx context.Context, r *run) error {
				return a.remove(ctx, r, name, args, shared)
			})
		},
	}
	cmd.Flags().BoolVar(&shared, "shared", false, "take it out of the shared file, every Mac's list")
	return cmd
}
