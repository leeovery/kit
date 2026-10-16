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
	"time"

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

// Commanded is a kind whose check and install run a command other than its
// Program, or none, as tmux's plugins, read from tmux's config and cloned
// with git, which macOS has, need no tmux.
type Commanded interface {
	// Command is the command its check and install run: "" for none.
	Command() string
}

// Command is the command k's check and install run, which, as kit applies,
// its step waits for while it isn't on kit's PATH, as another step may put
// it there: its Program, unless it says otherwise (Commanded); "" for none.
func Command(k Kind) string {
	if c, ok := k.(Commanded); ok {
		return c.Command()
	}
	return k.Program()
}

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
// far it's got, and tells the run's log of each, as in "Installed jq". Its
// Install gives its install command's lines to Lines(ctx).
type Counter interface {
	// Done is which of names line says is installed: none when it says
	// nothing of them, as of a dependency pulled in.
	Done(line string, names []string) (string, bool)
}

// Fetcher is a Counter whose install downloads all it needs before it
// installs any of it, as Homebrew's does: from when its command starts
// (Starting), the step says it's downloading, counting the things it was
// asked to install as each is downloaded, till the install proper begins.
type Fetcher interface {
	Counter
	// Fetched is which of names line says is downloaded: none when it says
	// nothing of them, as of a dependency's download.
	Fetched(line string, names []string) (string, bool)
	// Begun reports whether line says the install proper has begun.
	Begun(line string) bool
}

// Announcer is a Counter whose install says, in a line, which of the things
// it was asked to install it's installing now, as Homebrew's does: the step
// names that one, rather than the next not yet done, as the install needn't
// take them in the order they were asked for.
type Announcer interface {
	Counter
	// Installing is which of names line says is being installed now: none
	// when it says nothing of them, as of a dependency.
	Installing(line string, names []string) (string, bool)
}

// Readier is a kind that readies what the things it's to install need
// before it installs any, as Homebrew taps the taps they're from: as its
// step starts applying, while the Mac is as the run found it, before other
// steps change what that takes, as the user's git settings would send a
// tap's clone their way.
type Readier interface {
	// Ready readies what names need, saying what it's doing as it does.
	Ready(ctx context.Context, names []string) error
}

// Shortened is a kind whose things are shown by part of their names, as
// Homebrew's from a tap by their names in the tap.
type Shortened interface {
	// Short is the part of name it's shown by.
	Short(name string) string
}

// Sharing is a kind whose installs take turns with other kinds', as they go
// through one program that changes one thing at a time, as Homebrew's
// formulae and casks do: the lines it installs after its first (Placing)
// wait for the others' first, so none of theirs waits its turn behind them.
type Sharing interface {
	// Shares names the kinds it takes turns with.
	Shares() []string
}

// NotAllowed is the error a kind returns when macOS hasn't let kit do what
// it must, as look at what's installed, till the person at the Mac allows
// it: its step needs them, saying what isn't allowed and where to allow
// it, rather than failing.
type NotAllowed struct {
	// What is what macOS hasn't let kit do, as in "macOS hasn't let the
	// terminal control System Events".
	What string
	// Where is where to allow it, as in System Settings › Privacy &
	// Security › Automation: short enough for a row.
	Where string
}

func (n *NotAllowed) Error() string { return n.What + ": allow it in " + n.Where }

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
	return Placed(k, declared, nil, needs...)
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

// Placing is how a kind's lines install, as the run places them among its
// steps, once they're known.
type Placing struct {
	// First are the lines other steps wait for, by name, as in --after
	// cask:1password: installed ahead of the rest, as one command.
	First []string
	// Behind are what the rest wait for before they install, once First
	// are installed: the things of the kinds a Sharing kind takes turns
	// with that those kinds install first.
	Behind []engine.Wait
	// Held are its sections holding their lines back, in order.
	Held []Held
}

