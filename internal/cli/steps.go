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
	"sync"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/runner"
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
		if err == nil {
			s.Needed, s.Env, err = r.needing(s.Secrets)
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

// manualSteps are the steps by hand a step comes after (Ordered), each a
// step of its own among others, the run's other steps: one placed after or
// before what isn't a step fails, saying so; one named as another step is
// can't be told apart from it, and is refused.
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

// cardSteps are the steps by hand the run's kinds need done before their
// things (kind.Carded), as a secrets store's sign-in, each a step of its
// own, raised with raise at a terminal: once found not done, it waits for
// the program it needs on kit's PATH and the app it needs installed in
// folders, which kit opens as its card first comes up. Its kind's step,
// among kindSteps, waits for it partway through applying (Later), not
// before it starts: one store's sign-in holds back that store's secrets
// alone.
func (r *run) cardSteps(raise *steps.Raise, folders *apps.Folders, kindSteps []engine.Step) []engine.Step {
	var out []engine.Step
	for i, ks := range kindSteps {
		carded, ok := r.kindsByName[ks.Name].(kind.Carded)
		if !ok {
			continue
		}
		for _, c := range carded.Cards() {
			m := steps.ManualStep{Name: c.Name, Do: c.Title, Why: c.Why, Steps: c.Steps, Done: c.Done, Open: c.App}
			if c.Program != "" {
				m.Await = append(m.Await, engine.Wait{Thing: c.Program, Ready: func(context.Context) bool { return runner.Has(r.run, c.Program) }})
			}
			if c.App != "" {
				m.Await = append(m.Await, engine.Wait{Thing: c.AppName, Ready: func(context.Context) bool { return folders.Has(c.App) }})
			}
			kindSteps[i].Later = append(kindSteps[i].Later, engine.Wait{Step: c.Name})
			out = append(out, steps.OwnManual(r.run, r.stateDir, m, raise))
		}
	}
	return out
}

// appFolders are the folders apps are installed in: kit's own
// (Deps.AppDirs), and the Applications folder in home.
func (a *app) appFolders(home string) *apps.Folders {
	return &apps.Folders{Dirs: append(slices.Clone(a.AppDirs), filepath.Join(home, "Applications"))}
}

// placeByHand places the steps by hand with no place of their own among
// all, the run's steps, when they're raised at a terminal: applied once
// every other step has finished, however it stands, as what's done by hand
// comes last, saying so while it waits; but for the steps that come after
// them, which they can't wait for.
func (r *run) placeByHand(all []engine.Step) {
	i := slices.IndexFunc(all, func(s engine.Step) bool { return s.Name == steps.ManualName })
	if !r.byHandLast || i < 0 {
		return
	}
	all[i].Waiting = "at the end, once the rest is done"
	later := following(all, steps.ManualName)
	for _, s := range all {
		if !later[s.Name] && !slices.Contains(all[i].After, s.Name) {
			all[i].After = append(all[i].After, s.Name)
		}
	}
}

// following are the steps of all that come after the one called name, and
// it: needing it, applied after it, or placed after it by its Before, or
// after one of those.
func following(all []engine.Step, name string) map[string]bool {
	next := map[string][]string{}
	for _, s := range all {
		for _, n := range slices.Concat(s.Needs, s.After) {
			next[n] = append(next[n], s.Name)
		}
		next[s.Name] = append(next[s.Name], s.Before...)
	}
	later := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		if later[name] {
			return
		}
		later[name] = true
		for _, n := range next[name] {
			visit(n)
		}
	}
	visit(name)
	return later
}

// placeScript places s among names, the run's steps, as place does, and
// returns the waits for the things it's applied after.
func (r *run) placeScript(s *steps.Script, names map[string]bool) ([]engine.Wait, error) {
	after, waits, before, err := r.place(s.After, s.Before, names)
	s.After, s.Before = after, before
	return waits, err
}

