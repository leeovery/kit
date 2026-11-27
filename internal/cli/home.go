package cli

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/setup"
)

// menuKeys are the keys kit's menu takes.
var menuKeys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "run"}, {Key: "q", Does: "quit"}}

// underHome is the hidden flag kit runs a command from its menu with: under
// the menu's wordmark, its heading leaves the wordmark out.
const underHome = "under-home"

// home is bare kit: at a terminal, at once, kit's menu under the wordmark,
// with the Mac and the time, what to run next, each with what it does and
// its command, Status first. q folds it away; enter leaves the line chosen,
// and kit becomes that command, under the wordmark. Without a terminal,
// kit's help; kit status is the report. Neither takes --json.
func (a *app) home(cmd *cobra.Command) error {
	if err := a.textOnly(cmd); err != nil {
		return err
	}
	if !a.pretty(a.Stdout) || !a.Terminal(a.Stdin) {
		return cmd.Help()
	}
	if says, does := a.setupUnfinished(); says != "" {
		return a.offerSetup(cmd.Context(), says, does)
	}
	width := look.Fit(a.Width(a.Stdout))
	choices := []look.Choice{
		{Label: "Status", Does: "every check", Cmd: "kit status"},
		{Label: "Apply", Does: "install what's missing", Cmd: "kit apply"},
		{Label: "Reconcile", Does: "settle what differs from the config", Cmd: "kit reconcile"},
		{Label: "Log", Does: "past runs", Cmd: "kit log"},
		{Label: "Nightly", Does: "run the jobs that are due, then every check", Cmd: "kit nightly"},
		{Label: "App settings", Does: "save apps' settings now", Cmd: "kit prefs capture"},
		{Label: "Secrets", Does: "fetch them again from their stores", Cmd: "kit secret sync"},
		{Label: "Declared", Does: "what's declared for this Mac", Cmd: "kit list"},
		{Label: "Help", Does: "every command", Cmd: "kit --help"},
	}
	lines := make([]ask.Line, len(choices))
	for i, c := range choices {
		lines[i] = ask.Line{
			Text:   "  " + look.Answers(choices, -1)[i],
			Chosen: "  " + look.Answers(choices, i)[i],
			Value:  strings.TrimPrefix(c.Cmd, "kit "),
		}
	}
	heading := append([]string{""}, look.Head(look.Meta("", a.machineName(), a.Now().Format("Mon 2 Jan · 15:04"))...)...)
	heading = append(heading, "")
	chosen, err := a.Pick(cmd.Context(), heading, lines, menuKeys)
	switch {
	case errors.Is(err, ask.ErrCancelled):
		return nil
	case err != nil:
		return err
	}
	for _, l := range lines {
		if l.Value == chosen {
			record := append(heading, look.Cut(l.Chosen, width))
			_, _ = io.WriteString(a.colors(a.Stdout), strings.Join(record, "\n")+"\n")
		}
	}
	return a.becomeSelf(append([]string{"--" + underHome}, strings.Fields(chosen)...))
}

// setupUnfinished is what bare kit says of this Mac while kit setup hasn't
// finished, and what enter does: a run stopped part-way, as its record
// says, carries on; with the config folder absent or empty, as kit setup
// starts on, nothing's set up. "" once there's a config, for the menu.
func (a *app) setupUnfinished() (says, does string) {
	dirs, err := a.dirs()
	if err != nil {
		return "", ""
	}
	if setup.PartWay(dirs.State) {
		return "setup stopped part-way", "carries on"
	}
	if empty, err := setup.Empty(dirs.Config); err == nil && empty {
		return "isn't set up with kit", "runs kit setup"
	}
	return "", ""
}

// offerSetup is bare kit on a Mac kit setup hasn't finished: the wordmark,
// then a line saying how it stands; enter has kit become kit setup, any
// other key leaves.
func (a *app) offerSetup(ctx context.Context, says, does string) error {
	lines := append([]string{""}, look.Head(look.Muted(a.Now().Format("Mon 2 Jan · 15:04")))...)
	lines = append(lines, "", look.Rows("  ", look.Fit(a.Width(a.Stdout)), look.Row{State: look.NeedsYou, Name: "This Mac", Says: look.Words(look.NeedsYou, says)})[0])
	taken, err := a.Offer(ctx, lines, []look.Key{{Key: "enter", Does: does}, {Key: "any other key", Does: "leaves"}})
	if err != nil || !taken {
		return err
	}
	return a.becomeSelf([]string{"setup"})
}
