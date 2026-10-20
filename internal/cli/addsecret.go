package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/look"
)

// secretKind names the secrets' kind, and its command.
const secretKind = "secret"

// secretOptions are kit secret add's flags: where its value comes from (an
// existing reference, a file, standard input, or typed), where in a store
// it goes (a block, by its name, or an item; and the field), and a file's
// mode.
type secretOptions struct {
	ref, from, field, mode, item, in string
	stdin                            bool
}

// valueChoice is kit secret remove's answer, given ahead, to whether a
// secret's value goes from its store too.
type valueChoice struct {
	delete, keep bool
}

// secrets is the run's secrets kind.
func (r *run) secrets() (*secret.Secrets, error) {
	k, ok := r.kindsByName[secretKind].(*secret.Secrets)
	if !ok {
		return nil, errors.New("kit has no secrets")
	}
	return k, nil
}

// addSecret keeps a secret's value in its store (unless it's there
// already, --ref), reads it back to compare, declares the secret, and
// syncs, values never shown: one secret at a time. A new value goes where
// it's told, the block named (--in) or the item (--item), and its line in
// the file's block for that item, made when there's none; a reference in an
// item a block of the file is for goes in that block, short, and any other
// in the plain [secrets], in full. Declared for every Mac, it comes out of
// the Macs' files that had it, in the same edit. Everything's checked before
// the value's asked for, or read (secretTarget).
func (a *app) addSecret(ctx context.Context, r *run, names []string, opts addOptions) error {
	so := opts.secret
	k, err := r.secrets()
	if err != nil {
		return err
	}
	at, err := a.secretTarget(ctx, r, k, r.tildePaths(names)[0], opts)
	if err != nil {
		return err
	}
	value, err := a.secretValue(ctx, r, at.entry.Name, so)
	if err != nil {
		return err
	}
	d, item := at.store, at.entry.Item
	c := startChanges(r, []string{at.entry.Name})
	c.step(ctx, at.entry.Name, func(ctx context.Context) check.Result {
		kept := at.short
		var err error
		switch {
		case so.ref != "":
		case so.from != "":
			kept, err = k.Attach(ctx, d, item, at.short, so.from)
		default:
			kept, err = k.Store(ctx, d, item, at.short, value)
		}
		switch {
		case err != nil:
			return check.Result{State: check.Failed, Reason: err.Error()}
		case kept != at.short:
			return check.Result{State: check.Failed, Reason: fmt.Sprintf("kept in %s, but as %s, not %s, where kit checked it could go: nothing declared", d.About().Name, d.Join(item, kept), d.Join(item, at.short))}
		}
		if so.ref == "" {
			if err := k.ReadBack(ctx, d, d.Join(item, at.short), value); err != nil {
				return check.Result{State: check.Failed, Reason: "stored, but reading it back failed: " + err.Error()}
			}
		}
		e := at.entry
		moved, err := c.declare(secretKind, at.scope, e)
		if err != nil {
			return check.Result{State: check.Failed, Reason: "kept in " + d.About().Name + ", but couldn't declare: " + err.Error()}
		}
		list, err := r.cfg.List(secretKind, r.machine)
		if err == nil {
			_, err = k.Values(list)
		}
		if err != nil {
			return check.Result{State: check.Failed, Reason: "declared, but its line doesn't read: " + err.Error()}
		}
		report, err := k.Sync(ctx)
		switch {
		case err != nil:
			return check.Result{State: check.Failed, Reason: "declared, but couldn't sync: " + err.Error()}
		case report.Failed[e.Name] != "":
			return check.Result{State: check.Failed, Reason: "declared, but couldn't sync it: " + report.Failed[e.Name]}
		case report.Held[e.Name] != "":
			return check.Result{State: check.Failed, Reason: "declared, but couldn't sync it yet: " + report.Held[e.Name]}
		}
		summary := "declared in " + at.scope + outOf(moved) + ", synced"
		if so.ref == "" {
			summary = "kept in " + d.About().Name + " (" + d.Join(item, at.short) + ") and read back; " + summary
		}
		return check.Result{State: check.OK, Summary: summary}
	})
	c.sync(ctx, commitMessage("add", secretKind, []string{at.entry.Name}, r.machine, opts.note))
	return c.finish()
}

