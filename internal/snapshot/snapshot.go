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
	Run    runner.Runner
	Home   string
	// ConfigHome is XDG's config folder, "" for the default; Dirs are kit's
	// own folders.
	ConfigHome string
	Dirs       config.Dirs
	// AppDirs are the folders apps are installed in.
	AppDirs []string
	// SudoLocal is the file Touch ID for sudo's line goes in.
	SudoLocal string
	// SizeFor is how long the data folders are sized for: one not sized by
	// then says as much as was counted.
	SizeFor time.Duration
	// Now is the time, as the backup tools' last backups are judged by.
	Now func() time.Time
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
	// ByHand are the apps installed by hand, by name: kit never manages
	// them. ByHandErr is why they couldn't be told, when they couldn't.
	ByHand    []string
	ByHandErr error
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
	// Err is why it couldn't be looked at: none of it is declared.
	Err error
	// section is whether it declares its section with nothing in it: a
	// kind's, whose program is here.
	section bool
}

// Labels of the config's own parts.
const (
	featuresLabel = "Features"
	backupsLabel  = "Backups"
)

// settingsKinds are the settings' kinds a snapshot declares, as packages'
// are all declared.
var settingsKinds = []string{gitconfig.Descriptor.Name, defaults.Descriptor.Name}

// Find looks at the Mac as it stands: what each part would declare, and
// what the questions offer. It declares nothing: Write does. A part that
// couldn't be looked at says why, as the apps installed by hand do, and
// the rest are found; it fails only when the home folder, or what's kit's
// there, can't be read.
func Find(ctx context.Context, m Mac) (Snapshot, error) {
	deps := &kind.Deps{Run: m.Run, Home: m.Home, ConfigHome: m.ConfigHome, StateDir: m.Dirs.State, AppDirs: m.AppDirs}
	owned, err := m.owned(ctx, deps)
	if err != nil {
		return Snapshot{}, err
	}
	folders, err := m.folders(owned)
	if err != nil {
		return Snapshot{}, err
	}
	// The folders are sized while the rest is found, for no longer than
	// SizeFor.
	sizing, sized := context.WithTimeout(ctx, m.SizeFor)
	defer sized()
	sizes := make(chan []Folder, 1)
	go func() { sizes <- sizeAll(sizing, m.Home, folders, os.ReadDir) }()
	s := Snapshot{Parts: append(m.kindParts(ctx, deps), m.featuresPart(ctx), m.backupsPart(ctx)), Files: m.files(owned)}
	s.ByHand, s.ByHandErr = m.byHand(ctx, deps)
	s.Folders = <-sizes
	return s, nil
}

// kindParts are the parts of the kinds a snapshot declares whose program is
// on kit's PATH, in the order a run takes them, each looked at alongside
// the rest.
func (m Mac) kindParts(ctx context.Context, deps *kind.Deps) []Part {
	var parts []Part
	var ks []kind.Kind
	for _, d := range kinds.All {
		if d.Group != kind.Packages && !slices.Contains(settingsKinds, d.Name) {
			continue
		}
		if k := d.New(deps); runner.Has(m.Run, k.Program()) {
			parts = append(parts, Part{Kind: d.Name, Label: d.Label, section: true})
			ks = append(ks, k)
		}
	}
	var wg sync.WaitGroup
	for i, k := range ks {
		wg.Go(func() { parts[i].Lines, parts[i].Refused, parts[i].Err = m.undeclared(ctx, k) })
	}
	wg.Wait()
	return parts
}

