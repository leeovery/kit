package kind

import (
	"sync"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// Descriptor is everything kit's shared code knows of a kind, declared in
// the kind's own package: its names, the section declaring it, its command,
// where its step runs and how it's made. Adding a kind is its package and
// its descriptor in the list of kinds; nothing else names it.
type Descriptor struct {
	// Name names the kind, its things, its step and its command, as in
	// brew; the kind it makes is named so too.
	Name string
	// Label names its step and its row in kit's reports, as its section is
	// named, cased: Homebrew formulae for [homebrew formulae].
	Label string
	// Group is what its things are, as kit's help groups its command and
	// kit's reports count them.
	Group Group
	// Section is how its section in a declarations file reads, its Kind
	// the kind's Name; nil for a kind declared in a file of its own
	// (Declarer), as tmux's plugins are, in tmux's config.
	Section *config.Section
	// Help is its command, kit <name>, adding and removing its things: nil
	// for a kind with a command of its own.
	Help *Help
	// Row is the row of applying's screen it sits in, as in Developer tools.
	Row string
	// Tool is what its commands run, as the person knows it, when its row's
	// raw view is that tool's own output, as Homebrew's is: "" for none.
	Tool string
	// Needs are the steps that must stand ok before it's checked or
	// applied: those its Setup makes.
	Needs []string
	// After are the kinds it's applied after, when the run has them, as
	// what they put in place is its own data: login items after the apps
	// they open.
	After []string
	// Setup makes the steps the kind needs that are its own, as Homebrew's
	// installs Homebrew for its formulae and casks: in a run only with a
	// kind needing them, once however many do. Nil for none.
	Setup func(*Deps) []engine.Step
	// New makes the kind.
	New func(*Deps) Kind
}

// Group is what a kind's things are: packages, settings, or secrets.
type Group string

const (
	Packages Group = "packages"
	Settings Group = "settings"
	Secrets  Group = "secrets"
)

// Help is a kind's command in kit's help, kit <name>: what it manages, and
// what adding and removing do.
type Help struct {
	What, Add, Remove string
	// Switch is, for a kind whose things can be declared off and kept
	// (Switcher), its commands turning them off and on: nil for none.
	Switch *SwitchHelp
}

// SwitchHelp is what kit <name> on and off do, for a kind whose things can
// be declared off and kept.
type SwitchHelp struct {
	On, Off string
	// Naming says how a thing is named for them, when that needs saying, as
	// a project's MCP server is.
	Naming string
	// Holder is what has the kind's things on the Mac, as kit's words name
	// it when one's removed: Claude Code.
	Holder string
}

// Switcher is a kind whose things can be declared off, kept in the config
// but not installed (config.Entry.Off), and on again.
type Switcher interface {
	// SetOff is a thing's declared value, value, with it declared off, or
	// on.
	SetOff(value string, off bool) (string, error)
}

// Deps are what every kind is made from: the runner its commands go
// through, the user's home, XDG's config folder there ("" for the default)
// and kit's state folder, the backup tools the Mac uses, by the names its
// [backups] section gives them, the folders apps are installed in, and what
// kinds share, made once for them all.
type Deps struct {
	Run                        runner.Runner
	Home, ConfigHome, StateDir string
	Backups                    []string
	AppDirs                    []string

	mu     sync.Mutex
	shared map[any]any
}

// Key is what a thing kinds share is kept by: each NewKey makes is its own,
// holding a T alone, so no two kinds can take each other's by mistake.
type Key[T any] struct{ name string }

// NewKey is a key of its own for a T kinds share, named for what it is.
func NewKey[T any](name string) *Key[T] { return &Key[T]{name: name} }

// Share is the thing key keeps that the kinds made from d share, made by
// build the first time one asks for it: Homebrew's formulae and casks share
// Homebrew, taking turns with its lock.
func Share[T any](d *Deps, key *Key[T], build func() T) T {
	d.mu.Lock()
	defer d.mu.Unlock()
	if v, ok := d.shared[key]; ok {
		return v.(T)
	}
	if d.shared == nil {
		d.shared = map[any]any{}
	}
	v := build()
	d.shared[key] = v
	return v
}
