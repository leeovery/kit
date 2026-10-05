package cli

import (
	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/render"
)

// glance runs every check, as kit status does, and shows what needs
// attention a line an area: --json prints the same document as kit status.
func (a *app) glance(cmd *cobra.Command) error {
	var face render.Face = render.NewGlance(a.colors(a.Stdout), a.pretty(a.Stdout), a.Now)
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
	if report.Attention() {
		return attention{}
	}
	return nil
}
