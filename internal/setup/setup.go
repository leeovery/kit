// Package setup is kit setup: a Mac in use brought onto kit all the way, as
// the boot brings a new one. GitHub's sign-in, GitHub's CLI's when it has
// one, else the boot's; the config repository, created or joined, cloned as
// the boot clones it; this Mac's name; then the snapshot, the Mac declared
// as it stands, and the questions it asks, each with its answer suggested,
// committed and pushed as one. Each step's check looks at what's there,
// the Mac, kit-config's clone and git's settings in it, its record keeping
// only the choices made, so a setup stopped anywhere carries on where it
// was, and ends as one that never stopped. It asks
// the person through an Asker, at a terminal, in the row it's about;
// without one, every question is a flag's to answer. It says what it's
// doing through events.
package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/snapshot"
	kitstate "github.com/leeovery/kit/internal/state"
)

// Setup's steps, by name.
const (
	GitHubStep   = "github"
	ConfigStep   = "kit-config"
	MacStep      = "mac"
	SnapshotStep = "snapshot"
	DataStep     = "restore"
	PrefsStep    = "prefs"
	FilesStep    = "files"
	InstallStep  = "to-install"
	CommitStep   = "commit"
	ByHandStep   = "by-hand"
	SecretsStep  = "secrets"
)

// The areas setup's steps are in: setting up, then what's declared.
const (
	AreaSetup    = "Setup"
	AreaDeclared = "Declared"
)

// Asker is how setup asks the person at the Mac something, at a terminal,
// in the row of the step it's about.
type Asker interface {
	boot.Picker
	// Choose asks step's question, its answers under it: the answer's place.
	// A question with more to say says it on a line of its own, after its
	// first.
	Choose(ctx context.Context, step, question string, answers []boot.Answer) (int, error)
	// Text asks for text typed, suggested to start with.
	Text(ctx context.Context, step, question, suggested string) (string, error)
	// Tick asks which of items to keep, each ticked to start with as it
	// says: whether each is kept.
	Tick(ctx context.Context, step, question string, items []Item) ([]bool, error)
}

// Item is a thing to tick: its name, what more there is to say of it, and
// whether it's ticked to start with.
type Item struct {
	Name, More string
	Ticked     bool
}

// Answers are setup's questions answered as flags: nil, or "", for a
// question not answered.
type Answers struct {
	// Create names a new config repository, on the person's account; Join
	// one of theirs, as owner/name.
	Create, Join string
	// Mac is this Mac's name; From, a Mac a new one starts as a copy of.
	Mac, From string
	// Restore are the folders a new Mac brings back, and Link the files
	// kit-config keeps: an empty list for none.
	Restore, Link *[]string
	// Prefs is whether apps' settings are saved nightly; Apply whether kit
	// apply runs once setup's done.
	Prefs, Apply *bool
}

// Declaring is what setup's commit declares: the snapshot, and the answers.
type Declaring struct {
	Found   *snapshot.Snapshot
	Restore []string
	Link    []string
	// Prefs is whether apps' settings are saved: as answered, else as
	// they are, capture switched on for this Mac already.
	Prefs bool
	// Empty is whether the config repository was empty, created by setup
	// or joined so: its branch isn't on the remote till setup's first push.
	Empty bool
}

// Setup is kit setup, and what it runs with.
type Setup struct {
	Run  runner.Runner
	Sink event.Sink
	// Ask asks at a terminal: nil without one.
	Ask    Asker
	GitHub boot.GitHub
	Now    func() time.Time
	Dirs   config.Dirs
	// Kinds are the kinds' sections the config's declarations may hold.
	Kinds []config.Section
	// Getenv reads the environment kit runs in, and Version is kit's.
	Getenv  func(string) string
	Version string
	// Model is what this Mac is, as in MacBook Pro (Model).
	Model string
	// Given are the questions answered by flags.
	Given Answers
	// Snapshot looks at this Mac, named mac in cfg, for what it would
	// declare (snapshot.Find), every kind made on kit's PATH as cfg makes
	// it: what it found, and the Mac it looked at.
	Snapshot func(ctx context.Context, cfg *config.Config, mac string) (snapshot.Snapshot, snapshot.Mac, error)
	// Declare declares what the snapshot found and the answers, then
	// commits and pushes: how it went.
	Declare func(ctx context.Context, d Declaring) check.Result

	mu           sync.Mutex
	token, login string
	found        *snapshot.Snapshot
	mac          snapshot.Mac
	toInstall    *toInstall
	declared     *check.Result
	wanted       []string
}

