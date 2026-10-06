package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/steps"
)

// lineKinds are the sections of lines kit add and kit remove write that
// aren't kinds, by the name the commands take: checks, jobs, and steps by
// hand.
var lineKinds = map[string]string{
	"check":   config.ChecksKind,
	"hourly":  config.HourlyKind,
	"nightly": config.NightlyKind,
	"manual":  config.ManualKind,
}

// addLine declares a check, a job or a step by hand: args are its name, for
// a step by hand what to do, then its command, which starts at dash, where
// the -- was (-1 for none). A check runs once, and a step by hand's command
// too, to say how it stands.
func (a *app) addLine(ctx context.Context, r *run, name string, args []string, dash int, opts addOptions) error {
	kind := lineKinds[name]
	usage := map[string]string{
		config.ManualKind: `kit add manual <name> "<what to do>" [-- <command saying whether it's done>]`,
	}[kind]
	if usage == "" {
		usage = "kit add " + name + " <name> -- <command>"
	}
	var value string
	switch {
	case opts.temp || opts.group != "":
		return errors.New("--temp and --group are for packages")
	case kind == config.ManualKind && (len(args) < 2 || dash >= 0 && (dash != 2 || len(args) == 2) || dash < 0 && len(args) != 2):
		return errors.New("say it as " + usage)
	case kind == config.ManualKind:
		value = config.Quote(args[1])
		if dash >= 0 {
			value += " -- " + quoteAll(args[2:])
		}
	case len(args) < 2 || dash != 1:
		return errors.New("say it as " + usage)
	default:
		value = "-- " + quoteAll(args[1:])
	}
	scope := r.scope(opts.shared)
	entry := config.Entry{Name: args[0], Value: value, Note: opts.note}
	c := startChanges(r, args[:1])
	c.step(ctx, args[0], func(ctx context.Context) check.Result {
		if err := r.cfg.Declare(kind, scope, entry, ""); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		c.changed(config.DeclFile(scope))
		summary := "declared in " + scope
		switch kind {
		case config.ChecksKind:
			summary += "; " + a.tryCheck(ctx, r, entry)
		case config.ManualKind:
			if m, err := steps.ParseManual(r.homeDir, entry); err == nil && m.Command != nil {
				res, err := r.run.Run(ctx, *m.Command)
				if steps.Outcome(*m.Command, res, err) == "" {
					summary += "; done already"
				} else {
					summary += "; not done yet"
				}
			}
		}
		return check.Result{State: check.OK, Summary: summary}
	})
	c.sync(ctx, commitMessage("add", name, args[:1], r.machine, opts.note))
	return c.finish()
}

// tryCheck runs a check just declared, saying how it stands.
func (a *app) tryCheck(ctx context.Context, r *run, e config.Entry) string {
	cmd, err := steps.OwnCommand(r.homeDir, e.Value, 0)
	if err != nil {
		return err.Error()
	}
	res, err := r.run.Run(ctx, cmd)
	if what := steps.Outcome(cmd, res, err); what != "" {
		return "it fails now: " + what
	}
	return "it passes now"
}

// quoteAll is words as a line holds them, each quoted as a shell would need.
func quoteAll(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = config.Quote(w)
	}
	return strings.Join(quoted, " ")
}

// removeLines takes checks, jobs or steps by hand out of this Mac's
// declarations, and the shared ones with shared.
func (a *app) removeLines(ctx context.Context, r *run, name string, names []string, shared bool) error {
	kind := lineKinds[name]
	c := startChanges(r, names)
	for _, n := range names {
		c.step(ctx, n, func(context.Context) check.Result {
			where, err := r.cfg.Where(kind, n)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			var from []string
			for _, e := range where {
				switch {
				case e.Scope == config.Shared && !shared:
					return check.Result{State: check.Failed, Reason: fmt.Sprintf("declared for every Mac, in %s: --shared takes it out of every Mac's", e.Pos())}
				case e.Scope != config.Shared && e.Scope != r.machine:
					continue
				}
				if err := r.cfg.Undeclare(kind, e.Scope, n); err != nil {
					return check.Result{State: check.Failed, Reason: err.Error()}
				}
				c.changed(config.DeclFile(e.Scope))
				from = append(from, e.Scope)
			}
			if len(from) == 0 {
				return check.Result{State: check.Failed, Reason: n + " isn't declared for this Mac"}
			}
			return check.Result{State: check.OK, Summary: "out of " + strings.Join(from, " and ")}
		})
	}
	c.sync(ctx, commitMessage("remove", name, names, r.machine, ""))
	return c.finish()
}
