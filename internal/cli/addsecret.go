package cli

import (
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
	"github.com/leeovery/kit/internal/look"
)

// secretKind names the secrets' kind, and its command.
const secretKind = "secret"

// secretOptions are kit secret add's flags: where its value comes
// from (an existing reference, a file, standard input, or typed), where in
// 1Password it goes (the item and the field), and a file's mode.
type secretOptions struct {
	ref, from, field, mode, item string
	stdin                        bool
}

// valueChoice is kit secret remove's answer, given ahead, to whether a secret's
// value goes from 1Password too.
type valueChoice struct {
	delete, keep bool
}

// addSecret keeps a secret's value in 1Password (unless it's there already,
// --ref), reads it back to compare, declares the secret, and syncs, values
// never shown: one secret at a time. A new value goes in the item of the
// file's secrets section (--item when it has none, or several), and its
// line in that section; a reference in an item a section names goes in
// that section, short, and any other in the plain [secrets], in full.
func (a *app) addSecret(ctx context.Context, r *run, names []string, opts addOptions) error {
	so := opts.secret
	k, ok := r.kindsByName[secretKind].(*secret.Secrets)
	if !ok {
		return errors.New("kit has no secrets")
	}
	sources := 0
	for _, set := range []bool{so.ref != "", so.from != "", so.stdin} {
		if set {
			sources++
		}
	}
	switch {
	case sources > 1:
		return errors.New("one of --ref, --from or --stdin says where its value comes from")
	case so.ref != "" && so.item != "":
		return errors.New("--item is where a new value goes; --ref says where one is already")
	case so.ref != "" && (!strings.HasPrefix(so.ref, "op://") || strings.Count(so.ref, "/") < 4):
		return fmt.Errorf("--ref %s isn't where 1Password keeps a value, as in op://vault/item/field", so.ref)
	}
	name := r.tildePaths(names)[0]
	var value string
	switch {
	case so.ref != "":
	case so.from != "":
		data, err := os.ReadFile(so.from)
		if err != nil {
			return err
		}
		value = string(data)
	case so.stdin:
		data, err := io.ReadAll(io.LimitReader(a.Stdin, 1<<20))
		if err != nil {
			return err
		}
		// As a value typed has no newline, nor does one passed in.
		value = strings.TrimRight(string(data), "\r\n")
	case a.pretty(a.Stdout):
		// The run's heading first, then the field under it.
		r.sink.Emit(event.Preparing{Time: r.now, Command: r.command, Machine: r.machine})
		typed, err := a.ReadSecret(ctx, look.Row{State: look.NeedsYou, Name: name, Says: look.Orange("needs its value")})
		if errors.Is(err, ask.ErrCancelled) {
			return fmt.Errorf("%w: nothing was stored or declared", ask.ErrCancelled)
		}
		if err != nil {
			return err
		}
		value = typed
	default:
		return errors.New("say where its value comes from: --stdin (how an agent passes one), --from <file>, or --ref <where 1Password keeps it>")
	}
	if so.ref == "" && strings.TrimSpace(value) == "" {
		return errors.New("its value is empty")
	}
	if so.ref == "" && so.from == "" && so.field == "" {
		return errors.New("say where in 1Password it goes: --field <section>/<field>, as in GitHub/token")
	}
	scope := r.scope(opts.shared)
	items, err := r.cfg.Items(secretKind, scope)
	if err != nil {
		return err
	}
	item, err := secretItem(so, items, config.DeclFile(scope))
	if err != nil {
		return err
	}
	c := startChanges(r, []string{name})
	c.step(ctx, name, func(ctx context.Context) check.Result {
		if err := k.Ready(ctx); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		short := ""
		var err error
		switch {
		case so.ref != "" && item != "":
			short = strings.TrimPrefix(so.ref, item+"/")
		case so.ref != "":
			short = so.ref
		case so.from != "":
			short, err = k.Attach(ctx, item, cmpOr(so.field, filepath.Base(so.from)), so.from)
		default:
			short, err = k.Store(ctx, item, so.field, value)
		}
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		if so.ref == "" {
			if err := k.ReadBack(ctx, item+"/"+short, value); err != nil {
				return check.Result{State: check.Failed, Reason: "stored, but reading it back failed: " + err.Error()}
			}
		}
		line := config.Quote(short)
		if so.mode != "" {
			line += " --mode " + so.mode
		}
		if err := r.cfg.Declare(secretKind, scope, config.Entry{Name: name, Value: line, Note: opts.note, Item: item}); err != nil {
			return check.Result{State: check.Failed, Reason: "kept in 1Password, but couldn't declare: " + err.Error()}
		}
		c.changed(config.DeclFile(scope))
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
		case report.Failed[name] != "":
			return check.Result{State: check.Failed, Reason: "declared, but couldn't sync it: " + report.Failed[name]}
		}
		summary := "declared in " + scope + ", synced"
		if so.ref == "" {
			summary = "kept in 1Password (" + item + "/" + short + ") and read back; " + summary
		}
		return check.Result{State: check.OK, Summary: summary}
	})
	c.sync(ctx, commitMessage("add", secretKind, []string{name}, r.machine, opts.note))
	return c.finish()
}

