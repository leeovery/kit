package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/steps"
)

// removeOptions are removing's flags: --shared; and, for apps, --zap and
// --keep, saying what becomes of the files and settings they leave.
type removeOptions struct {
	shared, zap, keep bool
}

func (a *app) remove(ctx context.Context, r *run, kindName string, names []string, opts removeOptions) error {
	if _, line := lineKinds[kindName]; line {
		return a.removeLines(ctx, r, kindName, names, opts.shared)
	}
	k, err := r.kindNamed(kindName)
	if err != nil {
		return err
	}
	var removing []string
	for _, name := range names {
		if isIn, err := installed(ctx, k, name); err == nil && isIn {
			removing = append(removing, name)
		}
	}
	how, err := a.leftovers(ctx, r, k, removing, opts)
	if err != nil {
		return err
	}
	// What uninstalling will need an administrator's password for, it's
	// asked for first, Touch ID where sudo has it, as adding does.
	ctx, _, stop := a.holdAdmin(ctx, r, map[string][]string{kindName: removing})
	defer stop()
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return removeOne(ctx, r, c, k, name, opts.shared, how[name])
		})
	}
	c.sync(ctx, commitMessage("remove", kindName, names, r.machine, ""))
	return c.finish()
}

// uninstaller uninstalls a thing otherwise than its kind's Remove, saying
// how it went, as in uninstalled with its files; what it says beside an
// error is what it did before it failed.
type uninstaller func(ctx context.Context) (string, error)

// leftovers settles, before anything's uninstalled, what becomes of the
// files and settings each of names, apps of k installed, leaves, as opts
// say, else as the person answers at a terminal, asked once for each that
// leaves any: its files, as its kind finds them, and its settings kit
// prefs saved. Removed, it's uninstalled with its files, and its settings'
// domains deleted, so the next capture drops them from the store. Kept,
// it's uninstalled as any thing is; without a terminal to ask at, it says
// so, and that --zap removes them.
func (a *app) leftovers(ctx context.Context, r *run, k kind.Kind, names []string, opts removeOptions) (map[string]uninstaller, error) {
	l, ok := k.(kind.Leaver)
	switch {
	case !ok || len(names) == 0:
		return nil, nil
	case opts.zap && opts.keep:
		return nil, errors.New("--zap or --keep, not both")
	case opts.keep:
		return nil, nil
	}
	features, err := r.features()
	if err != nil {
		return nil, err
	}
	p, err := a.prefs(r)
	if err != nil {
		return nil, err
	}
	how := map[string]uninstaller{}
	for _, name := range names {
		left, err := l.Leaves(ctx, name)
		if err != nil && !opts.zap {
			// Not knowing what it leaves, kit uninstalls it as any thing.
			continue
		}
		lo := leftover{app: cmp.Or(left.App, name), files: len(left.Files) > 0, domains: left.Domains, saving: features.on(steps.FeaturePrefs)}
		if left.Bundle != "" && lo.saving {
			lo.domains = slices.Compact(slices.Sorted(slices.Values(slices.Concat(lo.domains, p.SavedFor(left.Bundle)))))
		}
		zap, said, err := a.zapping(ctx, lo, opts.zap)
		switch {
		case err != nil:
			return nil, err
		case zap:
			how[name] = func(ctx context.Context) (string, error) {
				if err := l.RemoveAll(ctx, []string{name}); err != nil {
					return "", err
				}
				if err := p.Forget(ctx, lo.domains); err != nil {
					return lo.removed(), fmt.Errorf("its settings are still held to be restored: %w", err)
				}
				return lo.removed(), nil
			}
		case said:
			how[name] = func(ctx context.Context) (string, error) {
				if err := k.Remove(ctx, []string{name}); err != nil {
					return "", err
				}
				return "uninstalled, " + lo.kept(), nil
			}
		}
	}
	return how, nil
}

// zapping settles whether what lo is left is removed: as zap says, else, at
// a terminal, as the person answers, when it's left anything; else it's
// kept, said when it's left anything.
func (a *app) zapping(ctx context.Context, lo leftover, zap bool) (removed, said bool, err error) {
	switch {
	case zap:
		return true, false, nil
	case !lo.files && len(lo.domains) == 0:
		return false, false, nil
	case !a.pretty(a.Stdout):
		return false, true, nil
	}
	i, err := a.Choose(ctx, ask.Question{
		About:   look.Row{State: look.NeedsYou, Name: lo.title()},
		Answers: []look.Choice{{Label: "Keep them", Does: "in case you come back"}, {Label: "Remove them", Does: lo.removes()}},
	})
	if errors.Is(err, ask.ErrCancelled) {
		return false, false, fmt.Errorf("%w: nothing was uninstalled or undeclared", ask.ErrCancelled)
	}
	return i == 1, false, err
}

// leftover is what an app, called app, leaves, as removing it tells of it:
// files, its kind's zap list's, on the Mac; its settings' domains; and
// whether kit prefs saves settings on this Mac.
type leftover struct {
	app     string
	files   bool
	domains []string
	saving  bool
}