// secretTarget is where kit secret add puts a secret: the file, by its
// scope; its store; where in the store, short (as the store will return it,
// for a new value); and its line, as it'll be declared.
type secretTarget struct {
	scope string
	store store.Driver
	short string
	entry config.Entry
}

// secretTarget checks everything kit secret add can before it asks for a
// value, or reads one, or stores one: where the value comes from and where
// it goes (--in, --item or --ref), where in its item (--field, by the
// store's form), the line as it'll be declared, as the config would read
// with it, the store answering, and --ref reading, or nothing kept where a
// new value goes.
func (a *app) secretTarget(ctx context.Context, r *run, k *secret.Secrets, name string, opts addOptions) (secretTarget, error) {
	so := opts.secret
	if err := checkSources(so, a.pretty(a.Stdout)); err != nil {
		return secretTarget{}, err
	}
	if so.ref != "" {
		if _, err := secret.ValueStore(so.ref); err != nil {
			return secretTarget{}, fmt.Errorf("--ref: %w", err)
		}
	}
	at := secretTarget{scope: r.scope(opts.shared)}
	places, err := r.cfg.Places(secretKind, r.machine)
	if err != nil {
		return at, err
	}
	item, as, err := secretPlace(so, places, at.scope)
	if err != nil {
		return at, err
	}
	if so.ref != "" && item == "" && opts.shared {
		if item, err = movedItem(r, name, so.ref); err != nil {
			return at, err
		}
	}
	if at.store, err = storeOf(item, so.ref); err != nil {
		return at, err
	}
	if at.short, err = secretShort(at.store, item, so); err != nil {
		return at, err
	}
	at.entry = config.Entry{Name: name, Value: secretLine(at.short, so.mode), Note: opts.note, Item: item, As: as}
	if err := r.checkDeclare(secretKind, at.scope, at.entry); err != nil {
		return at, sharingHint(err)
	}
	if err := k.Ready(ctx, at.store); err != nil {
		return at, err
	}
	if so.ref != "" {
		return at, k.Reads(ctx, at.store, so.ref)
	}
	return at, k.Free(ctx, at.store, item, at.short)
}

// sharingHint is err, a declare's refusal, saying, when it's of a Mac's
// line saying otherwise where the secret is, or its mode, how to share the
// value that line has: --ref and where it is, and the mode it gives.
func sharingHint(err error) error {
	differs, ok := errors.AsType[config.LineDiffers](err)
	if !ok {
		return err
	}
	ref, mode, rerr := secret.Where(differs.Line)
	if rerr != nil {
		return err
	}
	hint := "--ref " + config.Quote(ref)
	if mode != 0o600 {
		hint += " " + secret.ModeOption(mode)
	}
	return fmt.Errorf("%w; %s shares the value it has", err, hint)
}

// secretShort is where in item, or in full, so says a secret's value is, or
// goes, as its store will return it: --ref's, short for item when it's in
// it; an attachment's name, --field or the file's own; or --field, as the
// store says a field.
func secretShort(d store.Driver, item string, so secretOptions) (string, error) {
	switch {
	case so.ref != "" && item != "":
		short, _ := d.Short(item, so.ref)
		return short, nil
	case so.ref != "":
		return so.ref, nil
	case so.from != "":
		return d.Attachment(cmp.Or(so.field, filepath.Base(so.from)))
	case so.field == "":
		return "", fmt.Errorf("say where in %s's item it goes: --field %s", d.About().Name, d.About().Field)
	}
	return d.Field(so.field)
}

// secretLine is a secret's line, after its name: where its value is, and a
// file's mode, when given.
func secretLine(short, mode string) string {
	line := config.Quote(short)
	if mode != "" {
		line += " --mode " + mode
	}
	return line
}

// storeOf is the store of item, when there's one, else of ref, in full.
func storeOf(item, ref string) (store.Driver, error) {
	if item != "" {
		return secret.ItemStore(item)
	}
	return secret.ValueStore(ref)
}

