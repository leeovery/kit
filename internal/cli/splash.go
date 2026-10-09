package cli

import (
	"errors"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/render"
)

// newSplashCommand is kit splash: the boot's two splashes, played again.
func newSplashCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "splash",
		Short: "Play the boot's splashes again, for fun",
		Long: `Play the boot's splashes again, for fun: the bare machine's, in amber, as a new
Mac's boot starts in Terminal; then, enter pressed, its arrival in your
terminal, in colour. A key skips either to its end; ctrl+c stops.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !a.pretty(a.Stdout) {
				return errors.New("the splashes play at a terminal")
			}
			dark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
			splash := render.NewSplash(dark)
			if err := splash.Play(cmd.Context(), terminal()); err != nil || splash.Stopped() {
				return err
			}
			return render.NewArrival(dark, "splash", a.machineName(), a.Now()).Play(cmd.Context(), terminal())
		},
	}
}
