package boot

import (
	"slices"

	"github.com/leeovery/kit/internal/engine"
)

// Booted are the boot's steps as the terminal handed over to takes them, a
// light of their own beside kit apply's: checked again, not applied, as
// the boot applied them; named apart from kit apply's steps, each needing
// the one before.
func (b *Boot) Booted() []engine.Step {
	booted := slices.Concat(b.SignIn(), b.Order())
	for i := range booted {
		booted[i].Name = "boot-" + booted[i].Name
		booted[i].Area, booted[i].Apply, booted[i].Needs = AreaBooted, nil, nil
		if i > 0 {
			booted[i].Needs = []string{booted[i-1].Name}
		}
	}
	return booted
}

// InTerminal reports whether kit is running in the terminal kit.toml
// names: where the boot carries on, handed over to or not.
func (b *Boot) InTerminal() bool {
	t, ok := b.terminal(b.config())
	return ok && b.in(t)
}
