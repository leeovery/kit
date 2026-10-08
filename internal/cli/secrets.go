package cli

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/secret"
)

// newSecretCommand is kit secret: secrets from 1Password, added, removed
// and synced.
func newSecretCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     secretKind,
		Short:   "Secrets from 1Password, in the shell's environment or files",
		GroupID: groupFiles,
	}
	var opts addOptions
	so := &opts.secret
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Keep a secret in 1Password, declare it for this Mac (--shared: every Mac), and sync it",
		Long: `Keep a secret in 1Password, declare it, and sync it, its value never shown.
<name> is what it fills: an environment variable's name (in ~/.secrets.zsh,
which the shell sources), or a file's path.

Its value is typed, unshown, at a terminal; or comes on standard input
(--stdin, how an agent passes one, never on a command line); or from a file
(--from, kept as an attachment); or is in 1Password already (--ref
op://vault/item/field: nothing is stored, and its line goes in its item's
section, short, when the file has one, else in the plain [secrets], in full).
A new value goes in the item the file's secrets section names, as in
[secrets op://vault/item] (--item when the file has none, which makes the
section, or several), as the field --field names, a section and a field as in
GitHub/token; then it's read back and compared, declared and synced.
--mode sets a file's permissions (600 otherwise).

It's declared in this Mac's declarations file, unless --shared declares it
for every Mac. The change is committed and pushed to the config repository.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, secretKind+" add", func(ctx context.Context, r *run) error {
				return a.addSecret(ctx, r, args, opts)
			})
		},
	}
	f := add.Flags()
	f.BoolVar(&opts.shared, "shared", false, "declare for every Mac, not this one alone")
	f.StringVar(&opts.note, "note", "", "why it's declared, kept after its name")
	f.StringVar(&so.ref, "ref", "", "where 1Password keeps it already (op://vault/item/field): nothing is stored")
	f.StringVar(&so.from, "from", "", "the file whose contents it is, kept in 1Password as an attachment")
	f.BoolVar(&so.stdin, "stdin", false, "its value on standard input, never on a command line")
	f.StringVar(&so.field, "field", "", "where in the item its value goes, as in GitHub/token")
	f.StringVar(&so.mode, "mode", "", "a file's permissions, as in 644 (600 otherwise)")
	f.StringVar(&so.item, "item", "", "the 1Password item its value goes in (op://vault/item), when the file has no item's section or several")
	var shared bool
	var value valueChoice
	remove := &cobra.Command{
		Use:     "remove <name>...",
		Aliases: []string{"rm"},
		Short:   "Take secrets off this Mac, and undeclare them; their values kept or deleted",
		Long: `Take secrets off this Mac (a variable out of ~/.secrets.zsh, a file deleted),
and undeclare them. Their values stay in 1Password (--keep-value) or go
(--delete-value), asked at a terminal; a value given in full, outside the
items' sections, is left where it is. One declared for every Mac needs
--shared. The change is committed and pushed to the config repository.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, secretKind+" remove", func(ctx context.Context, r *run) error {
				return a.removeSecrets(ctx, r, args, shared, value)
			})
		},
	}
	remove.Flags().BoolVar(&shared, "shared", false, "take it out of the shared file, every Mac's list")
	remove.Flags().BoolVar(&value.delete, "delete-value", false, "delete its value from 1Password too")
	remove.Flags().BoolVar(&value.keep, "keep-value", false, "keep its value in 1Password")
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Read every secret from 1Password, and put each in place",
		Long: `Read every secret kit-config's secrets sections declare from 1Password, in
one sitting, and put each in place: ~/.secrets.zsh, and the files. Run it
after changing a value in 1Password. A value that fails to read keeps its
previous one. Values are never printed or logged.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, secretKind+" sync", a.secretsSync)
		},
	}
	cmd.AddCommand(add, remove, sync)
	return cmd
}

// secretsSync syncs every secret, as a run of one step.
func (a *app) secretsSync(ctx context.Context, r *run) error {
	k, ok := r.kindsByName["secret"].(*secret.Secrets)
	if !ok || !slices.Contains(r.kinds, "secret") {
		return errors.New("no secrets are declared: kit-config's [secrets] declares them")
	}
	r.sink.Emit(event.RunStarted{Time: r.now, Command: r.command, Machine: r.machine, Version: r.version, Steps: []event.Step{{Name: "secret", Title: k.Title(), Area: "Drift"}}})
	started := time.Now()
	r.sink.Emit(event.StepStarted{Time: started, Step: "secret", Doing: "syncing"})
	report, err := k.Sync(event.WithStep(ctx, "secret"))
	res := check.Result{State: check.OK, Summary: report.Summary()}
	switch {
	case err != nil:
		res = check.Result{State: check.Failed, Reason: err.Error()}
	case len(report.Failed) > 0:
		res = check.Result{State: check.Failed, Reason: report.Error()}
	}
	r.sink.Emit(event.StepFinished{Time: time.Now(), Step: "secret", Result: res, Duration: time.Since(started)})
	counts := event.Tally(res)
	r.sink.Emit(event.RunFinished{Time: time.Now(), Duration: time.Since(started), Counts: counts})
	if res.State != check.OK {
		return attention{}
	}
	return nil
}
