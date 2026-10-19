// Package skill is Claude's skills installed for every project, in
// ~/.claude, by agntc: each named as agntc keys it, a GitHub repo and what in
// it, as in github/gh-stack/gh-stack, the skill of that name, or
// leeovery/agentic-skills/laravel, the pack at that path; the repo alone when
// it is one skill or pack. A version or range may follow, as in
// …/gh-stack@^0.2; without one, agntc installs the latest release and
// follows updates within it.
package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// installTimeout is how long cloning a repo and installing from it may take.
const installTimeout = 10 * time.Minute

// Skills is agntc's installs for every project, driven through a runner.
type Skills struct {
	run runner.Runner
	// home is the user's home: agntc's record is in it, and agntc runs from
	// it, never from a project.
	home string
}

// New returns agntc's installs for every project, driven through run, for
// the user whose home is home.
func New(run runner.Runner, home string) *Skills {
	return &Skills{run: run, home: home}
}

func (*Skills) Name() string    { return "claude-skill" }
func (*Skills) Program() string { return "agntc" }

func (s *Skills) agntc(ctx context.Context, args ...string) error {
	res, err := s.run.Run(ctx, runner.Command{Name: "agntc", Args: append(args, "--json"), Dir: s.home, Timeout: installTimeout})
	if _, exited := errors.AsType[*runner.ExitError](err); !exited {
		return err
	}
	// agntc says what went wrong in the document it prints.
	var doc struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(res.Stdout, &doc) != nil || doc.Error.Message == "" {
		return err
	}
	if strings.Contains(doc.Error.Message, "no skill named") {
		return &noSkill{doc.Error.Message}
	}
	return errors.New(doc.Error.Message)
}

// noSkill is agntc finding no skill of the name asked for in a repo.
type noSkill struct{ said string }

func (n *noSkill) Error() string { return n.said }

// Installed lists what agntc's record says it installed for every project,
// each installed for itself. agntc list says the same, but asks each repo
// what's newer: slow, and nothing without the network.
func (s *Skills) Installed(context.Context) ([]kind.Installed, error) {
	data, err := os.ReadFile(filepath.Join(s.home, ".agntc", "manifest.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agntc's record: %w", err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("read agntc's record: %w", err)
	}
	keys := slices.Sorted(maps.Keys(record))
	installed := make([]kind.Installed, len(keys))
	for i, key := range keys {
		installed[i] = kind.Installed{Name: key, Explicit: true}
	}
	return installed, nil
}

// Key is what agntc keys an install by, without the version it may be
// declared with.
func (*Skills) Key(name string) string {
	name, _, _ = strings.Cut(name, "@")
	return name
}

// Resolve takes every name as agntc's key for an install: one that isn't
// fails to install.
func (*Skills) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Install installs each named, one at a time, as agntc takes one source at
// a time.
func (s *Skills) Install(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		errs = append(errs, s.install(ctx, name))
	}
	return errors.Join(errs...)
}

// install installs name: a repo alone, everything in it, as it is one skill
// or pack; a name after it, the skill of that name, else the pack at that
// path; a longer path, the pack there.
func (s *Skills) install(ctx context.Context, name string) error {
	key, version, versioned := strings.Cut(name, "@")
	at := func(source string) string {
		if versioned {
			return source + "@" + version
		}
		return source
	}
	parts := strings.SplitN(key, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("%s isn't a GitHub repo, as owner/repo, and what in it", name)
	}
	repo := parts[0] + "/" + parts[1]
	switch {
	case len(parts) == 2:
		return s.agntc(ctx, "add", "-g", at(repo), "--all")
	case !strings.Contains(parts[2], "/"):
		err := s.agntc(ctx, "add", "-g", at(repo), "--skill", parts[2])
		if _, none := errors.AsType[*noSkill](err); !none {
			return err
		}
	}
	return s.agntc(ctx, "add", "-g", at(key), "--pack")
}

// Remove removes what agntc installed from each named, files and all.
func (s *Skills) Remove(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		errs = append(errs, s.agntc(ctx, "remove", "-g", s.Key(name), "-y"))
	}
	return errors.Join(errs...)
}
