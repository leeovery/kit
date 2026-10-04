// Package tmux is the kind of tmux's plugins, as the Tmux Plugin Manager
// (TPM) keeps them. They're declared where TPM reads them, by set -g
// @plugin lines in tmux's config, so there's one declaration; kit reads it
// but doesn't write it. Installing clones a plugin into TPM's plugin folder,
// as TPM does, and removing deletes its folder.
package tmux

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// cloneTimeout is how long cloning a plugin may take.
const cloneTimeout = 5 * time.Minute

// Tmux is tmux's plugins, for the user whose home is home.
type Tmux struct {
	run runner.Runner
	// home is the user's home, and configHome XDG's config folder there.
	home, configHome string
	// System is the system's tmux config, read first.
	System string
}

// New returns tmux's plugins, driven through run, for the user whose home
// is home and whose XDG config folder is configHome ("" for the default,
// ~/.config).
func New(run runner.Runner, home, configHome string) *Tmux {
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	return &Tmux{run: run, home: home, configHome: configHome, System: "/etc/tmux.conf"}
}

func (*Tmux) Name() string    { return "tmux" }
func (*Tmux) Title() string   { return "tmux plugins" }
func (*Tmux) Program() string { return "tmux" }

// files are tmux's config files TPM reads, in its order.
func (t *Tmux) files() []string {
	return []string{t.System, filepath.Join(t.home, ".tmux.conf"), filepath.Join(t.configHome, "tmux", "tmux.conf")}
}

var (
	pluginLine  = regexp.MustCompile(`^[ \t]*set(-option)?[ \t]+-g[ \t]+@plugin[ \t]+(\S+)`)
	sourceLine  = regexp.MustCompile(`^[ \t]*source(-file)?[ \t]+(.+)$`)
	managerLine = regexp.MustCompile(`^[ \t]*set(-environment)?[ \t]+-g[ \t]+TMUX_PLUGIN_MANAGER_PATH[ \t]+(\S+)`)
)

