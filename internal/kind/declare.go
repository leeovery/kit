package kind

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
)

// Declared is what k declares for the Mac named mac: its sections in cfg,
// their values read by a kind whose lines carry them (Valued), or their
// patterns expanded (Expander). What doesn't read is unread: the kind's
// check fails, saying so, rather than kit.
func Declared(cfg *config.Config, k Kind, mac string) (list config.List, unread, err error) {
	if list, err = cfg.List(k.Name(), mac); err != nil {
		return list, nil, err
	}
	if v, ok := k.(Valued); ok {
		read, unread := v.Values(list)
		return read, unread, nil
	}
	if e, ok := k.(Expander); ok {
		expanded, unread := e.Expand(list)
		return expanded, unread, nil
	}
	return list, nil, nil
}

// Line is the line declaring name, one of k's things, adding or adopting it
// declares: its value, for a kind whose lines have one (Valued), as it's
// installed; note, after what the kind says it is (Describer), or that
// alone as the note kit makes up (DefaultNote).
func Line(ctx context.Context, k Kind, name, note string) (config.Entry, error) {
	e := config.Entry{Name: name, Note: note}
	if d, ok := k.(Describer); ok {
		if what := d.Describe(ctx, name); what != "" && note != "" {
			e.Note = what + ": " + note
		} else {
			e.DefaultNote = what
		}
	}
	if v, ok := k.(Valued); ok {
		var err error
		if e.Value, err = v.Value(ctx, name); err != nil {
			return e, err
		}
	}
	return e, nil
}

// SecretsKind names the secrets' kind, whose sections declare the secrets
// a line can be held for.
const SecretsKind = "secret"

// Hold has e, a line of k's to declare in scope's declarations, shared or
// the Mac called mac's, go in its kind's section held for the secret k says
// installing it needs (Private), which every Mac the line reaches must
// declare, said saying so; or that k couldn't tell, the line then declared
// as it is. check judges the line as it'll be declared: nil for no more
// than that. Refused, what finding out changed on the Mac is put back;
// stopped, it fails, rather than declare what it didn't check.
func Hold(ctx context.Context, k Kind, cfg *config.Config, scope, mac string, e *config.Entry, check func(config.Entry) error) (said string, err error) {
	private, ok := k.(Private)
	if !ok {
		return "", nil
	}
	need, unchecked, err := hold(ctx, private.NeedsSecret, cfg, scope, mac, e, check)
	switch {
	case unchecked != nil:
		said = "; couldn't check whether it needs a secret: " + engine.Reason(unchecked)
	case need.Secret != "":
		said = "; held for " + need.Secret + ": " + need.Why
	}
	return said, err
}

// HoldInstalled is Hold for e, one of k's things installed already, by its
// full name, declared in the Mac called mac's declarations, as a snapshot
// of the Mac declares it (Private.InstalledNeedsSecret): nothing on the
// Mac changes to find out. unchecked is why k couldn't tell, e then left
// as it is.
func HoldInstalled(ctx context.Context, k Kind, cfg *config.Config, mac string, e *config.Entry) (unchecked, err error) {
	private, ok := k.(Private)
	if !ok {
		return nil, nil
	}
	_, unchecked, err = hold(ctx, private.InstalledNeedsSecret, cfg, mac, mac, e, nil)
	return unchecked, err
}

// hold is Hold's judgement, needs finding what installing e needs: the
// need, why it couldn't be found, if it couldn't, and why e is refused.
func hold(ctx context.Context, needs func(context.Context, string) (Need, error), cfg *config.Config, scope, mac string, e *config.Entry, check func(config.Entry) error) (Need, error, error) {
	need, unchecked := needs(ctx, e.Name)
	if ctx.Err() != nil {
		return need, nil, fmt.Errorf("stopped checking whether it needs a secret: %w", ctx.Err())
	}
	if unchecked == nil && need.Secret != "" {
		e.Hold = config.SecretHold(need.Secret)
	}
	refused := secretDeclared(cfg, scope, mac, need)
	if refused == nil && check != nil {
		refused = check(*e)
	}
	if refused != nil && need.Undo != nil {
		if err := need.Undo(ctx); err != nil {
			refused = fmt.Errorf("%w; %s", refused, engine.Reason(err))
		}
	}
	return need, unchecked, refused
}

// secretDeclared says why a line in scope's declarations can't be held for
// the secret need names, if it can't: a Mac the line reaches doesn't
// declare it (the Mac called mac, or, for the shared declarations, any).
func secretDeclared(cfg *config.Config, scope, mac string, need Need) error {
	if need.Secret == "" {
		return nil
	}
	macs := []string{mac}
	if scope == config.Shared {
		macs = cfg.MacNames()
	}
	var without []string
	for _, m := range macs {
		list, err := cfg.List(SecretsKind, m)
		if err != nil {
			return err
		}
		if !slices.Contains(list.Names(), need.Secret) {
			without = append(without, m)
		}
	}
	switch {
	case len(without) == 0:
		return nil
	case scope == config.Shared:
		return fmt.Errorf("%s, so it needs %s, which no [secrets] section declares for %s: declare it for every Mac first (kit secret add %s --shared)", need.Why, need.Secret, strings.Join(without, " and "), need.Secret)
	}
	return fmt.Errorf("%s, so it needs %s, which no [secrets] section declares: declare it first (kit secret add %s)", need.Why, need.Secret, need.Secret)
}
