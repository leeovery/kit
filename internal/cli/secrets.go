package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/steps"
)

// newSecretCommand is kit secret: secrets from their stores, added,
// removed, synced, and the blocks they're declared in listed.
func newSecretCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     secretKind,
		Short:   "Secrets from their stores, in the shell's environment or files",
		GroupID: groupFiles,
	}
	var opts addOptions
	so := &opts.secret
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Keep a secret in its store, declare it for this Mac (--shared: every Mac), and put it in place",
		Long: `Keep a secret in its store, declare it, and put it in place, its value
never shown. <name> is what it fills: an environment variable's name (in
~/.secrets.zsh, which the shell sources), or a file's path.

Its value is typed, unshown, at a terminal; or comes on standard input
(--stdin, how an agent passes one, never on a command line); or from a file
(--from, kept as an attachment, named as the file is, or as --field says,
in a section as Section/name); or is in a store already (--ref and where
it's kept, in full: nothing is stored).

A new value goes where you say: --in and a block's name, as a block's
header gives it ([secrets <item> --as <name>]), or --item and the item, in
full; told neither, in this file's block for an item when it has one alone.
--field says where in the item, in its store's words. kit secret places
lists the blocks. The line goes in this file's block for that item, which
kit makes when there's none, going by the name --in gave. Then the value's
read back and compared, declared, and put in place: that secret alone, as
its store gives it back, no other secret read. A variable's value of many
lines is refused: declare it as a file.

A value in a store already (--ref) goes in this file's block for its item,
short, when there's one, else in the plain [secrets], in full.

--mode sets a file's permissions (600 otherwise). It's declared in this
Mac's declarations file, unless --shared declares it for every Mac, in the
shared file, which takes it out of each Mac's own, whatever block it was
in. The change is committed and pushed to the config repository.

` + storesKnown(),
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
	f.StringVar(&so.ref, "ref", "", "where a store keeps it already, in full: nothing is stored")
	f.StringVar(&so.from, "from", "", "the file whose contents it is, kept in its store as an attachment")
	f.BoolVar(&so.stdin, "stdin", false, "its value on standard input, never on a command line")
	f.StringVar(&so.in, "in", "", "the block its value goes in, by the name its header gives it (kit secret places)")
	f.StringVar(&so.item, "item", "", "the item its value goes in, in full")
	f.StringVar(&so.field, "field", "", "where in the item its value goes, in its store's words")
	f.StringVar(&so.mode, "mode", "", "a file's permissions, as in 644 (600 otherwise)")
	var shared bool
	var value valueChoice
	remove := &cobra.Command{
		Use:     "remove <name>...",
		Aliases: []string{"rm"},
		Short:   "Take secrets off this Mac, and undeclare them; their values kept or deleted",
		Long: `Take secrets off this Mac (a variable out of ~/.secrets.zsh, a file deleted),
and undeclare them. Their values stay in their stores (--keep-value) or go
(--delete-value), asked at a terminal; a value given in full, outside the
items' blocks, is left where it is. One declared for every Mac needs
--shared. The change is committed and pushed to the config repository.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, secretKind+" remove", func(ctx context.Context, r *run) error {
				return a.removeSecrets(ctx, r, args, shared, value)
			})
		},
	}
	remove.Flags().BoolVar(&shared, "shared", false, "take it out of the shared file, every Mac's list")
	remove.Flags().BoolVar(&value.delete, "delete-value", false, "delete its value from its store too")
	remove.Flags().BoolVar(&value.keep, "keep-value", false, "keep its value in its store")
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Read every secret from its store, and put each in place",
		Long: `Read every secret kit-config's secrets sections declare from its store, each
store's in one sitting, and put each in place: ~/.secrets.zsh, and the
files. Run it after changing a value in a store. A value that fails to read
keeps its previous one; a store that isn't signed in fails its own secrets
alone. Values are never printed or logged.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, secretKind+" sync", a.secretsSync)
		},
	}
	places := &cobra.Command{
		Use:   "places",
		Short: "List the blocks secrets are declared in: each one's name, store, item, file, count and sections",
		Long: `List the blocks secrets are declared in, in the shared file and this Mac's:
each block's name, if its header gives it one (--as), its store, its item,
its file, how many secrets it holds, and the sections of its item its lines
name, as kit-config has them, the store not asked. A new secret goes in one
with kit secret add --in <name> or --item <item>, or, told neither, in its
file's one block for an item. --json gives the same as one document.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, secretKind+" places", func(_ context.Context, r *run) error {
				return a.secretPlaces(r)
			})
		},
	}
	signin := &cobra.Command{
		Use:   "signin",
		Short: "Sign in to your secrets' stores with your password, for kit to read them while you're away from the Mac",
		Long: `Sign in to the stores your secrets are in with your password, for kit to read
them while you're away from the Mac, as over a remote connection, where a
store's approval on the Mac's screen can't be given. Each store's own
program asks for your password, at this terminal: kit never sees it.

The sign-in holds only for what runs in this terminal, so kit stays open
here, keeping it in memory, and runs the stores' commands for every other
kit command, in any shell, an agent's included. While it's open, anything
running as you can use the sign-in. Leave it open while that work goes
on. ctrl-C, closing the terminal, or kit secret signout ends it, signing
out; so does the store, once the sign-in's left unused a while. Run it in
a terminal of your own.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, secretKind+" signin", a.secretsSignIn)
		},
	}
	signout := &cobra.Command{
		Use:   "signout",
		Short: "End kit secret signin, open in another terminal, signing out of your secrets' stores",
		Long: `End kit secret signin, open in another terminal: it signs out of each store,
and kit's commands no longer use its sign-in. With none open, it says so.
Run it once the work that needed your secrets is done.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, secretKind+" signout", a.secretsSignOut)
		},
	}
	cmd.AddCommand(add, remove, sync, places, signin, signout)
	return cmd
}

// secretsStep is the one step of kit secret's commands that run as one.
var secretsStep = event.Step{Name: secret.Descriptor.Name, Title: secret.Descriptor.Label, Area: steps.AreaDrift, Row: secret.Descriptor.Row}

// storesKnown says the secrets stores kit knows, and how their references
// read, a paragraph each, within help's width.
func storesKnown() string {
	lines := []string{"The stores kit knows, and how their references read:"}
	for _, d := range secret.Stores {
		about := d.About()
		said := fmt.Sprintf("%s: an item, %s; a value, %s; where a value goes in an item, %s.", about.Name, about.Item, about.Ref, about.Field)
		for _, l := range look.Wrap(said, 74) {
			lines = append(lines, "  "+l)
		}
	}
	return strings.Join(lines, "\n")
}

// secretsSync syncs every secret, as a run of one step.
func (a *app) secretsSync(ctx context.Context, r *run) error {
	k, err := r.secrets()
	if err != nil || !slices.Contains(r.kinds, secretKind) {
		return errors.New("no secrets are declared: kit-config's [secrets] declares them")
	}
	return a.oneStep(ctx, r, secretsStep, "syncing", func(ctx context.Context) check.Result {
		report, err := k.Sync(ctx)
		switch {
		case err != nil:
			return check.Result{State: check.Failed, Reason: err.Error()}
		case len(report.Failed) > 0:
			return check.Result{State: check.Failed, Reason: report.Error()}
		case len(report.Held) > 0:
			return check.Result{State: check.Deferred, Reason: report.Error()}
		}
		return check.Result{State: check.OK, Summary: report.Summary()}
	})
}

// placesSchema is kit secret places --json's document's version.
const placesSchema = 1

// secretPlaces lists the blocks secrets are declared in, for this Mac: as
// one document with --json, else a row each.
func (a *app) secretPlaces(r *run) error {
	places, err := secretsPlaces(r.cfg, r.machine)
	if err != nil {
		return err
	}
	if a.json {
		return render.WriteJSON(a.Stdout, struct {
			Schema  int            `json:"schema"`
			Machine string         `json:"machine"`
			Places  []render.Place `json:"places"`
		}{placesSchema, r.machine, places})
	}
	out := a.colors(a.Stdout)
	if len(places) == 0 {
		_, err := fmt.Fprintln(out, "No secrets blocks for this Mac: kit secret add --item <item> makes one.")
		return err
	}
	if a.pretty(a.Stdout) {
		return writeLines(out, render.Places(a.Width(a.Stdout), places))
	}
	lines := make([]string, len(places))
	for i, p := range places {
		lines[i] = p.Plain()
	}
	return writeLines(out, lines)
}

// secretsPlaces are the blocks secrets are declared in for the Mac called
// machine, in cfg: the shared file's, then its own.
func secretsPlaces(cfg *config.Config, machine string) ([]render.Place, error) {
	found, err := cfg.Places(secretKind, machine)
	if err != nil {
		return nil, err
	}
	places := make([]render.Place, len(found))
	for i, p := range found {
		places[i] = render.Place{Name: p.As, Item: p.Item, Scope: p.Scope, File: config.DeclFile(p.Scope), Line: p.Line, Secrets: p.Count}
		if d, err := secret.ItemStore(p.Item); p.Item != "" && err == nil {
			places[i].Store = d.About().Name
			places[i].Sections = sectionsOf(d, p.Shorts)
		}
	}
	return places, nil
}

// sectionsOf are the sections of its item that shorts, references short
// for it, name, as d reads them: sorted, each once. d is asked nothing.
func sectionsOf(d store.Driver, shorts []string) []string {
	var sections []string
	for _, short := range shorts {
		if section, ok := d.Section(short); ok {
			sections = append(sections, section)
		}
	}
	slices.Sort(sections)
	return slices.Compact(sections)
}

// writeLines writes lines to out, each ended by a newline.
func writeLines(out io.Writer, lines []string) error {
	for _, l := range lines {
		if _, err := fmt.Fprintln(out, l); err != nil {
			return err
		}
	}
	return nil
}
