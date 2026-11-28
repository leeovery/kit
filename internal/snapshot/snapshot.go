// Package snapshot takes a Mac in use as it stands into its own
// declarations file, as kit setup brings it onto kit: every kind whose
// program is here, its things installed and not declared, as adopting each
// declares it; the macOS settings set now, but macOS's own state; git's
// settings; the features on, and the backup tools set up. It finds, too,
// what kit setup's questions offer and never declares by itself: the home
// folder's data folders, the shell's and tools' files to link, and the apps
// installed by hand, which kit leaves to the person.
package snapshot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/backup"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/kind/defaults"
	"github.com/leeovery/kit/internal/kind/gitconfig"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/steps"
)

// Mac is the Mac a snapshot is of: its name in kit.toml, the config it's
// declared in, and what every kind is made from.
type Mac struct {
	Name   string
	Config *config.Config
	// Run runs programs on kit's PATH, as kit-config declares it.
	Run  runner.Runner
	Home string
	// Beyond are folders off kit's PATH a kind's program Run doesn't find
	// is looked for in, in order: the PATH kit setup was run with, then
	// Homebrew's and the system's. RunOn is Run with folders last in the
	// Mac's [paths], as kit's later runs have them once they're declared;
	// without it, nothing is looked for there.
	Beyond []string
	RunOn  func(dirs []string) runner.Runner
	// Temp are the temporary folders, never declared on the PATH.
	Temp []string
	// ConfigHome is XDG's config folder, "" for the default; Dirs are kit's
	// own folders.
	ConfigHome string
	Dirs       config.Dirs
	// AppDirs are the folders apps are installed in.
	AppDirs []string
	// SudoLocal is the file Touch ID for sudo's line goes in.
	SudoLocal string
	// SizeFor is how long the data folders are sized for, DefaultSizeFor
	// when 0: one not sized by then says as much as was counted.
	SizeFor time.Duration
	// Now is the time, as the backup tools' last backups are judged by.
	Now func() time.Time
	// Looks, when set, is told how each part stands as Find looks at it,
	// from whichever goroutine looks: each waiting its turn first, then as
	// it's looked at, and once it has been; and the folders, while Find
	// waits on their sizes.
	Looks func(Look)
}

// Look is how a part of a snapshot stands as Find looks at the Mac: its
// label; what's being done, while it's looked at; once Looked, how many
// things it found, by name when they have names of their own (the features
// on, the backup tools set up), or why it couldn't be looked at (Err), or
// why some of it couldn't (Missed).
type Look struct {
	Label       string
	Doing       string
	Looked      bool
	Found       int
	Names       []string
	Err, Missed error
}

// FoldersLabel is the label of the home folder's folders, as Find tells
// Looks of their sizing, and kit setup's question of them is titled, so
// its screen shows the sizing in the question's row.
const FoldersLabel = "Your data"

func (m Mac) look(l Look) {
	if m.Looks != nil {
		m.Looks(l)
	}
}

// lookAt is the part labelled label, as find finds it, told to Looks as
// it's looked at, and once it has been.
func (m Mac) lookAt(label string, find func() Part) Part {
	m.look(Look{Label: label, Doing: "reading what's here"})
	p := find()
	m.look(Look{Label: label, Looked: true, Found: len(p.Lines), Names: p.names, Err: p.Err, Missed: p.Missed})
	return p
}

// Snapshot is what a snapshot of a Mac found: what it declares, a part
// each, and what kit setup's questions offer.
type Snapshot struct {
	Parts []Part
	// Folders are the home folder's own folders a new Mac would bring back
	// from backups.
	Folders []Folder
	// Files are the shell's and tools' files at their usual places, from ~,
	// that could be linked in.
	Files []string
	// ByHand are the apps installed by hand, each with the cask that
	// installs it, if one does. ByHandErr is why they couldn't be told,
	// when they couldn't; MatchErr, why which casks install them couldn't,
	// when it couldn't.
	ByHand              []brew.ByHand
	ByHandErr, MatchErr error
}