// checkSources checks kit secret add's flags say where its value comes
// from once, and where it goes once, for a new value alone: typed, at a
// terminal, when they don't say.
func checkSources(so secretOptions, terminal bool) error {
	sources := 0
	for _, set := range []bool{so.ref != "", so.from != "", so.stdin} {
		if set {
			sources++
		}
	}
	switch {
	case sources > 1:
		return errors.New("one of --ref, --from or --stdin says where its value comes from")
	case so.ref != "" && (so.item != "" || so.in != ""):
		return errors.New("--in and --item say where a new value goes; --ref says where one is already")
	case so.item != "" && so.in != "":
		return errors.New("--in or --item, not both: --in names a block, --item an item")
	case sources == 0 && !terminal:
		return errors.New("say where its value comes from: --stdin (how an agent passes one), --from <file>, or --ref <where its store keeps it>")
	}
	return nil
}

// secretValue is a new secret's value, as so says where it comes from:
// none for a reference; a file's; standard input's; else typed, unshown, at
// the terminal (checkSources).
func (a *app) secretValue(ctx context.Context, r *run, name string, so secretOptions) (string, error) {
	var value string
	switch {
	case so.ref != "":
		return "", nil
	case so.from != "":
		data, err := os.ReadFile(so.from)
		if err != nil {
			return "", err
		}
		value = string(data)
	case so.stdin:
		data, err := io.ReadAll(io.LimitReader(a.Stdin, 1<<20))
		if err != nil {
			return "", err
		}
		// As a value typed has no newline, nor does one passed in.
		value = strings.TrimRight(string(data), "\r\n")
	default:
		// The run's heading first, then the field under it.
		r.sink.Emit(event.Preparing{Time: r.now, Command: r.command, Machine: r.machine})
		typed, err := a.ReadSecret(ctx, look.Row{State: look.NeedsYou, Name: name, Says: look.Orange("needs its value")})
		if errors.Is(err, ask.ErrCancelled) {
			return "", fmt.Errorf("%w: nothing was stored or declared", ask.ErrCancelled)
		}
		if err != nil {
			return "", err
		}
		value = typed
	}
	if strings.TrimSpace(value) == "" {
		return "", errors.New("its value is empty")
	}
	return value, nil
}

// secretPlace is the item whose block a secret's line goes in, in scope's
// file, and the name the block goes by should kit make it, of places, the
// secrets' blocks this Mac reads: for a reference, the item of the file's
// block it's in, if one is, else none (the plain [secrets]); for a new
// value, --item's, or the item --in's name stands for. A new value needs
// one of them: else it's refused, listing the blocks.
func secretPlace(so secretOptions, places []config.Place, scope string) (item, as string, err error) {
	switch {
	case so.ref != "":
		for _, p := range places {
			if p.Scope != scope || p.Item == "" {
				continue
			}
			if d, err := secret.ItemStore(p.Item); err == nil {
				if _, in := d.Short(p.Item, so.ref); in && d.IsRef(so.ref) {
					return p.Item, "", nil
				}
			}
		}
		return "", "", nil
	case so.item != "":
		item := strings.TrimSuffix(so.item, "/")
		if _, err := secret.ItemStore(item); err != nil {
			return "", "", fmt.Errorf("--item: %w", err)
		}
		return item, "", nil
	case so.in != "":
		i := slices.IndexFunc(places, func(p config.Place) bool { return p.As == so.in })
		if i < 0 {
			return "", "", fmt.Errorf("no secrets block goes by %s: %s", so.in, blocksSaid(places))
		}
		return places[i].Item, so.in, nil
	}
	return "", "", fmt.Errorf("say where it goes: --in <a block's name>, or --item <an item> (%s), its block made when the file has none; %s", secret.Items(), blocksSaid(places))
}

// movedItem is the item of the block a Mac's line declaring name is in,
// when ref is in it: where, the shared file having no block for it, a
// shared declare by ref puts the line, so the block it's moved from keeps
// going by its name. None when no Mac's is.
func movedItem(r *run, name, ref string) (string, error) {
	where, err := r.cfg.Where(secretKind, name)
	if err != nil {
		return "", err
	}
	for _, e := range where {
		if e.Scope == config.Shared || e.Item == "" {
			continue
		}
		if d, err := secret.ItemStore(e.Item); err == nil {
			if _, in := d.Short(e.Item, ref); in && d.IsRef(ref) {
				return e.Item, nil
			}
		}
	}
	return "", nil
}