// settings reports whether it's left settings kit prefs saved.
func (lo leftover) settings() bool { return lo.saving && len(lo.domains) > 0 }

// title is what the question about what it's left is about.
func (lo leftover) title() string {
	if lo.files {
		return lo.app + "'s settings and files"
	}
	return lo.app + "'s settings"
}

// removes is what removing them does.
func (lo leftover) removes() string {
	switch {
	case lo.files && lo.settings():
		return "its files from this Mac, and its saved settings from kit-prefs"
	case lo.files:
		return "its files from this Mac"
	}
	return "its saved settings from kit-prefs"
}

// removed is what an uninstall that removed them did.
func (lo leftover) removed() string {
	switch {
	case lo.files && lo.settings():
		return "uninstalled with its files and settings"
	case lo.files:
		return "uninstalled with its files"
	case lo.settings():
		return "uninstalled with its saved settings"
	}
	return "uninstalled"
}

// kept is what's become of them, kept, and how to remove them.
func (lo leftover) kept() string {
	if lo.files {
		return "its files kept: --zap removes them"
	}
	return "its saved settings kept: --zap removes them"
}

// removeDeclared uninstalls name, a thing of a kind declared in a file of
// its own, when it's installed, and takes it out of that file.
func removeDeclared(ctx context.Context, r *run, c *changes, k kind.Kind, d kind.Declarer, name string) check.Result {
	isIn, err := installed(ctx, k, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	verb, err := takeOff(ctx, k, name, isIn, nil)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	where, err := r.where(k.Name(), name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	if len(where) == 0 {
		return check.Result{State: check.OK, Summary: verb + "; it wasn't declared"}
	}
	file, err := d.Undeclare(name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: verb + ", but couldn't undeclare: " + err.Error()}
	}
	r.changedFile(c, file)
	return check.Result{State: check.OK, Summary: verb + "; out of " + r.files.Tilde(file)}
}

// takeOff takes name off the Mac, when it's on it (isIn), as how does, or,
// nil, as its kind does, and says so in its kind's words: uninstalled, or
// wasn't installed.
func takeOff(ctx context.Context, k kind.Kind, name string, isIn bool, how uninstaller) (string, error) {
	d := descriptorOf(k)
	if !isIn {
		return "wasn't " + d.Installs().Done, nil
	}
	ctx = event.WithChanging(ctx)
	if how == nil {
		how = func(ctx context.Context) (string, error) { return d.Removes().Done, k.Remove(ctx, []string{name}) }
	}
	verb, err := how(ctx)
	switch {
	case err != nil && verb == "":
		return "", errors.New(couldnt(d.Removes()) + engine.Reason(err))
	case err != nil:
		return "", errors.New(verb + ", but " + err.Error())
	}
	return verb, nil
}

// descriptorOf is the descriptor of k's kind.
func descriptorOf(k kind.Kind) kind.Descriptor {
	d, _ := kinds.Named(k.Name())
	return d
}

// couldnt heads why doing w failed, as in "couldn't uninstall: ".
func couldnt(w kind.Words) string {
	return "couldn't " + strings.ToLower(w.Label[:1]) + w.Label[1:] + ": "
}

// removeOne uninstalls name, when it's installed, as how does, else as its
// kind does, and takes it out of this Mac's file, and the shared one with
// shared.
func removeOne(ctx context.Context, r *run, c *changes, k kind.Kind, name string, shared bool, how uninstaller) check.Result {
	kindName := k.Name()
	if d, ok := k.(kind.Declarer); ok {
		return removeDeclared(ctx, r, c, k, d, name)
	}
	own, sharedScope := r.scope(false), r.scope(true)
	where, err := r.where(kindName, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	var inShared, inOwn bool
	for _, e := range where {
		inShared = inShared || e.Scope == sharedScope
		inOwn = inOwn || e.Scope == own
	}
	if inShared && !shared {
		return check.Result{State: check.Failed, Reason: "declared for every Mac, in " + sharedScope + ": --shared takes it out of every Mac's list"}
	}
	isIn, err := installed(ctx, k, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	verb, err := takeOff(ctx, k, name, isIn, how)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	var from []string
	for _, scope := range []string{own, sharedScope} {
		if scope == own && !inOwn || scope == sharedScope && !inShared {
			continue
		}
		if err := r.cfg.Undeclare(kindName, scope, name); err != nil {
			return check.Result{State: check.Failed, Reason: verb + ", but couldn't undeclare: " + err.Error()}
		}
		c.changed(config.DeclFile(scope))
		from = append(from, scope)
	}
	switch {
	case len(from) > 0:
		return check.Result{State: check.OK, Summary: verb + "; out of " + strings.Join(from, " and ")}
	case isIn:
		return check.Result{State: check.OK, Summary: verb + "; it wasn't declared"}
	}
	return check.Result{State: check.OK, Summary: "neither installed nor declared: nothing to do"}
}
