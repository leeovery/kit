package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
)

// switchCommands are kit <kind> on and off, for a kind whose things can be
// declared off and kept (kind.Switcher): things turned on or off, kept
// declared.
func switchCommands(a *app, d kind.Descriptor) []*cobra.Command {
	help := d.Help.Switch
	var cmds []*cobra.Command
	for _, on := range []bool{true, false} {
		verb, short := onOff(on), help.Off
		if on {
			short = help.On
		}
		long := short + "."
		if help.Naming != "" {
			long += " " + help.Naming
		}
		cmds = append(cmds, &cobra.Command{
			Use:   verb + " <name>...",
			Short: short,
			Long:  long + "\nThe change is committed and pushed to the config repository.",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.prepared(cmd, d.Name+" "+verb, func(ctx context.Context, r *run) error {
					return a.switchThings(ctx, r, d, args, on)
				})
			},
		})
	}
	return cmds
}

// switchThings declares each of d's things named on, or off, commits and
// pushes the change, and installs or removes it to match.
func (a *app) switchThings(ctx context.Context, r *run, d kind.Descriptor, names []string, on bool) error {
	found, err := r.kindNamed(d.Name)
	if err != nil {
		return err
	}
	k, ok := found.(switchable)
	if !ok {
		return fmt.Errorf("%s's things can't be declared off", d.Name)
	}
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return switchOne(ctx, r, c, d, k, name, on)
		})
	}
	c.sync(ctx, fmt.Sprintf("kit %s %s %s (%s)", d.Name, onOff(on), strings.Join(names, ", "), r.machine))
	return c.finish()
}

// switchable is a kind whose things can be declared off and kept.
type switchable interface {
	kind.Kind
	kind.Switcher
}

// onOff is on, or off.
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// switchOne declares k's thing name on or off, in whichever of this Mac's
// files declares it, and installs or removes it to match.
func switchOne(ctx context.Context, r *run, c *changes, d kind.Descriptor, k switchable, name string, on bool) check.Result {
	where, err := r.cfg.Where(d.Name, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	verb, holder := onOff(on), d.Help.Switch.Holder
	i := slices.IndexFunc(where, func(e config.Entry) bool { return e.Scope == config.Shared || e.Scope == r.machine })
	if i < 0 {
		return check.Result{State: check.Failed, Reason: "not declared for this Mac: kit " + d.Name + " add declares one " + holder + " has"}
	}
	e := where[i]
	var done []string
	value, err := k.SetOff(e.Value, !on)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	if value != e.Value {
		e.Value = value
		if err := r.cfg.Replace(d.Name, e.Scope, e); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		c.changed(config.DeclFile(e.Scope))
		done = append(done, "declared "+verb+" in "+e.Scope)
	}
	isIn, err := installed(ctx, k, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	switch {
	case on && !isIn:
		if err := installNew(ctx, r, k, name); err != nil {
			return check.Result{State: check.Failed, Reason: strings.Join(append(done, "couldn't install: "+engine.Reason(err)), "; ")}
		}
		done = append(done, "installed")
	case !on && isIn:
		if err := k.Remove(event.WithChanging(ctx), []string{name}); err != nil {
			return check.Result{State: check.Failed, Reason: strings.Join(append(done, "couldn't remove it from "+holder+": "+err.Error()), "; ")}
		}
		done = append(done, "removed from "+holder)
	}
	if len(done) == 0 {
		return check.Result{State: check.OK, Summary: "already " + verb}
	}
	return check.Result{State: check.OK, Summary: strings.Join(done, "; ")}
}

// installNew installs name, of k, as it's declared now: a kind whose lines
// carry values reads them afresh first.
func installNew(ctx context.Context, r *run, k kind.Kind, name string) error {
	if v, ok := k.(kind.Valued); ok {
		list, err := r.cfg.List(k.Name(), r.machine)
		if err == nil {
			_, err = v.Values(list)
		}
		if err != nil {
			return err
		}
	}
	return k.Install(event.WithChanging(ctx), []string{name})
}
