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
	"github.com/leeovery/kit/internal/kinds"
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
	// Scope is whose declarations declare it, shared or a Mac's.
	Scope string `json:"scope,omitempty"`
	// File is the file declaring it, as in laptop/declarations.
	File      string `json:"file"`
	Line      int    `json:"line"`
	Note      string `json:"note,omitempty"`
	Installed bool   `json:"installed"`
	// Off is whether it's declared, but not to be installed.
	Off bool `json:"off,omitempty"`
}

func newListCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:         "list [kind]",
		Short:       "List what's declared for this Mac: each name, its file and note, and whether it's installed",
		Annotations: map[string]string{brief: "what's declared for this Mac"},
		Args:        cobra.MaximumNArgs(1),
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
	names := r.kinds
	if len(args) == 1 {
		if _, err := r.kindNamed(args[0]); err != nil {
			return err
		}
		if !slices.Contains(r.kinds, args[0]) {
			return fmt.Errorf("no kind named %s on this Mac: one of %s", args[0], strings.Join(r.kinds, ", "))
		}
		names = args
	}
	var entries []listed
	stop := a.loading(a.Stdout, "checking what's installed")
	defer stop()
	for _, name := range names {
		k := r.kindsByName[name]
		res := kind.Compare(ctx, k, r.lists[name])
		if err := unchecked(kinds.Label(k.Name()), res); err != nil {
			return err
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
	stop()
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
		line := strings.TrimRight(fmt.Sprintf("%-*s  %-*s  %s", kindWidth, e.Kind, nameWidth, e.Name, e.Scope), " ")
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
const whySchema = 3

func newWhyCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:         "why <name>",
		Short:       "Say where a package is declared, whether it's installed, and what needs it",
		Annotations: map[string]string{brief: "where a package is declared, and what needs it"},
		Args:        cobra.ExactArgs(1),
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
	stop := a.loading(a.Stdout, "looking for "+name)
	defer stop()
	var found []render.Found
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
				found = append(found, render.Found{Kind: linked.StepName, Step: "Linked files", Declared: []config.Entry{{Name: l.Name, Scope: l.Scope, File: l.Path}}, ForThisMac: true, Installed: linked.IsLinked(l)})
			}
		}
	}
	for _, kindName := range r.allKinds {
		k := r.kindsByName[kindName]
		declared, err := r.cfg.Where(kindName, name)
		if err != nil {
			return err
		}
		// A kind the run doesn't check isn't looked after on this Mac, so
		// nothing of it is looked for.
		isIn := false
		if slices.Contains(r.kinds, kindName) {
			if isIn, err = installed(ctx, k, name); err != nil {
				return err
			}
		}
		if len(declared) == 0 && !isIn {
			continue
		}
		w := render.Found{Kind: kindName, Step: kinds.Label(k.Name()), Declared: declared, Installed: isIn}
		w.ForThisMac = slices.ContainsFunc(declared, func(e config.Entry) bool {
			return e.Scope == config.Shared || e.Scope == r.machine
		})
		if d, ok := k.(kind.Dependents); ok && isIn {
			if w.NeededBy, err = d.NeededBy(ctx, name); err != nil {
				return err
			}
			w.KnowsNeeds = true
		}
		found = append(found, w)
	}
	stop()
	if a.json {
		if found == nil {
			found = []render.Found{}
		}
		return render.WriteJSON(a.Stdout, struct {
			Schema  int            `json:"schema"`
			Machine string         `json:"machine"`
			Name    string         `json:"name"`
			Kinds   []render.Found `json:"kinds"`
		}{whySchema, r.machine, name, found})
	}
	if len(found) == 0 {
		return notFound{message: name + " isn't declared for any Mac, nor installed here"}
	}
	_, err := fmt.Fprint(a.Stdout, render.Why(name, found))
	return err
}

// unchecked is the error for the kind called label whose check, res,
// couldn't tell what's installed: it failed, or macOS hasn't let it look.
// Nil when it could.
func unchecked(label string, res check.Result) error {
	if it, ok := res.Refused(); ok {
		return fmt.Errorf("couldn't check %s: %s; allow it in %s", label, it.Name, it.Detail)
	}
	if res.State == check.Failed {
		return fmt.Errorf("couldn't check %s: %s", label, res.Reason)
	}
	return nil
}
