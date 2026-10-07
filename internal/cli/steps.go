package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/steps"
)

// stepCommand is kit step's name.
const stepCommand = "step"

// scriptSteps are the steps of the user's own declared for this Mac, beside
// the pipeline's others, base: a line that doesn't read, or that needs a
// step that isn't here or comes after one kit doesn't know, is a step whose
// check says so; one named as a step of kit's is refused, saying where.
func (r *run) scriptSteps(base []engine.Step) ([]engine.Step, error) {
	list, err := r.cfg.List(config.StepsKind, r.machine)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(base)+len(list.Entries))
	for _, s := range base {
		names[s.Name] = true
	}
	for _, e := range list.Entries {
		if names[e.Name] {
			return nil, fmt.Errorf("%s: a step of your own can't be called %s, as one of kit's is: rename it", e.Pos(), e.Name)
		}
	}
	for _, e := range list.Entries {
		names[e.Name] = true
	}
	var out []engine.Step
	for _, e := range list.Entries {
		s, err := steps.ParseScript(r.cfg.Dir, e)
		if err == nil {
			err = r.placeScript(&s, names)
		}
		if err != nil {
			out = append(out, steps.BrokenScript(e, err))
			continue
		}
		step := steps.ScriptStep(r.run, r.admin, s)
		if step.Admin {
			r.adminSteps = append(r.adminSteps, step)
		}
		out = append(out, step)
	}
	return out, nil
}

// placeScript checks s needs only steps among names, and comes after steps
// among them, or kinds this Mac doesn't use, which it's then applied
// without: an error says which aren't.
func (r *run) placeScript(s *steps.Script, names map[string]bool) error {
	var wrong []string
	for _, need := range s.Needs {
		if !names[need] {
			wrong = append(wrong, "it needs "+need+", which isn't a step here")
		}
	}
	s.After = slices.DeleteFunc(s.After, func(after string) bool {
		if names[after] {
			return false
		}
		if !slices.Contains(r.allKinds, after) {
			wrong = append(wrong, "it comes after "+after+", which isn't a step")
		}
		return true
	})
	if len(wrong) > 0 {
		return errors.New(strings.Join(wrong, "; "))
	}
	return nil
}

// stepOptions are kit step add's options.
type stepOptions struct {
	after, needs []string
	admin        bool
	addOptions
}

// newStepCommand is kit step: adding and removing steps of your own.
func newStepCommand(a *app) *cobra.Command {
	var opts stepOptions
	add := &cobra.Command{
		Use:   `add <name> "<what it does>"`,
		Short: "Declare a step of your own, its script a template to fill in (--shared: every Mac)",
		Long: `Declare a step of your own: set-up that's done or not, as a script in
kit-config, its folder's other files its data. kit runs the script as run
check (exit 0: done; 1: not done; its first line saying how it stands) and,
when it isn't done, as run apply, then checks again. --after names a step it's
applied after, --needs one that must stand ok first, each as often as wanted;
--admin says applying needs an administrator's password.

The script starts as a template following that contract: write its check and
apply.` + declaredWhere,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, stepCommand+" add", func(ctx context.Context, r *run) error {
				return a.addStep(ctx, r, args[0], args[1], opts)
			})
		},
	}
	add.Flags().StringArrayVar(&opts.after, "after", nil, "a step it's applied after")
	add.Flags().StringArrayVar(&opts.needs, "needs", nil, "a step that must stand ok first")
	add.Flags().BoolVar(&opts.admin, "admin", false, "applying it needs an administrator's password")
	add.Flags().BoolVar(&opts.shared, "shared", false, "declare for every Mac, not this one alone")
	add.Flags().StringVar(&opts.note, "note", "", "why it's declared, kept after it")
	return newLineCommand(a, stepCommand, "Steps of your own, each a script in kit-config that sets something up", add)
}

// addStep declares the step called name, which does what does says, and
// writes its folder, its script a template.
func (a *app) addStep(ctx context.Context, r *run, name, does string, opts stepOptions) error {
	scope := r.scope(opts.shared)
	words := []string{config.Quote(does)}
	for _, after := range opts.after {
		words = append(words, "--after", config.Quote(after))
	}
	for _, need := range opts.needs {
		words = append(words, "--needs", config.Quote(need))
	}
	if opts.admin {
		words = append(words, "--admin")
	}
	entry := config.Entry{Name: name, Value: strings.Join(words, " "), Note: opts.note, Scope: scope}
	c := startChanges(r, []string{name})
	c.step(ctx, name, func(context.Context) check.Result {
		fail := func(err error) check.Result { return check.Result{State: check.Failed, Reason: err.Error()} }
		s, err := steps.ParseScript(r.cfg.Dir, entry)
		if err != nil {
			return fail(err)
		}
		names := make(map[string]bool)
		for _, step := range r.pipeline.Steps() {
			names[step.Name] = true
		}
		if names[name] {
			return fail(fmt.Errorf("a step called %s is there already", name))
		}
		if err := r.placeScript(&s, names); err != nil {
			return fail(err)
		}
		if _, err := os.Stat(s.Dir); !errors.Is(err, fs.ErrNotExist) {
			return fail(fmt.Errorf("%s is there already: remove it, or name the step otherwise", s.Folder))
		}
		if err := r.cfg.Declare(config.StepsKind, scope, entry, ""); err != nil {
			return fail(err)
		}
		c.changed(config.DeclFile(scope))
		if err := os.MkdirAll(s.Dir, 0o755); err != nil {
			return fail(err)
		}
		script := filepath.Join(s.Dir, steps.ScriptName)
		if err := os.WriteFile(script, []byte(steps.Template(name, does)), 0o755); err != nil {
			return fail(err)
		}
		c.changed(s.Folder)
		return check.Result{State: check.OK, Summary: "declared in " + scope + "; its script, " + s.Folder + "/" + steps.ScriptName + ", is a template: write its check and apply"}
	})
	c.sync(ctx, commitMessage("add", stepCommand, []string{name}, r.machine, opts.note))
	return c.finish()
}
