package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
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

// noun is a kind kit manages as a command of its own: its name, its group in
// kit's help, what it manages, as its help shows it, and what adding and
// removing do.
type noun struct {
	name, group, what, add, remove string
}

// kindNouns are the kinds whose things are added and removed alike:
// installed and declared, or uninstalled and undeclared.
var kindNouns = []noun{
	{"brew", groupPackages, "Homebrew's formulae", "Install formulae, and declare them", "Uninstall formulae, and undeclare them"},
	{"cask", groupPackages, "Homebrew's casks", "Install casks, and declare them", "Uninstall casks, and undeclare them"},
	{"mas", groupPackages, "App Store apps", "Install App Store apps, by name or id, and declare them", "Uninstall App Store apps, and undeclare them"},
	{"npm", groupPackages, "npm's global packages", "Install global npm packages, and declare them", "Uninstall global npm packages, and undeclare them"},
	{"composer", groupPackages, "Composer's global packages", "Install global Composer packages, and declare them", "Uninstall global Composer packages, and undeclare them"},
	{"go", groupPackages, "Go tools", "Install Go tools, by module path, and declare them", "Uninstall Go tools, and undeclare them"},
	{"gh", groupPackages, "The GitHub CLI's extensions", "Install GitHub CLI extensions, as owner/repo, and declare them", "Uninstall GitHub CLI extensions, and undeclare them"},
	{"tmux", groupPackages, "tmux's plugins, declared in tmux's config", "Install tmux plugins, and declare them in tmux's config", "Uninstall tmux plugins, and take them out of tmux's config"},
	{"claude-mcp", groupPackages, "Claude Code's MCP servers", "Declare MCP servers Claude Code has, as it has them", "Remove MCP servers from Claude Code, and undeclare them"},
	{"login-item", groupPackages, "macOS login items", "Add login items, by app, and declare them", "Remove login items, and undeclare them"},
	{"defaults", groupSettings, "macOS settings, in defaults write's form", "Declare macOS settings as this Mac has them", "Unset macOS settings, macOS's default coming back, and undeclare them"},
	{"power", groupSettings, "Power settings, in pmset's form", "Declare power settings as this Mac has them", "Undeclare power settings, leaving their values"},
	{"git-config", groupSettings, "git's global settings", "Declare git settings as this Mac has them", "Unset git settings, and undeclare them"},
	{"backup-exclusion", groupSettings, "Folders Time Machine and Arq leave out", "Leave folders out of backups, and declare them", "Put folders back in backups, and undeclare them"},
	{"spotlight-exclusion", groupSettings, "Folders Spotlight leaves out", "Leave folders out of Spotlight, and declare them", "Undeclare Spotlight's exclusions (System Settings takes a folder off its list)"},
}

// nounCommands are the commands of what kit manages.
func nounCommands(a *app) []*cobra.Command {
	var cmds []*cobra.Command
	for _, n := range kindNouns {
		var extra []*cobra.Command
		if n.name == claudeMCP {
			extra = mcpSwitches(a)
		}
		cmds = append(cmds, newKindCommand(a, n, extra...))
	}
	return append(cmds,
		newFileCommand(a),
		newPathCommand(a),
		newSecretCommand(a),
		newPrefsCommand(a),
		newFeatureCommand(a),
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

// newKindCommand is kit <noun>: adding and removing a kind's things, and
// extra, what else the kind does.
func newKindCommand(a *app, n noun, extra ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:     n.name,
		Short:   n.what,
		GroupID: n.group,
	}
	var opts addOptions
	add := &cobra.Command{
		Use:   "add <name>...",
		Short: n.add + " for this Mac (--shared: every Mac)",
		Long: n.add + "." + declaredWhere + `

Each goes in its sorted place. --note records why, after the name. --temp
installs without declaring, for a throwaway: it's quiet for 7 days, then kit
reconcile asks.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, n.name+" add", func(ctx context.Context, r *run) error {
				return a.add(ctx, r, n.name, args, opts)
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
		Short:   n.remove,
		Long: n.remove + "." + undeclaredWhere + `

When something installed still needs it, nothing changes, and why is passed
on.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, n.name+" remove", func(ctx context.Context, r *run) error {
				return a.remove(ctx, r, n.name, args, shared)
			})
		},
	}
	remove.Flags().BoolVar(&shared, "shared", false, "take it out of the shared file, every Mac's list")
	cmd.AddCommand(append([]*cobra.Command{add, remove}, extra...)...)
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
	return cmd
}

// lineThings are what each section of lines of your own holds, by its
// command's name.
var lineThings = map[string]string{"check": "checks", config.HourlyKind: "hourly jobs", config.NightlyKind: "nightly jobs", config.ManualKind: "steps by hand", stepCommand: "steps of your own, and their folders"}

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