// Placed is the step Step is, k's lines installed as placed says, once the
// run's steps are known: those other steps wait for first, then the rest,
// once what they wait for has come, the sections held apart
// (InstallDeclared). A section whose wait ends without what it waited for
// isn't installed: its lines stay missing, saying what isn't there, as a
// Blocker's do.
func Placed(k Kind, declared config.List, placed func() Placing, needs ...string) engine.Step {
	var unmet deferred
	return engine.Step{
		Name:  k.Name(),
		Title: Label(k.Name()),
		Needs: needs,
		Check: func(ctx context.Context) check.Result { return unmet.mark(Compare(ctx, k, declared), declared) },
		Apply: func(ctx context.Context, found check.Result) error {
			var placing Placing
			if placed != nil {
				placing = placed()
			}
			why, err := installApart(ctx, k, declared, placing, toInstall(found))
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
	why, err := installApart(ctx, k, declared, Placing{Held: held}, names)
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

// installApart installs names as placing says, as Placed does, a Readier's
// readied first, returning why each section held whose wait ended without
// what it waited for was left, by hold.
func installApart(ctx context.Context, k Kind, declared config.List, placing Placing, names []string) (map[string]string, error) {
	first, plain, apart := split(names, declared, placing)
	var errs []string
	if r, ok := k.(Readier); ok && len(names) > 0 {
		if err := r.Ready(ctx, names); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if err := install(ctx, k, declared, first, nil); err != nil {
		errs = append(errs, err.Error())
	}
	unmet := map[string]string{}
	if len(plain)+len(apart) > 0 && !waitBehind(ctx, placing.Behind) {
		return unmet, ctx.Err()
	}
	if err := install(ctx, k, declared, plain, nil); err != nil {
		errs = append(errs, err.Error())
	}
	for _, h := range placing.Held {
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

// waitBehind waits for each of behind in turn, as engine.Await does, saying
// it's waiting for its step, by its title: it reports whether the run's
// still going.
func waitBehind(ctx context.Context, behind []engine.Wait) bool {
	for _, w := range behind {
		if engine.Await(ctx, w, "waiting for "+Label(w.Step)); ctx.Err() != nil {
			return false
		}
	}
	return true
}

// split splits names, to install, into those placing installs first, those
// in no section held, and those in each section held, by its hold: a line
// held keeps to its section, first or not.
func split(names []string, declared config.List, placing Placing) (first, plain []string, apart map[string][]string) {
	holds := make(map[string]bool, len(placing.Held))
	for _, h := range placing.Held {
		holds[h.Hold] = true
	}
	holdOf := make(map[string]string, len(declared.Entries))
	for _, e := range declared.Entries {
		if holds[e.Hold] {
			holdOf[e.Name] = e.Hold
		}
	}
	apart = map[string][]string{}
	for _, name := range names {
		switch hold := holdOf[name]; {
		case hold != "":
			apart[hold] = append(apart[hold], name)
		case slices.Contains(placing.First, name):
			first = append(first, name)
		default:
			plain = append(plain, name)
		}
	}
	return first, plain, apart
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
// values env sets hidden. Once it ends, the steps waiting for k's things
// look again.
func install(ctx context.Context, k Kind, declared config.List, names, env []string) error {
	if len(names) == 0 {
		return nil
	}
	installing := runner.WithSecretEnv(ctx, env...)
	stop := func() {}
	if _, counts := k.(Counter); counts {
		// What it counts isn't there for a step waiting on it till the
		// install says it's done, whatever the kind's list says meanwhile.
		stop = engine.Putting(ctx, names)
	}
	counted := counting(installing, k, names)
	unwatch := watchQuiet(counted)
	err := k.Install(counted, names)
	unwatch()
	stop()
	engine.Changed(ctx)
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
		return errors.New(redact.Text(engine.Reason(err), values...))
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

type progressKey struct{}

// Lines is what an install command's lines go to, as runner.Command's Lines,
// in ctx: nil when nothing counts them.
func Lines(ctx context.Context) func(line string) {
	if p, ok := ctx.Value(progressKey{}).(*progress); ok {
		return p.line
	}
	return nil
}

// Saying has the step whose install ctx counts say says, till resume,
// rather than how its install's going: what the install does first, as
// Homebrew updating itself, or waits for; then how its install's going
// again. Where nothing counts the install, it says nothing.
func Saying(ctx context.Context, says string) (resume func()) {
	p, ok := ctx.Value(progressKey{}).(*progress)
	if !ok {
		return func() {}
	}
	p.aside(says)
	return func() { p.aside("") }
}

// Waiting is Saying that the install waits for what, the title of the step
// whose turn it is, as a Sharing kind's waits while another's install goes
// on.
func Waiting(ctx context.Context, what string) (resume func()) {
	return Saying(ctx, "waiting for "+what)
}

// Starting has the step whose install ctx counts say its install command is
// starting: a Fetcher's says it's downloading from then, and not before, as
// its Install may first do, or wait for, something else (Saying). A
// Fetcher's Install calls it as its command starts.
func Starting(ctx context.Context) {
	if p, ok := ctx.Value(progressKey{}).(*progress); ok {
		p.start()
	}
}

// counting is ctx for installing names, when k is a Counter: the step says
// how the install's going, from the start, by the lines it prints.
func counting(ctx context.Context, k Kind, names []string) context.Context {
	c, ok := k.(Counter)
	if !ok {
		return ctx
	}
	f, fetches := k.(Fetcher)
	a, _ := k.(Announcer)
	s, _ := k.(Shortened)
	p := &progress{ctx: ctx, counter: c, fetcher: f, announcer: a, shortened: s, names: names, program: Command(k),
		unstarted: fetches, done: map[string]bool{}, fetched: map[string]bool{}, heard: time.Now()}
	p.say()
	return context.WithValue(ctx, progressKey{}, p)
}

// progress is how an install of names is going, as a Counter reads the lines
// it prints: a Fetcher's downloading, from when its command starts till the
// install proper begins, counting those of names downloaded; then
// installing, as each is done, the next (next), counting those done. Each
// downloaded, and each done, is told to the run's log; each done has the
// steps waiting for the kind's things look again.
type progress struct {
	ctx       context.Context
	counter   Counter
	fetcher   Fetcher
	announcer Announcer
	shortened Shortened
	names     []string

	mu sync.Mutex
	// unstarted is a Fetcher's install before its command starts, saying
	// nothing of downloading yet; fetching, its command downloading.
	unstarted, fetching bool
	// says is what the install does first, or waits for, while it does.
	says          string
	done, fetched map[string]bool
	// now is the one of names an Announcer's install last said it's
	// installing.
	now string
	// program is the command installing; heard, when it last printed a
	// line, last; quiet, whether it's printed nothing for quietAfter since.
	program string
	heard   time.Time
	last    string
	quiet   bool
}

// quietAfter is how long an install may print nothing before its step says
// so, naming the last line it printed, as a command waiting on what no one
// can see shows: ssh asking at a terminal behind kit's screen (the fifth
// run, 16 Oct). quietLook is how often it looks.
var quietAfter, quietLook = 2 * time.Minute, 15 * time.Second

// watchQuiet watches the install ctx counts for going quiet, as hush does,
// till the func it returns is called: nothing, where nothing counts it.
func watchQuiet(ctx context.Context) (stop func()) {
	p, ok := ctx.Value(progressKey{}).(*progress)
	if !ok {
		return func() {}
	}
	done := make(chan struct{})
	after, tick := quietAfter, time.NewTicker(quietLook)
	go func() {
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				p.hush(after)
			}
		}
	}()
	return func() { close(done) }
}

// hush notes the install quiet, once it's printed nothing for after: the
// run's log told, as something to look at, naming its last line; the step
// saying since when.
func (p *progress) hush(after time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.quiet || time.Since(p.heard) < after {
		return
	}
	p.quiet = true
	last := "it's printed nothing yet"
	if p.last != "" {
		last = "its last line: " + p.last
	}
	event.Log(p.ctx, event.Logged{Line: fmt.Sprintf("%s has said nothing since %s; %s", p.program, p.heard.Format("15:04:05"), last), Alert: true})
	p.say()
}

// line reads a line the install printed: heard, it's no longer quiet.
func (p *progress) line(line string) {
	name, done := p.counter.Done(line, p.names)
	now, announced := p.announced(line)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.heard = time.Now()
	if l := strings.TrimSpace(line); l != "" {
		p.last = l
	}
	if p.quiet {
		p.quiet = false
		p.say()
	}
	moved := announced && now != p.now && !p.done[now]
	if moved {
		p.now = now
	}
	switch {
	case done:
		if p.note(p.done, name) {
			p.unstarted, p.fetching = false, false
			// What's next is said first, so a screen drawn as the log's told
			// is never a line behind it, still installing what's in.
			p.say()
			event.Log(p.ctx, event.Logged{Line: "Installed " + p.shown(name)})
			engine.Put(p.ctx, name)
		}
	case p.fetching && p.fetcher.Begun(line):
		p.fetching = false
		p.say()
	case p.fetching:
		if name, ok := p.fetcher.Fetched(line, p.names); ok && p.note(p.fetched, name) {
			p.say()
			event.Log(p.ctx, event.Logged{Line: "Downloaded " + p.shown(name)})
		}
	case moved:
		p.say()
	}
}

// announced is which of names line says the install is installing now: none
// unless its kind is an Announcer.
func (p *progress) announced(line string) (string, bool) {
	if p.announcer == nil {
		return "", false
	}
	return p.announcer.Installing(line, p.names)
}

// shown is name as the run's log shows it: by the part its kind shows it
// by, if it's Shortened.
func (p *progress) shown(name string) string {
	if p.shortened == nil {
		return name
	}
	return p.shortened.Short(name)
}

// next is which of names the install is at: for an Announcer's, the one it
// last said it's installing, till it's done; else the first not yet done.
// "" for none.
func (p *progress) next() string {
	if p.announcer != nil {
		if p.done[p.now] {
			return ""
		}
		return p.now
	}
	if i := slices.IndexFunc(p.names, func(name string) bool { return !p.done[name] }); i >= 0 {
		return p.names[i]
	}
	return ""
}

// note adds name to set, when it's one of the names installing and not in
// it yet, reporting whether it did.
func (p *progress) note(set map[string]bool, name string) bool {
	if !slices.Contains(p.names, name) || set[name] {
		return false
	}
	set[name] = true
	return true
}

// aside notes what the install does first, or waits for, "" once it's
// done, and says so.
func (p *progress) aside(says string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.says = says
	p.say()
}

// start notes the install's command is starting: a Fetcher's downloading.
func (p *progress) start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unstarted {
		p.unstarted, p.fetching = false, true
		p.say()
	}
}

// say says how the install's going: nothing, for a Fetcher's whose command
// hasn't started; a Fetcher's moving through downloading, then installing,
// each half the way; quiet, since when it's said nothing. Once lines can
// come, p.mu must be held.
func (p *progress) say() {
	quiet := func(says string) string {
		if p.quiet {
			return says + " · nothing since " + p.heard.Format("15:04")
		}
		return says
	}
	n := float64(len(p.names))
	switch {
	case p.says != "":
		event.Say(p.ctx, event.Doing{Says: quiet(p.says)})
	case p.unstarted:
	case p.fetching:
		event.Say(p.ctx, event.Doing{Says: quiet("downloading"), Done: len(p.fetched), Of: len(p.names), Far: float64(len(p.fetched)) / n / 2})
	case p.fetcher != nil:
		event.Say(p.ctx, event.Doing{Says: quiet(strings.TrimSpace("installing " + p.next())), Done: len(p.done), Of: len(p.names), Far: 0.5 + float64(len(p.done))/n/2})
	default:
		event.Say(p.ctx, event.Doing{Says: quiet(strings.TrimSpace("installing " + p.next())), Done: len(p.done), Of: len(p.names)})
	}
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
		return failed(k, err)
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
			return failed(k, err)
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
			return failed(k, err)
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
			return failed(k, err)
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

// failed is how k's step stands when looking at what's installed failed
// with err: needing the person at the Mac when macOS hasn't let kit look
// (NotAllowed), saying what to allow; else failed, saying why.
func failed(k Kind, err error) check.Result {
	if na, ok := errors.AsType[*NotAllowed](err); ok {
		return check.Result{State: check.Attention, Summary: "needs your permission", Items: []check.Item{{
			ID: k.Name() + ":allowed", Name: na.What, State: check.NotAllowed, Detail: na.Where,
		}}}
	}
	return check.Result{State: check.Failed, Reason: engine.Reason(err)}
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
