package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/render"
)

// listSchema is kit list --json's document's version.
const listSchema = 2

// listed is a declared name, where it's declared, and whether it's
// installed.
type listed struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Scope is whose declarations declare it, shared or a Mac's: none for a
	// thing declared in a file of its own.
	Scope string `json:"scope,omitempty"`
	// File is the file declaring it, as in laptop/declarations.
	File      string `json:"file"`
	Line      int    `json:"line"`
	Note      string `json:"note,omitempty"`
	Installed bool   `json:"installed"`
	// Off is whether it's declared, but not to be installed.
	Off bool `json:"off,omitempty"`
}

// where is whose declarations declare it, or the file of its own that does.
func (l listed) where() string {
	if l.Scope != "" {
		return l.Scope
	}
	return l.File
}

func newListCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list [kind]",
		Short: "List what's declared for this Mac: each name, its file and note, and whether it's installed",
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
	kinds := r.kinds
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
			// A kind deferred, its program not installed, has nothing installed;
			// a thing declared off is never missing, so it's looked for.
			isIn := !missing[e.Name] && res.State != check.Deferred
			if e.Off && isIn {
				var err error
				if isIn, err = installed(ctx, k, e.Name); err != nil {
					return err
				}
			}
			entries = append(entries, listed{Kind: name, Name: e.Name, Scope: e.Scope, File: e.Path(), Line: e.Line, Note: e.Note, Installed: isIn, Off: e.Off})
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
	kindWidth, nameWidth := 0, 0
	for _, e := range entries {
		kindWidth = max(kindWidth, ansi.StringWidth(e.Kind))
		nameWidth = max(nameWidth, ansi.StringWidth(e.Name))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "kit list · %s\n", r.machine)
	for _, e := range entries {
		line := strings.TrimRight(fmt.Sprintf("%-*s  %-*s  %s", kindWidth, e.Kind, nameWidth, e.Name, e.where()), " ")
		switch {
		case e.Off:
			line += "  (off)"
		case !e.Installed:
			line += "  (missing)"
		}
		if e.Note != "" {
			line += "  # " + e.Note
		}
		b.WriteString(line + "\n")
	}
	_, err := fmt.Fprint(a.colors(a.Stdout), b.String())
	return err
}

// whySchema is kit why --json's document's version.
const whySchema = 2

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
	// knowsNeeds is whether the kind says what needs a thing.
	knowsNeeds bool
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
	if strings.HasPrefix(name, "~/") || filepath.IsAbs(name) {
		_, shown, err := r.filePaths([]string{name})
		if err != nil {
			return err
		}
		name = shown[0]
		links, err := r.files.Declared(ctx)
		if err != nil {
			return err
		}
		for _, l := range links {
			if l.Name == name {
				found = append(found, why{Kind: linked.StepName, Step: "Linked files", Declared: []config.Entry{{Name: l.Name, Scope: l.Scope, File: l.Path}}, ForThisMac: true, Installed: linked.IsLinked(l)})
			}
		}
	}
	for _, kindName := range r.allKinds {
		k := r.kindsByName[kindName]
		declared, err := r.where(kindName, name)
		if err != nil {
			return err
		}
		// A kind the run doesn't check has its program not installed, so
		// nothing of it is.
		isIn := false
		if slices.Contains(r.kinds, kindName) {
			if isIn, err = installed(ctx, k, name); err != nil {
				return err
			}
		}
		if len(declared) == 0 && !isIn {
			continue
		}
		w := why{Kind: kindName, Step: k.Title(), Declared: declared, Installed: isIn}
		_, outside := k.(kind.Declarer)
		w.ForThisMac = slices.ContainsFunc(declared, func(e config.Entry) bool {
			return outside || e.Scope == config.Shared || e.Scope == r.machine
		})
		if d, ok := k.(kind.Dependents); ok && isIn {
			if w.NeededBy, err = d.NeededBy(ctx, name); err != nil {
				return err
			}
			w.knowsNeeds = true
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
		return attention{message: name + " isn't declared for any Mac, nor installed here"}
	}
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
		if w.knowsNeeds {
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
