package render

import (
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/look"
)

// Place is a block secrets are declared in, as kit secret places lists it:
// the name its header gives it, if any; its store and its item, none for
// the plain [secrets], whose references are in full; whose declarations
// declare it, shared or a Mac's, its file and its header's line; and how
// many secrets it holds; and the sections of its item they're in.
type Place struct {
	Name     string   `json:"name,omitempty"`
	Store    string   `json:"store,omitempty"`
	Item     string   `json:"item,omitempty"`
	Scope    string   `json:"scope"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Secrets  int      `json:"secrets"`
	Sections []string `json:"sections,omitempty"`
}

// Plain is a block as a plain line lists it: its title, and what's said of
// it after.
func (p Place) Plain() string {
	return p.Title() + "  " + strings.Join(p.Says(), " · ")
}

// Places are places as kit's face draws them in width: a row each, by its
// name, or its item, its store, item, file and count after it.
func Places(width int, places []Place) []string {
	rows := make([]look.Row, len(places))
	for i, p := range places {
		says := p.Says()
		for j, s := range says {
			says[j] = look.Muted(s)
		}
		rows[i] = look.Row{State: look.Done, Name: p.Title(), Says: look.Says(says...)}
	}
	return look.Rows("  ", width, rows...)
}

// Title is what a block's called: the name its header gives it, else its
// item, else, the plain block, its header.
func (p Place) Title() string {
	switch {
	case p.Name != "":
		return p.Name
	case p.Item != "":
		return p.Item
	}
	return "[secrets]"
}

// Says is what's said of a block after its title: its store, its item when
// its title is its name, its scope, how many secrets it holds, and the
// sections of its item they're in.
func (p Place) Says() []string {
	var says []string
	switch {
	case p.Store != "":
		says = append(says, p.Store)
	case p.Item == "":
		says = append(says, "references in full")
	}
	if p.Name != "" {
		says = append(says, p.Item)
	}
	says = append(says, p.Scope, fmt.Sprintf("%d %s", p.Secrets, look.Plural(p.Secrets, "secret", "secrets")))
	if len(p.Sections) > 0 {
		says = append(says, look.Plural(len(p.Sections), "section ", "sections ")+strings.Join(p.Sections, ", "))
	}
	return says
}
