// Package kind is kinds: lists of things of one sort, such as Homebrew's
// formulae, compared with what's on the Mac. A kind says what's installed,
// and the full name of a thing known by another; the comparison, and the
// step it makes, are the same for every kind.
package kind

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/redact"
	"github.com/leeovery/kit/internal/runner"
)

// Installed is a thing a kind finds on the Mac.
type Installed struct {
	// Name is its full name, as in owner/tap/tool.
	Name string
	// Explicit is whether it was installed for itself, rather than only as
	// another's dependency.
	Explicit bool
	// Needed is whether something else installed needs it.
	Needed bool
}

// Kind is a list of things of one sort.
type Kind interface {
	// Name names the kind, its items and its step, as in brew; the config
	// declares it in the section that names it, as in [homebrew formulae].
	Name() string
	// Program is the program the kind runs to find what's installed, as in
	// brew: while it isn't installed, there's nothing of the kind to find.
	Program() string
	// Installed lists what's on the Mac.
	Installed(ctx context.Context) ([]Installed, error)
	// Resolve finds the full names of things known by other names, such as
	// aliases and old names. A name the kind doesn't know at all is left
	// out.
	Resolve(ctx context.Context, names []string) (map[string]string, error)
	// Install installs names.
	Install(ctx context.Context, names []string) error
	// Remove uninstalls names.
	Remove(ctx context.Context, names []string) error
}

// labels name the kinds' rows, each as the config's section declaring it is
// named, cased: [homebrew formulae]'s row is Homebrew formulae. tmux's
// plugins are declared in tmux's own config.
var labels = map[string]string{
	"brew":                "Homebrew formulae",
	"cask":                "Homebrew casks",
	"mas":                 "App Store apps",
	"npm":                 "npm packages",
	"composer":            "Composer packages",
	"go":                  "Go tools",
	"gh":                  "GitHub extensions",
	"tmux":                "tmux plugins",
	"claude-mcp":          "Claude MCP",
	"login-item":          "macOS login items",
	"defaults":            "macOS settings",
	"power":               "Power settings",
	"git-config":          "git config",
	"backup-exclusion":    "Backup exclusions",
	"spotlight-exclusion": "Spotlight exclusions",
	"secret":              "Secrets",
}

// Label is the name of the row of the kind named name, as in Homebrew
// formulae for brew.
func Label(name string) string { return cmp.Or(labels[name], name) }

// Blocker is a kind that can say why some things can't be installed now.
type Blocker interface {
	// Blocked says, of the things missing (declared names, by their full
	// names), why each that can't be installed now can't, by declared name.
	Blocked(ctx context.Context, missing map[string]string, installed []Installed) (map[string]string, error)
}

// Admin is a kind some of whose installs need an administrator's password,
// which the command settles before anything is installed, so none asks
// mid-run.
type Admin interface {
	// NeedsAdmin finds which of names, to install, need the password.
	NeedsAdmin(ctx context.Context, names []string) ([]string, error)
	// SetAdmin is how the kind finds out whether the password is at hand:
	// while it's unset, kit isn't installing, and nothing's held up for it.
	SetAdmin(held func(ctx context.Context) bool)
}

// Dependents is a kind that knows what installed needs a thing.
type Dependents interface {
	// NeededBy lists what's installed that needs name.
	NeededBy(ctx context.Context, name string) ([]string, error)
}

// Declarer is a kind declared in a file of its own, where the program
// that uses the declarations reads them: tmux's plugins, in tmux's config.
// kit reads the file, and edits it in place.
type Declarer interface {
	// Declared is what the kind's file declares.
	Declared() (config.List, error)
	// Declare adds name to the kind's file, and Undeclare takes it out:
	// each says which file it changed, by its real path, links followed.
	Declare(name string) (string, error)
	Undeclare(name string) (string, error)
}

// Valued is a kind whose declarations carry a value after each name: an MCP
// server's definition, as claude mcp add's options.
type Valued interface {
	// Values reads what list declares, values and all: the list as it
	// compares, the entries declared off marked so. A value that doesn't
	// read is refused.
	Values(list config.List) (config.List, error)
	// Value is the value to declare name with, as it's installed.
	Value(ctx context.Context, name string) (string, error)
}

// Describer is a kind that says what a thing is, for the note kit writes
// beside it when it declares it: a login item's app's name.
type Describer interface {
	// Describe says what name is: "" when it can't.
	Describe(ctx context.Context, name string) string
}

