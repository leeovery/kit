package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/backup"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/steps"
)

// backupCommand is kit backup's name.
const backupCommand = "backup"

// backups are the backup tools this Mac uses, as its [backups] lines say,
// the shared file's and its own, in the order kit has them (backup.Tools),
// whatever the files' order: a tool kit doesn't know, or an option a line
// doesn't take, is refused, saying where.
func (r *run) backups() ([]steps.BackupTool, error) {
	list, err := r.cfg.List(config.BackupsKind, r.machine)
	if err != nil {
		return nil, err
	}
	tools := make([]steps.BackupTool, len(list.Entries))
	for i, e := range list.Entries {
		if tools[i], err = steps.ParseBackup(e); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(tools, func(a, b steps.BackupTool) int {
		return cmp.Compare(backup.Index(a.Driver.About().ID), backup.Index(b.Driver.About().ID))
	})
	return tools, nil
}

// backupNames are the IDs of tools.
func backupNames(tools []steps.BackupTool) []string {
	names := make([]string, len(tools))
	for i, b := range tools {
		names[i] = b.Driver.About().ID
	}
	return names
}

// placeBackups places the step of each tool among all, the run's steps, as
// its line says: after the steps it names, waiting for the things, checking
// once they're in, as a step of your own does, and before the steps it
// names. One placed after or before what isn't a step fails, saying so.
// What the tool comes as (its Package), declared for this Mac, is waited
// for too, as kit apply installs it.
func (r *run) placeBackups(all []engine.Step, tools []steps.BackupTool) {
	names := stepNames(all)
	for _, b := range tools {
		about := b.Driver.About()
		i := slices.IndexFunc(all, func(s engine.Step) bool { return s.Name == about.ID })
		if i < 0 {
			continue
		}
		afters := b.After
		if about.Package != "" && r.declares(about.Package) && !slices.Contains(afters, about.Package) {
			afters = append(slices.Clone(afters), about.Package)
		}
		after, waits, before, err := r.place(afters, b.Before, names)
		if err != nil {
			broken(&all[i], fmt.Sprintf("%s: %s", b.Pos, err))
			continue
		}
		all[i].After = append(all[i].After, after...)
		all[i].Waits = append(all[i].Waits, waits...)
		all[i].Before = append(all[i].Before, before...)
	}
}

// stepNames are the names of all, the run's steps.
func stepNames(all []engine.Step) map[string]bool {
	names := make(map[string]bool, len(all))
	for _, s := range all {
		names[s.Name] = true
	}
	return names
}

// notAFeature is the error of naming a backup tool as a feature, as it
// goes in [backups] now, saying the command, kit backup and its words, that
// does what was meant: nil for any other name.
func notAFeature(name string, words ...string) error {
	if _, ok := backup.Named(name); !ok {
		return nil
	}
	return fmt.Errorf("%s is a backup tool, so it goes in [backups] now, not [features]: kit %s %s", name, backupCommand, strings.Join(words, " "))
}

// movedToBackups is the error of a [features] line naming a backup tool,
// saying where it is and the command declaring it in [backups], as its
// section had it: nil for any other.
func movedToBackups(e config.Entry) error {
	words := []string{"add", e.Name}
	for _, after := range e.After() {
		words = append(words, "--after", config.Quote(after))
	}
	if e.Scope == config.Shared {
		words = append(words, "--shared")
	}
	if err := notAFeature(e.Name, words...); err != nil {
		return fmt.Errorf("%s: %w, once you've removed this line", e.Pos(), err)
	}
	return nil
}

// backupOptions are kit backup add's options.
type backupOptions struct {
	after, before []string
	addOptions
}

// newBackupCommand is kit backup: declaring the backup tools a Mac uses.
func newBackupCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     backupCommand,
		Short:   "Backup tools this Mac uses, each checked",
		GroupID: groupFiles,
	}
	var opts backupOptions
	add := &cobra.Command{
		Use:   "add <tool>",
		Short: "Declare a backup tool this Mac uses (--shared: every Mac)",
		Long: `Declare a backup tool this Mac uses, one of ` + strings.Join(backup.IDs(), ", ") + `: kit checks its
backups run, and keeps [backup exclusions] out of them through it. --after
names a step its check comes after, or one of a step's things, as in
cask:arq; --before a step that comes after it; each as often as wanted.` + declaredWhere,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, backupCommand+" add", func(ctx context.Context, r *run) error {
				return a.addBackup(ctx, r, args[0], opts)
			})
		},
	}
	add.Flags().StringArrayVar(&opts.after, "after", nil, "a step its check comes after, or one of a step's things, as in cask:arq")
	add.Flags().StringArrayVar(&opts.before, "before", nil, "a step that comes after it")
	add.Flags().BoolVar(&opts.shared, "shared", false, "declare for every Mac, not this one alone")
	add.Flags().StringVar(&opts.note, "note", "", "why it's declared, kept after it")
	cmd.AddCommand(add, newLineRemove(a, backupCommand))
	return cmd
}

// addBackup declares the backup tool called name, its check placed as opts
// say, in this Mac's file or the shared one, taking it out of the Macs'
// files that had it, then commits and pushes. One declared already, in the
// shared file or the one it goes in, is refused, as is one placed after or
// before what isn't a step.
func (a *app) addBackup(ctx context.Context, r *run, name string, opts backupOptions) error {
	var words []string
	for _, after := range opts.after {
		words = append(words, "--after", config.Quote(after))
	}
	for _, before := range opts.before {
		words = append(words, "--before", config.Quote(before))
	}
	b, err := steps.ReadBackup(name, strings.Join(words, " "))
	if err != nil {
		return err
	}
	scope := r.scope(opts.shared)
	c := startChanges(r, []string{name})
	c.step(ctx, name, func(context.Context) check.Result {
		where, err := r.cfg.Where(config.BackupsKind, name)
		if err != nil {
			return failed(err)
		}
		for _, e := range where {
			if e.Scope == config.Shared || e.Scope == scope {
				return check.Result{State: check.Failed, Reason: "declared already, in " + e.Pos()}
			}
		}
		if _, _, _, err := r.place(b.After, b.Before, stepNames(r.pipeline.Steps())); err != nil {
			return failed(err)
		}
		said, err := c.declare(config.BackupsKind, scope, config.Entry{Name: name, Value: strings.Join(words, " "), Note: opts.note})
		if err != nil {
			return failed(err)
		}
		return check.Result{State: check.OK, Summary: "declared in " + scope + said}
	})
	c.sync(ctx, commitMessage("add", backupCommand, []string{name}, r.machine, opts.note))
	return c.finish()
}
