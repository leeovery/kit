// Package spotlight is the folders kept out of Spotlight's index as a kind:
// paths declared in a [spotlight exclusions] section, each checked by
// asking Spotlight for files in it. Adding one writes Spotlight's privacy
// list, through sudo; the running Spotlight service doesn't reload it, so
// it takes effect after a restart, and kit remembers it has added it.
// Spotlight's list can only be read as root, so kit finds no folders
// excluded by hand.
package spotlight

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// VolumeConfiguration is Spotlight's settings for the data volume, whose
// Exclusions are its privacy list.
const VolumeConfiguration = "/System/Volumes/Data/.Spotlight-V100/VolumeConfiguration.plist"

// plistBuddy edits property lists in place.
const plistBuddy = "/usr/libexec/PlistBuddy"

// probeTimeout is how long asking Spotlight about a folder may take.
const probeTimeout = 10 * time.Second

// recordName names kit's record of the folders it has added to Spotlight's
// list, which wait for a restart.
const recordName = "spotlight.json"

type record struct {
	Added []string `json:"added,omitempty"`
}

// Spotlight is the folders kept out of Spotlight, driven through mdfind
// and PlistBuddy.
type Spotlight struct {
	run      runner.Runner
	home     string
	stateDir string
	declared []string
	admin    func(ctx context.Context) bool
}

// New returns the Spotlight exclusions of the user whose home is home.
func New(run runner.Runner, home, stateDir string) *Spotlight {
	return &Spotlight{run: run, home: home, stateDir: stateDir}
}

func (s *Spotlight) Name() string    { return "spotlight" }
func (s *Spotlight) Title() string   { return "Spotlight exclusions" }
func (s *Spotlight) Program() string { return "mdfind" }

// Expand notes the declared folders, which are what Spotlight is asked
// about: each name stands for itself.
func (s *Spotlight) Expand(list config.List) (config.List, error) {
	s.declared = list.Names()
	return list, nil
}

func (s *Spotlight) full(name string) string {
	if rest, ok := strings.CutPrefix(name, "~/"); ok {
		return filepath.Join(s.home, rest)
	}
	return name
}

// indexed reports whether Spotlight has files in folder in its index: none
// found means it's kept out (or holds nothing Spotlight indexes).
func (s *Spotlight) indexed(ctx context.Context, folder string) (bool, error) {
	res, err := s.run.Run(ctx, runner.Command{Name: "mdfind", Args: []string{"-onlyin", folder, "-count", `kMDItemFSName == "*.md" || kMDItemFSName == "*.json"`}, Timeout: probeTimeout})
	if err != nil {
		return false, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	return err == nil && n > 0, nil
}

// Installed lists the declared folders Spotlight keeps out of its index.
func (s *Spotlight) Installed(ctx context.Context) ([]kind.Installed, error) {
	var out []kind.Installed
	for _, name := range s.declared {
		on, err := s.indexed(ctx, s.full(name))
		if err != nil {
			return nil, fmt.Errorf("ask Spotlight about %s: %w", name, err)
		}
		if !on {
			out = append(out, kind.Installed{Name: name, Explicit: true})
		}
	}
	return out, nil
}

// Resolve knows every folder.
func (s *Spotlight) Resolve(_ context.Context, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = name
	}
	return out, nil
}

// Install adds each folder to Spotlight's privacy list, through sudo, and
// remembers it: it takes effect after a restart.
func (s *Spotlight) Install(ctx context.Context, names []string) error {
	sudo := func(cmd string) error {
		_, err := s.run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", plistBuddy, "-c", cmd, VolumeConfiguration}})
		return err
	}
	// The list may not be there yet: adding it again fails, harmlessly.
	_ = sudo("Add :Exclusions array")
	var errs []error
	var added []string
	for _, name := range names {
		if err := sudo("Add :Exclusions: string " + s.full(name)); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		added = append(added, name)
	}
	if err := state.Update(s.stateDir, recordName, func(rec *record) {
		for _, name := range added {
			if !slices.Contains(rec.Added, name) {
				rec.Added = append(rec.Added, name)
			}
		}
	}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Remove can't take a folder out of the list: System Settings does that.
func (s *Spotlight) Remove(_ context.Context, names []string) error {
	return fmt.Errorf("take %s out of Spotlight's list in System Settings › Spotlight › Search Privacy", strings.Join(names, ", "))
}

// NeedsAdmin is names: Spotlight's list belongs to root.
func (s *Spotlight) NeedsAdmin(_ context.Context, names []string) ([]string, error) {
	return slices.Clone(names), nil
}

// SetAdmin is how the kind finds out whether an administrator's password is
// at hand.
func (s *Spotlight) SetAdmin(held func(ctx context.Context) bool) { s.admin = held }

// Blocked holds up a folder kit has added already, until a restart, and
// every folder while kit has no administrator's password.
func (s *Spotlight) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	rec, err := state.Load[record](s.stateDir, recordName)
	if err != nil {
		return nil, err
	}
	blocked := map[string]string{}
	for name := range missing {
		switch {
		case slices.Contains(rec.Added, name):
			blocked[name] = "added to Spotlight's list: it takes effect after a restart; still indexed after one, add it in System Settings › Spotlight › Search Privacy"
		case s.admin != nil && !s.admin(ctx):
			blocked[name] = "waiting for an administrator's password: kit apply at a terminal asks for it"
		}
	}
	return blocked, nil
}

var _ interface {
	kind.Kind
	kind.Expander
	kind.Admin
	kind.Blocker
} = (*Spotlight)(nil)