// Differ is a kind whose things can be installed otherwise than they're
// declared: an MCP server whose definition has changed.
type Differ interface {
	// Differs says, of the declared names installed, how each installed
	// otherwise than declared differs, by declared name.
	Differs(ctx context.Context, names []string) (map[string]string, error)
}

// Diverger is a Differ some of whose things installed otherwise than
// declared were changed on the Mac on purpose, as a setting kit has seen as
// declared is: those applying leaves, and kit reconcile adopts the Mac's or
// puts the declared back.
type Diverger interface {
	Differ
	// Diverges reports whether name, installed otherwise than declared, was
	// changed on the Mac on purpose.
	Diverges(name string) bool
}

// Expander is a kind whose declarations are patterns, each standing for what
// matches it on the Mac now, as the backup exclusions' globs do.
type Expander interface {
	// Expand is list with each pattern's entry replaced by an entry for
	// each match, keeping its place, group and note; a name that isn't a
	// pattern stands for itself.
	Expand(list config.List) (config.List, error)
}

// Unattended is a kind some of whose things kit's scheduled run may install
// by itself, with no one there: cheap, safe, never removing anything.
type Unattended interface {
	// Unattended finds which of names, to install, the scheduled run may.
	Unattended(names []string) []string
}

// Verber is a kind whose things aren't installed but set, or excluded:
// its summaries say so.
type Verber interface {
	// Verb is what a thing on the Mac as declared is, as in set.
	Verb() string
}

// Reverter is a kind whose things found on the Mac and not declared were
// changed there from a value kit knows, as a watched setting is: kit
// reconcile can put the value back.
type Reverter interface {
	// Revert puts name back as it was.
	Revert(ctx context.Context, name string) error
}

// Keyed is a kind whose things match by part of their names: an App Store
// app by its id, an npm package without its version.
type Keyed interface {
	// Key is the part of name things match by.
	Key(name string) string
}

// Finder is a kind that finds what a person means by what they type, as an
// App Store app from its name, before it's installed and declared.
type Finder interface {
	// Find finds what typed means: one thing, or several to choose from.
	Find(ctx context.Context, typed string) ([]Found, error)
}

// Found is a thing a Finder found: the name to declare it by, and a label
// saying what it is, to choose by.
type Found struct {
	Name  string
	Label string
}

// Counter is a kind that can tell, in a line its install command printed,
// that one of the things it was asked to install is installed: the step
// counts them as they're done, saying which it's installing next and how
// far it's got. Its Install gives its install command's lines to
// Lines(ctx).
type Counter interface {
	// Done is which of names line says is installed: none when it says
	// nothing of them, as of a dependency pulled in.
	Done(line string, names []string) (string, bool)
}

// Install is the action that installs an item.
const Install = "install"

// The states of a kind's items.
const (
	Missing          = "missing"
	Extra            = "extra"
	UnusedDependency = "unused-dependency"
	// Changed is a thing installed otherwise than declared: applying
	// installs it again, as declared.
	Changed = "changed"
	// Diverged is a Diverger's thing installed otherwise than declared:
	// applying leaves it.
	Diverged = "diverged"
)

// Step is the step that checks k, what declared lists against what's on
// the Mac, and applies it: installing what's missing, as it can. It needs
// the steps named in needs. An install that fails has failed for what's
// still missing after it, named.
func Step(k Kind, declared config.List, needs ...string) engine.Step {
	return Holding(k, declared, nil, needs...)
}

// Held is a section of a kind's lines held back, as [homebrew formulae
// --secret NAME] declares them: installed apart from the rest, as one
// command, once what they wait for has come, in an environment of their
// own.
type Held struct {
	// Hold is what the section holds its lines back for, as its entries
	// say (config.Entry's Hold).
	Hold string
	// Waits are what its lines wait for, one at a time, each among the
	// step's Later.
	Waits []engine.Wait
	// Env is the environment their install runs in, read as it starts: a
	// variable for each secret they need, as in NAME=value.
	Env func() ([]string, error)
}