// recordName is setup's record, in kit's state.
const recordName = "setup.json"

// record is what setup keeps between its runs: the config repository;
// whether it was to be created, whether it was empty, created or joined
// so, kit-config starting on this Mac, and whether its clone was looked at
// for that; this Mac's name, whether it's new to kit-config, and the Mac
// it starts as a copy of; the answers given; whether the commit's edits
// were begun; and whether it's done, pushed.
type record struct {
	Repo     string    `json:"repo,omitempty"`
	Created  bool      `json:"created,omitempty"`
	Empty    bool      `json:"empty,omitempty"`
	Cloned   bool      `json:"cloned,omitempty"`
	Mac      string    `json:"mac,omitempty"`
	NewMac   bool      `json:"new_mac,omitempty"`
	From     string    `json:"from,omitempty"`
	Restore  *[]string `json:"restore,omitempty"`
	Link     *[]string `json:"link,omitempty"`
	Prefs    *bool     `json:"prefs,omitempty"`
	Apply    *bool     `json:"apply,omitempty"`
	Declared bool      `json:"declared,omitempty"`
	Done     bool      `json:"done,omitempty"`
}

// partWay reports whether setup was stopped part-way: a config repository
// chosen, and not done.
func (r record) partWay() bool { return r.Repo != "" && !r.Done }

// PartWay reports whether a run of setup stopped part-way, as its record in
// kit's state, in stateDir, says.
func PartWay(stateDir string) bool {
	rec, err := kitstate.Load[record](stateDir, recordName)
	return err == nil && rec.partWay()
}

// Begin readies a run of setup: refused while the config folder holds
// anything, unless setup's own run part-way left it there (carriesOn),
// which carries on; else started afresh.
func (s *Setup) Begin(ctx context.Context) error {
	rec, err := kitstate.Load[record](s.Dirs.State, recordName)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(s.Dirs.Config)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", s.Dirs.Config, err)
	}
	empty := len(entries) == 0
	carries, err := s.carriesOn(ctx, rec, empty)
	switch {
	case err != nil:
		return err
	case carries:
		return nil
	case empty:
		return s.update(func(r *record) { *r = record{} })
	}
	if _, err := os.Stat(filepath.Join(s.Dirs.Config, config.File)); err == nil {
		return fmt.Errorf("%s holds a config already, so kit looks after this Mac: kit status says how it stands", s.Dirs.Config)
	}
	return fmt.Errorf("%s is there, and isn't a config: move it aside, then run kit setup again", s.Dirs.Config)
}

// carriesOn reports whether rec is setup's run part-way, for this one to
// carry on: the config folder empty, or setup's own clone (ownClone); kit's
// name for this Mac none yet, or the one rec names.
func (s *Setup) carriesOn(ctx context.Context, rec record, empty bool) (bool, error) {
	if !rec.partWay() {
		return false, nil
	}
	name, err := config.ReadMachine(s.Dirs.State)
	if err != nil || name != "" && name != rec.Mac {
		return false, err
	}
	return empty || s.ownClone(ctx, rec), nil
}

// ownClone reports whether the config folder holds setup's own clone, of
// the repository rec names, by the URL its origin has set, as cloned,
// before any insteadOf of the person's rewrites it.
func (s *Setup) ownClone(ctx context.Context, rec record) bool {
	url, err := gitrepo.Repo{Dir: s.Dirs.Config, Run: s.Run}.SetURL(ctx)
	return err == nil && strings.EqualFold(gitrepo.OnGitHub(url), rec.Repo)
}

