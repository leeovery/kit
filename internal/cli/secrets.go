package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
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
		Short: "Keep a secret in its store, declare it for this Mac (--shared: every Mac), and sync it",
		Long: `Keep a secret in its store, declare it, and sync it, its value never shown.
<name> is what it fills: an environment variable's name (in ~/.secrets.zsh,
which the shell sources), or a file's path.

Its value is typed, unshown, at a terminal; or comes on standard input
(--stdin, how an agent passes one, never on a command line); or from a file
(--from, kept as an attachment); or is in a store already (--ref and where
it's kept, in full: nothing is stored).

A new value goes where you say, nowhere by default: --in and a block's name,
as a block's header gives it ([secrets <item> --as <name>]), or --item and
the item, in full. --field says where in the item, in its store's words.
kit secret places lists the blocks. The line goes in this file's block for
that item, which kit makes when there's none, going by the name --in gave.
Then the value's read back and compared, declared and synced.

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
		Short: "List the blocks secrets are declared in: each one's name, store, item, file and count",
		Long: `List the blocks secrets are declared in, in the shared file and this Mac's:
each block's name, if its header gives it one (--as), its store, its item,
its file, and how many secrets it holds. A new secret goes in one with kit
secret add --in <name> or --item <item>. --json gives the same as one
document.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepared(cmd, secretKind+" places", func(_ context.Context, r *run) error {
				return a.secretPlaces(r)
			})
		},
	}
	cmd.AddCommand(add, remove, sync, places)
	return cmd
}

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
	r.sink.Emit(event.RunStarted{Time: r.now, Command: r.command, Machine: r.machine, Version: r.version, Steps: []event.Step{{Name: secret.Descriptor.Name, Title: secret.Descriptor.Label, Area: "Drift", Row: secret.Descriptor.Row}}})
	started := time.Now()
	r.sink.Emit(event.StepStarted{Time: started, Step: secretKind, Doing: "syncing"})
	report, err := k.Sync(event.WithStep(ctx, secretKind))
	res := check.Result{State: check.OK, Summary: report.Summary()}
	switch {
	case err != nil:
		res = check.Result{State: check.Failed, Reason: err.Error()}
	case len(report.Failed) > 0:
		res = check.Result{State: check.Failed, Reason: report.Error()}
	case len(report.Held) > 0:
		res = check.Result{State: check.Deferred, Reason: report.Error()}
	}
	r.sink.Emit(event.StepFinished{Time: time.Now(), Step: secretKind, Result: res, Duration: time.Since(started)})
	counts := event.Tally(res)
	r.sink.Emit(event.RunFinished{Time: time.Now(), Duration: time.Since(started), Counts: counts})
	if res.State != check.OK {
		return attention{}
	}
	return nil
}

// placesSchema is kit secret places --json's document's version.
const placesSchema = 1

// place is a block secrets are declared in, as kit secret places lists it:
// the name its header gives it, if any; its store and its item, none for
// the plain [secrets], whose references are in full; whose declarations
// declare it, shared or a Mac's, its file and its header's line; and how
// many secrets it holds.
type place struct {
	Name    string `json:"name,omitempty"`
	Store   string `json:"store,omitempty"`
	Item    string `json:"item,omitempty"`
	Scope   string `json:"scope"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Secrets int    `json:"secrets"`
}

// secretPlaces lists the blocks secrets are declared in, for this Mac: as
// one document with --json, else a row each.
func (a *app) secretPlaces(r *run) error {
	found, err := r.cfg.Places(secretKind, r.machine)
	if err != nil {
		return err
	}
	places := make([]place, len(found))
	for i, p := range found {
		places[i] = place{Name: p.As, Item: p.Item, Scope: p.Scope, File: config.DeclFile(p.Scope), Line: p.Line, Secrets: p.Count}
		if d, err := secret.ItemStore(p.Item); p.Item != "" && err == nil {
			places[i].Store = d.About().Name
		}
	}
	if a.json {
		return render.WriteJSON(a.Stdout, struct {
			Schema  int     `json:"schema"`
			Machine string  `json:"machine"`
			Places  []place `json:"places"`
		}{placesSchema, r.machine, places})
	}
	out := a.colors(a.Stdout)
	if len(places) == 0 {
		_, err := fmt.Fprintln(out, "No secrets blocks for this Mac: kit secret add --item <item> makes one.")
		return err
	}
	if a.pretty(a.Stdout) {
		return writeLines(out, look.Rows("  ", a.Width(a.Stdout), placeRows(places)...))
	}
	lines := make([]string, len(places))
	for i, p := range places {
		lines[i] = p.title() + "  " + strings.Join(p.says(), " · ")
	}
	return writeLines(out, lines)
}

// placeRows are places as rows: each by its name, or its item, its store,
// item, file and count after it.
func placeRows(places []place) []look.Row {
	rows := make([]look.Row, len(places))
	for i, p := range places {
		says := p.says()
		for j, s := range says {
			says[j] = look.Muted(s)
		}
		rows[i] = look.Row{State: look.Done, Name: p.title(), Says: look.Says(says...)}
	}
	return rows
}

// title is what a block's called: the name its header gives it, else its
// item, else, the plain block, its header.
func (p place) title() string {
	switch {
	case p.Name != "":
		return p.Name
	case p.Item != "":
		return p.Item
	}
	return "[secrets]"
}

// says is what's said of a block after its title: its store, its item when
// its title is its name, its scope and how many secrets it holds.
func (p place) says() []string {
	var says []string
	switch {
	case p.Store != "":
		says = append(says, p.Store)
	case p.Item == "":
		says = append(says, "references in full")
	}
	if p.Name != "" {
		says = append(says, p.Item)
	}
	return append(says, p.Scope, plural(p.Secrets, "secret"))
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
