package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// glance runs every check, as kit status does, and shows what needs
// attention a line an area: --json prints the same document as kit status.
// At a terminal, it's kit's home: a menu under the lines offers what to run
// next, and kit becomes the command chosen.
func (a *app) glance(cmd *cobra.Command) error {
	pretty := a.pretty(a.Stdout)
	glance := render.NewGlance(a.colors(a.Stdout), a.Width(a.Stdout), pretty, a.Now)
	var face render.Face = glance
	if a.json {
		face = render.NewJSON(a.Stdout)
	}
	r, err := a.prepareWith("", "kit", face)
	if err != nil {
		return err
	}
	report, err := r.pipeline.Check(cmd.Context(), r.sink, r.options(a, nil))
	if err == nil {
		err = r.remember(report)
	}
	if closeErr := r.close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if pretty {
		if err := a.menu(cmd.Context(), glance.Home(), report); err != nil {
			return err
		}
	}
	if report.Attention() {
		return attention{}
	}
	return nil
}

// menuKeys are the keys the home's menus take.
var menuKeys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "run"}, {Key: "q", Does: "quit"}}

// more is the home's menu's choice of its second menu.
const more = "more"

// menu offers, under the home, what to run next, each with what it does and
// its command: Reconcile, Apply, Status, Log, and More, the rest; the cursor
// on what's needed first. kit becomes the command chosen; q leaves the home
// as it is.
func (a *app) menu(ctx context.Context, home []string, report engine.Report) error {
	lead := append(home, "", look.Rule(min(max(a.Width(a.Stdout), 40), look.Width)))
	decide, missing := needs(report)
	reconcile, start := "settle what differs from the config", "status"
	switch {
	case len(decide) == 1:
		reconcile, start = "decide on "+decide[0], "reconcile"
	case len(decide) > 1:
		reconcile, start = fmt.Sprintf("decide on %d things", len(decide)), "reconcile"
	}
	if len(missing) > 0 && len(missing) == len(decide) {
		start = "apply"
	}
	chosen, err := a.Pick(ctx, lead, menuLines(start,
		look.Choice{Label: "Reconcile", Does: reconcile, Cmd: "kit reconcile"},
		look.Choice{Label: "Apply", Does: "install what's missing", Cmd: "kit apply"},
		look.Choice{Label: "Status", Does: "every check", Cmd: "kit status"},
		look.Choice{Label: "Log", Does: "past runs", Cmd: "kit log"},
		look.Choice{Label: "More", Does: "jobs, app settings, secrets, what's declared, help"},
	), menuKeys)
	if chosen == more {
		chosen, err = a.Pick(ctx, lead, menuLines("",
			look.Choice{Label: "Nightly", Does: "run the jobs that are due, then every check", Cmd: "kit nightly"},
			look.Choice{Label: "App settings", Does: "save apps' settings now", Cmd: "kit prefs capture"},
			look.Choice{Label: "Secrets", Does: "fetch them from 1Password again", Cmd: "kit secret sync"},
			look.Choice{Label: "Declared", Does: "what's declared for this Mac", Cmd: "kit list"},
			look.Choice{Label: "Help", Does: "every command", Cmd: "kit --help"},
		), menuKeys)
	}
	switch {
	case errors.Is(err, ask.ErrCancelled):
		return nil
	case err != nil:
		return err
	}
	return a.Become(strings.Fields(chosen))
}

// menuLines are a menu's choices as lines to pick from, at the left, each
// giving its command, after kit; the cursor starting on start's.
func menuLines(start string, choices ...look.Choice) []ask.Line {
	lines := make([]ask.Line, len(choices))
	for i, c := range choices {
		value := strings.TrimPrefix(c.Cmd, "kit ")
		if c.Cmd == "" {
			value = strings.ToLower(c.Label)
		}
		lines[i] = ask.Line{
			Text:   "  " + look.Answers(choices, -1)[i],
			Chosen: "  " + look.Answers(choices, i)[i],
			Value:  value,
			Start:  value == start,
		}
	}
	return lines
}

// needs is what the home found differing from the config: the things kit
// reconcile decides on, by name, and those of them kit apply sees to.
func needs(report engine.Report) (decide, missing []string) {
	for _, s := range report.Steps {
		for _, it := range report.Results[s.Name].Items {
			if it.Quiet != "" || !render.IsDrift(it) {
				continue
			}
			decide = append(decide, it.Name)
			if it.Action != "" || it.State == "missing" {
				missing = append(missing, it.Name)
			}
		}
	}
	return decide, missing
}