// blocksSaid says the blocks for items of places, the blocks a new value
// could go in: each by its name and its item, or its item, and its file.
func blocksSaid(places []config.Place) string {
	var said []string
	for _, p := range places {
		switch {
		case p.Item == "":
		case p.As != "":
			said = append(said, fmt.Sprintf("%s (%s, %s)", p.As, p.Item, config.DeclFile(p.Scope)))
		default:
			said = append(said, fmt.Sprintf("%s (%s)", p.Item, config.DeclFile(p.Scope)))
		}
	}
	if len(said) == 0 {
		return "there are no blocks for items yet"
	}
	return "the blocks are " + strings.Join(said, ", ")
}

// removeSecrets takes each secret off the Mac and undeclares it, then
// deletes its value from its store too, or keeps it, as value says or a
// terminal answers: a value in the item its block is for; one given in
// full, in the plain [secrets], is left where it is.
func (a *app) removeSecrets(ctx context.Context, r *run, names []string, shared bool, value valueChoice) error {
	k, err := r.secrets()
	if err != nil {
		return err
	}
	names = r.tildePaths(names)
	declared, err := r.cfg.List(secretKind, r.machine)
	if err != nil {
		return err
	}
	refs := map[string]config.Entry{}
	for _, e := range declared.Entries {
		if slices.Contains(names, e.Name) {
			if words, err := config.Words(e.Value); err == nil && len(words) > 0 {
				e.Value = words[0]
				refs[e.Name] = e
			}
		}
	}
	where := storesSaid(refs)
	del := value.delete
	switch {
	case value.delete && value.keep:
		return errors.New("--delete-value or --keep-value, not both")
	case !value.delete && !value.keep && a.pretty(a.Stdout):
		i, err := a.Choose(ctx, ask.Question{
			About:   look.Row{State: look.NeedsYou, Name: strings.Join(names, ", "), Says: look.Orange("delete their values from " + where + " too?")},
			Answers: []look.Choice{{Label: "Keep", Does: "keep them in " + where}, {Label: "Delete", Does: "delete them from " + where}},
		})
		if errors.Is(err, ask.ErrCancelled) {
			return fmt.Errorf("%w: nothing was changed", ask.ErrCancelled)
		}
		if err != nil {
			return err
		}
		del = i == 1
	case !value.delete && !value.keep:
		return errors.New("say what becomes of their values in " + where + ": --keep-value or --delete-value")
	}
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			res := removeOne(ctx, r, c, k, name, shared)
			if res.State != check.OK || !del {
				return res
			}
			e, ok := refs[name]
			switch {
			case !ok:
				return res
			case e.Item == "":
				res.Summary += fmt.Sprintf("; its value is outside every item a secrets block is for (%s), so kit leaves it", e.Value)
				return res
			}
			d, err := secret.ItemStore(e.Item)
			if err == nil {
				err = k.Delete(ctx, d, e.Item, e.Value)
			}
			if err != nil {
				return check.Result{State: check.Failed, Reason: res.Summary + ", but " + err.Error()}
			}
			res.Summary += "; its value deleted from " + d.About().Name
			return res
		})
	}
	c.sync(ctx, commitMessage("remove", secretKind, names, r.machine, ""))
	return c.finish()
}

// storesSaid names the stores keeping the values of the secrets declared
// by refs, as in 1Password: "their stores" when it can't say.
func storesSaid(refs map[string]config.Entry) string {
	var stores []string
	for _, e := range refs {
		d, err := storeOf(e.Item, e.Value)
		if err == nil && !slices.Contains(stores, d.About().Name) {
			stores = append(stores, d.About().Name)
		}
	}
	if len(stores) == 0 {
		return "their stores"
	}
	slices.Sort(stores)
	return strings.Join(stores, " and ")
}
