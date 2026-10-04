package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind/mcp"
)

func newMCPCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Turn Claude Code's MCP servers on or off, keeping their definitions declared",
	}
	for _, on := range []bool{true, false} {
		verb, short := "off", "Declare MCP servers off, and remove them from Claude Code: their definitions stay"
		if on {
			verb, short = "on", "Declare MCP servers on, and install them"
		}
		cmd.AddCommand(&cobra.Command{
			Use:   verb + " <name>...",
			Short: short,
			Long: short + `. Name a project's server as <folder>:<name>, as kit status shows it.
The change is committed and pushed to the config repository.`,
			Args: cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := a.prepare("mcp "+verb, "mcp-"+verb)
				if err != nil {
					return err
				}
				err = a.switchMCP(cmd.Context(), r, args, on)
				if closeErr := r.close(); err == nil {
					err = closeErr
				}
				return err
			},
		})
	}
	return cmd
}

// switchMCP declares each server named on, or off, commits and pushes the
// change, and installs or removes it.
func (a *app) switchMCP(ctx context.Context, r *run, names []string, on bool) error {
	m, ok := r.kindsByName["mcp"].(*mcp.MCP)
	if !ok {
		return errors.New("kit has no MCP servers' kind")
	}
	verb := "off"
	if on {
		verb = "on"
	}
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return switchOne(ctx, c, m, name, on)
		})
	}
	c.sync(ctx, fmt.Sprintf("kit mcp %s %s (%s)", verb, strings.Join(names, ", "), r.machine))
	return c.finish()
}

// switchOne declares the server name on or off, in whichever of this Mac's
// files declares it, and installs or removes it to match.
func switchOne(ctx context.Context, c *changes, m *mcp.MCP, name string, on bool) check.Result {
	verb := "off"
	if on {
		verb = "on"
	}
	where, err := m.Where(name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	i := slices.IndexFunc(where, func(e config.Entry) bool { return e.File == m.FileFor(true) || e.File == m.FileFor(false) })
	if i < 0 {
		return check.Result{State: check.Failed, Reason: "not declared for this Mac: kit add mcp declares a server installed in Claude Code"}
	}
	var done []string
	if e := where[i]; e.Off == on {
		if err := m.SetOff(e.File, name, !on); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		c.changed(e.File)
		done = append(done, "declared "+verb+" in "+e.File)
	}
	isIn, err := installed(ctx, m, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	switch {
	case on && !isIn:
		if _, err := m.Declared(); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		if err := m.Install(ctx, []string{name}); err != nil {
			return check.Result{State: check.Failed, Reason: strings.Join(append(done, "couldn't install: "+err.Error()), "; ")}
		}
		done = append(done, "installed")
	case !on && isIn:
		if err := m.Remove(ctx, []string{name}); err != nil {
			return check.Result{State: check.Failed, Reason: strings.Join(append(done, "couldn't remove it from Claude Code: "+err.Error()), "; ")}
		}
		done = append(done, "removed from Claude Code")
	}
	if len(done) == 0 {
		return check.Result{State: check.OK, Summary: "already " + verb}
	}
	return check.Result{State: check.OK, Summary: strings.Join(done, "; ")}
}
