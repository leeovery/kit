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

// claudeMCP names Claude Code's MCP servers' kind, and its command.
const claudeMCP = "claude-mcp"

func newClaudeMCPCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   claudeMCP,
		Short: "Turn Claude Code's MCP servers on or off, keeping them declared",
	}
	for _, on := range []bool{true, false} {
		verb, short := "off", "Declare MCP servers off, and remove them from Claude Code: their lines stay"
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
				r, err := a.prepare(claudeMCP+" "+verb, claudeMCP+"-"+verb)
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
// change, and installs or removes it to match.
func (a *app) switchMCP(ctx context.Context, r *run, names []string, on bool) error {
	m, ok := r.kindsByName[claudeMCP].(*mcp.MCP)
	if !ok {
		return errors.New("kit has no kind for Claude's MCP servers")
	}
	verb := "off"
	if on {
		verb = "on"
	}
	c := startChanges(r, names)
	for _, name := range names {
		c.step(ctx, name, func(ctx context.Context) check.Result {
			return switchOne(ctx, r, c, m, name, on, verb)
		})
	}
	c.sync(ctx, fmt.Sprintf("kit %s %s %s (%s)", claudeMCP, verb, strings.Join(names, ", "), r.machine))
	return c.finish()
}

// switchOne declares the server name on or off, in whichever of this Mac's
// files declares it, and installs or removes it to match.
func switchOne(ctx context.Context, r *run, c *changes, m *mcp.MCP, name string, on bool, verb string) check.Result {
	where, err := r.cfg.Where(claudeMCP, name)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	i := slices.IndexFunc(where, func(e config.Entry) bool { return e.File == config.Shared || e.File == r.machine })
	if i < 0 {
		return check.Result{State: check.Failed, Reason: "not declared for this Mac: kit add " + claudeMCP + " declares a server Claude Code has"}
	}
	e := where[i]
	var done []string
	value, err := mcp.SetOff(e.Value, !on)
	if err != nil {
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	if value != e.Value {
		e.Value = value
		if err := r.cfg.Replace(claudeMCP, e.File, e); err != nil {
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
		list, err := r.cfg.List(claudeMCP, r.machine)
		if err == nil {
			_, err = m.Values(list)
		}
		if err == nil {
			err = m.Install(ctx, []string{name})
		}
		if err != nil {
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