// Part is a part of what a snapshot declares: a kind's things, or the
// features on, or the backup tools set up.
type Part struct {
	// Kind names its section, a kind's or the config's own; Label names it
	// as kit's reports do.
	Kind, Label string
	// Lines are what it declares, each as adopting it would, held for the
	// secret its install needs; Refused, what declaring refused, and why,
	// as a secret the Mac doesn't declare, naming kit secret add.
	Lines   []config.Entry
	Refused []config.Refusal
	// Unchecked are those of Lines declared as they are, as whether they
	// need a secret couldn't be told, as without the network.
	Unchecked []Unchecked
	// Err is why it couldn't be looked at: none of it is declared. Missed
	// is why some of it couldn't be: the rest is.
	Err, Missed error
	// section is whether it declares its section with nothing in it: a
	// kind's, whose program is here.
	section bool
	// names are its Lines as people call them, when that isn't their
	// names: the features' titles, the backup tools'.
	names []string
}

// Unchecked is a line declared as it is, by its name, as whether it needs a
// secret couldn't be told, and why.
type Unchecked struct {
	Name string
	Err  error
}

// DefaultSizeFor is how long the data folders are sized for when Mac
// doesn't say. Measured, 68 GB in 11 folders took 29 seconds.
const DefaultSizeFor = time.Minute

// Labels of the config's own parts.
const (
	featuresLabel = "Features"
	backupsLabel  = "Backups"
)

// settingsKinds are the settings' kinds a snapshot declares, as packages'
// are all declared.
var settingsKinds = []string{gitconfig.Descriptor.Name, defaults.Descriptor.Name}

// Find looks at the Mac as it stands: what each part would declare, and
// what the questions offer; and returns m as it looked, its Run on the PATH
// the folders it declares make (along). It declares nothing: Write does. A
// part that couldn't be looked at says why, as the apps installed by hand
// do, and the rest are found; it fails only when the home folder, or what's
// kit's there, can't be read.
func Find(ctx context.Context, m Mac) (Snapshot, Mac, error) {
	m, paths := m.along()
	deps := &kind.Deps{Run: m.Run, Home: m.Home, ConfigHome: m.ConfigHome, StateDir: m.Dirs.State, AppDirs: m.AppDirs}
	owned, err := m.owned(ctx, deps)
	if err != nil {
		return Snapshot{}, m, err
	}
	folders, err := m.folders(owned)
	if err != nil {
		return Snapshot{}, m, err
	}
	// The folders are sized while the rest is found, for no longer than
	// SizeFor.
	sizing, sized := context.WithTimeout(ctx, cmp.Or(m.SizeFor, DefaultSizeFor))
	defer sized()
	sizes := make(chan []Folder, 1)
	go func() { sizes <- sizeAll(sizing, m.Home, folders, os.ReadDir) }()
	var parts []Part
	if paths != nil {
		parts = append(parts, *paths)
	}
	parts = append(parts, m.kindParts(ctx, deps, featuresLabel, backupsLabel)...)
	parts = append(parts, m.lookAt(featuresLabel, func() Part { return m.featuresPart(ctx) }), m.lookAt(backupsLabel, func() Part { return m.backupsPart(ctx) }))
	s := Snapshot{Parts: parts, Files: m.files(owned)}
	hand, err := brew.Of(deps).AppsByHand(ctx)
	s.ByHand, s.MatchErr, s.ByHandErr = hand.Apps, hand.Unmatched, err
	select {
	case s.Folders = <-sizes:
	default:
		m.look(Look{Label: FoldersLabel, Doing: "sizing your folders"})
		s.Folders = <-sizes
		m.look(Look{Label: FoldersLabel, Looked: true, Found: len(s.Folders)})
	}
	return s, m, nil
}

// kindParts are the parts of the kinds a snapshot declares whose program is
// on kit's PATH, in the order a run takes them, each looked at alongside
// the rest, once Looks is told they wait their turn, and the parts after
// them, labelled then.
func (m Mac) kindParts(ctx context.Context, deps *kind.Deps, then ...string) []Part {
	var parts []Part
	var ks []kind.Kind
	for _, d := range kinds.All {
		if !snapshotted(d) {
			continue
		}
		if k := d.New(deps); runner.Has(m.Run, k.Program()) {
			parts = append(parts, Part{Kind: d.Name, Label: d.Label, section: true})
			ks = append(ks, k)
		}
	}
	for _, p := range parts {
		m.look(Look{Label: p.Label})
	}
	for _, label := range then {
		m.look(Look{Label: label})
	}
	var wg sync.WaitGroup
	for i, k := range ks {
		wg.Go(func() {
			parts[i] = m.lookAt(parts[i].Label, func() Part {
				p := parts[i]
				m.undeclared(ctx, k, &p)
				return p
			})
		})
	}
	wg.Wait()
	return parts
}

