// Package kinds is every kind kit knows, each described in its own package
// (kind.Descriptor): the one list kit's shared code reads them from, for
// the config's sections, the commands, the run's steps and the screen's
// rows. A kind comes and goes with its line here.
package kinds

import (
	"cmp"
	"slices"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/appstore"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/kind/composer"
	"github.com/leeovery/kit/internal/kind/defaults"
	"github.com/leeovery/kit/internal/kind/exclusion"
	"github.com/leeovery/kit/internal/kind/ghext"
	"github.com/leeovery/kit/internal/kind/gitconfig"
	"github.com/leeovery/kit/internal/kind/gotool"
	"github.com/leeovery/kit/internal/kind/login"
	"github.com/leeovery/kit/internal/kind/mcp"
	"github.com/leeovery/kit/internal/kind/npm"
	"github.com/leeovery/kit/internal/kind/power"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/skill"
	"github.com/leeovery/kit/internal/kind/spotlight"
	"github.com/leeovery/kit/internal/kind/tmux"
)

// All are every kind kit knows, in the order a run takes them.
var All = []kind.Descriptor{
	brew.FormulaeDescriptor,
	brew.CasksDescriptor,
	appstore.Descriptor,
	npm.Descriptor,
	composer.Descriptor,
	gotool.Descriptor,
	ghext.Descriptor,
	tmux.Descriptor,
	login.Descriptor,
	mcp.Descriptor,
	skill.Descriptor,
	secret.Descriptor,
	gitconfig.Descriptor,
	defaults.Descriptor,
	power.Descriptor,
	exclusion.Descriptor,
	spotlight.Descriptor,
}

// Named is the kind called name, and whether kit knows one.
func Named(name string) (kind.Descriptor, bool) {
	i := slices.IndexFunc(All, func(d kind.Descriptor) bool { return d.Name == name })
	if i < 0 {
		return kind.Descriptor{}, false
	}
	return All[i], true
}

// Label is the label of the kind called name, as in Homebrew formulae for
// brew; name itself for a step that isn't a kind's.
func Label(name string) string {
	d, _ := Named(name)
	return cmp.Or(d.Label, name)
}

// Of is the kind the step called name is, or the first that needs it, as
// Homebrew's step is its formulae's; and whether there's one.
func Of(step string) (kind.Descriptor, bool) {
	if d, ok := Named(step); ok {
		return d, true
	}
	i := slices.IndexFunc(All, func(d kind.Descriptor) bool { return slices.Contains(d.Needs, step) })
	if i < 0 {
		return kind.Descriptor{}, false
	}
	return All[i], true
}

// fileOrder is the order of the kinds' groups in a declarations file: the
// Mac's settings, then its packages, then its secrets.
var fileOrder = []kind.Group{kind.Settings, kind.Packages, kind.Secrets}

// Sections are the kinds' sections of a declarations file, in the order
// kit writes them: by group (fileOrder), then in the run's order.
func Sections() []config.Section {
	var out []config.Section
	for _, group := range fileOrder {
		for _, d := range All {
			if d.Section != nil && d.Group == group {
				s := *d.Section
				s.Kind = d.Name
				out = append(out, s)
			}
		}
	}
	return out
}