// undeclared are k's things on the Mac and not declared for it, each as
// adopting it declares it, and those whose line couldn't be made.
func (m Mac) undeclared(ctx context.Context, k kind.Kind) ([]config.Entry, []config.Refusal, error) {
	list, unread, err := kind.Declared(m.Config, k, m.Name)
	if err = errors.Join(err, unread); err != nil {
		return nil, nil, err
	}
	if d, ok := k.(*defaults.Defaults); ok {
		version, err := macOSVersion(ctx, m.Run)
		if err != nil {
			return nil, nil, err
		}
		lines, err := d.Current(ctx, version)
		return lines, nil, err
	}
	res := kind.Compare(ctx, k, list)
	if res.State == check.Failed {
		return nil, nil, errors.New(res.Reason)
	}
	var extra []string
	for _, it := range res.Items {
		switch it.State {
		case check.NotAllowed:
			return nil, nil, fmt.Errorf("%s: allow it in %s", it.Name, it.Detail)
		case kind.Extra:
			extra = append(extra, it.Name)
		}
	}
	entries, errs := make([]config.Entry, len(extra)), make([]error, len(extra))
	var wg sync.WaitGroup
	free := make(chan struct{}, liners)
	for i, name := range extra {
		wg.Go(func() {
			free <- struct{}{}
			defer func() { <-free }()
			entries[i], errs[i] = m.line(ctx, k, name)
		})
	}
	wg.Wait()
	var lines []config.Entry
	var refused []config.Refusal
	for i, e := range entries {
		if errs[i] != nil {
			refused = append(refused, config.Refusal{Kind: k.Name(), Entry: e, Err: errs[i]})
		} else {
			lines = append(lines, e)
		}
	}
	return lines, refused, nil
}

// liners is how many of a kind's lines are made at once: holding one for a
// secret asks Homebrew about it, a second or so each.
const liners = 8

// line is the line adopting name, one of k's things, declares in the Mac's
// file: held for the secret installing it needs, refused while the Mac
// doesn't declare that secret.
func (m Mac) line(ctx context.Context, k kind.Kind, name string) (config.Entry, error) {
	e, err := kind.Line(ctx, k, name, "")
	if err != nil {
		return e, err
	}
	_, err = kind.Hold(ctx, k, m.Config, m.Name, m.Name, &e, nil)
	return e, err
}

// macOSVersion is the Mac's macOS's, as in 26.1.
func macOSVersion(ctx context.Context, run runner.Runner) (string, error) {
	res, err := run.Run(ctx, runner.Command{Name: "sw_vers", Args: []string{"-productVersion"}})
	if err != nil {
		return "", fmt.Errorf("find macOS's version: %w", err)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// featuresPart is the features that change the system that are on, each
// as its own check finds it, and not declared already.
func (m Mac) featuresPart(ctx context.Context) Part {
	p := Part{Kind: config.FeaturesKind, Label: featuresLabel}
	declared, err := m.Config.List(config.FeaturesKind, m.Name)
	if err != nil {
		p.Err = err
		return p
	}
	admin := &steps.Admin{}
	var errs []error
	for _, s := range []engine.Step{steps.TouchIDSudo(m.Run, admin, m.SudoLocal), steps.RemoteLogin(m.Run, admin), steps.FileSharing(m.Run, admin)} {
		res := s.Check(ctx)
		switch {
		case res.State == check.Failed:
			errs = append(errs, fmt.Errorf("%s: %s", s.Title, res.Reason))
		case res.State == check.OK && !slices.Contains(declared.Names(), s.Name):
			p.Lines = append(p.Lines, config.Entry{Name: s.Name})
		}
	}
	p.Err = errors.Join(errs...)
	return p
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
		}
	}
	p.Err = errors.Join(errs...)
	return p
}

// Write declares what s's parts found in the Mac's declarations file: one
// edit, every section placed and sorted as kit writes them, the file
// written whole or not at all once it reads back as the config's loader
// reads it. A line the config refuses is left out, moved to its part's
// Refused. Committing it is the caller's.
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
		i := slices.IndexFunc(s.Parts, func(p Part) bool { return p.Kind == r.Kind })
		p := &s.Parts[i]
		p.Lines = slices.DeleteFunc(p.Lines, func(e config.Entry) bool { return e.Name == r.Entry.Name })
		p.Refused = append(p.Refused, r)
	}
	return nil
}