// place checks what a line's step comes after and before are among names,
// the run's steps, or kinds this Mac doesn't use and the steps they need,
// which it's then placed without; it returns the steps it comes after, a
// wait for each of a kind's things it comes after, as in cask:firefox,
// and the steps it comes before. An error says what isn't a step or a kind.
func (r *run) place(after, before []string, names map[string]bool) (steps []string, waits []engine.Wait, ahead []string, err error) {
	var wrong []string
	for _, a := range after {
		kindName, thing, isThing := strings.Cut(a, ":")
		switch {
		case isThing && names[kindName] && r.kindsByName[kindName] != nil:
			waits = append(waits, engine.Wait{Step: kindName, Thing: thing, Ready: r.installed(kindName, thing)})
		case names[a]:
			steps = append(steps, a)
		case !ofKinds(kindName):
			wrong = append(wrong, "it comes after "+a+", which isn't a step")
		}
	}
	for _, b := range before {
		switch {
		case names[b]:
			ahead = append(ahead, b)
		case !ofKinds(b):
			wrong = append(wrong, "it comes before "+b+", which isn't a step")
		}
	}
	if len(wrong) > 0 {
		return steps, waits, ahead, errors.New(strings.Join(wrong, "; "))
	}
	return steps, waits, ahead, nil
}

// ofKinds reports whether the step called name is a kind's, or one a kind
// needs, whether or not this run has it.
func ofKinds(name string) bool {
	_, ok := kinds.Of(name)
	return ok
}

// placeFeatures places the steps of each feature whose section says what it
// waits for, as [features --after cask:app] does, among all, the run's
// steps, featured saying which feature each step is for: each comes after
// the steps named, and waits for the things, checking once they're in, as a
// step of your own does. One placed after what isn't a step fails, saying
// so.
func (r *run) placeFeatures(all []engine.Step, on featuresOn, featured map[string]string) {
	names := make(map[string]bool, len(all))
	for _, s := range all {
		names[s.Name] = true
	}
	for i, s := range all {
		e, ok := on[featured[s.Name]]
		if !ok || e.Hold == "" {
			continue
		}
		after, waits, _, err := r.place(e.After(), nil, names)
		if err != nil {
			broken(&all[i], fmt.Sprintf("%s: [%s]: %s", e.Path(), e.Section, err))
			continue
		}
		all[i].After = append(all[i].After, after...)
		all[i].Waits = append(all[i].Waits, waits...)
	}
}

// broken has step fail at once, saying reason, as its check, waiting for
// and applying nothing.
func broken(step *engine.Step, reason string) {
	step.Waits, step.Apply = nil, nil
	step.Check = func(context.Context) check.Result { return check.Result{State: check.Failed, Reason: reason} }
}

// placeLines places each kind's lines among all, the run's steps: its
// sections held back (heldSections), each waited for partway through
// applying; the lines a step or a section held back names, as in --after
// cask:firefox, to install first; and, for a kind taking turns with
// others (kind.Sharing), their first, for the rest of its lines to wait
// for. A section placed after what isn't a step, or needing a secret not
// declared, fails its kind's step, saying so.
func (r *run) placeLines(all []engine.Step) {
	names := make(map[string]bool, len(all))
	named := map[string][]string{}
	for _, s := range all {
		names[s.Name] = true
		for _, w := range s.Waits {
			named[w.Step] = append(named[w.Step], w.Thing)
		}
	}
	for i, s := range all {
		if !slices.Contains(r.kinds, s.Name) {
			continue
		}
		held, things, err := r.heldSections(r.lists[s.Name], names)
		if err != nil {
			broken(&all[i], err.Error())
			continue
		}
		r.placing[s.Name] = kind.Placing{Held: held}
		for _, h := range held {
			all[i].Later = append(all[i].Later, h.Waits...)
		}
		for _, w := range things {
			named[w.Step] = append(named[w.Step], w.Thing)
		}
	}
	for name, p := range r.placing {
		p.First = firstOf(r.lists[name], named[name])
		r.placing[name] = p
	}
	// What a kind waits for behind another stays out of its step's Later,
	// unlike its sections' waits: there each kind's lead-up would take in
	// the other's, and a step waiting for a thing of one would free from it
	// what comes after the other, as secrets from their store's sign-in.
	for name, p := range r.placing {
		sharing, ok := r.kindsByName[name].(kind.Sharing)
		if !ok {
			continue
		}
		for _, other := range sharing.Shares() {
			for _, thing := range r.placing[other].First {
				p.Behind = append(p.Behind, engine.Wait{Step: other, Thing: thing, Ready: r.behind(other, thing)})
			}
		}
		r.placing[name] = p
	}
}

