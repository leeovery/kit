package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

func newTerminalCommand(a *app) *cobra.Command {
	var none bool
	cmd := &cobra.Command{
		Use:         "terminal [<name>]",
		Short:       "Show the terminal a new Mac's boot hands over to, or set it",
		Annotations: map[string]string{brief: "the terminal a new Mac's boot hands over to"},
		Long: `Show the terminal kit.toml names, the one a new Mac's boot installs and hands
over to, or set it: one of ` + apps.Names(apps.Terminals) + `. --none takes it out of kit.toml, and the
boot runs on in Terminal. A change is committed and pushed to the config
repository. --json gives the terminal as a document.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			switch {
			case none && len(args) > 0:
				return usageError{errors.New("name a terminal, or give --none, not both")}
			case len(args) > 0 && args[0] == "":
				return usageError{errors.New("name a terminal, or give --none")}
			case len(args) > 0:
				name = args[0]
			case !none:
				return a.showTerminal(cmd.OutOrStdout())
			}
			command := "terminal " + name
			if none {
				command = "terminal --none"
			}
			return a.preparedAs(cmd.Context(), command, "terminal", func(ctx context.Context, r *run) error {
				return changeSetting(ctx, r, config.TerminalSetting, r.cfg.Terminal, name, func() error { return r.cfg.SetTerminal(name) })
			})
		},
	}
	cmd.Flags().BoolVar(&none, "none", false, "take the terminal out: the boot runs on in Terminal")
	return cmd
}

func newNightlyAtCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "at [<time>]",
		Short: "Show when the nightly run falls due, or set it",
		Long: `Show when the nightly run falls due each day: nightly_at in kit.toml, else
03:00, the default. Given a time of day, as in 04:30, it sets nightly_at. A
change is committed and pushed to the config repository. --json gives the
time as a document.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return a.showNightlyAt(cmd.OutOrStdout())
			}
			at, err := config.ParseTimeOfDay(args[0])
			if err != nil {
				return err
			}
			now := config.TimeOfDay(at)
			return a.preparedAs(cmd.Context(), "nightly at "+now, "nightly-at", func(ctx context.Context, r *run) error {
				was := ""
				if r.cfg.NightlyAtSet() {
					was = config.TimeOfDay(r.cfg.NightlyAt)
				}
				return changeSetting(ctx, r, config.NightlyAtSetting, was, now, func() error { return r.cfg.SetNightlyAt(now) })
			})
		},
	}
}

// changeSetting changes kit.toml's own setting key from was to now, "" for
// none, with set, as a run's step, then commits kit.toml alone and pushes,
// saying what was run. As it is already, it sets nothing, and commits
// nothing.
func changeSetting(ctx context.Context, r *run, key, was, now string, set func() error) error {
	if was != now {
		if err := set(); err != nil {
			return err
		}
	}
	c := startChanges(r, []string{key})
	c.step(ctx, key, func(context.Context) check.Result {
		switch {
		case was == now && now == "":
			return check.Result{State: check.OK, Summary: "none already"}
		case was == now:
			return check.Result{State: check.OK, Summary: now + " already"}
		}
		c.changed(config.File)
		if now == "" {
			return check.Result{State: check.OK, Summary: "taken out of " + config.File}
		}
		return check.Result{State: check.OK, Summary: "set to " + now}
	})
	c.sync(ctx, fmt.Sprintf("kit %s (%s)", r.command, r.machine))
	return c.finish()
}

// loadedConfig is the config repository, loaded to show its settings.
func (a *app) loadedConfig() (*config.Config, error) {
	dirs, err := a.dirs()
	if err != nil {
		return nil, err
	}
	return a.loadConfig(dirs)
}

func (a *app) showTerminal(out io.Writer) error {
	cfg, err := a.loadedConfig()
	if err != nil {
		return err
	}
	if a.json {
		return render.WriteJSON(a.Stdout, struct {
			Schema   int    `json:"schema"`
			Terminal string `json:"terminal"`
		}{briefSchema, cfg.Terminal})
	}
	said := "a new Mac's boot hands over to " + look.Strong(cfg.Terminal)
	if cfg.Terminal == "" {
		said = config.File + " names no terminal: a new Mac's boot runs on in " + look.Strong("Terminal")
	}
	_, err = fmt.Fprintln(a.colors(out), said)
	return err
}

func (a *app) showNightlyAt(out io.Writer) error {
	cfg, err := a.loadedConfig()
	if err != nil {
		return err
	}
	at := config.TimeOfDay(cfg.NightlyAt)
	if a.json {
		return render.WriteJSON(a.Stdout, struct {
			Schema    int    `json:"schema"`
			NightlyAt string `json:"nightly_at"`
			Default   bool   `json:"default"`
		}{briefSchema, at, !cfg.NightlyAtSet()})
	}
	said := "the nightly run falls due at " + look.Strong(at)
	if !cfg.NightlyAtSet() {
		said += ", the default: " + config.File + " sets no " + config.NightlyAtSetting
	}
	_, err = fmt.Fprintln(a.colors(out), said)
	return err
}