// undeclared finds p's lines, k's things on the Mac and not declared for
// it, each as adopting it declares it; those whose line couldn't be made,
// refused; or why k couldn't be looked at.
func (m Mac) undeclared(ctx context.Context, k kind.Kind, p *Part) {
	list, unread, err := kind.Declared(m.Config, k, m.Name)
	if p.Err = errors.Join(err, unread); p.Err != nil {
		return
	}
	if d, ok := k.(*defaults.Defaults); ok {
		version, err := macOSVersion(ctx, m.Run)
		if err != nil {
			p.Err = err
			return
		}
		p.Lines, p.Err = d.Current(ctx, version)
		return
	}
	res := kind.Compare(ctx, k, list)
	if res.State == check.Failed {
		p.Err = errors.New(res.Reason)
		return
	}
	var extra []string
	for _, it := range res.Items {
		switch it.State {
		case check.NotAllowed:
			p.Err = fmt.Errorf("%s: allow it in %s", it.Name, it.Detail)
			return
		case kind.Extra:
			extra = append(extra, it.Name)
		}
	}
	lines := make([]madeLine, len(extra))
	var wg sync.WaitGroup
	free := make(chan struct{}, liners)
	for i, name := range extra {
		wg.Go(func() {
			free <- struct{}{}
			defer func() { <-free }()
			lines[i] = m.line(ctx, k, name)
		})
	}
	wg.Wait()
	for _, l := range lines {
		switch {
		case l.err != nil:
			p.Refused = append(p.Refused, config.Refusal{Kind: k.Name(), Entry: l.entry, Err: l.err})
			continue
		case l.unchecked != nil:
			p.Unchecked = append(p.Unchecked, Unchecked{Name: l.entry.Name, Err: l.unchecked})
		}
		p.Lines = append(p.Lines, l.entry)
	}
}

// liners is how many of a kind's lines are made at once: holding one for a
// secret asks for its download, over the network.
const liners = 8

// madeLine is a line a part declares: its entry; why whether it needs a secret
// couldn't be told, when it couldn't; and why it's refused, when it is.
type madeLine struct {
	entry          config.Entry
	unchecked, err error
}

// line is the line adopting name, one of k's things installed, declares in
// the Mac's file: held for the secret installing it needs, refused while
// the Mac doesn't declare that secret, nothing changed on the Mac to find
// out (kind.HoldInstalled).
func (m Mac) line(ctx context.Context, k kind.Kind, name string) madeLine {
	e, err := kind.Line(ctx, k, name, "")
	if err != nil {
		return madeLine{entry: e, err: err}
	}
	unchecked, err := kind.HoldInstalled(ctx, k, m.Config, m.Name, &e)
	return madeLine{entry: e, unchecked: unchecked, err: err}
}

// macOSVersion is the Mac's macOS's, as in 26.1.
func macOSVersion(ctx context.Context, run runner.Runner) (string, error) {
	res, err := run.Run(ctx, runner.Command{Name: "sw_vers", Args: []string{"-productVersion"}})
	if err != nil {
		return "", fmt.Errorf("find macOS's version: %w", err)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// featuresPart is the features that change the system that are on, and
// not declared already: each as its own check finds it, but Screen
// Sharing, on once its service is (steps.ScreenSharingOn), as the person
// switched it on whether or not viewers can take control, which its check
// says once it's declared. One that can't be looked at is left out, the
// rest declared, and why is the part's Missed.
func (m Mac) featuresPart(ctx context.Context) Part {
	p := Part{Kind: config.FeaturesKind, Label: featuresLabel}
	declared, err := m.Config.List(config.FeaturesKind, m.Name)
	if err != nil {
		p.Err = err
		return p
	}
	admin := &steps.Admin{}
	var errs []error
	for _, f := range []struct {
		name, title string
		on          func(context.Context) (bool, error)
	}{
		{steps.FeatureTouchIDSudo, "Touch ID for sudo", checkedOn(steps.TouchIDSudo(m.Run, admin, m.SudoLocal))},
		{steps.FeatureRemoteLogin, "Remote Login", checkedOn(steps.RemoteLogin(m.Run, admin))},
		{steps.FeatureFileSharing, "File Sharing", checkedOn(steps.FileSharing(m.Run, admin))},
		{steps.FeatureScreenSharing, "Screen Sharing", func(ctx context.Context) (bool, error) { return steps.ScreenSharingOn(ctx, m.Run) }},
	} {
		on, err := f.on(ctx)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", f.title, err))
		case on && !slices.Contains(declared.Names(), f.name):
			p.Lines = append(p.Lines, config.Entry{Name: f.name})
			p.names = append(p.names, f.title)
		}
	}
	p.Missed = errors.Join(errs...)
	return p
}

// checkedOn is whether s's feature is on, as its check finds it: ok.
func checkedOn(s engine.Step) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		res := s.Check(ctx)
		if res.State == check.Failed {
			return false, errors.New(res.Reason)
		}
		return res.State == check.OK, nil
	}
}