// read reads tmux's config files, and the files they source, calling line
// for each line of each, with the file's name and the line's number.
func (t *Tmux) read(line func(file string, n int, text string)) error {
	seen := make(map[string]bool)
	var visit func(path string) error
	visit = func(path string) error {
		if seen[path] {
			return nil
		}
		seen[path] = true
		f, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tmux's config: %w", err)
		}
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(f)
		var sourced []string
		for n := 1; scanner.Scan(); n++ {
			text := scanner.Text()
			line(path, n, text)
			if m := sourceLine.FindStringSubmatch(text); m != nil {
				for word := range strings.FieldsSeq(m[2]) {
					if word = unquote(word); !strings.HasPrefix(word, "-") {
						sourced = append(sourced, t.expand(word))
						break
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		for _, s := range sourced {
			if err := visit(s); err != nil {
				return err
			}
		}
		return nil
	}
	for _, path := range t.files() {
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}

// Declared is what tmux's config declares: each set -g @plugin line's
// plugin, the file and line declaring it.
func (t *Tmux) Declared() (config.List, error) {
	list := config.List{Kind: t.Name()}
	err := t.read(func(file string, n int, text string) {
		if m := pluginLine.FindStringSubmatch(text); m != nil {
			list.Entries = append(list.Entries, config.Entry{Name: unquote(m[2]), File: t.shown(file), Line: n})
		}
	})
	return list, err
}

// HowToDeclare says how a plugin is declared or undeclared: by hand, in
// tmux's config.
func (t *Tmux) HowToDeclare(name string) string {
	return fmt.Sprintf("tmux plugins are declared in tmux's config (%s): add or remove the line set -g @plugin '%s' there", t.shown(t.files()[2]), name)
}

// dir is TPM's plugin folder: the config's TMUX_PLUGIN_MANAGER_PATH, else
// XDG's tmux folder when tmux's config is there, else ~/.tmux/plugins.
func (t *Tmux) dir() (string, error) {
	set := ""
	err := t.read(func(_ string, _ int, text string) {
		if m := managerLine.FindStringSubmatch(text); m != nil {
			set = t.expand(unquote(m[2]))
		}
	})
	switch {
	case err != nil:
		return "", err
	case set != "":
		return set, nil
	}
	if _, err := os.Stat(t.files()[2]); err == nil {
		return filepath.Join(t.configHome, "tmux", "plugins"), nil
	}
	return filepath.Join(t.home, ".tmux", "plugins"), nil
}

// Installed lists the plugins in TPM's folder, each named by where its
// clone came from (its folder's name, when that doesn't say), each
// installed for itself.
func (t *Tmux) Installed(context.Context) ([]kind.Installed, error) {
	dir, err := t.dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read TPM's plugin folder: %w", err)
	}
	var installed []kind.Installed
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if repo := repository(origin(filepath.Join(dir, name, ".git", "config"))); repo != "" {
			name = repo
		}
		installed = append(installed, kind.Installed{Name: name, Explicit: true})
	}
	return installed, nil
}

// Key is a plugin's folder: TPM clones owner/name, or a URL ending in
// name, into a folder called name.
func (*Tmux) Key(name string) string {
	name, _, _ = strings.Cut(name, "#")
	name = strings.TrimSuffix(strings.TrimRight(name, "/"), ".git")
	return name[strings.LastIndexAny(name, "/:")+1:]
}

// Resolve takes every name as a plugin's: one that isn't fails to clone.
func (*Tmux) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Install clones each plugin into TPM's folder, as TPM does: a branch after
// a #; owner/name from GitHub.
func (t *Tmux) Install(ctx context.Context, names []string) error {
	dir, err := t.dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("make TPM's plugin folder: %w", err)
	}
	var errs []error
	for _, name := range names {
		url, branch, _ := strings.Cut(name, "#")
		if !strings.Contains(url, ":") {
			url = "https://github.com/" + url
		}
		args := []string{"clone", "--single-branch", "--recursive"}
		if branch != "" {
			args = append(args, "--branch", branch)
		}
		args = append(args, url, filepath.Join(dir, t.Key(name)))
		_, err := t.run.Run(ctx, runner.Command{Name: "git", Args: args, Timeout: cloneTimeout})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Remove deletes each plugin's folder, as TPM's clean does.
func (t *Tmux) Remove(_ context.Context, names []string) error {
	dir, err := t.dir()
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		folder := filepath.Join(dir, t.Key(name))
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			errs = append(errs, fmt.Errorf("no plugin %s in %s", t.Key(name), t.shown(dir)))
			continue
		}
		if err := os.RemoveAll(folder); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", t.shown(folder), err))
		}
	}
	return errors.Join(errs...)
}

// shown is path as a person would write it: ~ for the home.
func (t *Tmux) shown(path string) string {
	if rest, ok := strings.CutPrefix(path, t.home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// expand expands a leading ~ and $HOME, as tmux does.
func (t *Tmux) expand(path string) string {
	switch {
	case path == "~":
		return t.home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(t.home, path[2:])
	}
	return strings.NewReplacer("$HOME", t.home, "${HOME}", t.home).Replace(path)
}

// unquote takes the quotes off a word of tmux's config.
func unquote(word string) string {
	return strings.Trim(word, `'"`)
}

// origin is the URL of the remote called origin in the git config at path:
// "" when there's none.
func origin(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	inOrigin := false
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok && inOrigin && strings.TrimSpace(key) == "url" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// githubPath is a GitHub URL's owner and repository: over HTTPS, with or
// without credentials, or SSH.
var githubPath = regexp.MustCompile(`^(?:https://(?:[^@/]*@)?github\.com/|git@github\.com:|ssh://git@github\.com/)([^/]+/[^/]+?)(?:\.git)?/?$`)

// repository is a plugin's owner/name, from where it was cloned: GitHub's
// owner/name, or the URL itself; "" for none.
func repository(url string) string {
	if url == "" {
		return ""
	}
	if m := githubPath.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	return url
}
