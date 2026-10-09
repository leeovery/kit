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
	"github.com/leeovery/kit/internal/kind"
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
		var waits []engine.Wait
		if err == nil {
			waits, err = r.placeScript(&s, names)
		}
		if err != nil {
			out = append(out, steps.BrokenScript(e, err))
			continue
		}
		step := steps.ScriptStep(r.run, r.admin, s)
		step.Waits = waits
		if step.Admin {
			r.adminSteps = append(r.adminSteps, step)
		}
		out = append(out, step)
	}
	return out, nil
}

// manualSteps are the steps by hand that say what they come after or
// before, each a step of its own among others, the run's other steps: one
// placed after or before what isn't a step fails, saying so; one named as
// another step is can't be told apart from it, and is refused.
func (r *run) manualSteps(ordered []steps.ManualStep, others []engine.Step, raise *steps.Raise) ([]engine.Step, error) {
	names := make(map[string]bool, len(others)+len(ordered))
	for _, s := range others {
		names[s.Name] = true
	}
	for _, m := range ordered {
		if names[m.Name] {
			return nil, fmt.Errorf("%s: a step by hand can't be called %s, as another step is: rename it", m.Pos, m.Name)
		}
		names[m.Name] = true
	}
	var out []engine.Step
	for _, m := range ordered {
		step := steps.OwnManual(r.run, r.stateDir, m, raise)
		var err error
		if step.After, step.Waits, step.Before, err = r.place(m.After, m.Before, names); err != nil {
			reason := err.Error()
			step.After, step.Waits, step.Before, step.Apply = nil, nil, nil, nil
			step.Check = func(context.Context) check.Result { return check.Result{State: check.Failed, Reason: reason} }
		}
		out = append(out, step)
	}
	return out, nil
}

// placeScript places s among names, the run's steps, as place does, and
// returns the waits for the things it's applied after.
func (r *run) placeScript(s *steps.Script, names map[string]bool) ([]engine.Wait, error) {
	after, waits, before, err := r.place(s.After, s.Before, names)
	s.After, s.Before = after, before
	return waits, err
}

// place checks what a line's step comes after and before are among names,
// the run's steps, or kinds this Mac doesn't use, which it's then placed
// without; it returns the steps it comes after, a wait for each of a
// kind's things it comes after, as in cask:1password, and the steps it
// comes before. An error says what isn't a step or a kind.
func (r *run) place(after, before []string, names map[string]bool) (steps []string, waits []engine.Wait, ahead []string, err error) {
	var wrong []string
	for _, a := range after {
		kindName, thing, isThing := strings.Cut(a, ":")
		switch {
		case isThing && names[kindName]:
			waits = append(waits, engine.Wait{Step: kindName, Thing: thing, Ready: r.installed(kindName, thing)})
		case names[a]:
			steps = append(steps, a)
		case !slices.Contains(r.allKinds, kindName):
			wrong = append(wrong, "it comes after "+a+", which isn't a step")
		}
	}
	for _, b := range before {
		switch {
		case names[b]:
			ahead = append(ahead, b)
		case !slices.Contains(r.allKinds, b):
			wrong = append(wrong, "it comes before "+b+", which isn't a step")
		}
	}
	if len(wrong) > 0 {
		return steps, waits, ahead, errors.New(strings.Join(wrong, "; "))
	}
	return steps, waits, ahead, nil
}

// installed reports whether the kind named kind has thing installed, by its
// full name, or a tap's thing by its own, as Homebrew lists a tap's cask.
func (r *run) installed(kindName, thing string) func(context.Context) bool {
	short := thing[strings.LastIndex(thing, "/")+1:]
	return func(ctx context.Context) bool {
		have, err := r.kindsByName[kindName].Installed(ctx)
		if err != nil {
			return false
		}
		return slices.ContainsFunc(have, func(in kind.Installed) bool {
			return in.Name == thing || in.Name[strings.LastIndex(in.Name, "/")+1:] == short
		})
	}
}

// stepOptions are kit step add's options.
type stepOptions struct {
	after, before []string
	sudo          bool
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
check (exit 0: done; 1: not done; 3: can't be done yet; its first line saying
how it stands) and, when it isn't done, as run apply, then checks again.
--after names a step it's applied after, or one of a step's things, as in
cask:1password; --before a step applied after it; each as often as wanted.
--sudo says applying needs an administrator's password.

The script starts as a template following that contract: write its check and
apply.` + declaredWhere,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, stepCommand+" add", func(ctx context.Context, r *run) error {
				return a.addStep(ctx, r, args[0], args[1], opts)
			})
		},
	}
	add.Flags().StringArrayVar(&opts.after, "after", nil, "a step it's applied after, or one of a step's things, as in cask:1password")
	add.Flags().StringArrayVar(&opts.before, "before", nil, "a step applied after it")
	add.Flags().BoolVar(&opts.sudo, "sudo", false, "applying it needs an administrator's password")
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
	for _, before := range opts.before {
		words = append(words, "--before", config.Quote(before))
	}
	if opts.sudo {
		words = append(words, "--sudo")
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
		if _, err := r.placeScript(&s, names); err != nil {
			return fail(err)
		}
		if _, err := os.Stat(s.Dir); !errors.Is(err, fs.ErrNotExist) {
			return fail(fmt.Errorf("%s is there already: remove it, or name the step otherwise", s.Folder))
		}
		if err := r.cfg.Declare(config.StepsKind, scope, entry); err != nil {
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