// sameMac refuses a --mac or --from other than rec, a run part-way,
// records: this Mac's name is written in kit-config and kit's state.
func (s *Setup) sameMac(rec record) error {
	g := s.Given
	if rec.Mac == "" {
		return nil
	}
	switch {
	case g.Mac != "" && g.Mac != rec.Mac:
		return fmt.Errorf("kit setup, part-way, named this Mac %s: run kit setup again without --mac, or with --mac %s", rec.Mac, rec.Mac)
	case g.From != "" && g.From != rec.From && rec.From == "":
		return fmt.Errorf("kit setup, part-way, named this Mac %s, a copy of none of your Macs: run kit setup again without --from", rec.Mac)
	case g.From != "" && g.From != rec.From:
		return fmt.Errorf("kit setup, part-way, named this Mac %s, a copy of %s: run kit setup again without --from, or with --from %s", rec.Mac, rec.From, rec.From)
	}
	return nil
}

// Steps are setup's steps, in order, each needing the one before it, but
// the questions, which need only the snapshot, and what's said once it's
// committed, which only comes after it.
func (s *Setup) Steps() []engine.Step {
	first := []engine.Step{s.gitHubStep(), s.configStep(), s.macStep(), s.snapshotStep()}
	for i := range first {
		first[i].Area = AreaSetup
		if i > 0 {
			first[i].Needs = []string{first[i-1].Name}
		}
	}
	first[len(first)-1].Area = AreaDeclared
	questions := []engine.Step{s.dataStep(), s.prefsStep(), s.filesStep(), s.installStep()}
	commit := s.commitStep()
	for i := range questions {
		questions[i].Area, questions[i].Needs = AreaDeclared, []string{SnapshotStep}
		commit.Needs = append(commit.Needs, questions[i].Name)
	}
	told := []engine.Step{s.byHandStep(), s.secretsStep()}
	for i := range told {
		told[i].Area, told[i].After = AreaDeclared, []string{CommitStep}
	}
	commit.Area = AreaDeclared
	return append(append(append(first, questions...), commit), told...)
}

// Done reports whether setup committed what it declared: kit apply's then.
func (s *Setup) Done() bool { return s.record().Done }

// Mac is this Mac's name, as setup recorded it: "" till it has one.
func (s *Setup) Mac() string { return s.record().Mac }

// Applies reports whether kit apply runs once setup's done: unless the
// person said to stop to look first, or not to, by a flag now, else as
// recorded.
func (s *Setup) Applies() bool {
	if a := cmp.Or(s.Given.Apply, s.record().Apply); a != nil {
		return *a
	}
	return true
}

// Wanted are the flags the questions asked without a terminal want, each
// with what it answers.
func (s *Setup) Wanted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wanted
}

// ErrNoTerminal is a question asked without a terminal to ask it at.
var ErrNoTerminal = errors.New("no terminal to ask at")

// unanswered is step's question, asked without a terminal: an error naming
// what answers it, which Wanted keeps.
func (s *Setup) unanswered(answer string) error {
	s.mu.Lock()
	s.wanted = append(s.wanted, answer)
	s.mu.Unlock()
	return fmt.Errorf("%w: %s", ErrNoTerminal, answer)
}

func (s *Setup) record() record {
	rec, _ := kitstate.Load[record](s.Dirs.State, recordName)
	return rec
}

func (s *Setup) update(change func(*record)) error {
	return kitstate.Update(s.Dirs.State, recordName, change)
}

func (s *Setup) doing(step, what string) {
	s.Sink.Emit(event.Doing{Time: s.Now(), Step: step, Says: what})
}

// says is what a step's row says, its parts after dots, as a summary does.
func says(parts ...string) string { return strings.Join(parts, " · ") }

// config is the config repository, as cloned, checked that this kit reads
// it.
func (s *Setup) config() (*config.Config, error) {
	cfg, err := config.Load(s.Dirs.Config, s.Kinds)
	if err != nil {
		return nil, err
	}
	return cfg, cfg.Supports(s.Version)
}

// cloned reports whether the config repository is cloned.
func (s *Setup) cloned() bool {
	_, err := os.Stat(filepath.Join(s.Dirs.Config, ".git"))
	return err == nil
}
