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
	"github.com/leeovery/kit/internal/kind/secret"
)

// secretKind names the secrets' kind, as kit add and kit remove take it.
const secretKind = "secret"

// secretOptions are kit add's flags for a secret: where its value comes
// from (an existing reference, a file, standard input, or typed), where in
// 1Password it goes, and a file's mode or the repositories an Actions
// secret is on.
type secretOptions struct {
	ref, from, field, mode, github string
	stdin                          bool
}

// valueChoice is kit remove's answer, given ahead, to whether a secret's
// value goes from 1Password too.
type valueChoice struct {
	delete, keep bool
}

// addSecret keeps a secret's value in 1Password (unless it's there already,
// --ref), reads it back to compare, declares the secret, and syncs, values
// never shown: one secret at a time.
func (a *app) addSecret(ctx context.Context, r *run, names []string, opts addOptions) error {
	so := opts.secret
	k, ok := r.kindsByName[secretKind].(*secret.Secrets)
	switch {
	case !ok:
		return errors.New("kit has no secrets")
	case len(names) != 1:
		return errors.New("one secret at a time: kit add secret <name>")
	case opts.temp || opts.group != "":
		return errors.New("--temp and --group are for packages")
	}
	sources := 0
	for _, set := range []bool{so.ref != "", so.from != "", so.stdin} {
		if set {
			sources++
		}
	}
	if sources > 1 {
		return errors.New("one of --ref, --from or --stdin says where its value comes from")
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
		typed, err := a.ReadSecret(name + ", typed (it isn't shown): ")
		if errors.Is(err, ask.ErrCancelled) {
			return errors.New("cancelled: nothing was stored or declared")
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
	c := startChanges(r, []string{name})
	c.step(ctx, name, func(ctx context.Context) check.Result {
		if !k.Answers(ctx) {
			return check.Result{State: check.Failed, Reason: secret.SignIn}
		}
		short := ""
		var err error
		switch {
		case so.ref != "":
			short = k.Short(so.ref)
		case so.from != "":
			short, err = k.Attach(ctx, cmpOr(so.field, filepath.Base(so.from)), so.from)
		default:
			short, err = k.Store(ctx, so.field, value)
		}
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		if so.ref == "" {
			if err := k.ReadBack(ctx, short, value); err != nil {
				return check.Result{State: check.Failed, Reason: "stored, but reading it back failed: " + err.Error()}
			}
		}
		line := config.Quote(short)
		if so.mode != "" {
			line += " --mode " + so.mode
		}
		if so.github != "" {
			line += " --github " + so.github
		}
		if err := r.cfg.Declare(secretKind, scope, config.Entry{Name: name, Value: line, Note: opts.note}, ""); err != nil {
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
			summary = "kept in 1Password (" + short + ") and read back; " + summary
		}
		return check.Result{State: check.OK, Summary: summary}
	})
	c.sync(ctx, commitMessage("add", secretKind, []string{name}, r.machine, opts.note))
	return c.finish()
}

// cmpOr is a, or b when a is empty.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// removeSecrets takes each secret off the Mac or GitHub and undeclares it,
// then deletes its value from 1Password too, or keeps it, as value says or
// a terminal answers.
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
	refs := map[string]string{}
	for _, e := range declared.Entries {
		if slices.Contains(names, e.Name) {
			if words, err := config.Words(e.Value); err == nil && len(words) > 0 {
				refs[e.Name] = words[0]
			}
		}
	}
	del := value.delete
	switch {
	case value.delete && value.keep:
		return errors.New("--delete-value or --keep-value, not both")
	case !value.delete && !value.keep && a.pretty(a.Stdout):
		i, err := a.Choose(ctx, "Delete their values from 1Password too?", []string{"keep them in 1Password", "delete them from 1Password"})
		if errors.Is(err, ask.ErrCancelled) {
			return errors.New("cancelled: nothing was changed")
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
			ref, ok := refs[name]
			switch {
			case !ok:
				return res
			case strings.HasPrefix(ref, "op://"):
				res.Summary += fmt.Sprintf("; its value is outside kit.toml's item (%s), so kit leaves it", ref)
				return res
			}
			if err := k.Delete(ctx, ref); err != nil {
				return check.Result{State: check.Failed, Reason: res.Summary + ", but " + err.Error()}
			}
			res.Summary += "; its value deleted from 1Password"
			return res
		})
	}
	c.sync(ctx, commitMessage("remove", secretKind, names, r.machine, ""))
	return c.finish()
}
