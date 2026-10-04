package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/render"
)

// listSchema is kit list --json's document's version.
const listSchema = 1

// listed is a declared name, where it's declared, and whether it's
// installed.
type listed struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Group     string `json:"group,omitempty"`
	Note      string `json:"note,omitempty"`
	Installed bool   `json:"installed"`
}

func newListCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list [kind]",
		Short: "List what's declared for this Mac: each name, its file, group and note, and whether it's installed",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := a.prepare("list", "list")
			if err != nil {
				return err
			}
			err = a.list(cmd.Context(), r, args)
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	}
}

func (a *app) list(ctx context.Context, r *run, args []string) error {
	kinds := []string{"brew", "cask"}
	if len(args) == 1 {
		if _, err := r.kindNamed(args[0]); err != nil {
			return err
		}
		kinds = args
	}
	var entries []listed
	for _, name := range kinds {
		k := r.kindsByName[name]
		res := kind.Compare(ctx, k, r.lists[name])
		if res.State == check.Failed {
			return fmt.Errorf("couldn't check %s: %s", k.Title(), res.Reason)
		}
		missing := make(map[string]bool)
		for _, it := range res.Items {
			if it.State == kind.Missing {
				missing[it.Name] = true
			}
		}
		for _, e := range r.lists[name].Entries {
			entries = append(entries, listed{Kind: name, Name: e.Name, File: e.File, Line: e.Line, Group: e.Group, Note: e.Note, Installed: !missing[e.Name]})
		}
	}
	if a.json {
		if entries == nil {
			entries = []listed{}
		}
		return render.WriteJSON(a.Stdout, struct {
			Schema  int      `json:"schema"`
			Machine string   `json:"machine"`
			Entries []listed `json:"entries"`
		}{listSchema, r.machine, entries})
	}
	nameWidth, fileWidth := 0, 0
	for _, e := range entries {
		nameWidth = max(nameWidth, ansi.StringWidth(e.Name))
		fileWidth = max(fileWidth, ansi.StringWidth(e.File))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "kit list · %s\n", r.machine)
	for _, e := range entries {
		line := fmt.Sprintf("%-4s  %-*s  %-*s  %s", e.Kind, nameWidth, e.Name, fileWidth, e.File, e.Group)
		if !e.Installed {
			line += "  (missing)"
		}
		if e.Note != "" {
			line += "  # " + e.Note
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	_, err := fmt.Fprint(a.colors(a.Stdout), b.String())
	return err
}

// whySchema is kit why --json's document's version.
const whySchema = 1

// why is what kit knows of a name, as one kind.
type why struct {
	Kind string `json:"kind"`
	// Step is the step that installs it.
	Step string `json:"step"`
	// Declared are the files declaring it, this Mac's and other Macs'.
	Declared []config.Entry `json:"declared"`
	// ForThisMac is whether this Mac declares it.
	ForThisMac bool     `json:"for_this_mac"`
	Installed  bool     `json:"installed"`
	NeededBy   []string `json:"needed_by,omitempty"`
}

func newWhyCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "why <name>",
		Short: "Say where a package is declared, whether it's installed, and what needs it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := a.prepare("why", "why")
			if err != nil {
				return err
			}
			err = a.why(cmd.Context(), r, args[0])
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			return err
		},
	}
}

func (a *app) why(ctx context.Context, r *run, name string) error {
	var found []why
	for _, kindName := range []string{"brew", "cask"} {
		k := r.kindsByName[kindName]
		declared, err := r.cfg.Where(kindName, name)
		if err != nil {
			return err
		}
		isIn, err := installed(ctx, k, name)
		if err != nil {
			return err
		}
		if len(declared) == 0 && !isIn {
			continue
		}
		w := why{Kind: kindName, Step: k.Title(), Declared: declared, Installed: isIn}
		w.ForThisMac = slices.ContainsFunc(declared, func(e config.Entry) bool {
			return e.File == kindName || e.File == kindName+"."+r.machine
		})
		if isIn && kindName == "brew" {
			if w.NeededBy, err = r.homebrew.Uses(ctx, name); err != nil {
				return err
			}
		}
		found = append(found, w)
	}
	if a.json {
		if found == nil {
			found = []why{}
		}
		return render.WriteJSON(a.Stdout, struct {
			Schema  int    `json:"schema"`
			Machine string `json:"machine"`
			Name    string `json:"name"`
			Kinds   []why  `json:"kinds"`
		}{whySchema, r.machine, name, found})
	}
	if len(found) == 0 {
		return attention{message: name + " isn't declared or installed, as a formula or a cask"}
	}
	var b strings.Builder
	for _, w := range found {
		fmt.Fprintf(&b, "%s (%s)\n", name, w.Kind)
		for _, e := range w.Declared {
			line := fmt.Sprintf("  declared in %s, line %d", e.File, e.Line)
			if e.Group != "" {
				line += ", under " + e.Group
			}
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
		if w.Installed && w.Kind == "brew" {
			needed := "nothing installed"
			if len(w.NeededBy) > 0 {
				needed = strings.Join(w.NeededBy, ", ")
			}
			fmt.Fprintf(&b, "  needed by %s\n", needed)
		}
	}
	_, err := fmt.Fprint(a.Stdout, b.String())
	return err
}