// secretItem is the item whose section a secret's line goes in: for a
// reference, the item of the section it's in, if one is, else none (the
// plain [secrets]); for a new value, --item, or the file's one item.
func secretItem(so secretOptions, items []string, file string) (string, error) {
	if so.ref != "" {
		for _, item := range items {
			if strings.HasPrefix(so.ref, item+"/") {
				return item, nil
			}
		}
		return "", nil
	}
	item := strings.TrimSuffix(so.item, "/")
	switch {
	case item != "" && !config.ItemRef(item):
		return "", fmt.Errorf("--item %s isn't a 1Password item's reference, as in op://vault/item", so.item)
	case item != "":
		return item, nil
	case len(items) == 1:
		return items[0], nil
	case len(items) == 0:
		return "", fmt.Errorf("%s has no secrets section naming an item to keep it in: say which with --item op://vault/item (its section is made), or give where it is with --ref", file)
	}
	return "", fmt.Errorf("%s has secrets sections for %s: say which item it goes in with --item", file, strings.Join(items, ", "))
}

// cmpOr is a, or b when a is empty.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// removeSecrets takes each secret off the Mac and undeclares it, then
// deletes its value from 1Password too, or keeps it, as value says or a
// terminal answers: a value in the item its section names; one given in
// full, in the plain [secrets], is left where it is.
func (a *app) removeSecrets(ctx context.Context, r *run, names []string, shared bool, value valueChoice) error {
	k, ok := r.kindsByName[secretKind].(*secret.Secrets)
	if !ok {
		return errors.New("kit has no secrets")
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
	del := value.delete
	switch {
	case value.delete && value.keep:
		return errors.New("--delete-value or --keep-value, not both")
	case !value.delete && !value.keep && a.pretty(a.Stdout):
		i, err := a.Choose(ctx, ask.Question{
			About:   look.Row{State: look.NeedsYou, Name: strings.Join(names, ", "), Says: look.Orange("delete their values from 1Password too?")},
			Answers: []look.Choice{{Label: "Keep", Does: "keep them in 1Password"}, {Label: "Delete", Does: "delete them from 1Password"}},
		})
		if errors.Is(err, ask.ErrCancelled) {
			return fmt.Errorf("%w: nothing was changed", ask.ErrCancelled)
		}
		if err != nil {
			return err
		}
		del = i == 1
	case !value.delete && !value.keep:
		return errors.New("say what becomes of their values in 1Password: --keep-value or --delete-value")
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
				res.Summary += fmt.Sprintf("; its value is outside every item a secrets section names (%s), so kit leaves it", e.Value)
				return res
			}
			if err := k.Delete(ctx, e.Item, e.Value); err != nil {
				return check.Result{State: check.Failed, Reason: res.Summary + ", but " + err.Error()}
			}
			res.Summary += "; its value deleted from 1Password"
			return res
		})
	}
	c.sync(ctx, commitMessage("remove", secretKind, names, r.machine, ""))
	return c.finish()
}