// backupsPart is the backup tools set up on the Mac, as each one's driver
// says, and not declared already.
func (m Mac) backupsPart(ctx context.Context) Part {
	p := Part{Kind: config.BackupsKind, Label: backupsLabel}
	declared, err := m.Config.List(config.BackupsKind, m.Name)
	if err != nil {
		p.Err = err
		return p
	}
	var errs []error
	for _, d := range backup.Tools {
		about := d.About()
		st, err := d.Look(ctx, m.Run, m.Now())
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", about.Name, err))
		case !st.Missing && !st.NotSetUp && !slices.Contains(declared.Names(), about.ID):
			p.Lines = append(p.Lines, config.Entry{Name: about.ID})
			p.names = append(p.names, about.Name)
		}
	}
	p.Err = errors.Join(errs...)
	return p
}

// Adopt has s declare casks, each to take over an app installed by hand,
// among the casks' lines: those it declares already as they are. With no
// casks' part looked at, they're a part of their own.
func (s *Snapshot) Adopt(casks []string) {
	if len(casks) == 0 {
		return
	}
	d := brew.CasksDescriptor
	i := slices.IndexFunc(s.Parts, func(p Part) bool { return p.Kind == d.Name && p.Err == nil })
	if i < 0 {
		s.Parts = append(s.Parts, Part{Kind: d.Name, Label: d.Label, section: true})
		i = len(s.Parts) - 1
	}
	p := &s.Parts[i]
	for _, c := range casks {
		if !slices.ContainsFunc(p.Lines, func(e config.Entry) bool { return e.Name == c }) {
			p.Lines = append(p.Lines, config.Entry{Name: c})
		}
	}
}

// Write declares what s's parts found in the Mac's declarations file: one
// edit, every section placed and sorted as kit writes them, the file
// written whole or not at all once it reads back as the config's loader
// reads it. A line the config refuses is left out, moved to its part's
// Refused. Nothing on the Mac is put back for one: finding its line
// changed nothing, as its tap is on the Mac already (kind.HoldInstalled).
// Committing it is the caller's.
func Write(m Mac, s *Snapshot) error {
	var sections []string
	var lines []config.Declaration
	for _, p := range s.Parts {
		if p.Err != nil {
			continue
		}
		if p.section {
			sections = append(sections, p.Kind)
		}
		for _, e := range p.Lines {
			lines = append(lines, config.Declaration{Kind: p.Kind, Entry: e})
		}
	}
	refused, err := m.Config.DeclareAll(m.Name, sections, lines)
	if err != nil {
		return fmt.Errorf("declare the snapshot in %s: %w", config.DeclFile(m.Name), err)
	}
	for _, r := range refused {
		i := slices.IndexFunc(s.Parts, func(p Part) bool { return p.Kind == r.Kind && p.Err == nil })
		if i < 0 {
			continue
		}
		p := &s.Parts[i]
		p.Lines = slices.DeleteFunc(p.Lines, func(e config.Entry) bool { return e.Name == r.Entry.Name })
		p.Unchecked = slices.DeleteFunc(p.Unchecked, func(u Unchecked) bool { return u.Name == r.Entry.Name })
		p.Refused = append(p.Refused, r)
	}
	return nil
}