// firstOf are list's lines that things name, by full name or a tap's thing
// by its own, as installed finds them: those installed first, in order. A
// line held back isn't one: it keeps to its section.
func firstOf(list config.List, things []string) []string {
	var first []string
	for _, e := range list.Entries {
		if e.Hold == "" && slices.ContainsFunc(things, func(thing string) bool { return sameThing(e.Name, thing) }) {
			first = append(first, e.Name)
		}
	}
	return first
}

// heldSections are list's sections holding their lines back, in order, each
// placed among names, the run's steps: a step it comes after is waited for
// till it's finished, one of a step's things till it's in, then each secret
// it needs. things are the steps' things they come after.
func (r *run) heldSections(list config.List, names map[string]bool) (held []kind.Held, things []engine.Wait, err error) {
	for _, e := range list.Entries {
		if e.Hold == "" || slices.ContainsFunc(held, func(h kind.Held) bool { return h.Hold == e.Hold }) {
			continue
		}
		after, comesAfter, _, err := r.place(e.After(), nil, names)
		var secrets []engine.Wait
		var env func() ([]string, error)
		if err == nil {
			secrets, env, err = r.needing(e.Secrets())
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%s: [%s]: %w", e.Path(), e.Section, err)
		}
		var waits []engine.Wait
		for _, step := range after {
			waits = append(waits, engine.Wait{Step: step})
		}
		held = append(held, kind.Held{Hold: e.Hold, Waits: slices.Concat(waits, comesAfter, secrets), Env: env})
		things = append(things, comesAfter...)
	}
	return held, things, nil
}

// needing is what needing the secrets named takes: a wait for each, till
// it's in place, and the environment of the commands that need them, read
// as each starts, each that's a variable set to its value as kit's secrets
// hold it (~/.secrets.zsh, as kit wrote it, syncing, in this run or
// before), never the environment kit inherited. Each must be declared.
func (r *run) needing(names []string) ([]engine.Wait, func() ([]string, error), error) {
	if len(names) == 0 {
		return nil, nil, nil
	}
	secrets, ok := r.kindsByName[secretKind].(*secret.Secrets)
	if !ok {
		return nil, nil, errors.New("kit has no secrets")
	}
	declared := r.lists[secretKind].Names()
	var waits []engine.Wait
	var variables, undeclared []string
	for _, name := range names {
		if !slices.Contains(declared, name) {
			undeclared = append(undeclared, name)
			continue
		}
		waits = append(waits, engine.Wait{Step: secretKind, Thing: name, Ready: func(ctx context.Context) bool { return secrets.Has(ctx, name) }})
		if !(secret.Secret{Name: name}).IsFile() {
			variables = append(variables, name)
		}
	}
	switch {
	case len(undeclared) == 1:
		return nil, nil, fmt.Errorf("it needs %s, which no [secrets] section declares", undeclared[0])
	case len(undeclared) > 1:
		return nil, nil, fmt.Errorf("it needs %s, which no [secrets] section declares", strings.Join(undeclared, " and "))
	}
	env := func() ([]string, error) {
		var env []string
		for _, name := range variables {
			value, ok, err := secrets.Variable(name)
			if err != nil {
				return nil, err
			}
			if ok {
				env = append(env, name+"="+value)
			}
		}
		return env, nil
	}
	return waits, env, nil
}

