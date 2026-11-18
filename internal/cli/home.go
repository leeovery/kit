package cli

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/config"
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
	if !a.pretty(a.Stdout) {
		return cmd.Help()
	}
	if a.nothingSetUp() {
		return a.offerSetup(cmd.Context())
	}
	width := min(max(a.Width(a.Stdout), 40), look.Width)
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

// nothingSetUp reports whether kit has nothing set up on this Mac: no
// config in its folder, and no run of kit setup stopped part-way.
func (a *app) nothingSetUp() bool {
	dirs, err := a.dirs()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dirs.Config, config.File))
	return errors.Is(err, fs.ErrNotExist) && !setup.PartWay(dirs.State)
}

// offerSetup is bare kit on a Mac with nothing set up: the wordmark, then
// a line saying so; enter has kit become kit setup, any other key leaves.
func (a *app) offerSetup(ctx context.Context) error {
	lines := append([]string{""}, look.Head(look.Muted(a.Now().Format("Mon 2 Jan · 15:04")))...)
	lines = append(lines, "", look.Rows("  ", look.Width, look.Row{State: look.NeedsYou, Name: "This Mac", Says: look.Words(look.NeedsYou, "isn't set up with kit")})[0])
	taken, err := a.Offer(ctx, lines, []look.Key{{Key: "enter", Does: "runs kit setup"}, {Key: "any other key", Does: "leaves"}})
	if err != nil || !taken {
		return err
	}
	return a.becomeSelf([]string{"setup"})
}
