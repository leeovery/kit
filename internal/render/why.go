package render

import (
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/linked"
)

// Found is what kit knows of a name, as one kind, as kit why says it.
type Found struct {
	Kind string `json:"kind"`
	// Step is the step that installs it.
	Step string `json:"step"`
	// Declared are the files declaring it, this Mac's and other Macs'.
	Declared []config.Entry `json:"declared"`
	// ForThisMac is whether this Mac declares it.
	ForThisMac bool     `json:"for_this_mac"`
	Installed  bool     `json:"installed"`
	NeededBy   []string `json:"needed_by,omitempty"`
	// KnowsNeeds is whether the kind says what needs a thing.
	KnowsNeeds bool `json:"-"`
}

// Why is what kit why prints of name, found as each kind: where it's
// declared or linked from, whether it's here, and what needs it.
func Why(name string, found []Found) string {
	var b strings.Builder
	for _, w := range found {
		fmt.Fprintf(&b, "%s (%s)\n", name, w.Kind)
		if w.Kind == linked.StepName {
			fmt.Fprintf(&b, "  linked from %s\n", w.Declared[0].Path())
			if w.Installed {
				b.WriteString("  linked here\n")
			} else {
				fmt.Fprintf(&b, "  not linked here: kit status says why, kit apply or kit reconcile %s:%s settles it\n", w.Kind, name)
			}
			continue
		}
		for _, e := range w.Declared {
			line := fmt.Sprintf("  declared in %s, line %d", e.Path(), e.Line)
			if e.Note != "" {
				line += ": " + e.Note
			}
			b.WriteString(line + "\n")
		}
		switch {
		case w.ForThisMac && w.Installed:
			fmt.Fprintf(&b, "  installed here, by kit apply's %s step\n", w.Step)
		case w.ForThisMac:
			fmt.Fprintf(&b, "  not installed here: kit apply installs it\n")
		case w.Installed:
			fmt.Fprintf(&b, "  installed here, not declared for this Mac: kit reconcile %s:%s\n", w.Kind, name)
		default:
			fmt.Fprintf(&b, "  not declared for this Mac, nor installed here\n")
		}
		if w.KnowsNeeds {
			needed := "nothing installed"
			if len(w.NeededBy) > 0 {
				needed = strings.Join(w.NeededBy, ", ")
			}
			fmt.Fprintf(&b, "  needed by %s\n", needed)
		}
	}
	return b.String()
}