// installed reports whether the kind named kind has thing installed, by its
// full name, or a tap's thing by its own, as Homebrew lists a tap's cask.
func (r *run) installed(kindName, thing string) func(context.Context) bool {
	return func(ctx context.Context) bool {
		have, err := kind.InstalledOf(ctx, r.kindsByName[kindName], []string{thing})
		if err != nil {
			return false
		}
		return slices.ContainsFunc(have, func(in kind.Installed) bool { return sameThing(in.Name, thing) })
	}
}

// behind is whether what waits behind thing, a thing of the kind called
// kindName, as a kind taking turns with it does, has waited enough: once
// it's installed, or the kind's done with it for now (settled), as when it
// failed, or is put off till kit has the administrator's password, which
// nothing waits for for order's sake.
func (r *run) behind(kindName, thing string) func(context.Context) bool {
	installed := r.installed(kindName, thing)
	return func(ctx context.Context) bool {
		return r.settled.has(kindName, thing) || installed(ctx)
	}
}

// settled are the things the kinds are done with for now, by kind (Placing's
// Settled).
type settled struct {
	mu     sync.Mutex
	things map[string][]string
}

// note notes things of the kind called kindName settled.
func (s *settled) note(kindName string, things []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.things == nil {
		s.things = map[string][]string{}
	}
	s.things[kindName] = append(s.things[kindName], things...)
}

// has reports whether thing, of the kind called kindName, is settled.
func (s *settled) has(kindName, thing string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.things[kindName], func(name string) bool { return sameThing(name, thing) })
}

// sameThing reports whether name is thing, by its full name, or a tap's
// thing by its own.
func sameThing(name, thing string) bool {
	return name == thing || name[strings.LastIndex(name, "/")+1:] == thing[strings.LastIndex(thing, "/")+1:]
}

// stepOptions are kit step add's options.
type stepOptions struct {
	after, before, secrets []string
	sudo                   bool
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
cask:firefox; --before a step applied after it; --secret a secret it needs,
declared in a secrets section, which it's applied once it's in place, its
script run with it set when it's a variable; each as often as wanted. --sudo
says applying needs an administrator's password.

The script starts as a template following that contract: write its check and
apply.` + declaredWhere,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.prepared(cmd, stepCommand+" add", func(ctx context.Context, r *run) error {
				return a.addStep(ctx, r, args[0], args[1], opts)
			})
		},
	}
	add.Flags().StringArrayVar(&opts.after, "after", nil, "a step it's applied after, or one of a step's things, as in cask:firefox")
	add.Flags().StringArrayVar(&opts.before, "before", nil, "a step applied after it")
	add.Flags().StringArrayVar(&opts.secrets, "secret", nil, "a secret it needs, by what it fills, as its secrets section declares it")
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
	for _, name := range opts.secrets {
		words = append(words, "--secret", config.Quote(name))
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
		if _, _, err := r.needing(s.Secrets); err != nil {
			return fail(err)
		}
		if _, err := os.Stat(s.Dir); !errors.Is(err, fs.ErrNotExist) {
			return fail(fmt.Errorf("%s is there already: remove it, or name the step otherwise", s.Folder))
		}
		moved, err := c.declare(config.StepsKind, scope, entry)
		if err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(s.Dir, 0o755); err != nil {
			return fail(err)
		}
		script := filepath.Join(s.Dir, steps.ScriptName)
		if err := os.WriteFile(script, []byte(steps.Template(name, does)), 0o755); err != nil {
			return fail(err)
		}
		c.changed(s.Folder)
		return check.Result{State: check.OK, Summary: "declared in " + scope + outOf(moved) + "; its script, " + s.Folder + "/" + steps.ScriptName + ", is a template: write its check and apply"}
	})
	c.sync(ctx, commitMessage("add", stepCommand, []string{name}, r.machine, opts.note))
	return c.finish()
}