// Holding is the step Step is, k's lines in the sections held, as the run
// places them once its steps are known, installed apart (InstallDeclared). A
// section whose wait ends without what it waited for isn't installed: its
// lines stay missing, saying what isn't there, as a Blocker's do.
func Holding(k Kind, declared config.List, held func() []Held, needs ...string) engine.Step {
	var unmet deferred
	return engine.Step{
		Name:  k.Name(),
		Title: Label(k.Name()),
		Needs: needs,
		Check: func(ctx context.Context) check.Result { return unmet.mark(Compare(ctx, k, declared), declared) },
		Apply: func(ctx context.Context, found check.Result) error {
			var sections []Held
			if held != nil {
				sections = held()
			}
			why, err := installApart(ctx, k, declared, sections, toInstall(found))
			unmet.add(why)
			return err
		},
	}
}

// InstallDeclared installs names, k's things as declared lists them: those
// in no section held at once, then each section held's, once what it waits
// for has come (engine.Await: outside a run, once it's in place), as one
// command in its own environment. A section whose wait ended without what
// it waited for isn't installed, saying what isn't there.
func InstallDeclared(ctx context.Context, k Kind, declared config.List, held []Held, names []string) error {
	why, err := installApart(ctx, k, declared, held, names)
	var errs []string
	if err != nil {
		errs = append(errs, err.Error())
	}
	for _, h := range held {
		if reason, ok := why[h.Hold]; ok {
			errs = append(errs, reason)
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// installApart installs names as InstallDeclared does, returning why each
// section held whose wait ended without what it waited for was left, by
// hold.
func installApart(ctx context.Context, k Kind, declared config.List, held []Held, names []string) (map[string]string, error) {
	plain, apart := heldApart(names, declared, held)
	var errs []string
	if err := install(ctx, k, declared, plain, nil); err != nil {
		errs = append(errs, err.Error())
	}
	unmet := map[string]string{}
	for _, h := range held {
		why, err := installHeld(ctx, k, declared, h, apart[h.Hold])
		switch {
		case ctx.Err() != nil:
			return unmet, ctx.Err()
		case err != nil:
			errs = append(errs, err.Error())
		case why != "":
			unmet[h.Hold] = why
		}
	}
	if len(errs) > 0 {
		return unmet, errors.New(strings.Join(errs, "; "))
	}
	return unmet, nil
}

// heldApart splits names, to install, into those in no section held, and
// those in each section held, by its hold.
func heldApart(names []string, declared config.List, held []Held) ([]string, map[string][]string) {
	holds := make(map[string]bool, len(held))
	for _, h := range held {
		holds[h.Hold] = true
	}
	holdOf := make(map[string]string, len(declared.Entries))
	for _, e := range declared.Entries {
		if holds[e.Hold] {
			holdOf[e.Name] = e.Hold
		}
	}
	var plain []string
	apart := map[string][]string{}
	for _, name := range names {
		if hold := holdOf[name]; hold != "" {
			apart[hold] = append(apart[hold], name)
		} else {
			plain = append(plain, name)
		}
	}
	return plain, apart
}

// installHeld installs names, the lines of section h, once what it waits
// for has come, in its environment: when a wait ends without it, they're
// left, and why says what isn't there.
func installHeld(ctx context.Context, k Kind, declared config.List, h Held, names []string) (why string, err error) {
	if len(names) == 0 {
		return "", nil
	}
	var missing []string
	for _, w := range h.Waits {
		what := cmp.Or(w.Thing, Label(w.Step))
		if !engine.Await(ctx, w, fmt.Sprintf("waiting for %s: %d to install", what, len(names))) {
			missing = append(missing, what)
		}
	}
	if len(missing) > 0 {
		return engine.NotThere(missing), nil
	}
	var env []string
	if h.Env != nil {
		if env, err = h.Env(); err != nil {
			return "", fmt.Errorf("couldn't install %s: %w", strings.Join(names, ", "), err)
		}
	}
	return "", install(ctx, k, declared, names, env)
}

// install installs names of k, with env set for the install alone: one
// that fails has failed for what's still missing after it, named, the
// values env sets hidden.
func install(ctx context.Context, k Kind, declared config.List, names, env []string) error {
	if len(names) == 0 {
		return nil
	}
	installing := runner.WithSecretEnv(ctx, env...)
	err := k.Install(counting(installing, k, names), names)
	if err == nil {
		return nil
	}
	// An install can fail having installed all it was given: a formula
	// Homebrew installs but can't link, as another's linked in its place, is
	// installed. What's still missing is what failed.
	left := names
	if again := Compare(event.WithChecking(ctx), k, declared); again.State != check.Failed && again.State != check.Deferred {
		left = slices.DeleteFunc(toInstall(again), func(name string) bool { return !slices.Contains(names, name) })
	}
	switch {
	case len(left) == 0:
		return nil
	case len(left) > 3:
		err = fmt.Errorf("couldn't install %d of %d: %w", len(left), len(names), err)
	default:
		err = fmt.Errorf("couldn't install %s: %w", strings.Join(left, ", "), err)
	}
	if values := runner.SecretValues(installing); len(values) > 0 {
		return errors.New(redact.Text(err.Error(), values...))
	}
	return err
}

// deferred are the sections of a step's lines held back whose waits ended
// without what they waited for, by hold, each with why.
type deferred struct {
	mu  sync.Mutex
	why map[string]string
}

// add notes why each section held, by hold, was left.
func (d *deferred) add(why map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.why == nil {
		d.why = map[string]string{}
	}
	maps.Copy(d.why, why)
}

// mark has res's missing items in sections deferred say why, as a kind's
// Blocked does, with nothing to do about them.
func (d *deferred) mark(res check.Result, declared config.List) check.Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.why) == 0 {
		return res
	}
	holdOf := make(map[string]string, len(declared.Entries))
	for _, e := range declared.Entries {
		holdOf[e.Name] = e.Hold
	}
	for i, it := range res.Items {
		if why, ok := d.why[holdOf[it.Name]]; ok && it.Action == Install && it.State == Missing {
			res.Items[i].Detail, res.Items[i].Action = why, ""
		}
	}
	return res
}

// toInstall are the things found that applying installs.
func toInstall(found check.Result) []string {
	var names []string
	for _, it := range found.Items {
		if it.Action == Install {
			names = append(names, it.Name)
		}
	}
	return names
}

type linesKey struct{}

// Lines is what an install command's lines go to, as runner.Command's Lines,
// in ctx: nil when nothing counts them.
func Lines(ctx context.Context) func(line string) {
	lines, _ := ctx.Value(linesKey{}).(func(string))
	return lines
}

// counting is ctx for installing names, when k is a Counter: as each is
// done, by the lines its install prints, the step says it's installing the
// next, and how many of names are done.
func counting(ctx context.Context, k Kind, names []string) context.Context {
	c, ok := k.(Counter)
	if !ok {
		return ctx
	}
	var mu sync.Mutex
	done := make(map[string]bool, len(names))
	say := func() {
		next := ""
		if i := slices.IndexFunc(names, func(name string) bool { return !done[name] }); i >= 0 {
			next = " " + names[i]
		}
		event.Say(ctx, event.Doing{Says: "installing" + next, Done: len(done), Of: len(names)})
	}
	say()
	return context.WithValue(ctx, linesKey{}, func(line string) {
		name, ok := c.Done(line, names)
		mu.Lock()
		defer mu.Unlock()
		if !ok || done[name] || !slices.Contains(names, name) {
			return
		}
		done[name] = true
		say()
	})
}

// UnattendedApply is k's step's apply as the scheduled run takes it:
// installing only the things missing that k says it may, alone; nil when k
// lets it install none.
func UnattendedApply(k Kind) func(ctx context.Context, found check.Result) error {
	u, ok := k.(Unattended)
	if !ok {
		return nil
	}
	return func(ctx context.Context, found check.Result) error {
		var names []string
		for _, it := range found.Items {
			if it.Action == Install {
				names = append(names, it.Name)
			}
		}
		if names = u.Unattended(names); len(names) == 0 {
			return nil
		}
		return k.Install(ctx, names)
	}
}

// Compare checks what declared lists against what k finds installed: a
// declared thing is ok installed for any reason, or missing; an undeclared
// one installed for itself, and needed by nothing, is extra; one installed
// only as a dependency, and needed by nothing now, is an unused dependency.
// A thing declared off is neither missing nor extra. A kind that can tell
// finds things installed otherwise than declared. While k's program isn't
// installed, nothing declared is fine, and anything declared defers the
// step.
func Compare(ctx context.Context, k Kind, declared config.List) check.Result {
	installed, err := k.Installed(ctx)
	switch {
	case errors.Is(err, runner.ErrNotFound) && len(declared.Entries) == 0:
		return check.Result{State: check.OK, Summary: "none declared; " + k.Program() + " isn't installed"}
	case errors.Is(err, runner.ErrNotFound):
		return check.Result{State: check.Deferred, Reason: "needs " + k.Program() + ", which isn't installed"}
	case err != nil:
		return check.Result{State: check.Failed, Reason: err.Error()}
	}
	key := func(name string) string { return name }
	if kd, ok := k.(Keyed); ok {
		key = kd.Key
	}
	byKey := make(map[string]Installed, len(installed))
	for _, it := range installed {
		byKey[key(it.Name)] = it
	}
	// matched are the installed things declared, by name, and installedDeclared
	// the declared names of those declared on.
	matched := make(map[string]bool)
	var unmatched, installedDeclared []string
	off := 0
	for _, e := range declared.Entries {
		it, ok := byKey[key(e.Name)]
		switch {
		case e.Off:
			off++
			if ok {
				matched[it.Name] = true
			}
		case ok:
			matched[it.Name] = true
			installedDeclared = append(installedDeclared, e.Name)
		default:
			unmatched = append(unmatched, e.Name)
		}
	}
	var missing, unknown []string
	fullNames := make(map[string]string)
	if len(unmatched) > 0 {
		resolved, err := k.Resolve(ctx, unmatched)
		if err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
		for _, name := range unmatched {
			full, known := resolved[name]
			switch {
			case !known:
				unknown = append(unknown, name)
			case byKey[key(full)].Name != "":
				matched[byKey[key(full)].Name] = true
			default:
				missing = append(missing, name)
				fullNames[name] = full
			}
		}
	}
	blocked := map[string]string{}
	if b, ok := k.(Blocker); ok && len(missing) > 0 {
		var err error
		if blocked, err = b.Blocked(ctx, fullNames, installed); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
	}
	var extra, unused []string
	for _, it := range installed {
		if matched[it.Name] || it.Needed {
			continue
		}
		if it.Explicit {
			extra = append(extra, it.Name)
		} else {
			unused = append(unused, it.Name)
		}
	}

	changed := map[string]string{}
	if d, ok := k.(Differ); ok && len(installedDeclared) > 0 {
		var err error
		if changed, err = d.Differs(ctx, installedDeclared); err != nil {
			return check.Result{State: check.Failed, Reason: err.Error()}
		}
	}

	verb := "installed"
	if v, ok := k.(Verber); ok {
		verb = v.Verb()
	}
	declaredCount := len(declared.Entries) - off
	missingCount := len(missing) + len(unknown)
	res := check.Result{
		State:   check.OK,
		Summary: fmt.Sprintf("%d declared, all %s", declaredCount, verb),
		Counts: map[string]int{
			"declared": declaredCount, "installed": declaredCount - missingCount,
			"missing": missingCount, "extra": len(extra), "unused_dependencies": len(unused),
		},
	}
	if len(changed) > 0 {
		res.Counts["changed"] = len(changed)
	}
	if off > 0 {
		res.Counts["off"] = off
	}
	if missingCount > 0 {
		res.Summary = fmt.Sprintf("%d declared, %d %s", declaredCount, declaredCount-missingCount, verb)
	}
	if declaredCount == 0 {
		res.Summary = "none declared"
	}
	if len(changed) > 0 {
		res.Summary += fmt.Sprintf("; %d changed", len(changed))
	}
	if off > 0 {
		res.Summary += fmt.Sprintf("; %d off", off)
	}
	missingItems := items(k, missing, Missing, "")
	for i, it := range missingItems {
		if reason, ok := blocked[it.Name]; ok {
			missingItems[i].Detail = reason
		} else {
			missingItems[i].Action = Install
		}
	}
	var changedNames []string
	for name := range changed {
		changedNames = append(changedNames, name)
	}
	changedItems := items(k, changedNames, Changed, "")
	for i, it := range changedItems {
		changedItems[i].Detail, changedItems[i].Action = changed[it.Name], Install
		if d, ok := k.(Diverger); ok && d.Diverges(it.Name) {
			changedItems[i].State, changedItems[i].Action = Diverged, ""
		}
	}
	res.Items = slices.Concat(
		missingItems,
		items(k, unknown, Missing, "unknown"),
		changedItems,
		items(k, extra, Extra, ""),
		items(k, unused, UnusedDependency, ""),
	)
	if len(res.Items) > 0 {
		res.State = check.Attention
	}
	return res
}

// items are names as k's items in state, sorted, each with detail.
func items(k Kind, names []string, state, detail string) []check.Item {
	slices.Sort(names)
	out := make([]check.Item, len(names))
	for i, name := range names {
		out[i] = check.Item{ID: k.Name() + ":" + name, Name: name, State: state, Detail: detail}
	}
	return out
}
